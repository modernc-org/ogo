// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// cIntLit renders an integer literal as C, appending a "ULL" suffix to a value that
// exceeds the signed 64-bit range so C reads it as unsigned (uint64) rather than
// warning that the constant is too large to be signed. Smaller values need no
// suffix: they assign cleanly to any integer type.
// cIdent turns an OctoGo identifier into a valid C identifier. It is the identity on
// an already-valid ASCII C identifier -- so every ASCII program's emitted C is
// byte-for-byte unchanged -- and escapes any identifier containing a non-ASCII rune
// (flexcc, like older C, rejects Unicode identifiers). The escape is injective: a
// name with any non-C rune becomes "ogo_U_" followed by each rune's hex code, so
// distinct identifiers never collide and the result cannot collide with a normal
// identifier (the ogo_ prefix is reserved for the compiler's own symbols).
func cIdent(name string) string {
	if isCIdent(name) {
		return name
	}
	var b strings.Builder
	b.WriteString("ogo_U")
	for _, r := range name {
		fmt.Fprintf(&b, "_%x", r)
	}
	return b.String()
}

// userIdent is the C spelling of a name the USER chose -- a variable, parameter,
// field, method or top-level symbol. It escapes the name as cIdent does and renames
// it if C has the name reserved, which is what lets a program declare a `static` or
// a `char`.
//
// It is deliberately NOT cIdent itself. cIdent also spells C TYPE names into the
// generated helper names -- ogo_mod_int, ogo_shl_uint32_t -- where "int" is C's own
// type and must stay exactly that. Renaming there produced ogo_mod_ogo_kw_int, which
// is why the two are separate funnels: one for names C gave us, one for names the
// program did.
func userIdent(name string) string {
	if cUnusable[name] {
		return "ogo_kw_" + name
	}
	return cIdent(name)
}

// cUnusable is the set of names that cannot be a C identifier ANYWHERE -- C's own
// keywords, the flexcc extensions, and the four library names its headers make
// MACROS of. Being reserved by the language or replaced by the preprocessor, a
// declaration of one is a syntax error wherever it stands: at file scope, as a
// local, as a parameter, or as a struct field. cIdent renames them.
//
// This is the set shadowing cannot rescue, which is what separates it from
// cReserved below. An ordinary library FUNCTION -- memcpy, atoi -- is an
// identifier like any other, so a local or a field of that name shadows or
// qualifies it and needs no help; only a file-scope symbol collides with the
// header's declaration. Every name in cReserved was measured as a parameter
// against this backend, and exactly these four proved unshadowable.
var cUnusable = map[string]bool{}

func init() {
	for _, name := range []string{
		// C keywords. The ones that are also OctoGo keywords ("for", "return")
		// cannot be identifiers here either, and are listed for completeness rather
		// than because they can be reached.
		"auto", "break", "case", "char", "const", "continue", "default", "do",
		"double", "else", "enum", "extern", "float", "for", "goto", "if", "inline",
		"int", "long", "register", "restrict", "return", "short", "signed",
		"sizeof", "static", "struct", "switch", "typedef", "union", "unsigned",
		"void", "volatile", "while",
		"_Alignas", "_Alignof", "_Atomic", "_Bool", "_Complex", "_Generic",
		"_Imaginary", "_Noreturn", "_Static_assert", "_Thread_local",
		// A flexcc extension, which is a keyword to its parser like any other.
		"__using",
		// Macros, not functions: the preprocessor replaces the name before the C
		// compiler sees a scope, so shadowing does not apply. printf is
		// __builtin_printf here, NULL is a constant, and the three streams are
		// expressions.
		"printf", "NULL", "stdin", "stdout", "stderr",
		// A library function the EMITTER ITSELF calls inside a user function body:
		// `b := a` for an array copies through memcpy. Shadowing works here, which
		// is the problem -- a user local of this name shadows the declaration for
		// the emitter's own call too, and `memcpy(b, a, sizeof(b))` becomes a call
		// to an int.
		//
		// This is the whole of that set today; every other libc name the emitter
		// uses is inside a runtime helper at file scope, where a user local is not
		// in scope at all. Emitting a new one directly into a user body means adding
		// it here, and the run case "a name C has spoken for, in every position"
		// is what would catch forgetting to.
		"memcpy",
	} {
		cUnusable[name] = true
	}
}

// cReserved is the set of library names the emitted C has already spoken for at FILE
// SCOPE: the functions declared by the headers the output includes -- <stdio.h>,
// <stdlib.h>, <string.h> and propeller2.h. A top-level user symbol of one of these
// names is emitted with the ogo_ prefix instead; a local, parameter or field of the
// same name shadows the declaration and is left alone (see cUnusable for the names
// where that is not enough).
//
// It does not need to be exhaustive to be worth having: it covers what a program is
// plausibly going to name, and a name missing from it fails the way it does today,
// as a C compile error naming the collision.
var cReserved = map[string]bool{}

func init() {
	for _, name := range []string{
		// <stdio.h>
		"fprintf", "sprintf", "snprintf", "puts", "putchar", "getchar",
		"fopen", "fclose", "fread", "fwrite", "fseek", "ftell", "rewind", "remove",
		"rename", "perror", "EOF",
		// <stdlib.h>
		"abort", "abs", "atexit", "atof", "atoi", "atol", "bsearch", "calloc",
		"div", "exit", "free", "getenv", "labs", "ldiv", "malloc", "qsort", "rand",
		"realloc", "srand", "strtod", "strtol", "strtoul", "system",
		// <string.h>
		"memchr", "memcmp", "memcpy", "memmove", "memset", "strcat", "strchr",
		"strcmp", "strcoll", "strcpy", "strcspn", "strerror", "strlen", "strncat",
		"strncmp", "strncpy", "strpbrk", "strrchr", "strspn", "strstr", "strtok",
		"strxfrm", "index", "rindex",
		// propeller2.h and the P2 intrinsics it declares.
		"cnt", "clkfreq", "clkmode", "waitcnt", "waitx", "getcnt", "getms",
		"getsec", "reboot", "cogid", "coginit", "cogstop", "locknew", "lockret",
		"locktry", "lockrel", "pinh", "pinl", "pinnot", "pinf", "pinr", "pinw",
		"rdpin", "akpin", "wrpin", "wxpin", "wypin", "rev", "rnd",
	} {
		cReserved[name] = true
	}
}

// isCIdent reports whether name is already a valid, non-empty C identifier: ASCII
// letters, digits and underscore, not starting with a digit.
func isCIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// pkgPrefix is the C-symbol prefix for a package's top-level names, derived from its
// import path. The main package (import path "") has no prefix, so its symbols keep
// their source names (and main() stays main); an imported package's path is made a
// C identifier (a "/" separator becomes "_", non-ASCII is escaped) so its symbols
// are namespaced and cannot collide with another package's.
func pkgPrefix(importPath string) string {
	if importPath == "" {
		return ""
	}
	return cIdent(strings.ReplaceAll(importPath, "/", "_"))
}

// mangle is a package's C symbol name for a top-level identifier: the (Unicode-safe)
// name in the main package, or prefix_name in an imported one.
func mangle(prefix, name string) string {
	if prefix == "" {
		// A top-level name C has already spoken for moves out of its way. The main
		// package's symbols keep their own names in the emitted C, which reads far
		// better -- but a program is entitled to a function called atoi or abs, and
		// C's declaration of one is already in scope through the headers the output
		// includes. Only the colliding names move.
		//
		// Only top-level names: a local, a parameter or a struct field of such a name
		// shadows or qualifies whatever C declared and needs no help. That is also the
		// safe boundary, since a top-level name is reached through this one funnel
		// while a local's is written in several places.
		if cReserved[name] {
			return "ogo_" + userIdent(name)
		}
		return userIdent(name)
	}
	return prefix + "_" + userIdent(name)
}

func cIntLit(src string) string {
	lit := normalizeIntLit(src)
	if v, err := strconv.ParseUint(lit, 0, 64); err == nil && v > math.MaxInt64 {
		lit += "ULL"
	}
	return lit
}

// cFloatLit renders a Go float literal as C. Go and C float syntax overlap for the
// decimal forms OctoGo's scanner accepts -- "3.14", "1.0", ".5", "1.", and the
// exponent forms -- so the text is valid C as written except for one thing: Go's
// digit separators. C has none, and "1_0.5" is not a C float at all but an integer
// with an invalid suffix, so they are stripped, exactly as normalizeIntLit does.
func cFloatLit(src string) string { return strings.ReplaceAll(src, "_", "") }

// runeLitValue decodes a rune literal's source ('A', '\n', '\x41', 'é', 'é')
// to its Unicode code point. strconv.Unquote handles every Go rune escape and, for
// a single-quoted literal, yields the one rune's UTF-8 string.
func runeLitValue(src string) (rune, bool) {
	s, err := strconv.Unquote(src)
	if err != nil {
		return 0, false
	}
	r := []rune(s)
	if len(r) != 1 {
		return 0, false
	}
	return r[0], true
}

// p2Intrinsic is one exported function of the builtin "p2" package: the flexcc /
// propeller2.h C intrinsic it wraps and its C result type (empty for a void one, so
// a p2 result is typed accurately -- a uint32 intrinsic like Rnd or ReadPin as
// unsigned, not signed int, which would print a high-bit value as negative).
type p2Intrinsic struct {
	c   string
	ret string
}

// p2Constants are the p2 package's exported constants: the pin-configuration bits
// a smart pin is brought up with, named rather than written as hex.
//
// They exist because the hex is unforgiving in a way that looks like working code.
// _examples/gopher was written with the mode word 0x140006 -- the DAC range and the
// smart-pin mode, and no OUTPUT ENABLE -- which compiles, runs, drives nothing, and
// puts about twenty millivolts of dither ripple on the pin. On a scope that is a
// small blob, which reads as a bug in the drawing rather than as a pin that was
// never switched on. `p2.DAC990R3V | p2.DACDitherPWM | p2.OutputEnable` cannot be
// written with a bit missing without the name of the missing bit being absent from
// the line.
//
// The values are from flexcc's smartpins.h, which is the authority and is embedded
// in this repository (internal/flexcc/p2include.tar.gz). The DAC and ADC paths are
// here; the rest of the vocabulary is a table to grow, not a design to settle.
var p2Constants = map[string]string{
	// The DAC output ranges: drive strength and full-scale voltage.
	"DAC990R3V": "0x140000", // P_DAC_990R_3V
	"DAC600R2V": "0x150000", // P_DAC_600R_2V
	"DAC124R3V": "0x160000", // P_DAC_124R_3V
	"DAC75R2V":  "0x170000", // P_DAC_75R_2V

	// The smart-pin DAC modes. Each takes its level from the pin's Y register, which
	// is what p2.WritePinY writes; the dithered ones take a 16-bit level.
	"DACNoise":     "0x02", // P_DAC_NOISE
	"DACDitherRnd": "0x04", // P_DAC_DITHER_RND
	"DACDitherPWM": "0x06", // P_DAC_DITHER_PWM

	// OutputEnable is what makes the pin drive at all. A mode without it configures
	// a pin that is switched off.
	"OutputEnable": "0x40", // P_OE

	// How hard a pin drives, one strength for the state it holds HIGH and one for the
	// state it holds LOW. The default at both ends is Fast, which is the full-strength
	// push-pull drive an output wants and the wrong thing to read a switch through.
	//
	// A PULL-UP IS A WEAK HIGH DRIVE AND A FLOATING LOW ONE, which is the part worth
	// writing down because the P2 has no separate pull-up bit and nothing named like
	// one. A switch to ground on pin 40, read with p2.PinIn:
	//
	//	p2.PinFloat(pin)                                          // clear any old mode
	//	p2.WritePinMode(pin, p2.DriveHigh15K|p2.DriveLowFloat)
	//	p2.PinHigh(pin)                                           // now a weak 1
	//
	// and a pull-down is the mirror -- DriveHighFloat|DriveLow15K with p2.PinLow. Both
	// verified on a P2-EDGE, where the pulled-down pin read 0 while the same pin left
	// floating read 1.
	//
	// The resistances are nominal; the current sources (1mA, 100uA, 10uA) are the
	// other way to do it. Which to choose is an electrical question -- a weaker pull
	// costs less current and picks up more noise -- and 15K is the usual answer for a
	// switch. Reading an input with NEITHER a pull nor something external driving it
	// is the failure this exists to prevent: the pin floats, and a floating pin does
	// not read a stable anything.
	"DriveHighFast":  "0x00",   // P_HIGH_FAST, the default
	"DriveHigh1K5":   "0x800",  // P_HIGH_1K5
	"DriveHigh15K":   "0x1000", // P_HIGH_15K
	"DriveHigh150K":  "0x1800", // P_HIGH_150K
	"DriveHigh1mA":   "0x2000", // P_HIGH_1MA
	"DriveHigh100uA": "0x2800", // P_HIGH_100UA
	"DriveHigh10uA":  "0x3000", // P_HIGH_10UA
	"DriveHighFloat": "0x3800", // P_HIGH_FLOAT

	"DriveLowFast":  "0x00",  // P_LOW_FAST, the default
	"DriveLow1K5":   "0x100", // P_LOW_1K5
	"DriveLow15K":   "0x200", // P_LOW_15K
	"DriveLow150K":  "0x300", // P_LOW_150K
	"DriveLow1mA":   "0x400", // P_LOW_1MA
	"DriveLow100uA": "0x500", // P_LOW_100UA
	"DriveLow10uA":  "0x600", // P_LOW_10UA
	"DriveLowFloat": "0x700", // P_LOW_FLOAT

	// The ADC input ranges. The gain is the fraction of full scale the pin's own
	// voltage covers, so a bigger number is a SMALLER measurable range: ADC1X reads
	// the whole 0..3.3 V span, ADC100X a hundredth of it at a hundred times the
	// resolution. The pin's analog input is what these select, and one of them and a
	// mode below are both needed.
	"ADC1X":   "0x118000", // P_ADC_1X
	"ADC3X":   "0x120000", // P_ADC_3X
	"ADC10X":  "0x128000", // P_ADC_10X
	"ADC30X":  "0x130000", // P_ADC_30X
	"ADC100X": "0x138000", // P_ADC_100X

	// The ADC's two internal reference inputs and its off position. They measure no
	// pin: ADCGround reads the chip's own ground and ADCSupply its 3.3 V rail, which
	// is how a reading is turned into a voltage. The converter is ratiometric and its
	// zero and span drift, so the pair is read and the pin's own reading scaled
	// between them -- the P2's documented calibration, and the reason these exist at
	// all rather than a fixed full-scale constant.
	"ADCGround": "0x100000", // P_ADC_GIO
	"ADCSupply": "0x108000", // P_ADC_VIO
	"ADCFloat":  "0x110000", // P_ADC_FLOAT

	// The smart-pin ADC modes. Each accumulates its result where p2.ReadPin reads it.
	// ADCSample is the ordinary one: it clocks itself, and the sample period is the
	// pin's X register -- 2^X clocks, so X is a number of BITS and each one doubles
	// both the time taken and the full-scale count. Unlike the DAC modes these want
	// no OutputEnable; a pin that drives is not measuring.
	//
	// X IS USABLE TO 13 AND NO FURTHER, measured on a P2-EDGE. Up to there the
	// doubling is exact -- spans of 1330, 2659, 5319, 10640 counts at X of 10, 11, 12,
	// 13 -- and at 14 and 15 every reading is 0, above that noise that changes run to
	// run. The Y register selects the filter variant and does NOT lift the ceiling;
	// all four values behave identically. So the best this mode offers is X=13: about
	// 10640 counts between the two references, a little over 13 bits, taking 8192
	// clocks. Ask for more and the reading does not degrade, it stops meaning
	// anything -- and nothing reports that, which is why the number is written here.
	"ADCSample":    "0x30", // P_ADC
	"ADCSampleExt": "0x32", // P_ADC_EXT
	"ADCScope":     "0x34", // P_ADC_SCOPE
}

// p2Intrinsics maps the p2 package's exported functions to their intrinsics (the
// mapping documented in CLAUDE.md's appendix). The call `p2.PinHigh(56)` emits
// `_pinh(56)`; `p2.Rnd()` emits `_rnd()` and types as unsigned.
var p2Intrinsics = map[string]p2Intrinsic{
	"PinHigh":      {"_pinh", ""},
	"PinLow":       {"_pinl", ""},
	"PinToggle":    {"_pinnot", ""},
	"PinFloat":     {"_pinf", ""},
	"PinIn":        {"_pinr", "int"},
	"PinWrite":     {"_pinw", ""},
	"WaitMs":       {"_waitms", ""},
	"WaitUs":       {"_waitus", ""},
	"WaitCycles":   {"_waitx", ""},
	"WaitUntil":    {"_waitcnt", ""},
	"AckPin":       {"_akpin", ""},
	"ReadPin":      {"_rdpin", "unsigned"},
	"PinStart":     {"_pinstart", ""},
	"WritePinMode": {"_wrpin", ""},
	"WritePinX":    {"_wxpin", ""},
	"WritePinY":    {"_wypin", ""},
	"GetCt":        {"_cnt", "unsigned"},
	"GetMs":        {"_getms", "unsigned"},
	"GetSec":       {"_getsec", "unsigned"},
	"GetUs":        {"_getus", "unsigned"},
	"Rnd":          {"_rnd", "unsigned"},
	"Rev":          {"_rev", "unsigned"},
	"SetBaud":      {"_setbaud", ""},
	"ReadByte":     {"_rxraw", "int"},
	"WriteByte":    {"_txraw", ""},
	"Reboot":       {"_reboot", ""},

	// The hardware locks. The P2 has 16, shared with the channel runtime, which
	// claims one per channel -- NewLock reports -1 when none is left, exactly as
	// _locknew does, rather than trapping. TryLock is the only way to take one:
	// the hardware offers no blocking acquire, so a caller that must wait spins on
	// it. Unlock and FreeLock discard the int their intrinsics return (whether the
	// lock had been held, and nothing, respectively), so both are statements.
	"NewLock":  {"_locknew", "int"},
	"FreeLock": {"_lockret", ""},
	"TryLock":  {"_locktry", cBool},
	"Unlock":   {"_lockrel", ""},
}

// importIncludes maps an OctoGo import path to the C header it pulls in.
var importIncludes = map[string]string{
	"p2": "propeller2.h",
}

// cTypes maps predeclared OctoGo type names to C types. int is a type of its own,
// 32 bits wide on this target as the P2's C int is, so it maps to plain int --
// int32 is a DIFFERENT type and maps to int32_t, which is how the two stay apart
// in the emitted C as they do in the checker. Fixed-width names need <stdint.h>.
var cTypes = map[string]string{
	"int": "int", "uint": "unsigned", "bool": cBool,
	"int8": "int8_t", "int16": "int16_t", "int32": "int32_t", "int64": "int64_t",
	"uint8": "uint8_t", "uint16": "uint16_t", "uint32": "uint32_t", "uint64": "uint64_t",
	"byte": "uint8_t", "rune": "int32_t", "uintptr": "uintptr_t",
	// The compiler-known string builder; see registerBuilder. Mapping it here lets
	// "*Builder" appear in a signature (-> ogo_builder*).
	"Builder": "ogo_builder",
	// float64 -> C double, but note the P2 toolchain's double is 32-bit (no
	// double-precision hardware), so float64 has float32 precision here (~7 digits).
	// The name is kept for Go compatibility; %g's 6-digit default matches the real
	// precision, so no spurious digits are printed. See the Numeric types spec.
	"float32": "float", "float64": "double",
	// A Go string is an immutable { pointer, length } header -- a value type that
	// will later support slicing -- so it maps to the ogo_string struct.
	"string": cString,
}

// goCTypeNames inverts cTypes for the names a diagnostic or "%T" prints. It is
// written out rather than derived, because cTypes is not injective: byte and uint8
// are one C type and so are rune and int32, and the name to print back is the one
// the language calls the type -- "uint8", "int32". `int` maps to C `int` and is
// therefore already its own answer.
var goCTypeNames = map[string]string{
	"unsigned": "uint", cBool: "bool", cString: "string",
	"int8_t": "int8", "int16_t": "int16", "int32_t": "int32", "int64_t": "int64",
	"uint8_t": "uint8", "uint16_t": "uint16", "uint32_t": "uint32", "uint64_t": "uint64",
	"uintptr_t": "uintptr", "float": "float32", "double": "float64",
	"ogo_builder": "Builder",
}

// cNamedEscape is the escapes C and Go spell identically. They are kept for the
// sake of whoever reads the emitted C: "\n" is every other line of it, and octal
// would be correct and unreadable.
var cNamedEscape = map[byte]string{
	'\a': `\a`, '\b': `\b`, '\t': `\t`, '\n': `\n`, '\v': `\v`, '\f': `\f`, '\r': `\r`,
}

// cQuote renders a string as a C literal, byte by byte. Every byte that is not
// printable ASCII becomes a THREE-DIGIT OCTAL escape, which is where it differs
// from Go's strconv.Quote and why it exists: C's hex escape has no length limit, so
// the "a\xffb" that Go quotes as "a\xffb" reads in C as an 'a' followed by ONE
// escape of value 0xffb -- a warning, and the wrong bytes. C caps an octal escape
// at three digits, so it always ends where it is written.
//
// A non-ASCII byte goes the same way rather than through as itself: the emitted C
// then holds no byte a compiler could read as anything but a string, whatever it
// believes the source encoding to be.
// runeString is Go's conversion of an integer constant to a string: the UTF-8
// encoding of that code point, and "\uFFFD" for a value that is not one. Go's own
// string(rune) answers the surrogate range that way already; what it cannot answer
// is a value outside int32, which would silently truncate, so that is checked here.
//
// The result may hold a NUL and is not a C string: emitFoldedString writes an
// explicit length beside the bytes, which is what makes string(rune(0)) a string of
// length one rather than of length zero.
func runeString(v int64) string {
	if v < 0 || v > unicode.MaxRune {
		return string(unicode.ReplacementChar)
	}
	return string(rune(v))
}

func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			// The named escapes mean the same in both languages and are a whole
			// escape by themselves, so they cannot run into what follows the way
			// "\x" does. Everything else goes out as octal.
			if named := cNamedEscape[c]; named != "" {
				b.WriteString(named)
				continue
			}
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// cString is the C type of an OctoGo string: a { const char* str; int len; }
// header emitted as stringTypedef, printed via the stringHelpers.
const cString = "ogo_string"

// cBool is the C type of an OctoGo bool: C99 _Bool. A distinct type, not int, so
// the emitter can tell a bool from an integer -- which is what lets it print
// true/false -- and so a bool packs to one byte in a struct or array. _Bool also
// normalizes any nonzero to 1 on store, matching Go's strict {false, true}. The
// checker forbids arithmetic on bool, so nothing relies on it being int.
const cBool = "_Bool"

const stringTypedef = "typedef struct { const char* str; int len; } ogo_string;\n"

// builderTypedef is the compiler-known Builder: a write cursor over a caller-owned
// []byte. It carries the backing pointer, its capacity (the backing length), and
// the count written so far. No allocation: the storage is the caller's.
const builderTypedef = "typedef struct { uint8_t* ptr; int cap; int len; } ogo_builder;\n"

// builderHelpers implement the Builder methods. NewBuilder(back) takes a []byte and
// starts empty. WriteString/WriteByte append into the free tail, bounded by cap (an
// overflowing write is truncated, not a trap -- the caller sized the backing).
// String() returns a VIEW: an ogo_string aliasing the backing's written prefix, no
// copy -- so it is only valid until the next write, exactly like Go's strings.Builder
// forbidding use after further building.
const builderHelpers = "static ogo_builder ogo_builder_new(ogo_slice_uint8_t back) { ogo_builder b; b.ptr = back.ptr; b.cap = back.len; b.len = 0; return b; }\n" +
	"static void ogo_builder_WriteString(ogo_builder* b, ogo_string s) { int n = s.len; if (n > b->cap - b->len) n = b->cap - b->len; if (n > 0) { memcpy(b->ptr + b->len, s.str, (unsigned)n); b->len += n; } }\n" +
	// Write appends a byte slice's bytes. Named Write (not WriteBytes) so a future
	// Builder can satisfy io.Writer's method set once interfaces exist, matching Go's
	// strings.Builder.Write; the result will grow to (int, error) then.
	"static void ogo_builder_Write(ogo_builder* b, ogo_slice_uint8_t p) { int n = p.len; if (n > b->cap - b->len) n = b->cap - b->len; if (n > 0) { memcpy(b->ptr + b->len, p.ptr, (unsigned)n); b->len += n; } }\n" +
	"static void ogo_builder_WriteByte(ogo_builder* b, uint8_t c) { if (b->len < b->cap) b->ptr[b->len++] = c; }\n" +
	// WriteRune encodes a rune as UTF-8. An out-of-range or surrogate value is
	// written as U+FFFD, as Go's WriteRune does. It writes nothing if the encoding
	// would not fit whole (never a partial rune).
	"static void ogo_builder_WriteRune(ogo_builder* b, int32_t r) {\n" +
	"\tunsigned int c = (unsigned int)r; uint8_t t[4]; int n;\n" +
	"\tif (r < 0 || c > 0x10FFFF || (c >= 0xD800 && c <= 0xDFFF)) c = 0xFFFD;\n" +
	"\tif (c < 0x80) { t[0] = (uint8_t)c; n = 1; }\n" +
	"\telse if (c < 0x800) { t[0] = (uint8_t)(0xC0 | (c >> 6)); t[1] = (uint8_t)(0x80 | (c & 0x3F)); n = 2; }\n" +
	"\telse if (c < 0x10000) { t[0] = (uint8_t)(0xE0 | (c >> 12)); t[1] = (uint8_t)(0x80 | ((c >> 6) & 0x3F)); t[2] = (uint8_t)(0x80 | (c & 0x3F)); n = 3; }\n" +
	"\telse { t[0] = (uint8_t)(0xF0 | (c >> 18)); t[1] = (uint8_t)(0x80 | ((c >> 12) & 0x3F)); t[2] = (uint8_t)(0x80 | ((c >> 6) & 0x3F)); t[3] = (uint8_t)(0x80 | (c & 0x3F)); n = 4; }\n" +
	"\tif (n > b->cap - b->len) return;\n" +
	"\tfor (int i = 0; i < n; i++) b->ptr[b->len++] = t[i];\n}\n" +
	"static ogo_string ogo_builder_String(ogo_builder* b) { ogo_string s; s.str = (const char*)b->ptr; s.len = b->len; return s; }\n" +
	"static int ogo_builder_Len(ogo_builder* b) { return b->len; }\n" +
	"static void ogo_builder_Reset(ogo_builder* b) { b->len = 0; }\n"

// runePrintHelper prints a rune as its UTF-8 bytes, which is what Go's %c does --
// a rune is not a byte, and putchar of one above 127 would emit a single wrong
// byte silently. Out-of-range and surrogate values print U+FFFD, as a string(rune)
// conversion does. Written out rather than shared with ogo_builder_WriteRune: that
// one writes into a buffer and can decline for want of room, and this one cannot.
const runePrintHelper = "static void ogo_print_rune(int32_t r) {\n" +
	"\tunsigned int c = (unsigned int)r;\n" +
	"\tif (r < 0 || c > 0x10FFFF || (c >= 0xD800 && c <= 0xDFFF)) c = 0xFFFD;\n" +
	"\tif (c < 0x80) { putchar((int)c); return; }\n" +
	"\tif (c < 0x800) { putchar((int)(0xC0 | (c >> 6))); }\n" +
	"\telse {\n" +
	"\t\tif (c < 0x10000) { putchar((int)(0xE0 | (c >> 12))); }\n" +
	"\t\telse { putchar((int)(0xF0 | (c >> 18))); putchar((int)(0x80 | ((c >> 12) & 0x3F))); }\n" +
	"\t\tputchar((int)(0x80 | ((c >> 6) & 0x3F)));\n" +
	"\t}\n" +
	"\tputchar((int)(0x80 | (c & 0x3F)));\n}\n"

// hexPrintHelper prints a SIGNED integer in hex as Go prints it -- a sign and the
// magnitude, "-ff" -- where C's %x prints the two's complement of the same value,
// "ffffff01". The magnitude is negated as UNSIGNED, which is defined for the most
// negative value where negating the signed one is not.
const hexPrintHelper = "static void ogo_print_hex(long long v, int upper) {\n" +
	"\tunsigned long long u = (unsigned long long)v;\n" +
	"\tif (v < 0) { putchar('-'); u = -u; }\n" +
	"\tif (upper) printf(\"%llX\", u); else printf(\"%llx\", u);\n}\n"

// stringHelpers print a string header's exact bytes. A string is not
// null-terminated, so %s is wrong; and the target's printf TRUNCATES "%.*s" at 62
// characters -- silently, so a 63-character line printed 62 of it and nothing said
// so. The bytes go out one at a time instead, which is exact at any length and
// costs nothing next to a serial line.
const stringHelpers = "static inline void ogo_print_str(ogo_string s) { for (int _i = 0; _i < s.len; _i++) { putchar(s.str[_i]); } }\n" +
	"static inline void ogo_println_str(ogo_string s) { ogo_print_str(s); putchar('\\n'); }\n"

// stringPadHelper prints a string to a field width, for printf's "%-8s" and friends.
// It counts RUNES, not bytes, because that is what fmt's width and precision mean
// for a string -- "%3s" of a two-rune six-byte string pads by one, and padding by
// nothing would be the easy way to get it wrong. Precision truncates, and truncates
// on a rune boundary. The pass over the bytes recognises a rune by its lead byte:
// every continuation byte is 10xxxxxx, so anything else starts one.
const stringPadHelper = "static inline void ogo_print_str_pad(ogo_string s, int w, int prec, int left) {\n" +
	"\tint _b = s.len, _n = 0;\n" +
	"\tfor (int _i = 0; _i < s.len; _i++) {\n" +
	"\t\tif ((s.str[_i] & 0xC0) != 0x80) {\n" +
	"\t\t\tif (prec >= 0 && _n == prec) { _b = _i; break; }\n" +
	"\t\t\t_n++;\n" +
	"\t\t}\n" +
	"\t}\n" +
	"\tint _p = w > _n ? w - _n : 0;\n" +
	"\tif (!left) for (int _i = 0; _i < _p; _i++) putchar(' ');\n" +
	"\tfor (int _i = 0; _i < _b; _i++) putchar(s.str[_i]);\n" +
	"\tif (left) for (int _i = 0; _i < _p; _i++) putchar(' ');\n" +
	"}\n"

// runePadHelper prints a rune to a field width. A rune is one character however many
// bytes it takes, so the padding is around a count of one.
const runePadHelper = "static inline void ogo_print_rune_pad(int32_t r, int w, int left) {\n" +
	"\tint _p = w > 1 ? w - 1 : 0;\n" +
	"\tif (!left) for (int _i = 0; _i < _p; _i++) putchar(' ');\n" +
	"\togo_print_rune(r);\n" +
	"\tif (left) for (int _i = 0; _i < _p; _i++) putchar(' ');\n" +
	"}\n"

// stringEqHelper compares two ogo_string values by content, as Go's == does. The
// byte loop avoids a memcmp (and its <string.h>); a string here is never long
// enough for that to matter.
const stringEqHelper = "static inline int ogo_string_eq(ogo_string a, ogo_string b) {\n" +
	"\tif (a.len != b.len) return 0;\n" +
	"\tfor (int i = 0; i < a.len; i++) if (a.str[i] != b.str[i]) return 0;\n" +
	"\treturn 1;\n" +
	"}\n"

// stringCmpHelper orders two ogo_string values lexicographically by unsigned byte,
// as Go's < <= > >= do: negative if a < b, zero if equal, positive if a > b. A
// prefix ties on the shorter length.
const stringCmpHelper = "static inline int ogo_string_cmp(ogo_string a, ogo_string b) {\n" +
	"\tint n = a.len < b.len ? a.len : b.len;\n" +
	"\tfor (int i = 0; i < n; i++) {\n" +
	"\t\tunsigned char ca = a.str[i], cb = b.str[i];\n" +
	"\t\tif (ca != cb) return ca < cb ? -1 : 1;\n" +
	"\t}\n" +
	"\treturn a.len == b.len ? 0 : (a.len < b.len ? -1 : 1);\n" +
	"}\n"

// runeDecodeHelper decodes one UTF-8 rune of an ogo_string at byte offset i, writes
// its byte width through w, and returns the rune -- matching Go's for-range over a
// string, including RuneError (U+FFFD, width 1) for any invalid, overlong, out-of-
// range or surrogate encoding. Bytes are read unsigned.
const runeDecodeHelper = `static inline int ogo_decode_rune(ogo_string s, int i, int *w) {
	unsigned char b0 = (unsigned char)s.str[i];
	if (b0 < 0x80) { *w = 1; return b0; }
	int n = s.len - i;
	if (b0 < 0xC0) { *w = 1; return 0xFFFD; }
	if (b0 < 0xE0) {
		if (n < 2) { *w = 1; return 0xFFFD; }
		unsigned char b1 = (unsigned char)s.str[i+1];
		if ((b1 & 0xC0) != 0x80) { *w = 1; return 0xFFFD; }
		int r = ((b0 & 0x1F) << 6) | (b1 & 0x3F);
		if (r < 0x80) { *w = 1; return 0xFFFD; }
		*w = 2; return r;
	}
	if (b0 < 0xF0) {
		if (n < 3) { *w = 1; return 0xFFFD; }
		unsigned char b1 = (unsigned char)s.str[i+1], b2 = (unsigned char)s.str[i+2];
		if ((b1 & 0xC0) != 0x80 || (b2 & 0xC0) != 0x80) { *w = 1; return 0xFFFD; }
		int r = ((b0 & 0x0F) << 12) | ((b1 & 0x3F) << 6) | (b2 & 0x3F);
		if (r < 0x800 || (r >= 0xD800 && r <= 0xDFFF)) { *w = 1; return 0xFFFD; }
		*w = 3; return r;
	}
	if (b0 < 0xF8) {
		if (n < 4) { *w = 1; return 0xFFFD; }
		unsigned char b1 = (unsigned char)s.str[i+1], b2 = (unsigned char)s.str[i+2], b3 = (unsigned char)s.str[i+3];
		if ((b1 & 0xC0) != 0x80 || (b2 & 0xC0) != 0x80 || (b3 & 0xC0) != 0x80) { *w = 1; return 0xFFFD; }
		int r = ((b0 & 0x07) << 18) | ((b1 & 0x3F) << 12) | ((b2 & 0x3F) << 6) | (b3 & 0x3F);
		if (r < 0x10000 || r > 0x10FFFF) { *w = 1; return 0xFFFD; }
		*w = 4; return r;
	}
	*w = 1; return 0xFFFD;
}
`

// sliceTypePrefix leads the C typedef name of an OctoGo slice `[]T`: a { pointer,
// length, capacity } header (`T* ptr; int len; int cap`) named per element type,
// e.g. []int -> ogo_slice_int, []*Point -> ogo_slice_Point_ptr. Like ogo_string it
// is a value type -- copied by value, a non-owning view over an array or another
// slice's backing storage. cap tracks that storage's remaining length (from ptr to
// its end), so a slice may be re-sliced or grown up to cap; it never acquires new
// backing memory (the P2 has no heap).
const sliceTypePrefix = "ogo_slice_"

// sliceCName is the C type name of a slice with C element type elem. A pointer
// element's "*" is spelled "_ptr" so the name stays a valid C identifier.
func sliceCName(elem string) string {
	return sliceTypePrefix + sanitizeElem(elem)
}

// chanTypePrefix leads the C typedef name of an OctoGo channel `chan T`. A channel
// is a rendezvous cell in Hub RAM guarded by one P2 hardware lock: a sender
// deposits into the cell and waits for a receiver to take it, so the two meet in
// lock step and no buffer is needed. `taken` counts consumed values, which is how a
// sender identifies its own handoff when several senders share the channel --
// watching `full` alone would let another sender's deposit be mistaken for its own.
const chanTypePrefix = "ogo_chan_"

// chanCName is the C type name of `chan elem`.
func chanCName(elem string) string { return chanTypePrefix + sanitizeElem(elem) }

// chanCellCName is the cell a channel points at. A channel is a reference type in
// Go, and must be one here: passing a by-value cell to a goroutine would hand it a
// copy, and the two would rendezvous with themselves.
func chanCellCName(elem string) string { return chanTypePrefix + sanitizeElem(elem) + "_cell" }

// chanInitCName, chanSendCName and chanRecvCName name the per-element runtime
// helpers.
func chanInitCName(elem string) string { return "ogo_chan_init_" + sanitizeElem(elem) }

func chanSendCName(elem string) string { return "ogo_chan_send_" + sanitizeElem(elem) }

func chanRecvCName(elem string) string { return "ogo_chan_recv_" + sanitizeElem(elem) }

// chanTryRecvCName names the non-blocking receive a select polls with.
func chanTryRecvCName(elem string) string { return "ogo_chan_tryrecv_" + sanitizeElem(elem) }

// recvOperand recognises a receive expression `<-ch`, returning the channel's
// element C type and the C name of the channel. Shared by emission and inference so
// the two cannot disagree about what a receive yields.
func (e *emitter) recvOperand(n Node, kids []Node) (elem, base string, ok bool) {
	if n.sym != UnaryExpr || len(kids) != 2 || kids[0].sym != UnaryOp {
		return "", "", false
	}
	tok, ok := e.unaryOpTok(kids[0].ast)
	if !ok || e.f.ch(tok) != ARROW {
		return "", "", false
	}
	return e.chanOperand(kids[1].ast)
}

// chanOperand resolves an expression that names a channel to the channel's element
// C type and its C name. It is the half of a receive that does not involve the
// "<-": recvOperand reaches it with the operator inside a unary expression, and a
// bare receive statement reaches it with the operator on the statement, so the two
// cannot disagree about what channel is being read.
func (e *emitter) chanOperand(ast []int32) (elem, base string, ok bool) {
	if name, isName := e.exprIdent(ast); isName {
		ct, isVar := e.varType(name)
		if !isVar || !e.isChanCType(ct) {
			return "", "", false
		}
		return e.chanElemOfCType(ct), e.varRef(name), true
	}
	// A channel held in a struct FIELD, `<-ports.rx`. A channel is a pointer to its
	// cell, so the field access is what names it and nothing else changes.
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term || nodes[0].sym == UnaryExpr) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	// The operand arrives either wrapped in a Factor or already as one's children,
	// depending on which caller asked; both are the same expression.
	kids := nodes
	if len(nodes) == 1 && nodes[0].sym == Factor {
		kids = slices.Collect(it(nodes[0].ast))
	}
	if root, fields, isField := e.factorFieldAccess(kids); isField {
		ct, okf := e.fieldType(root, fields)
		if !okf || !e.isChanCType(ct) {
			return "", "", false
		}
		return e.chanElemOfCType(ct), e.fieldAccessC(root, fields), true
	}
	// A channel field reached through an INDEX, `ws[i].cmd`. The chain walker knows
	// how to render one and what type it reaches, which is more than the fixed
	// field path can do.
	root, steps, isChain := e.factorAccessChain(kids)
	if !isChain {
		return "", "", false
	}
	text, ct, _, okc := e.chainCText(root, steps)
	if !okc || !e.isChanCType(ct) {
		return "", "", false
	}
	return e.chanElemOfCType(ct), text, true
}

// goSite is one `go` statement: the callee's C name and the C types of its
// arguments. Each gets a struct to marshal the arguments through and a trampoline
// matching _cogstart's `void (*)(void *)` signature.
type goSite struct {
	callee string
	args   []string
	id     int
	// fnCType is the function typedef when the callee is a VALUE rather than a name:
	// `go g(21)` for a g holding a function. There is no name to generate an entry
	// point against, so the pointer travels in the argument block like an argument
	// and the trampoline calls through it.
	fnCType string
}

// goArgsCName and goTrampolineCName name a site's generated struct and trampoline.
func goArgsCName(id int) string { return fmt.Sprintf("ogo_go_args%d", id) }

func goTrampolineCName(id int) string { return fmt.Sprintf("ogo_go%d", id) }

// ogoCogPool is the goroutine slot pool. A goroutine needs a stack and somewhere
// to marshal its arguments, both of which must outlive the `go` statement -- the
// launched cog reads them asynchronously -- so neither can be a local of the
// launching function.
//
// The pool is sized to the hardware: 8 cogs less the one running main. That makes
// "out of slots" and "out of cogs" the same condition, and bounds the whole thing
// statically, with no allocator. It also makes `go` inside a loop safe, which is
// why it need not be rejected the way `defer` in a loop is: defer's problem was
// unbounded storage in the current frame, while this is bounded by the silicon.
//
// A slot is freed on two signals together: the goroutine reached the end of the
// trampoline (done), and its cog has stopped. Neither alone is enough. done cannot
// be trusted by itself because the goroutine sets it while still executing on the
// slot's stack, with the return through _cogstart's epilogue ahead of it -- handing
// that stack to a new cog wedges both. A stopped cog alone is not enough either: a
// slot between claim and _cogstart has no cog yet, which is why cog stays -1 over
// that window and a slot holding -1 is never freed.
//
// The gap between those two signals is what claim has to wait out. A goroutine that
// has just handed main its result is a few instructions short of stopping, so the
// next `go` in the sequence arrives while its predecessor still reads live; giving
// up there caps a program at 7 goroutines for its whole run rather than at 7 at a
// time, which is what the spec promises. Waiting is bounded, because done is set on
// the way out and nothing between it and the stop can block.
//
// Freeing is done by a sweep at the top of each claim rather than only when a slot
// is needed, so a cog id is never left recorded in a slot whose cog has stopped.
// That matters because the hardware reissues ids: were a stale pairing kept, a later
// _cogchk would answer about the id's new occupant, and a slot whose goroutine ended
// long ago would read as forever-finishing. The sweep also catches that case
// directly, in the rare order where the reissue happens between two claims.
const ogoCogPool = `#define OGO_COGS 8
#define OGO_STACK_LONGS 256
// The argument block is a union of every go site's arguments (see goDefs), so it is
// exactly as wide and as aligned as the widest of them.
// A backstop, not a timeout: a goroutine reaches its epilogue and its cog stops
// within a few instructions of the body ending, so this many spins cannot elapse
// legitimately. It turns a state this protocol did not anticipate into a
// diagnosable panic instead of a silent hang.
#define OGO_STOP_SPINS 100000
typedef struct { int ogo_used; int ogo_done; int ogo_cog; ogo_go_args ogo_args; long ogo_stack[OGO_STACK_LONGS]; } ogo_cog_slot;
static ogo_cog_slot ogo_cog_pool[OGO_COGS - 1];
static int ogo_cog_lock = -1;
static void ogo_cog_sweep(void) { // frees every finished slot; caller holds ogo_cog_lock
	for (int i = 0; i < OGO_COGS - 1; i++) {
		if (!ogo_cog_pool[i].ogo_used || !ogo_cog_pool[i].ogo_done || ogo_cog_pool[i].ogo_cog < 0) {
			continue;
		}
		int stopped = !_cogchk(ogo_cog_pool[i].ogo_cog);
		for (int j = 0; !stopped && j < OGO_COGS - 1; j++) {
			// The id is running another slot's goroutine, so this slot's own cog
			// stopped and the id was handed out again: _cogchk is answering about
			// the new occupant. Only a slot that has not finished proves that,
			// being the one certain owner of the id it holds.
			if (j != i && ogo_cog_pool[j].ogo_used && !ogo_cog_pool[j].ogo_done &&
				ogo_cog_pool[j].ogo_cog == ogo_cog_pool[i].ogo_cog) {
				stopped = 1;
			}
		}
		if (stopped) {
			ogo_cog_pool[i].ogo_used = 0;
			ogo_cog_pool[i].ogo_done = 0;
			ogo_cog_pool[i].ogo_cog = -1;
		}
	}
}
static int ogo_cog_claim(void) {
	if (ogo_cog_lock < 0) {
		// The first claim is always main's: another cog can only be running
		// because a spawn already came through here, so this races nothing.
		ogo_cog_lock = _locknew();
		if (ogo_cog_lock < 0) {
			ogo_panic("out of hardware locks");
		}
	}
	for (int spin = 0;; spin++) {
		int got = -1;
		int stopping = 0;
		while (!_locktry(ogo_cog_lock)) { // a goroutine may itself spawn one
			_waitx(1);
		}
		ogo_cog_sweep();
		for (int i = 0; i < OGO_COGS - 1; i++) {
			if (!ogo_cog_pool[i].ogo_used) {
				got = i;
				break;
			}
			stopping |= ogo_cog_pool[i].ogo_done; // finished, but still stopping
		}
		if (got >= 0) {
			ogo_cog_pool[got].ogo_used = 1;
			ogo_cog_pool[got].ogo_done = 0;
			ogo_cog_pool[got].ogo_cog = -1;
		}
		_lockrel(ogo_cog_lock);
		if (got >= 0) {
			return got;
		}
		// No slot is free. That is not yet proof there is no cog to be had: a
		// goroutine whose body has just ended has not necessarily marked its slot,
		// and the caller can learn the body is over before the epilogue runs -- a
		// receive of the value the goroutine sent last returns first. So every slot
		// busy is waited on, not just a slot already known to be finishing. A slot
		// held by a goroutine that really is running stays held, so the wait only
		// delays a diagnosis the program was going to get either way.
		if (spin == OGO_STOP_SPINS) {
			if (stopping) { // one finished, said so, and its cog never stopped
				ogo_panic("cog failed to stop");
			}
			return -1; // every slot is running a goroutine that has not finished
		}
		_waitx(1);
	}
}
static void ogo_cog_release(int slot) { ogo_cog_pool[slot].ogo_used = 0; }
static void ogo_cog_done(int slot) { ogo_cog_pool[slot].ogo_done = 1; }
`

// emitGo emits a `go` statement: claim a pool slot, marshal the arguments into it,
// and hand the trampoline, the argument block and the slot's stack to _cogstart.
// Exceeding the cogs panics at runtime, which is what the spec prescribes.
func (e *emitter) emitGo(nodes []Node) {
	var head, lit Node
	var suffix []Node
	for _, n := range nodes {
		switch n.sym {
		case AssignHead:
			head = n
		case FuncLiteral:
			lit = n
		case Selector, Index, CallSuffix:
			suffix = append(suffix, n)
		}
	}
	base := e.soleIdent(head.ast)
	crossed := func(what, advice string, at Node) {
		e.fail("%v: cannot pass %s to a goroutine: its storage does not outlive the function, and the "+
			"goroutine may; %s",
			e.f.tok(at.Pos()).Position(), what, advice)
	}
	// `go x.M(args)` is `go f(args)` with the receiver in front: the trampoline's
	// struct carries it like any other argument, so the cog calls <T>_M(recv, ...)
	// with a receiver evaluated here, at the `go` statement, as Go evaluates it.
	var site goSite
	var callSuffix Node
	var recvText, recvCType string
	switch {
	case lit.sym == FuncLiteral:
		// `go func() { ... }()`: a cog's entry point is generated per function, and a
		// lifted literal IS one. It takes no arguments for now, which is what a cog
		// entry usually wants anyway -- what it shares, it shares through a channel.
		if len(suffix) != 1 || suffix[0].sym != CallSuffix || len(e.callArgExprs(suffix[0].ast)) != 0 {
			e.fail("a function literal started with go takes no arguments yet")
			return
		}
		cname, ok := e.liftFuncLit(lit)
		if !ok {
			return
		}
		site = goSite{callee: cname, id: len(e.goSites)}
		callSuffix = suffix[0]
	case base != "" && len(suffix) == 1 && suffix[0].sym == CallSuffix:
		if _, ok := e.userFunc(base); !ok {
			// A variable holding a function: which function it is is not known until
			// run time, so the trampoline is generated against its TYPE and the
			// pointer is marshalled with the arguments.
			ct, isVar := e.varType(base)
			if !isVar || !e.isFuncCType(ct) {
				e.fail("only `go f(args)` on a package function or a variable holding one is supported yet")
				return
			}
			site = goSite{callee: e.varRef(base), fnCType: e.underlyingCType(ct), id: len(e.goSites)}
			callSuffix = suffix[0]
			break
		}
		site = goSite{callee: e.funcCallC(base), id: len(e.goSites)}
		callSuffix = suffix[0]
	case base != "" && len(suffix) >= 2 && suffix[len(suffix)-1].sym == CallSuffix &&
		suffix[len(suffix)-2].sym == Selector && isAccessChain(suffix[:len(suffix)-1]):
		callSuffix = suffix[len(suffix)-1]
		steps := suffix[:len(suffix)-1]
		name := e.soleIdent(steps[len(steps)-1].ast)
		// The receiver may be reached through fields and indexes -- `go ws[i].run(ch)`
		// is a worker per cog, which is what this target is for. The chain is walked
		// here and the value it reaches is what the trampoline carries; a plain
		// variable is the same path with an empty chain.
		if chain := steps[:len(steps)-1]; len(chain) != 0 {
			cur, ok := e.accessChainType(base, chain)
			if !ok {
				e.fail("unsupported receiver in a go statement")
				return
			}
			rct, ok := e.chainValueCType(cur)
			if !ok {
				// An ARRAY has no C VALUE type; its DEFINED name is what says which
				// type it is, the same fallback the deferred receiver makes.
				rct = cur.name
			}
			if !e.isMethodBase(methodBaseType(rct)) {
				e.fail("unsupported receiver in a go statement")
				return
			}
			cname := methodCName(methodBaseType(rct), name)
			wantPtr := e.methodPtr[cname]
			if r, bad := e.receiverFrameRef(base, wantPtr); bad {
				crossed(r.what, r.advice(), head)
				return
			}
			text, pro := e.capturePrologue(func() { e.emitAccessChain(base, chain) })
			for _, line := range pro {
				e.ind()
				e.emit(line)
			}
			recv, ok := e.chainReceiver(text, rct, true, wantPtr)
			if !ok {
				e.fail("cannot take the address of %s for a pointer-receiver method", text)
				return
			}
			recvCType = methodBaseType(rct)
			if wantPtr {
				recvCType += "*"
			}
			recvText = recv
			site = goSite{callee: cname, args: []string{recvCType}, id: len(e.goSites)}
			break
		}
		// A struct FIELD holding a function, `go t.fn(5)`: the same value path a bare
		// variable takes, with the field access as the pointer's source. Asked before
		// the method path, which would read `t.fn` as a method of t's type and emit a
		// call to a name nothing declares.
		if ft, okf := e.fieldType(base, []string{name}); okf && e.isFuncCType(ft) && len(suffix) == 2 {
			site = goSite{
				callee:  e.fieldAccessC(base, []string{name}),
				fnCType: e.underlyingCType(ft),
				id:      len(e.goSites),
			}
			break
		}
		// A variable of a user type is a method call; an import qualifier is a
		// function of that package. The variable is asked about first, since a local
		// of the qualifier's name shadows the import, as it does at an ordinary call.
		//
		// methodRecvCType rather than varType-plus-isUserType: an ARRAY variable has
		// no C type, so the pair answered no for one and `go g.run()` was refused
		// where `go ws[i].run()` for a struct element was not.
		rct, isVar := e.methodRecvCType(base)
		if !isVar {
			prefix, isImport := e.importQualifiers[base]
			if !isImport {
				e.fail("only `go f(args)` on a package function or `go x.M(args)` on a method is supported yet")
				return
			}
			// The imported function is emitted in its own package's namespace, so the
			// launch resolves to the same mangled name an ordinary call would.
			site = goSite{callee: mangle(prefix, name), id: len(e.goSites)}
			break
		}
		cname := methodCName(methodBaseType(rct), name)
		wantPtr := e.methodPtr[cname]
		// A pointer receiver hands the goroutine the address of the receiver, which
		// is a reference leaving this frame's control exactly as `go f(&x)` is. A
		// value receiver is a copy and crosses nothing, unless the value itself holds
		// a reference to the frame.
		if r, bad := e.receiverFrameRef(base, wantPtr); bad {
			crossed(r.what, r.advice(), head)
			return
		}
		recvCType = methodBaseType(rct)
		if wantPtr {
			recvCType += "*"
		}
		recvText = e.captureC(func() { e.emitMethodReceiver(e.varRef(base), rct, wantPtr) })
		site = goSite{callee: cname, args: []string{recvCType}, id: len(e.goSites)}
	default:
		e.fail("only `go f(args)` on a package function or `go x.M(args)` on a method is supported yet")
		return
	}
	args := e.callArgExprs(callSuffix.ast)
	if x, r, bad := e.frameRefIn(args); bad {
		crossed(r.what, r.advice(), x)
		return
	}
	// The argument block holds each value as its PARAMETER's type, not as the type
	// the argument expression happens to have. A goroutine argument is assigned to
	// the parameter, and Go converts there: storing 1234567890123 as the "int" its
	// literal defaults to truncated it to 1912276171 before the cog ever started.
	// A receiver, when there is one, is already site.args[0], so the parameters line
	// up after it.
	// The receiver, when there is one, is site.args[0] already; cParamTypes excludes
	// it, so the parameters line up with the arguments one for one either way.
	params := e.funcParams[site.callee]
	if site.fnCType != "" {
		params = e.funcTypeParams[site.fnCType]
	}
	for i, a := range args {
		ct := ""
		if i < len(params) {
			ct = params[i]
		}
		if ct == "" {
			var ok bool
			if ct, ok = e.inferCType(a.ast); !ok {
				e.fail("cannot infer the type of a go argument")
				return
			}
		}
		site.args = append(site.args, ct)
	}
	e.goSites = append(e.goSites, site)
	e.needPanic()
	e.includes["propeller2.h"] = true

	slot := e.newTmp()
	ap := e.newTmp()
	e.ind()
	e.emit("{\n")
	e.indent++
	e.ind()
	e.emit("int " + slot + " = ogo_cog_claim();\n")
	e.ind()
	e.emit("if (" + slot + " < 0) { ogo_panic(\"out of cogs\"); }\n")
	e.ind()
	e.emit(goArgsCName(site.id) + "* " + ap + " = (void*)&ogo_cog_pool[" + slot + "].ogo_args;\n")
	e.ind()
	e.emit(ap + "->ogo_slot = " + slot + ";\n")
	if site.fnCType != "" {
		// Read once, here: the variable may be reassigned before the cog runs, and
		// Go evaluates the callee at the `go` statement.
		e.ind()
		e.emit(ap + "->fn = " + site.callee + ";\n")
	}
	first := 0
	if recvText != "" {
		e.ind()
		// An ARRAY receiver is COPIED into the slot: C assigns no array, and a value
		// receiver crossing to a cog is a copy, which is what Go says a goroutine's
		// receiver is.
		if _, isArr := e.namedArrays[site.args[0]]; isArr {
			e.includes["string.h"] = true
			e.emit("memcpy(" + ap + "->a0, " + recvText + ", sizeof(" + ap + "->a0));\n")
		} else {
			e.emit(ap + "->a0 = " + recvText + ";\n")
		}
		first = 1
	}
	for i, a := range args {
		// A concrete value crossing to a cog as an INTERFACE parameter is wrapped
		// where it stands, the same two words every other position writes. Without
		// this the raw pointer was stored in a slot of interface type and the
		// target's C compiler reported "expected _struct__Shape but got pointer to
		// _struct__Quad" about generated code -- `go show(&q)` for a `show(Shape)`
		// had never worked, in any spelling.
		//
		// Through capturePrologue because building one may need a temporary (an
		// interface WIDENED from another is statements, not an expression), and it
		// has to be declared here rather than wherever the prologue is next flushed:
		// the cog starts at the end of this block.
		if ct := site.args[i+first]; e.isIfaceCType(ct) {
			var text string
			var wrapped bool
			_, pro := e.capturePrologue(func() { text, wrapped = e.ifaceValueC(ct, a.ast) })
			if wrapped {
				for _, line := range pro {
					e.ind()
					e.emit(line)
				}
				e.ind()
				e.emit(fmt.Sprintf("%s->a%d = %s;\n", ap, i+first, text))
				continue
			}
		}
		// A composite literal is built in a variable of its own first. The target's
		// C compiler refuses one as the right-hand side of an assignment -- "global
		// initializers are evaluated at compile time and therefore must be
		// constant", wherever the assignment stands -- while it takes the same
		// braces in a declaration.
		rhs := e.captureC(func() { e.emitExpr(a.ast) })
		if strings.HasPrefix(rhs, "(") && strings.Contains(rhs, "){") {
			tmp := e.newTmp()
			e.ind()
			e.emit(site.args[i+first] + " " + tmp + " = " + rhs[strings.Index(rhs, "){")+1:] + ";\n")
			rhs = tmp
		}
		e.ind()
		e.emit(fmt.Sprintf("%s->a%d = %s;\n", ap, i+first, rhs))
	}
	e.ind()
	e.emit("ogo_cog_pool[" + slot + "].ogo_cog = _cogstart_C(" + goTrampolineCName(site.id) + ", " + ap +
		", ogo_cog_pool[" + slot + "].ogo_stack, sizeof ogo_cog_pool[" + slot + "].ogo_stack);\n")
	e.ind()
	e.emit("if (ogo_cog_pool[" + slot + "].ogo_cog < 0) {\n")
	e.indent++
	e.ind()
	e.emit("ogo_cog_release(" + slot + ");\n")
	e.ind()
	e.emit("ogo_panic(\"out of cogs\");\n")
	e.indent--
	e.ind()
	e.emit("}\n")
	e.indent--
	e.ind()
	e.emit("}\n")
}

// goDefs renders the argument struct and trampoline for every launched goroutine,
// plus the pool, whose argument block is a union of every site's struct. The
// trampoline releases the slot when the goroutine returns, which is where the cog is
// freed too.
//
// The union is what sizes and aligns the block, rather than any arithmetic here.
// Counting one long per argument is what it replaced, and that undercounts every
// argument wider than one: a slice is three longs, an int64 or float64 two, a struct
// as many as it has. The block sat directly above the goroutine's stack in the same
// slot, so a `go` statement passing a slice overflowed into the stack the cog was
// about to run on -- with no diagnostic anywhere, on either compiler.
func (e *emitter) goDefs() string {
	if len(e.goSites) == 0 {
		return ""
	}
	var b, tramps strings.Builder
	for _, s := range e.goSites {
		fmt.Fprintf(&b, "typedef struct { int ogo_slot;")
		if s.fnCType != "" {
			fmt.Fprintf(&b, " %s fn;", s.fnCType)
		}
		for i, a := range s.args {
			fmt.Fprintf(&b, " %s a%d;", a, i)
		}
		fmt.Fprintf(&b, " } %s;\n", goArgsCName(s.id))
	}
	b.WriteString("typedef union {")
	for i, s := range e.goSites {
		fmt.Fprintf(&b, " %s s%d;", goArgsCName(s.id), i)
	}
	b.WriteString(" } ogo_go_args;\n")
	for _, s := range e.goSites {
		callee := s.callee
		if s.fnCType != "" {
			callee = "a->fn"
		}
		fmt.Fprintf(&tramps, "static void %s(void* p) {\n\t%s* a = p;\n\t%s(",
			goTrampolineCName(s.id), goArgsCName(s.id), callee)
		for i := range s.args {
			if i != 0 {
				tramps.WriteString(", ")
			}
			fmt.Fprintf(&tramps, "a->a%d", i)
		}
		// Not ogo_cog_release: the goroutine is still on this slot's stack
		// here, with the return through _cogstart's epilogue ahead of it. done
		// only makes the slot a candidate; ogo_cog_sweep waits for _cogchk to
		// confirm the cog stopped before the stack is handed to anyone else.
		tramps.WriteString(");\n\togo_cog_done(a->ogo_slot);\n}\n")
	}
	// The argument structs come before the pool that embeds them, the trampolines
	// after it: they call ogo_cog_done.
	return b.String() + ogoCogPool + tramps.String()
}

// selectCase is one CommClause of a select: the channel polled, where its value
// lands, and the statements to run. def marks the default clause, which has no
// channel.
type selectCase struct {
	def     bool
	send    bool         // `case ch <- v:` rather than a receive
	ch      string       // channel variable
	elem    string       // its element C type
	target  assignTarget // what receives the value; its name is empty for a bare `case <-ch:`
	declare bool         // ":=", so the target is introduced in the clause
	val     Node         // the value being sent, for a send clause
	body    []Node
}

// emitSelect emits a select as a poll over each channel's non-blocking receive, in
// clause order, which is the lowering the spec prescribes.
//
// With a default clause the poll runs once and falls through to the default, so it
// never blocks. Without one it repeats, yielding with _waitx(1) between rounds: a
// cog cannot sleep, and spinning on the Hub bus without yielding would starve the
// cogs doing real work.
//
// Send clauses are not modelled. An unbuffered send completes only once a receiver
// has taken the value, so a non-blocking send would have to report a rendezvous
// that has not happened yet; that needs a two-phase handshake the cell does not
// carry. Receive and default are what the spec's own example uses.
func (e *emitter) emitSelect(ast []int32) {
	// A name a select header declares belongs to the statement, not to the block
	// around it (see enterScope).
	defer e.enterScope()()
	var cases []selectCase
	for n := range it(ast) {
		if n.sym != CommClause {
			continue
		}
		c, ok := e.selectClause(n)
		if !ok {
			return // selectClause has latched the failure
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		e.fail("an empty select blocks forever and is not supported yet")
		return
	}
	hasDefault, sends := false, 0
	for _, c := range cases {
		if c.def {
			hasDefault = true
		}
		if c.send {
			sends++
		}
	}
	// A send clause offers its value and waits for it to be taken, which is what
	// leaves the other clauses reachable: the offer can be taken back. Two offers
	// cannot both stand -- a receiver taking each would send twice where Go sends
	// once -- and offering them by turns would let a receiver polling one miss it
	// while the other is up, so that shape is refused rather than made unfair.
	//
	// A default cannot be answered at all. It asks whether a receiver is ready
	// *now*, and a receiver here reveals itself only by taking a value: the cell
	// carries no "waiting" state, and both sides poll, so there is nowhere to look.
	switch {
	case sends > 1:
		e.fail("a select may have at most one send clause yet")
		return
	case sends != 0 && hasDefault:
		e.fail("a select with a send clause may not have a default yet: whether a receiver is ready cannot be known without offering the value")
		return
	}
	// A break in a comm clause leaves the select. Both lowerings below are C loop
	// constructs -- a polling "while", or a "do { } while (0)" for the
	// non-blocking form -- so a plain C break is exactly that jump, and the switch
	// context must be cleared or a select written inside a switch case would emit
	// that switch's end-label goto instead and leave the switch too. Restored
	// after, like emitLoopBody does for a loop.
	savedBreak := e.switchBreak
	e.switchBreak = ""
	defer func() { e.switchBreak = savedBreak }()

	done := e.newTmp()
	e.ind()
	e.emit("{\n")
	e.indent++
	// A send clause's value is evaluated once, where the select stands, as Go
	// evaluates it -- not afresh on every round of the poll.
	var send *selectCase
	var valTmp, offered, mine string
	for i := range cases {
		if cases[i].send {
			send = &cases[i]
		}
	}
	if send != nil {
		e.chanTrySendElems[send.elem] = true
		valTmp, offered, mine = e.newTmp(), e.newTmp(), e.newTmp()
		switch a, isArr := e.namedArrays[send.elem]; {
		case isArr:
			// An ARRAY is copied, not assigned: C has no array assignment, so
			// `elem tmp = arr` was not C at all.
			e.emitArrayCopy(valTmp, e.captureC(func() { e.emitExpr(send.val.ast) }), a)
		default:
			e.ind()
			e.emit(send.elem + " " + valTmp + " = ")
			// A concrete value sent on a channel of INTERFACE type is wrapped into
			// the two words the element is, as the blocking send wraps it. Without
			// this the raw pointer went where the pair goes.
			if text, ok := e.ifaceValueC(send.elem, send.val.ast); ok && e.isIfaceCType(send.elem) {
				e.emit(text)
			} else {
				e.emitExpr(send.val.ast)
			}
			e.emit(";\n")
		}
		e.ind()
		e.emit("int " + offered + " = 0, " + mine + " = 0;\n")
	}
	if hasDefault {
		// One pass, so no loop and no flag to test: a default clause makes the
		// select non-blocking, and the clauses are a plain if/else chain.
		e.ind()
		e.emit("do {\n")
	} else {
		e.ind()
		e.emit("int " + done + " = 0;\n")
		e.ind()
		e.emit("while (!" + done + ") {\n")
	}
	e.indent++
	// Every clause's received value is declared ahead of the chain, not beside its
	// own test. A declaration between one clause's closing brace and the next
	// clause's "else" is not C -- the else has no if before it -- so a select with
	// more than one clause did not compile at all until these moved up here.
	tmps := make([]string, len(cases))
	for i, c := range cases {
		if c.def || c.send {
			continue
		}
		tmps[i] = e.newTmp()
		e.ind()
		if a, isArr := e.namedArrays[c.elem]; isArr {
			// An ARRAY element cannot be declared through its typedef here and is
			// filled by the try-receive rather than assigned, so it is declared with
			// its own extents and passed as the pointer it decays to.
			e.arrays[tmps[i]] = a
			e.emit(a.elem + " " + tmps[i] + a.declSuffix() + ";\n")
		} else {
			e.emit(c.elem + " " + tmps[i] + ";\n")
		}
	}
	// The offer stands across rounds, taken back only when some receive clause looks
	// ready -- because taking a value commits to that clause, and the offer must be
	// gone by then or the round would communicate twice. A withdrawal that fails
	// means a receiver got there first, and the send clause's own test runs it, so
	// racing the receiver costs nothing.
	tryRecv := ""
	if send != nil {
		e.ind()
		e.emit("if (!" + offered + ") { " + offered + " = " +
			chanOfferCName(send.elem) + "(" + send.ch + ", " + valTmp + ", &" + mine + "); }\n")
		if peek := peekReady(cases); peek != "" {
			tryRecv = e.newTmp()
			e.ind()
			e.emit("int " + tryRecv + " = !" + offered + ";\n")
			e.ind()
			e.emit("if (" + offered + " && (" + peek + ")) { " + tryRecv + " = " +
				chanWithdrawCName(send.elem) + "(" + send.ch + ", " + mine + "); " +
				offered + " = !" + tryRecv + "; }\n")
		}
	}
	first := true
	for i, c := range cases {
		if c.def {
			continue // emitted last, as the else
		}
		e.ind()
		if !first {
			e.emit("else ")
		}
		first = false
		if c.send {
			e.emit("if (" + offered + " && " + chanOfferedCName(c.elem) + "(" + c.ch + ", " + mine + ")) {\n")
			e.indent++
			e.ind()
			e.emit(offered + " = 0;\n")
			e.ind()
			e.emit(done + " = 1;\n") // set before the body, so a break in it is the user's
			for _, st := range c.body {
				e.emitStatement(st.ast)
			}
			e.indent--
			e.ind()
			e.emit("}\n")
			continue
		}
		tmp := tmps[i]
		e.chanTryRecvElems[c.elem] = true
		// An array temporary is already a pointer where one is wanted.
		addr := "&"
		if _, isArr := e.namedArrays[c.elem]; isArr {
			addr = ""
		}
		guard := ""
		if tryRecv != "" {
			guard = tryRecv + " && "
		}
		e.emit("if (" + guard + chanTryRecvCName(c.elem) + "(" + c.ch + ", " + addr + tmp + ")) {\n")
		e.indent++
		if !hasDefault {
			e.ind()
			e.emit(done + " = 1;\n") // set before the body, so a break in it is the user's
		}
		if c.target.name != "" {
			if a, isArr := e.namedArrays[c.elem]; isArr {
				// An ARRAY element is copied out of the clause's temporary: C cannot
				// assign one, and the clause's variable is a value of its own, as it
				// is for every other element type.
				if c.declare {
					e.locals[c.target.name] = c.elem
					e.emitArrayCopy(c.target.name, tmp, a)
				} else {
					e.includes["string.h"] = true
					e.ind()
					e.emit("memcpy(" + e.varRef(c.target.name) + ", " + tmp + ", sizeof(" +
						e.varRef(c.target.name) + "));\n")
				}
			} else {
				// The same store a multiple assignment writes, so a clause may receive
				// into a field or an element -- `case b.v = <-ch:` -- as the plain
				// assignment `b.v = <-ch` always could.
				e.emitStore(c.target, c.declare, c.elem, tmp)
			}
		}
		for _, st := range c.body {
			e.emitStatement(st.ast)
		}
		e.indent--
		e.ind()
		e.emit("}\n")
	}
	for _, c := range cases {
		if !c.def {
			continue
		}
		e.ind()
		if !first {
			e.emit("else ")
		}
		e.emit("{\n")
		e.indent++
		for _, st := range c.body {
			e.emitStatement(st.ast)
		}
		e.indent--
		e.ind()
		e.emit("}\n")
	}
	if !hasDefault {
		e.ind()
		e.emit("if (!" + done + ") { _waitx(1); }\n")
	}
	e.indent--
	e.ind()
	if hasDefault {
		e.emit("} while (0);\n")
	} else {
		e.emit("}\n")
	}
	e.indent--
	e.ind()
	e.emit("}\n")
}

// chanOfferCName, chanOfferedCName and chanWithdrawCName name the three helpers a
// select's send clause needs: offer a value, ask whether it was taken, take it back.
func chanOfferCName(elem string) string    { return "ogo_chan_offer_" + sanitizeElem(elem) }
func chanOfferedCName(elem string) string  { return "ogo_chan_offered_" + sanitizeElem(elem) }
func chanWithdrawCName(elem string) string { return "ogo_chan_withdraw_" + sanitizeElem(elem) }

// peekReady tests whether any receive clause has a value waiting, as a bare read of
// each cell's flag. It only decides whether to take the offer back and try, so being
// wrong costs a round rather than correctness: a false positive withdraws and offers
// again, a false negative waits one more turn. It is empty when a send clause stands
// alone, where there is nothing an offer could be in the way of.
func peekReady(cases []selectCase) string {
	var b strings.Builder
	for _, c := range cases {
		if c.def || c.send {
			continue
		}
		if b.Len() != 0 {
			b.WriteString(" || ")
		}
		b.WriteString(c.ch + "->full")
	}
	return b.String()
}

// selectClause reads one CommClause into a selectCase.
func (e *emitter) selectClause(n Node) (selectCase, bool) {
	var c selectCase
	for k := range it(n.ast) {
		switch k.sym {
		case CommHead:
			for h := range it(k.ast) {
				switch {
				case h.sym == 0 && e.f.ch(h.tok) == DEFAULT:
					c.def = true
				case h.sym == CommOp:
					if !e.selectCommOp(h, &c) {
						return c, false
					}
				}
			}
		case Statement:
			c.body = append(c.body, k)
		}
	}
	return c, true
}

// selectCommOp reads a CommOp: a bare `<-ch`, a receive into a target (`x = <-ch`
// or `x := <-ch`), or a send (`ch <- v`).
//
// All three are a head, an arrow and an expression, and the assignment operator is
// what tells them apart: with one, the head is the target and the expression names
// the channel; without one, the head names the channel and the expression is the
// value.
func (e *emitter) selectCommOp(n Node, c *selectCase) bool {
	var head, post, bare Node
	hasHead, hasPost, hasBare := false, false, false
	for k := range it(n.ast) {
		switch k.sym {
		case AssignHead:
			head, hasHead = k, true
		case PostfixComm:
			post, hasPost = k, true
		case Expression:
			bare, hasBare = k, true
		}
	}
	if !hasHead && hasBare {
		return e.selectChan(bare, c) // `case <-ch:`
	}
	if !hasHead || !hasPost {
		e.fail("only a receive, send or default clause is supported in select yet")
		return false
	}
	var value Node
	var chain []Node
	assigns, hasValue := false, false
	for q := range it(post.ast) {
		switch {
		case q.sym == Selector, q.sym == Index:
			chain = append(chain, q)
		case q.sym == 0 && e.f.ch(q.tok) == DEFINE:
			assigns, c.declare = true, true
		case q.sym == 0 && e.f.ch(q.tok) == ASSIGN:
			assigns = true
		case q.sym == Expression:
			value, hasValue = q, true
		}
	}
	if !hasValue {
		e.fail("only a receive, send or default clause is supported in select yet")
		return false
	}
	if assigns {
		c.target = assignTarget{name: e.soleIdent(head.ast), stars: e.derefStars(head.ast), chain: chain}
		return e.selectChan(value, c)
	}
	c.send, c.val = true, value
	if len(chain) != 0 {
		// `case ports.tx <- v:` -- the channel is a FIELD of the head. A channel is a
		// pointer to its cell, so the field access is what names it.
		return e.selectChanField(head, chain, c)
	}
	return e.selectChan(head, c)
}

// selectChan resolves the channel a clause polls: a variable, or a field of one --
// chanOperand answers for both, a channel being a pointer either way.
func (e *emitter) selectChan(n Node, c *selectCase) bool {
	elem, text, ok := e.chanOperand(n.ast)
	if !ok {
		e.fail("a select clause needs a channel operand: a variable or a field of one")
		return false
	}
	c.ch, c.elem = text, elem
	return true
}

// selectChanField resolves the channel of a SEND clause written on a field,
// `case ports.tx <- v:`. The head and the selectors arrive separately there, since
// the clause's own grammar keeps them apart, so they are rejoined here.
func (e *emitter) selectChanField(head Node, chain []Node, c *selectCase) bool {
	base := e.soleIdent(head.ast)
	var fields []string
	for _, step := range chain {
		if step.sym != Selector {
			e.fail("a select send clause takes a channel variable or a field of one")
			return false
		}
		fld := e.soleIdent(step.ast)
		if fld == "" {
			e.fail("a select send clause takes a channel variable or a field of one")
			return false
		}
		fields = append(fields, fld)
	}
	ct, ok := e.fieldType(base, fields)
	if base == "" || !ok || !e.isChanCType(ct) {
		e.fail("a select send clause takes a channel variable or a field of one")
		return false
	}
	c.ch, c.elem = e.fieldAccessC(base, fields), e.chanElemOfCType(ct)
	return true
}

// pkgInitCName is the synthesized function that performs package initialization:
// the variable initializers C cannot express at file scope, the channels whose
// locks must be acquired before anything uses them, and the user's own init().
const pkgInitCName = "ogo_pkg_init"

// zeroInitC is the C initializer for a zero value of ctype: a brace for anything
// aggregate, 0 otherwise.
func (e *emitter) zeroInitC(ctype string) string {
	// The Builder is an aggregate the emitter builds rather than reads from a
	// declaration, so it is in no struct table; `var b Builder` is a zero Builder,
	// which writes nowhere until it is given a backing, and needs braces like any
	// other struct.
	//
	// A DEFINED type is whatever it is defined over, and only the underlying name
	// says so: `type List []int` is a slice header and needs the braces, while the
	// name "List" matches none of the tests below. Reading the written name alone
	// emitted `List l = 0;` for `var l List` -- a scalar assigned to a struct, which
	// the target's C compiler refuses outright ("Expected multiple values"), so a
	// variable of a named slice type could not be declared without an initializer at
	// all. The written name is still tested first, since a type that IS one of these
	// has no underlying to resolve to.
	aggregate := func(ct string) bool {
		return e.isStruct(ct) || ct == cString || e.isSliceCType(ct) || ct == "ogo_builder"
	}
	if aggregate(ctype) || aggregate(e.underlyingCType(ctype)) {
		return "{0}"
	}
	return "0"
}

// zeroBraceC is zeroInitC written out in full: every field of a struct, with an
// array field's extents brace-nested. "{0}" is C's universal zero only at the top
// level of an initializer; nested inside one, an aggregate sub-object initialized
// by a bare 0 draws -Wmissing-braces from the host compiler and, past one array
// dimension, defeats flexcc outright. A struct cannot contain itself -- the
// checker refuses a recursive type -- so this terminates.
func (e *emitter) zeroBraceC(ctype string) string {
	fields, ok := e.structs[ctype]
	if !ok {
		return e.zeroInitC(ctype) // a string or slice header leads with a scalar
	}
	if len(fields) == 0 {
		return "{0}" // empty struct: zero its one hidden byte, not "{}" (invalid C)
	}
	var parts []string
	for _, f := range fields {
		parts = append(parts, e.zeroFieldC(f))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// zeroFieldC is the written-out zero of one struct field. An array's extents live
// on the declarator, so its zero is the element's wrapped in one brace per
// dimension.
func (e *emitter) zeroFieldC(f structField) string {
	z := e.zeroBraceC(f.ctype)
	if f.dim.bound != "" {
		for range f.dim.dims() {
			z = "{" + z + "}"
		}
	}
	return z
}

// hasArrayField reports whether a struct type holds a fixed-size array anywhere
// within it. flexcc cannot copy such a struct by value: `y = x` fails with
// "Unable to multiply assign this target", naming C the user never wrote.
//
// The predicate is deliberately coarser than the bug. What actually trips flexcc
// depends on its own layout decisions -- `struct { int a[3]; int n; }` fails while
// `struct { int a[2]; int n; }`, `struct { int m[2][3]; }` and a struct merely
// *containing* the failing one all copy fine -- so mirroring it would mean
// encoding a backend heuristic nobody can state. Every array-free struct copies
// correctly, at every size and shape tried, so "holds an array" is a safe
// over-approximation that leaves the common case untouched.
func (e *emitter) hasArrayField(ctype string) bool {
	for _, f := range e.structs[ctype] {
		if f.dim.bound != "" || e.hasArrayField(f.ctype) {
			return true
		}
	}
	return false
}

// emitStructCopy emits a copy of an array-holding struct as a memcpy, the one form
// flexcc lowers correctly (see hasArrayField). dst is a C lvalue; src is the
// right-hand side, which must be addressable -- taking its address is how the copy
// is expressed. A composite literal is not addressable, so it initializes a
// temporary first, initialization being the case that does work.
func (e *emitter) emitStructCopy(dst, ctype string, src []int32) {
	e.includes["string.h"] = true
	from := ""
	switch name, lit, ok := e.soleCompositeLit(src); {
	case ok:
		tmp := e.newTmp()
		fixups := e.captureLitFixups(func() {
			e.ind()
			e.emit(ctype + " " + tmp + " = ")
			e.emitCompositeLit(name, lit, true)
			e.emit(";\n")
		})
		e.flushLitFixups(tmp, fixups)
		from = tmp
	default:
		from = e.exprC(src)
	}
	// sizeof takes the type, not the destination: an indexed destination carries a
	// bounds check, and naming it twice would repeat that in the source even though
	// C leaves a sizeof operand unevaluated.
	e.ind()
	e.emit("memcpy(&" + dst + ", &" + from + ", sizeof(" + ctype + "));\n")
}

// checkStructCopySrc reports whether a struct-copy source can have its address
// taken, which is what memcpy needs. Only a call cannot be, and a function that
// returns such a struct is already refused where it is declared
// (refuseArrayStructABI), so this is a backstop that keeps the lowering from
// emitting "&f(...)" if that report is ever bypassed.
func (e *emitter) checkStructCopySrc(ctype string, src []int32) bool {
	kids, ok := e.soleFactor(src)
	if !ok {
		return true // not a bare factor: an operator chain, which is not a struct
	}
	recv, suffix, isCall := e.factorCall(kids)
	if !isCall {
		return true
	}
	// A CONVERSION is spelled like a call and is not one: `Arr2(x)` yields a
	// temporary this frame owns, which has an address, so the copy below is the
	// ordinary one. Without this it was reported as a copy "from a call" of an
	// operand that is a variable, which named the wrong thing entirely.
	if _, used, isConv := e.convChainHead(recv, suffix); isConv && used == len(suffix) {
		return true
	}
	e.fail("cannot copy %s from a call: it holds an array, which the target's C compiler cannot return by value", ctype)
	return false
}

// captureC renders whatever emit writes to C text instead of the output stream.
// It is what lets a lowering that rewrites a statement -- memcpy needs its
// destination as a string -- reuse the emitters that otherwise stream their output.
func (e *emitter) captureC(emit func()) string {
	saved, savedIndent := e.w, e.indent
	var b bytes.Buffer
	e.w, e.indent = &b, 0
	emit()
	e.w, e.indent = saved, savedIndent
	return b.String()
}

// exprC renders an expression to C text instead of the output stream, for the
// statements collected into the package initializer.
func (e *emitter) exprC(ast []int32) string {
	return e.captureC(func() { e.emitExpr(ast) })
}

// pkgInitStep is one variable's worth of package initialization: the statements
// that run for it, what it initializes, and the package variables its initializer
// reads. The steps are ordered by those dependencies before they are emitted, which
// is what makes `var a = b + 1` work with b declared below it -- Go initializes a
// package's variables in dependency order, not in source order.
//
// The statements of a step travel together: an initializer that hoists a temporary
// out of itself puts the temporary in the same step as the assignment that reads
// it, so ordering cannot separate the two.
type pkgInitStep struct {
	target string
	deps   []string
	stmts  []string

	// srcName and pos are what a diagnostic about this step says: the variable as
	// the program spells it, and where. The rendered position is kept rather than a
	// token, since a cycle is reported after every file has been walked and there is
	// no longer a current file to resolve one against.
	srcName string
	pos     string
}

// deferPkgInit records a statement to run at package initialization, as a step of
// its own that depends on nothing and so keeps its place.
func (e *emitter) deferPkgInit(stmt string) {
	e.pkgInit = append(e.pkgInit, pkgInitStep{stmts: []string{stmt}})
}

// pkgInitAssign records a package variable's initialization at run time, the
// assignment C forbids in a file-scope initializer.
//
// A temporary the initializer hoists out of itself becomes a package-init statement
// of its own, placed ahead of the assignment. Here there is no enclosing statement
// for emitStatement to put it before, and without this the temporary is referenced
// and never declared -- which is what `var g = mk().y` did.
func (e *emitter) pkgInitAssign(target, srcName string, initExpr []int32) {
	saved := e.prologue
	e.prologue = nil
	text := e.exprC(initExpr)
	pro := e.prologue
	e.prologue = saved
	step := pkgInitStep{
		target:  target,
		deps:    e.globalRefs(initExpr),
		srcName: srcName,
		pos:     e.astPos(initExpr),
	}
	for _, line := range pro {
		step.stmts = append(step.stmts, strings.TrimSuffix(line, "\n"))
	}
	step.stmts = append(step.stmts, target+" = "+text+";")
	e.pkgInit = append(e.pkgInit, step)
}

// astPos renders the source position an AST begins at, for a diagnostic reported
// after the walk has moved on: a token index alone would need the file it came from,
// and by then there is no current one.
func (e *emitter) astPos(ast []int32) string {
	for n := range it(ast) {
		return fmt.Sprintf("%v", e.f.tok(n.Pos()).Position())
	}
	return ""
}

// globalRefs names every package-level variable an initializer could read, as the
// C names they are emitted under. It is deliberately generous -- every identifier
// in the expression, mangled into this package -- because a name that turns out not
// to be a package variable of this package matches no step and is ignored by the
// ordering. Mangling has to happen here, while the file being emitted says which
// package that is.
func (e *emitter) globalRefs(ast []int32) []string {
	var out []string
	var walk func([]int32)
	walk = func(a []int32) {
		for n := range it(a) {
			switch {
			case n.sym == Selector:
				// The member of `x.f` is a FIELD or a METHOD name, not a variable,
				// and neither is the type in `x.(T)`. Reading one as a reference
				// invented dependencies -- harmless while the list only ORDERED the
				// initializers, and not once it also decides whether they cycle:
				// `var a = s.a` was reported as "a refers to itself". The base `x`
				// is not in here; it is the identifier this suffix hangs off.
				continue
			case n.sym == Element:
				// A keyed element's KEY names a field or a constant index, so only
				// the VALUE reads anything: `S{q: 1}` refers to no variable q.
				val := Node{}
				for c := range it(n.ast) {
					if c.sym == ElementValue {
						val = c
					}
				}
				if val.sym != 0 {
					walk(val.ast)
					continue
				}
				walk(n.ast)
			case n.sym != 0:
				walk(n.ast)
			case e.f.ch(n.tok) != IDENT:
			default:
				if gn := e.globalC(e.src(n.tok)); !slices.Contains(out, gn) {
					out = append(out, gn)
				}
			}
		}
	}
	walk(ast)
	return out
}

// staticInitOK reports whether a package variable's initializer is a constant
// expression, which C requires of a file-scope initializer. An integer literal is,
// and so is a name the checker folded to one. Anything else -- a call, a reference
// to another variable, arithmetic over one -- is not, and C rejects it outright
// ("initializer element is not constant"), so it is assigned at package
// initialization instead.
//
// A composite literal qualifies when every element does, because at file scope it
// is emitted as a brace initializer (see emitCompositeLit), and a brace of
// constants is constant. Deferring one instead would be both wasteful -- the
// variable is zeroed and then overwritten with the same values at startup -- and,
// for a struct with an array field, broken: the deferred form is an assignment
// from a compound literal, which flexcc cannot lower.
func (e *emitter) staticInitOK(initExpr []int32) bool {
	if _, lit, ok := e.soleCompositeLit(initExpr); ok {
		return e.staticLitElementsOK(lit)
	}
	// An array or slice literal, which a struct literal may hold as an element and
	// which is as constant as what is inside it.
	if _, lit, ok := e.soleArrayLit(initExpr); ok {
		return e.staticLitElementsOK(lit)
	}
	tok, ok := e.soleToken(initExpr)
	if !ok {
		return false
	}
	switch e.f.ch(tok) {
	case INT, STRING:
		return true
	case IDENT:
		if _, isConst := e.foldedInt(e.src(tok)); isConst {
			return true
		}
		s := e.src(tok)
		return s == "true" || s == "false"
	}
	return false
}

// staticLitElementsOK reports whether every element of a composite literal is
// itself a constant expression. An element that elides its type arrives as a bare
// CompositeLit node rather than as an initializer's nodes, so it is recognised here
// rather than by staticInitOK.
func (e *emitter) staticLitElementsOK(lit Node) bool {
	for _, el := range compositeLitElements(lit) {
		if el.value.sym == CompositeLit {
			if !e.staticLitElementsOK(el.value) {
				return false
			}
			continue
		}
		if !e.staticInitOK(el.value.ast) {
			return false
		}
	}
	return true
}

// pkgInitDefs renders the synthesized initializer, or "" when there is nothing to
// do. It is emitted after the prototypes, since it calls user functions.
func (e *emitter) pkgInitDefs() string {
	if !e.needsPkgInit() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "static void %s(void) {\n", pkgInitCName)
	names := make([]string, len(e.pkgInit))
	deps := make([][]string, len(e.pkgInit))
	for i, st := range e.pkgInit {
		names[i], deps[i] = st.target, st.deps
	}
	order, cyclic := stableTopoOrderCycle(names, deps)
	e.reportInitCycle(names, deps, cyclic)
	for _, i := range order {
		for _, stmt := range e.pkgInit[i].stmts {
			fmt.Fprintf(&b, "\t%s\n", stmt)
		}
	}
	// The variable initializers run first, then init(), which is Go's order.
	for _, fn := range e.initFuncs {
		fmt.Fprintf(&b, "\t%s();\n", fn)
	}
	b.WriteString("}\n")
	return b.String()
}

// reportInitCycle reports a cycle among the package variables' initializers, which
// is what the ordering pass cannot place. Go refuses such a program -- there is no
// order that makes every initializer see the value it reads -- and this accepted it,
// leaving the variables in source order with whatever zeros that produced. specs.go
// said so, as a known gap; it no longer is.
//
// The trace is Go's, and it is worth the walk: which pair closes the ring is what a
// reader has to know, and a program with three variables in a cycle has three
// candidate edges.
func (e *emitter) reportInitCycle(names []string, deps [][]string, cyclic []int) {
	if len(cyclic) == 0 {
		return
	}
	// A step that assigns nothing (a channel cell, a deferred statement) provides no
	// name, so it cannot be part of a cycle; it only ends up here by depending on
	// something that is. The report is about the variables.
	at := map[string]int{}
	for _, i := range cyclic {
		if names[i] != "" && e.pkgInit[i].srcName != "" {
			at[names[i]] = i
		}
	}
	start := -1
	for _, i := range cyclic {
		if _, named := at[names[i]]; named {
			start = i
			break
		}
	}
	if start < 0 {
		return // nothing to name: leave it to whatever else the build reports
	}
	// Follow one edge at a time through the cycle until a variable repeats, which is
	// where the ring closes.
	path := []int{start}
	seen := map[int]bool{start: true}
	for cur := start; ; {
		next := -1
		for _, d := range deps[cur] {
			if j, isCyclic := at[d]; isCyclic {
				next = j
				break
			}
		}
		if next < 0 {
			break
		}
		path = append(path, next)
		if seen[next] {
			break
		}
		seen[next] = true
		cur = next
	}
	first := e.pkgInit[path[0]]
	if len(path) == 2 && path[0] == path[1] {
		e.fail("%s: initialization cycle: %s refers to itself", first.pos, first.srcName)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: initialization cycle for %s", first.pos, first.srcName)
	for i := 0; i+1 < len(path); i++ {
		from, to := e.pkgInit[path[i]], e.pkgInit[path[i+1]]
		fmt.Fprintf(&b, "\n\t%s: %s refers to %s", from.pos, from.srcName, to.srcName)
	}
	e.fail("%s", b.String())
}

// needsPkgInit reports whether the package has anything to initialize.
func (e *emitter) needsPkgInit() bool { return len(e.pkgInit) != 0 || len(e.initFuncs) != 0 }

// chanType recognises a channel type `chan T`, returning its element C type.
func (e *emitter) chanType(typeAST []int32) (elem string, ok bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != CHAN {
		return "", false
	}
	for _, n := range nodes {
		if n.sym == Type {
			// An ARRAY element takes the same typedef a slice's does. The rendezvous
			// cannot copy it BY VALUE -- C has no array assignment and a parameter of
			// a typedef'd array type miscompiles here -- so the cell holds the array
			// and the helpers take a pointer both ways; see chanRuntimeDefs.
			if name, isArr := e.arrayElemTypedef(n.ast); isArr {
				return name, true
			}
			if elem = e.cType(n.ast); elem == "" {
				return "", false
			}
			return elem, true
		}
	}
	return "", false
}

// funcTypePrefix names the typedefs minted for function types.
const funcTypePrefix = "ogo_functype"

// funcType maps a Type subtree of the shape `func Signature` to the C typedef that
// stands for it, minting one on first sight.
//
// C cannot spell a function pointer as a suffixable type string: the name goes in
// the middle of the declarator, `int (*g)(int)`, which the emitter's ctype-is-a-
// string model has nowhere to put. A typedef moves the declarator out of the way
// once and for all, so a function value is thereafter an ordinary C type name and
// every downstream form -- a local, a parameter, a result, a struct field, a slice
// element -- works with no further special case.
//
// Distinct written types that render the same C signature share one typedef, which
// is what makes `func(int) int` written twice mint once.
func (e *emitter) funcType(typeAST []int32) (name string, ok bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) != 2 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != FUNC || nodes[1].sym != Signature {
		return "", false
	}
	return e.funcTypeOfSig(nodes[1].ast)
}

// funcValueCType is the C type of a top-level function's name used as a value --
// the typedef for the function's own signature, minted on demand.
//
// The signature is looked up as C text rather than re-read from the AST, since the
// function may belong to another package, whose file the emitter has moved on from.
func (e *emitter) funcValueCType(cname string) (string, bool) {
	fv, ok := e.funcValueTypes[cname]
	if !ok {
		return "", false
	}
	return e.funcTypeFor(fv), true
}

// funcValueType is a function's type rendered as C: the key a typedef is minted
// under, the result types a call through it yields, and the parameter types. The
// last two are what the typedef depends on -- a function type may name a defined
// type, whose own typedef has to come first (see typedefUnit).
type funcValueType struct {
	key    string
	res    []string
	params []string
}

// funcSigCParts renders a Signature as the parts a function type is minted from.
func (e *emitter) funcSigCParts(sig []int32) funcValueType {
	_, resTypes := e.cSig(sig)
	paramTypes, _ := e.cParamTypes(sig)
	params := strings.Join(paramTypes, ", ")
	if params == "" {
		params = "void"
	}
	ret := "void"
	switch {
	case len(resTypes) == 1:
		ret = resTypes[0]
	case len(resTypes) > 1:
		// The shared result struct, which is what makes two functions of one
		// signature return one C type and so lets the signature have a typedef.
		ret = e.retStructNameOf(resTypes)
	}
	return funcValueType{key: ret + " (*)(" + params + ")", res: resTypes, params: paramTypes}
}

// funcTypeOfSig mints (or reuses) the typedef standing for a Signature, shared by
// the written type `func(...)...` and by a function name used as a value, so the
// two agree by construction.
func (e *emitter) funcTypeOfSig(sig []int32) (string, bool) {
	return e.funcTypeFor(e.funcSigCParts(sig)), true
}

// funcTypeFor returns the typedef standing for a C function-pointer signature,
// minting it on first sight. Distinct written types rendering the same C signature
// share one typedef, which is what makes `func(int) int` written twice mint once.
func (e *emitter) funcTypeFor(fv funcValueType) string {
	if name, ok := e.funcTypeNames[fv.key]; ok {
		return name
	}
	name := fmt.Sprintf("%s%d", funcTypePrefix, len(e.funcTypeNames))
	e.funcTypeNames[fv.key] = name
	e.funcTypeRet[name] = fv.res
	e.funcTypeParams[name] = fv.params
	// "ret (*)(params)" -> "typedef ret (*name)(params);"
	e.addTypedef(name, "typedef "+strings.Replace(fv.key, "(*)", "(*"+name+")", 1)+";\n",
		append(slices.Clone(fv.res), fv.params...)...)
	return name
}

// fieldDeclSuffix is the C declarator suffix a field needs: the extents of a fixed
// array field, and nothing at all otherwise. A zero arrDim's declSuffix is "[]",
// which is a declaration C rejects in a struct.
func fieldDeclSuffix(fld structField) string {
	if fld.dim.bound == "" {
		return ""
	}
	return fld.dim.declSuffix()
}

// anonStructType mints (or reuses) the typedef standing for an anonymous struct
// type. Go gives two of them the same identity when their fields match, so the
// typedef is keyed by the SHAPE and not by where it was written -- which is what
// makes a value of one assignable to a variable of the other, and what stops a
// program from minting a typedef per mention.
func (e *emitter) anonStructType(structAST []int32) string {
	fields := e.structFieldsOf(structAST)
	var key strings.Builder
	for _, fld := range fields {
		key.WriteString(fld.ctype + " " + fld.name + fieldDeclSuffix(fld) + ";")
	}
	if name, ok := e.anonStructNames[key.String()]; ok {
		return name
	}
	name := mangle(e.curPkgPrefix, fmt.Sprintf("ogo_anon%d", len(e.anonStructNames)))
	e.anonStructNames[key.String()] = name
	e.structs[name] = fields
	e.typeNames[name] = true

	deps := make([]string, 0, len(fields))
	text := e.captureC(func() {
		e.emit("typedef struct {")
		for _, fld := range fields {
			deps = append(deps, fld.ctype)
			e.emit(" " + fld.ctype + " " + e.fieldIdent(fld.name) + fieldDeclSuffix(fld) + ";")
		}
		if len(fields) == 0 {
			// As for a named empty struct: C rejects a member-less one.
			e.emit(" char _ogo_empty;")
		}
		e.emit(" } " + name + ";\n")
	})
	e.addTypedef(name, text, deps...)
	return name
}

// typedefUnit is one declaration of the typedef section: the C name it declares,
// the text declaring it, and the names that must be declared before it. The section
// is emitted in dependency order rather than in fixed groups (see orderTypedefs),
// which is what lets a function type name a defined type, a struct hold a slice of
// one, and a struct field be a struct declared further down the file.
type typedefUnit struct {
	name string
	text string
	deps []string
}

// addTypedef records one declaration of the typedef section. The variadic arguments
// are the C types the declaration writes, as it writes them; typedefDeps reduces
// them to the names whose own declaration has to come first.
func (e *emitter) addTypedef(name, text string, ctypes ...string) {
	e.typedefUnits = append(e.typedefUnits, typedefUnit{name: name, text: text, deps: e.typedefDeps(ctypes)})
}

// typedefDeps reduces C type texts to the names whose declaration must precede
// them.
//
// A pointer to a struct is the one use that does not depend on anything: every
// struct's forward declaration leads the section, and that is all `P*` needs. A
// pointer to anything else names a typedef, and C wants a typedef declared before
// even a pointer to it can be written -- which is why `[]Celsius` could not be
// emitted ahead of `typedef int Celsius;` and a struct-element slice could.
func (e *emitter) typedefDeps(ctypes []string) []string {
	var deps []string
	for _, ct := range ctypes {
		name := strings.TrimRight(ct, "* ")
		if name == "" || name == "void" {
			continue
		}
		if _, isStruct := e.structs[name]; isStruct && strings.Contains(ct, "*") {
			continue
		}
		if !slices.Contains(deps, name) {
			deps = append(deps, name)
		}
	}
	return deps
}

// orderTypedefs returns the units in an order where each follows what it depends
// on, keeping the order it was given wherever the dependencies allow: a unit only
// ever moves later, never earlier, so a program whose declarations already ordered
// themselves emits exactly the bytes it emitted before.
//
// A dependency on a name no unit declares is a name C already has -- int, int32_t,
// a struct reached through its forward declaration -- and is satisfied from the
// start. A cycle leaves units nothing can place; they are emitted in their original
// order rather than dropped, so the C compiler reports it and the program does not
// quietly lose a type.
func orderTypedefs(units []typedefUnit) []typedefUnit {
	names := make([]string, len(units))
	deps := make([][]string, len(units))
	for i, u := range units {
		names[i], deps[i] = u.name, u.deps
	}
	out := make([]typedefUnit, 0, len(units))
	for _, i := range stableTopoOrder(names, deps) {
		out = append(out, units[i])
	}
	return out
}

// stableTopoOrder returns an ordering of the items in which each follows what it
// depends on, keeping the given order wherever the dependencies allow: an item only
// ever moves later, never earlier, so a list that already ordered itself comes back
// unchanged. names[i] is what item i provides (empty if it provides nothing);
// deps[i] what it needs.
//
// A dependency on a name no item provides is satisfied from the start -- it is
// something that exists already. A cycle leaves its items in their original order
// rather than dropping them, so whatever reads the result reports the problem
// instead of quietly losing them.
func stableTopoOrder(names []string, deps [][]string) []int {
	order, _ := stableTopoOrderCycle(names, deps)
	return order
}

// stableTopoOrderCycle is stableTopoOrder with the items it could not place named
// too. They are exactly the ones in a dependency CYCLE, and whether that is an error
// is the caller's to decide -- the typedef ordering leaves it to the C compiler,
// while package initialization reports it as Go does.
func stableTopoOrderCycle(names []string, deps [][]string) (order, cyclic []int) {
	provides := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			provides[n] = true
		}
	}
	done := make(map[string]bool, len(names))
	out := make([]int, 0, len(names))
	rest := make([]int, len(names))
	for i := range rest {
		rest[i] = i
	}
	for len(rest) != 0 {
		var deferred []int
		for _, i := range rest {
			ready := true
			for _, d := range deps[i] {
				if provides[d] && !done[d] {
					ready = false
					break
				}
			}
			if !ready {
				deferred = append(deferred, i)
				continue
			}
			out = append(out, i)
			if names[i] != "" {
				done[names[i]] = true
			}
		}
		if len(deferred) == len(rest) {
			return append(out, deferred...), deferred // a cycle
		}
		rest = deferred
	}
	return out, nil
}

// isFuncCType reports whether a C type is one of the minted function-type typedefs.
// isFuncCType reports whether a C type name is one of the emitted function-type
// typedefs, following a chain of definitions to reach one: `type Fn func(int) int`
// is a function type wherever it is written, so a variable, parameter or field of
// it is called through rather than dispatched to.
func (e *emitter) isFuncCType(ctype string) bool {
	return strings.HasPrefix(e.underlyingCType(ctype), funcTypePrefix)
}

// needChan records that `chan elem` is used, so its typedef and helpers are
// emitted.
func (e *emitter) needChan(elem string) {
	// The send and receive helpers take and return the element BY VALUE, so an
	// element that cannot cross that boundary cannot be a channel's element either.
	// Without this the backend was reached and reported an internal error.
	e.refuseArrayStructABI(elem, "channel element")
	e.chanElems[elem] = true
	e.chanElemByName[chanCName(elem)] = elem
}

// chanElemOfCType names the element type of a channel C type, following a DEFINED
// type over one to what it stands for: `type Ch chan int` is its own C type now --
// which is what gives it a name to hang a method on -- and the element, the cell and
// the helpers are all keyed by the channel's own name.
func (e *emitter) chanElemOfCType(ctype string) string {
	if el, ok := e.chanElemByName[ctype]; ok {
		return el
	}
	return e.chanElemByName[e.underlyingCType(ctype)]
}

// isChanCType reports whether a C type is a channel cell, a defined type over one
// included -- what it is asked for is the representation, and a value of
// `type Ch chan int` is a channel.
func (e *emitter) isChanCType(ctype string) bool {
	return strings.HasPrefix(e.underlyingCType(ctype), chanTypePrefix)
}

// chanRuntimeDefs returns the typedef for `chan elem` plus the helpers the
// program actually reaches for: a send-only program never sees the receive, and
// only a select polls with tryrecv. Emitting the unused ones would be harmless
// except that they are `static` (see below), which makes an unused one a
// -Wunused-function warning the host test suite treats as a failure.
//
// Blocking is a poll with a _waitx(1) yield between attempts: a blocked cog
// cannot sleep, since there is no scheduler, and spinning on the Hub bus without
// yielding would starve the cogs doing real work.
//
// Each poll reads the volatile flag it is waiting on *before* asking for the
// lock, and only asks when the read says there is plausibly something to do. The
// authoritative check is still the one inside the lock, so the outer read is a
// hint and may be wrong either way: a false positive costs one acquire and
// release, a false negative costs one more turn round the loop.
//
// Testing first is what makes the rendezvous work, not an optimization. A loop
// that calls _locktry every turn re-takes the lock so quickly that the cog on the
// other side never wins it -- both sides live, neither progressing. It is a
// livelock in the polling loop, and it is timing-dependent, so it appears only
// once the loop is fast enough: with FCACHE lifting the loop into Cog RAM, a
// program with a few channels and a few goroutines would hang at a rendezvous.
// That was misread as an FCACHE miscompilation for a while, and builds carried
// --fcache=0 to avoid it; the flag is gone now (see internal/build) and the
// backoff is still one cycle. Raising the backoff instead also works -- 256
// cycles was enough for every case here -- but it paces the symptom, costs
// latency on every rendezvous, and leaves the threshold to be rediscovered by the
// next program that beats it.
//
// The helpers are deliberately `static` and NOT `static inline`. Inlined into a
// call argument -- `println(<-ch)` rather than `v := <-ch` -- flexcc miscompiles
// the rendezvous loop: sender and receiver both spin forever, each holding the
// other off, on hardware only. gcc compiles both shapes correctly, so the host
// tests cannot see it, and the board case above is what guards it. Do not re-add
// `inline` here; the call costs nothing next to the lock-and-yield loop it
// guards.
// chanTypedefDef is the cell struct and the channel type over it. It is a unit of
// the TYPEDEF section rather than of the helpers below, because a struct field may
// hold a channel and C wants the type declared before the struct that holds one.
// The helpers cannot move with it: they call ogo_panic and the P2 intrinsics.
func chanTypedefDef(elem string) string {
	return chanTypedefDefDim(elem, arrDim{}, false)
}

// chanTypedefDefDim is chanTypedefDef for an element that is an ARRAY: the cell's
// payload is declared with the element's own extents, `volatile int val[3]`, rather
// than through its typedef.
func chanTypedefDefDim(elem string, a arrDim, isArr bool) string {
	// The qualifier goes AFTER the element type, not before it. Written before, it
	// binds to what a POINTER element points at rather than to the field: `chan *P`
	// declared `volatile P* val`, which is a pointer to volatile P and a field that
	// is not volatile at all -- so the one word two cogs poll was the one the
	// compiler was free to cache, which is the opposite of what the rendezvous
	// needs. `P* volatile val` is the volatile pointer meant. The host compiler said
	// so ("initialization discards volatile qualifier") where the target's said
	// nothing.
	//
	// It reads the same for every other element: `int volatile val` is `volatile
	// int`, and an array's `int volatile val[3]` is an array of volatile int.
	member := elem + " volatile val"
	if isArr {
		member = a.elem + " volatile val" + a.declSuffix()
	}
	return fmt.Sprintf("typedef struct { int lock; volatile int full; volatile int taken; %[1]s; } %[2]s;\ntypedef %[2]s* %[3]s;\n",
		member, chanCellCName(elem), chanCName(elem))
}

func (e *emitter) chanRuntimeDefs(elem string) string {
	c, snd, rcv, ini := chanCName(elem), chanSendCName(elem), chanRecvCName(elem), chanInitCName(elem)
	var b strings.Builder
	if e.chanInitElems[elem] {
		fmt.Fprintf(&b, `static void %[5]s(%[1]s ch) {
	ch->lock = _locknew();
	if (ch->lock < 0) {
		ogo_panic("out of hardware locks");
	}
	ch->full = 0;
	ch->taken = 0;
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	sendParam, sendStore := elem+" v", "ch->val = v;"
	recvRet, recvSig, recvTake, recvOut := elem, "", elem+" v = ch->val;", "return v;"
	tryOut, tryStore := elem+"* out", "*out = ch->val;"
	if _, isArr := e.namedArrays[elem]; isArr {
		// The element crosses by POINTER in both directions: C has no array
		// assignment, and a parameter of a typedef'd array type miscompiles on this
		// target (doc/array-param-corrupts.c). The cell's payload is the array
		// itself, so both copies are a memcpy of its own size.
		e.includes["string.h"] = true
		// void*, not a pointer to the element: what the caller hands over is an
		// array, and a MULTI-DIMENSIONAL one decays to a pointer to its ROW --
		// `int (*)[3]`, not `int*` -- so naming the innermost element mismatches
		// every rank above one. The copy is a memcpy either way, which needs no
		// element type at all.
		sendParam, sendStore = "const void* v", "memcpy((void*)ch->val, v, sizeof ch->val);"
		recvRet, recvSig = "void", ", void* out"
		recvTake, recvOut = "memcpy(out, (const void*)ch->val, sizeof ch->val);", "return;"
		tryOut, tryStore = "void* out", "memcpy(out, (const void*)ch->val, sizeof ch->val);"
	}
	if e.chanSendElems[elem] {
		fmt.Fprintf(&b, `static void %[3]s(%[1]s ch, %[8]s) {
	int mine = 0; // always set below before the rendezvous loop reads it; the
	// initializer only quiets flexcc, whose flow analysis cannot prove the first
	// loop exits solely through the break that follows the assignment.
	while (1) { // wait for the cell to be free, then deposit
		if (!ch->full && _locktry(ch->lock)) {
			if (!ch->full) {
				mine = ch->taken;
				%[9]s
				ch->full = 1;
				_lockrel(ch->lock);
				break;
			}
			_lockrel(ch->lock);
		}
		_waitx(1);
	}
	while (1) { // rendezvous: wait until a receiver has taken *this* value
		int done = 0;
		if (ch->taken != mine && _locktry(ch->lock)) {
			done = ch->taken != mine;
			_lockrel(ch->lock);
		}
		if (done) {
			return;
		}
		_waitx(1);
	}
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem), sendParam, sendStore)
	}
	if e.chanTrySendElems[elem] {
		// The three halves a select's send clause needs, which the blocking send does
		// in one go: offer a value, ask whether it was taken, take it back.
		//
		// An offer is the blocking send's first phase without the wait -- it fails
		// when the cell is occupied, and the select simply tries again next round.
		// `mine` is the taken-count at the moment of the offer, and it is what makes
		// the two later questions answerable: after an offer the cell is in exactly
		// one of two states, ours-still-there (full, taken unchanged) or consumed
		// (not full, taken moved), so a withdrawal cannot race a receiver -- under
		// the lock it sees one or the other, never a middle.
		// The offer takes its value the way the blocking send does -- sendParam and
		// sendStore, so an ARRAY element crosses by pointer and is memcpy'd. Written
		// out as `%[2]s v` and `ch->val = v` it was a parameter of array type, which
		// miscompiles on this target, storing with an assignment C does not have: a
		// select with `case ch <- arr:` did not compile at all, though the blocking
		// `ch <- arr` always did.
		fmt.Fprintf(&b, `static int ogo_chan_offer_%[7]s(%[1]s ch, %[8]s, int* mine) {
	if (!ch->full && _locktry(ch->lock)) {
		if (!ch->full) {
			*mine = ch->taken;
			%[9]s
			ch->full = 1;
			_lockrel(ch->lock);
			return 1;
		}
		_lockrel(ch->lock);
	}
	return 0;
}
static int ogo_chan_offered_%[7]s(%[1]s ch, int mine) {
	int done = 0;
	if (ch->taken != mine && _locktry(ch->lock)) {
		done = ch->taken != mine;
		_lockrel(ch->lock);
	}
	return done;
}
static int ogo_chan_withdraw_%[7]s(%[1]s ch, int mine) {
	while (1) {
		if (_locktry(ch->lock)) {
			int ours = ch->full && ch->taken == mine;
			if (ours) {
				ch->full = 0;
			}
			_lockrel(ch->lock);
			return ours;
		}
		_waitx(1);
	}
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem), sendParam, sendStore)
	}
	if e.chanTryRecvElems[elem] {
		fmt.Fprintf(&b, `static int ogo_chan_tryrecv_%[7]s(%[1]s ch, %[8]s) {
	if (ch->full && _locktry(ch->lock)) {
		if (ch->full) {
			%[9]s
			ch->full = 0;
			ch->taken++;
			_lockrel(ch->lock);
			return 1;
		}
		_lockrel(ch->lock);
	}
	return 0;
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem), tryOut, tryStore)
	}
	if e.chanRecvElems[elem] {
		fmt.Fprintf(&b, `static %[8]s %[4]s(%[1]s ch%[9]s) {
	while (1) {
		if (ch->full && _locktry(ch->lock)) {
			if (ch->full) {
				%[10]s
				ch->full = 0;
				ch->taken++;
				_lockrel(ch->lock);
				%[11]s
			}
			_lockrel(ch->lock);
		}
		_waitx(1);
	}
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem), recvRet, recvSig, recvTake, recvOut)
	}
	return b.String()
}

// arrayRecvInit recognises `<-ch` for a channel whose element is an ARRAY, and
// answers with the element type, the channel's C text and the element's extents.
func (e *emitter) arrayRecvInit(ast []int32) (string, string, arrDim, bool) {
	// `<-ch` is a UnaryExpr, not a Factor: descending past it would drop the arrow.
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && nodes[0].sym != 0 && nodes[0].sym != UnaryExpr {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != UnaryExpr {
		return "", "", arrDim{}, false
	}
	n := nodes[0]
	elem, base, ok := e.recvOperand(n, slices.Collect(it(n.ast)))
	if !ok {
		return "", "", arrDim{}, false
	}
	a, isArr := e.namedArrays[elem]
	return elem, base, a, isArr
}

// hoistChanRecv binds a receive of an ARRAY element to a temporary of this frame,
// declared before the statement, and answers with its name.
func (e *emitter) hoistChanRecv(base, elem string, a arrDim) (string, bool) {
	if e.declInit || e.deferReplay >= 0 {
		return "", false
	}
	name := e.newTmp()
	e.prologue = append(e.prologue,
		a.elem+" "+name+a.declSuffix()+";\n",
		chanRecvCName(elem)+"("+base+", "+name+");\n")
	e.arrays[name] = a
	return name, true
}

// sliceTypedefDef returns the C typedef declaring the slice header for element
// type elem. It has two emission sites -- the typedef section in EmitC, and
// inline in structFieldsOf for a struct-element slice field -- so the shape is
// defined once here.
func sliceTypedefDef(elem string) string {
	return fmt.Sprintf("typedef struct { %s* ptr; int len; int cap; } %s;\n", elem, sliceCName(elem))
}

// sanitizeElem turns a C element type into an identifier fragment: a pointer's "*"
// becomes "_ptr", so []*Point -> ogo_slice_Point_ptr stays a valid C identifier.
func sanitizeElem(elem string) string { return strings.ReplaceAll(elem, "*", "_ptr") }

// sliceElemFromCName recovers the element C type from a slice type name -- the
// inverse of sliceCName ("ogo_slice_int" -> "int", "ogo_slice_Point_ptr" ->
// "Point*").
func sliceElemFromCName(ct string) string {
	return strings.ReplaceAll(strings.TrimPrefix(ct, sliceTypePrefix), "_ptr", "*")
}

// appendCName, tryappendCName and appendokCName name the per-element append
// helpers: the trapping ogo_append_<T>, the ok-form ogo_tryappend_<T>, and the
// { slice, ok } result struct ogo_appendok_<T> the ok form returns.
func appendCName(elem string) string    { return "ogo_append_" + sanitizeElem(elem) }
func tryappendCName(elem string) string { return "ogo_tryappend_" + sanitizeElem(elem) }
func appendokCName(elem string) string  { return "ogo_appendok_" + sanitizeElem(elem) }

// appendSliceCName and tryappendSliceCName name the SPREAD forms of the same two,
// `append(s, xs...)`: they take a whole slice rather than one value. A string
// spread onto a []byte -- Go's one mixed-type append -- has its own pair, since
// what it reads from is a string header and not a slice one.
func appendSliceCName(elem string) string    { return "ogo_appendslice_" + sanitizeElem(elem) }
func tryappendSliceCName(elem string) string { return "ogo_tryappendslice_" + sanitizeElem(elem) }

// appendStrCName and tryappendStrCName name `append(bs, s...)` for a []byte and a
// string. There is one of each: the element type is fixed at uint8_t.
const appendStrCName = "ogo_appendstr"
const tryappendStrCName = "ogo_tryappendstr"

// copyCName names the per-element helper for the copy builtin, ogo_copy_<T>.
func copyCName(elem string) string { return "ogo_copy_" + sanitizeElem(elem) }

// resliceCName names the per-element bounds-checking slice-expression helper,
// ogo_reslice_<T>. It is not ogo_slice_<T>, which is the header type itself.
func resliceCName(elem string) string { return "ogo_reslice_" + sanitizeElem(elem) }

// reslice3CName names its three-bound twin, which takes the capacity to set.
func reslice3CName(elem string) string { return "ogo_reslice3_" + sanitizeElem(elem) }

// sliceBoundsCheck is the test the two-bound reslice helpers make: Go requires
// 0 <= lo <= hi <= c, and each unsigned compare folds one pair's low and high
// tests, since a negative bound wraps past the limit it is compared with.
const sliceBoundsCheck = "\tif ((unsigned)hi > (unsigned)c || (unsigned)lo > (unsigned)hi) ogo_panic(\"slice bounds out of range\");\n"

// sliceBoundsCheck3 is the same chain with the third bound in it, 0 <= lo <= hi <=
// mx <= c.
const sliceBoundsCheck3 = "\tif ((unsigned)mx > (unsigned)c || (unsigned)hi > (unsigned)mx || (unsigned)lo > (unsigned)hi) ogo_panic(\"slice bounds out of range\");\n"

// clearCName names the per-element helper for the clear builtin, ogo_clear_<T>.
func clearCName(elem string) string { return "ogo_clear_" + sanitizeElem(elem) }

// minCName and maxCName name the per-type two-argument helpers for min and max.
func minCName(ct string) string { return "ogo_min_" + sanitizeElem(ct) }
func maxCName(ct string) string { return "ogo_max_" + sanitizeElem(ct) }

// printSliceCName and printlnSliceCName name the per-element slice print helpers
// that render a slice header as "[e0 e1 ...]" over the serial line -- the newline
// form appends a trailing '\n'. An array is printed through the same helpers by
// viewing it as a full-length slice header.
func printSliceCName(elem string) string   { return "ogo_print_slice_" + sanitizeElem(elem) }
func printlnSliceCName(elem string) string { return "ogo_println_slice_" + sanitizeElem(elem) }

// ogoPanicDef is the runtime panic: a best-effort diagnostic to the serial line, a
// short drain so it flushes, then a halt or -- in a release build -- a reboot. A
// debug halt (abort -> _Exit -> _cogstop) stops the offending cog for inspection; a
// release _reboot() restarts the board so an unattended device self-heals.
func ogoPanicDef(release bool) string {
	tail := "\tabort(); // -> _Exit -> _cogstop: halt the offending cog\n"
	if release {
		tail = "\t_reboot(); // restart the board (release: self-heal)\n"
	}
	return "static void ogo_panic(const char* msg) {\n" +
		"\tprintf(\"panic: %s\\n\", msg);\n" +
		"\tfflush(stdout); // abort discards a buffered message; a pipe buffers\n" +
		"\t_waitms(10); // let the message flush over the serial line first\n" +
		tail +
		"}\n"
}

// ogoBound bounds-checks an index: it returns i when 0 <= i < n, else panics. The
// unsigned compare folds the low and high checks (a negative i wraps to >= n).
const ogoBound = "static int ogo_bound(int i, int n) {\n" +
	"\tif ((unsigned)i >= (unsigned)n) ogo_panic(\"index out of range\");\n" +
	"\treturn i;\n" +
	"}\n"

// nilHelperDef is the guard for a DEREFERENCE of one pointer type: it returns p when
// non-nil, else panics. One is emitted per pointer type used.
//
// A single void* helper with a cast at the site was tried first and does not work:
// flexcc loses the value for a pointer to an ARRAY, so `po[0] = "hi"` through a
// *[1]string wrote nothing and the read printed an empty string, while the host
// compiler accepted the same C and behaved. Casting a void* back to a
// pointer-to-array typedef is the part it cannot follow, so no cast is used at all
// and the type is exact everywhere -- the same reason the slice, channel and
// equality helpers are per type here.
//
// Address zero on this target is ordinary Hub RAM rather than a trap, so without
// this a read through a nil pointer yields whatever lives at 0 and a WRITE stores
// into the boot area, both silently. Go panics for each.
func nilHelperDef(ptrType, name string) string {
	return "static " + ptrType + " " + name + "(" + ptrType + " p) {\n" +
		"\tif (p == 0) ogo_panic(\"nil pointer dereference\");\n" +
		"\treturn p;\n" +
		"}\n"
}

// nilHelperName is the guard's C name for a pointer type: "ogo_nil_int_ptr" for an
// int*, the star spelled the way sanitizeElem spells one.
func nilHelperName(ptrType string) string { return "ogo_nil_" + sanitizeElem(ptrType) }

// ogoNonzero guards a divisor: it returns b when non-zero, else panics.
const ogoNonzero = "static int ogo_nonzero(int b) {\n" +
	"\tif (b == 0) ogo_panic(\"integer divide by zero\");\n" +
	"\treturn b;\n" +
	"}\n"

// ogoNonzero64 is the 64-bit divisor guard. A single signed-long-long form serves
// both int64 and uint64: the divisor's bits pass through unchanged and the division
// context (the dividend's type) decides signedness, while the == 0 test is the same
// for either sign.
const ogoNonzero64 = "static long long ogo_nonzero64(long long b) {\n" +
	"\tif (b == 0) ogo_panic(\"integer divide by zero\");\n" +
	"\treturn b;\n" +
	"}\n"

// cIntWidths is the bit width of each integer C type this target emits, which is
// what a shift count is measured against.
var cIntWidths = map[string]int{
	"int8_t": 8, "uint8_t": 8,
	"int16_t": 16, "uint16_t": 16,
	"int": 32, "unsigned": 32, "int32_t": 32, "uint32_t": 32,
	"int64_t": 64, "uint64_t": 64, "uintptr_t": 32,
}

// narrowCType returns the C type an arithmetic expression's value has to be
// truncated to after C has computed it, or "" when none is needed.
//
// Go computes in the operands' own type; C promotes anything narrower than int to
// int and computes there, keeping the extra bits. So with `var a uint8 = 200`,
// `a * 3` is 88 in Go and 600 in C, and `-a` is 56 in Go and 4294967096 in C.
// Storing the result back into a narrow variable truncated it, which is why this
// only ever showed for a value that was used without being stored -- printed,
// passed, compared.
//
// Only a type narrower than 32 bits needs it: at 32 and above the C type is its own
// promoted type and wraps exactly where Go says it does.
func (e *emitter) narrowCType(ast []int32) string {
	ct, _ := e.inferCType(ast)
	return narrowOf(e.underlyingCType(ct), ct)
}

// narrowCTypeNode is narrowCType for a single expression node. The two differ in
// how they reach a type: an expression's ast is a LEVEL, whose children are typed
// as a sequence, while a node like the Factor `b[i]` has to be typed as a whole --
// typing its children instead reads `b`, the array, rather than its element.
func (e *emitter) narrowCTypeNode(n Node) string {
	ct, _ := e.inferNode(n)
	return narrowOf(e.underlyingCType(ct), ct)
}

func narrowOf(underlying, ct string) string {
	if w, ok := cIntWidths[underlying]; !ok || w >= 32 {
		return ""
	}
	return ct
}

// cUnsignedOf is the unsigned counterpart of a signed integer C type. A left shift
// is done in it, C leaving a signed overflow undefined where Go defines it to wrap.
var cUnsignedOf = map[string]string{
	"int8_t": "uint8_t", "int16_t": "uint16_t", "int": "unsigned",
	"int32_t": "uint32_t", "int64_t": "uint64_t",
}

// shiftHelperName is the C name of the guarded shift helper for a value type.
func shiftHelperName(op, ctype string) string {
	dir := "shl"
	if op == ">>" {
		dir = "shr"
	}
	return "ogo_" + dir + "_" + cIdent(ctype)
}

// shiftHelperDef defines a guarded shift, which is what makes a shift mean in the
// emitted C what it means in Go.
//
// Go defines a shift by a count at least as wide as the value's type: the result is
// 0, or -1 for an arithmetic right shift of a negative value. C leaves it undefined,
// and this target's compilers take the count modulo the width -- so `x << 40` on an
// int32 yielded `x << 8`. Go also panics on a negative count, where C is undefined
// again. Both are decided here, once per value type.
//
// A left shift is performed in the unsigned counterpart: Go defines the overflow to
// wrap, and C does not define a signed one at all.
func shiftHelperDef(op, ctype string, checks bool) string {
	bits := cIntWidths[ctype]
	name := shiftHelperName(op, ctype)
	// A negative count is a run-time panic in Go, so a checked build says so. An
	// unchecked one has no panic to raise and must still not shift by a negative
	// count, which C leaves undefined: comparing unsigned puts every negative count
	// past the width, so it yields what an enormous one does.
	body, cmp := "", fmt.Sprintf("n >= %d", bits)
	if checks {
		body = "\tif (n < 0) ogo_panic(\"negative shift amount\");\n"
	} else {
		cmp = fmt.Sprintf("(uint64_t)n >= %d", bits)
	}
	switch {
	case op == "<<":
		u := ctype
		if v, ok := cUnsignedOf[ctype]; ok {
			u = v
		}
		// The shifted value is bound to a variable before the cast back. flexcc
		// miscompiles a cast to a 64-bit type applied to a 64-bit EXPRESSION (see
		// doc/shift64-by-variable.c and the div helper, which routes around the same
		// fault), and "(int64_t)((uint64_t)v << n)" is exactly that shape: every
		// 64-bit left shift by a variable count came back wrong on the target while
		// the right shift, which needs no cast, was right. Casting a VARIABLE is
		// fine, so the temporary is the whole fix.
		body += fmt.Sprintf("\tif (%s) return 0;\n\t%s t = (%s)v << n;\n\treturn (%s)t;\n", cmp, u, u, ctype)
	case cUnsignedOf[ctype] != "": // a signed right shift keeps the sign
		body += fmt.Sprintf("\tif (%s) return v < 0 ? -1 : 0;\n\treturn v >> n;\n", cmp)
	default:
		body += fmt.Sprintf("\tif (%s) return 0;\n\treturn v >> n;\n", cmp)
	}
	return fmt.Sprintf("static %s %s(%s v, int64_t n) {\n%s}\n", ctype, name, ctype, body)
}

// divHelperName is the C name of the guarded division helper for a value type.
func divHelperName(op, ctype string) string {
	dir := "div"
	if op == "%" {
		dir = "mod"
	}
	return "ogo_" + dir + "_" + cIdent(ctype)
}

// divHelperDef defines a guarded signed division or remainder.
//
// Two operands are wrong in C and defined in Go. A zero divisor is a run-time panic
// in Go and undefined in C, which is what ogo_nonzero already answered. The other is
// the most negative value divided by -1: its quotient is not representable, so C
// leaves it undefined and traps on some hosts (SIGFPE), while Go defines the result
// to be that same most negative value, with a remainder of 0. Both need the dividend
// as well as the divisor, which is why this replaces the divisor-only guard.
//
// The negation is done in the unsigned counterpart: negating the most negative value
// overflows, which C does not define either.
func divHelperDef(op, ctype string, checks bool) string {
	name := divHelperName(op, ctype)
	u := cUnsignedOf[ctype]
	body := ""
	if checks {
		body += "\tif (b == 0) ogo_panic(\"integer divide by zero\");\n"
	}
	switch op {
	case "%":
		body += "\tif (b == -1) return 0;\n\treturn a % b;\n"
	default:
		// The negation is bound to a variable before it is converted back. The
		// target's C compiler miscompiles a cast to int64_t applied to a 64-bit
		// EXPRESSION -- the same cast of a variable is fine -- and yields a value
		// that varies from run to run.
		body += fmt.Sprintf("\tif (b == -1) { %s t = (%s)0 - (%s)a; return (%s)t; }\n\treturn a / b;\n", u, u, u, ctype)
	}
	return fmt.Sprintf("static %s %s(%s a, %s b) {\n%s}\n", ctype, name, ctype, ctype, body)
}

// needDiv records that a guarded division helper is used, so it is defined.
func (e *emitter) needDiv(op, ctype string) string {
	e.divHelpers[divHelperName(op, ctype)] = [2]string{op, ctype}
	if e.checks {
		e.needPanic()
	}
	return divHelperName(op, ctype)
}

// needShift records that a guarded shift helper is used, so it is defined.
func (e *emitter) needShift(op, ctype string) string {
	e.shiftHelpers[shiftHelperName(op, ctype)] = [2]string{op, ctype}
	if e.checks {
		e.needPanic()
	}
	e.includes["stdint.h"] = true
	return shiftHelperName(op, ctype)
}

// sortedKeys returns the keys of a set in deterministic (sorted) order, for stable
// emission of per-element-type typedefs and helpers.
func sortedKeys(m map[string]bool) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	slices.Sort(r)
	return r
}

// EmitC writes C source for the built package pkg to w. It is the walking
// skeleton of the OctoGo C backend, grown to a first computational subset: a
// `func main()` with local int variables, `for {}` loops, assignments and
// arithmetic, calls to the builtin p2 package (mapped to P2 intrinsics), and
// print/println (mapped to printf over the P2 serial line). Anything it does not
// yet understand produces an "emit:" error rather than wrong C, so the surface
// grows honestly.
//
// The traversal mirrors the checker (see it()/sourceFile/funcDecl in check.go):
// dispatch non-terminals on Node.sym, read terminals via File.tok/File.ch.
// EmitOption configures a build. The zero configuration emits no automatic runtime
// checks and halts the offending cog on a panic (abort -> _cogstop). The `ogo build`
// CLI enables checks by default (see internal/build); its --unchecked omits them and
// its --release reboots instead of halting.
type EmitOption func(*emitter)

// Checked emits automatic runtime bounds and divide-by-zero checks: an out-of-range
// index or a divide-by-zero calls ogo_panic rather than silently corrupting memory
// or yielding a garbage quotient. append's own capacity trap is always present,
// independent of this option (choose the s, ok = append form to avoid it).
func Checked() EmitOption { return func(e *emitter) { e.checks = true } }

// Release makes a panic reboot the board (_reboot) instead of halting the cog, so
// an unattended device self-heals. Diagnostics and checks are unaffected.
func Release() EmitOption { return func(e *emitter) { e.release = true } }

// TestEntry makes the named function the program's entry point instead of main,
// which is what a test binary is: the tests and the code under test, entered
// through a generated runner. A "main" the package under test declares is not
// emitted at all -- a test binary replaces it, and emitting it would collide.
func TestEntry(name string) EmitOption { return func(e *emitter) { e.testEntry = name } }

// TestFuncs names the test functions of a package: a function called Test<Name>
// taking one parameter and no results, which is the shape "ogo test" generates a
// runner for. Order is the source order of the files, so a run is reproducible.
func TestFuncs(p *Package) (out []string) {
	for _, f := range p.Files {
		if f == nil {
			continue
		}
		for n := range it(f.AST) {
			if n.sym != SourceFile {
				continue
			}
			for c := range it(n.ast) {
				if c.sym != TopLevelDecl {
					continue
				}
				for d := range it(c.ast) {
					if d.sym != FuncDecl {
						continue
					}
					if nm, ok := testFuncName(f, d); ok {
						out = append(out, nm)
					}
				}
			}
		}
	}
	return out
}

// testFuncName reports whether a FuncDecl is a test function, and its name. It must
// be a plain function (no receiver) called Test<Something> with exactly one
// parameter and no results; the parameter's type is checked by the compiler, which
// sees the generated runner pass a *testing.T.
func testFuncName(f *File, decl Node) (string, bool) {
	name := ""
	var sig Node
	for c := range it(decl.ast) {
		switch {
		case c.sym == Receiver:
			return "", false
		case c.sym == Signature:
			sig = c
		case c.sym == 0 && Symbol(f.tok(c.tok).Ch) == IDENT && name == "":
			name = f.tok(c.tok).Src()
		}
	}
	if !strings.HasPrefix(name, "Test") || len(name) == len("Test") || sig.sym != Signature {
		return "", false
	}
	params, results := 0, false
	for c := range it(sig.ast) {
		switch c.sym {
		case ParameterList:
			for d := range it(c.ast) {
				if d.sym == ParamDecl {
					params++
				}
			}
		case ResultList, Type:
			results = true
		}
	}
	if params != 1 || results {
		return "", false
	}
	return name, true
}

// reachableFiles returns the files of the main package and every package reachable
// through its imports, in dependency order: a package's imports precede it, and the
// main package's files come last. Each package appears once. This flattens the whole
// program into the single C translation unit the emitter produces (all reachable
// packages are compiled together, matching the no-separate-compilation model).
//
// An INTRINSIC package is skipped. p2 has source -- the checker reads real
// signatures and real constants from it -- but every declaration in it is
// substituted at the use: a function by its C intrinsic, a constant by its value.
// There is nothing to emit, and emitting it would declare symbols that do not exist.
func reachablePackages(main *Package) []*Package {
	var order []*Package
	seen := map[string]bool{}
	var visit func(p *Package)
	visit = func(p *Package) {
		if p == nil || p == noPkg || seen[p.ImportPath] || intrinsicImports[p.ImportPath] {
			return
		}
		seen[p.ImportPath] = true
		for _, f := range p.Files {
			for _, spec := range f.ImportSpecs {
				visit(spec.Pkg)
			}
		}
		order = append(order, p)
	}
	visit(main)
	return order
}

func EmitC(pkg *Package, w io.Writer, opts ...EmitOption) error {
	e := &emitter{includes: map[string]bool{}, funcRet: map[string][]string{}, funcSliceParams: map[string][]string{}, funcVariadic: map[string]int{}, nilHelpers: map[string]bool{}, funcArrayRet: map[string]arrDim{}, funcArrayParams: map[string][]arrDim{}, anonStructNames: map[string]string{}, methodValueTypes: map[string]funcValueType{}, methodValueOf: map[string]string{}, funcParams: map[string][]string{}, methodPtr: map[string]bool{}, globals: map[string]string{}, structs: map[string][]structField{}, namedTypes: map[string]bool{}, typeNames: map[string]bool{}, interfaceTypes: map[string]bool{}, ifaceMethods: map[string][]ifaceMethod{}, anonIfaceNames: map[string]string{}, anonIfaceMinted: map[string]bool{}, ifaceASTs: map[string][]int32{}, ifaceVTables: map[string]bool{}, namedUnderlying: map[string]string{}, namedArrays: map[string]arrDim{}, constInt: map[string]string{}, constStr: map[string]string{}, constUntyped: map[string]bool{}, arrays: map[string]arrDim{}, globalArrays: map[string]arrDim{}, sliceVars: map[string]string{}, globalSliceVars: map[string]string{}, chanElems: map[string]bool{}, chanInitElems: map[string]bool{}, chanSendElems: map[string]bool{}, chanRecvElems: map[string]bool{}, chanTryRecvElems: map[string]bool{}, chanTrySendElems: map[string]bool{}, chanElemByName: map[string]string{}, sliceElems: map[string]bool{}, sliceElemByName: map[string]string{}, appendElems: map[string]bool{}, tryappendElems: map[string]bool{}, appendSliceElems: map[string]bool{}, tryappendSliceEls: map[string]bool{}, appendokStructs: map[string]bool{}, copyElems: map[string]bool{}, resliceElems: map[string]bool{}, reslice3Elems: map[string]bool{}, clearElems: map[string]bool{}, minElems: map[string]bool{}, maxElems: map[string]bool{}, printSliceElems: map[string]bool{}, printlnElems: map[string]bool{}, switchBreakUsed: map[string]bool{}, labelBreak: map[string]string{}, labelContinue: map[string]string{}, labelUsed: map[string]bool{}, eqStructs: map[string]bool{}, eqArrays: map[string]arrDim{}, frameBacked: map[string]bool{}, frameHolder: map[string]string{}, crossParams: map[string][]leak{}, crossInto: map[string][]uint32{}, ifaceSummaries: map[string]ifaceSummary{}, retParams: map[string][]bool{}, funcValueOf: map[string]string{}, crossNames: map[string]string{}, initNames: map[string]string{}, funcValueTypes: map[string]funcValueType{}, funcTypeNames: map[string]string{}, funcTypeRet: map[string][]string{}, funcTypeParams: map[string][]string{}, retStructs: map[string]string{}, retStructByKey: map[string]string{}, shiftHelpers: map[string][2]string{}, divHelpers: map[string][2]string{}, deferReplay: -1, iota: -1}
	for _, opt := range opts {
		opt(e)
	}
	e.registerBuilder()

	// The whole program -- the main package and every package it imports,
	// transitively -- is emitted into one translation unit, in dependency order.
	// forEachFile runs a pass over every reachable file with e.curPkgPrefix set to
	// that file's package, so top-level symbols are mangled into their package's
	// namespace (see mangle) and cannot collide across packages.
	pkgs := reachablePackages(pkg)
	forEachFile := func(fn func()) {
		for _, p := range pkgs {
			e.curPkgPrefix = pkgPrefix(p.ImportPath)
			for _, f := range p.Files {
				e.f = f
				fn()
			}
		}
		e.curPkgPrefix = ""
	}

	// Record each import qualifier -> the imported package's C prefix, so a
	// `pkg.F(...)` call resolves into that package's namespace. A p2 import is
	// unresolved (noPkg) and stays on the intrinsic path.
	e.importQualifiers = map[string]string{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, spec := range f.ImportSpecs {
				if spec.Pkg != nil && spec.Pkg != noPkg {
					e.importQualifiers[spec.ImportQualifier] = pkgPrefix(spec.Pkg.ImportPath)
				}
			}
		}
	}

	// Pass -1: struct type declarations -> C typedefs, recorded in the struct
	// environment (for typing `var p T` and field accesses). Emitted first so a
	// later signature, result struct, or variable of struct type resolves. Every
	// struct's forward declaration (`typedef struct T T;`) is emitted first, across
	// all files, before any body, so a field may point to a struct declared later or
	// to a mutually-recursive one.
	// The struct forward declarations go in their own buffer so they can be emitted
	// ahead of the function typedefs, which may name a struct: `func(m *Machine)
	// bool` becomes `typedef _Bool (*ogo_functype0)(Machine*);`, and a POINTER to a
	// struct needs only the forward declaration, which is what this ordering gives
	// it. Written after them, it named a type C had not seen.
	// Every declaration of the typedef section is collected as a unit and emitted in
	// dependency order (see typedefUnit); the struct FORWARD declarations are the one
	// thing that always leads it, and depend on nothing, so they keep a buffer of
	// their own. e.w points at a scratch buffer while the units are collected, each
	// capturing its own text.
	var forwards, scratch bytes.Buffer
	e.w = &forwards
	// Constant VALUES first: a struct field's array bound may name one, and the
	// typedefs below are emitted before the constants themselves.
	forEachFile(func() { e.collectConstValues(e.f.AST) })
	forEachFile(func() { e.collectStructForwards(e.f.AST) })
	e.w = &scratch
	forEachFile(func() { e.collectStructs(e.f.AST) })
	// A defined type over a struct is that struct: `type Q P` indexes, is written as
	// a literal, converts and carries methods exactly as P does. Every one of those
	// asks e.structs for the fields, keyed by C type name, and Q was not in it --
	// which cost the whole family at once, field access included. Resolving here
	// rather than at each of those sites is also what makes declaration ORDER
	// irrelevant: `type Q P` may be written before P.
	for _, mn := range sortedKeys(e.namedTypes) {
		if _, ok := e.structs[mn]; ok {
			continue
		}
		if flds, ok := e.structs[e.underlyingCType(mn)]; ok {
			e.structs[mn] = flds
		}
	}

	// Pass 0: record each function's C result types in funcRet (for typing calls
	// in `x := f()` and destructuring `a, b := f()`), and emit a result-struct
	// typedef for each multi-result function (C has no multiple return, so a
	// function returning N>1 values returns a struct of N fields).
	forEachFile(func() { e.collectResults(e.f.AST) })

	// Pass 0.5: package-level constant declarations, emitted (in source order)
	// before the functions that use them and recorded in the global type
	// environment so a `x := CONST` short declaration can be typed.
	var globals bytes.Buffer
	e.w = &globals
	forEachFile(func() { e.emitPackageConsts(e.f.AST) })
	// Package-level variables follow the constants (so a variable's initializer may
	// fold a constant), each a file-scope `static` recorded in the global type
	// environment. Their TYPES are collected first, over every file, so an
	// initializer may name a variable declared BELOW it or in another file of the
	// package -- which Go's package block allows and this could not, the emitting
	// pass having typed each variable as it arrived in source order. Ordering the
	// INITIALIZERS was already done (see pkgInitStep); this is the other half.
	forEachFile(func() { e.collectPackageVarTypes(e.f.AST) })
	e.resolvePkgVarTypes()
	forEachFile(func() { e.emitPackageVars(e.f.AST) })

	// Pass 0.6: which parameters of which functions let a value escape the frame it
	// was chosen in -- by reaching another cog, or by being stored where the program
	// outlives that frame -- closed over the call graph. A call site cannot be
	// checked against a callee that has not been seen yet, so the whole program is
	// summarised before any body is emitted.
	//
	// It runs AFTER the package variables, not before: asking whether an assignment
	// targets one of them is what seeds the store half, and until they are emitted
	// the global environment is empty and the answer is always no.
	forEachFile(func() { e.collectCrossParams(e.f.AST) })
	e.closeCrossParams()

	// Pass 1: forward prototypes for user functions. C requires a declaration
	// before use, but OctoGo (like Go) does not order top-level declarations, so
	// a call may precede its definition; the prototypes make emission order
	// independent of source order.
	var protos bytes.Buffer
	e.w = &protos
	forEachFile(func() { e.emitPrototypes(e.f.AST) })

	// Pass 2: the function definitions themselves.
	var body bytes.Buffer
	e.w = &body
	e.wroteDecl = false
	forEachFile(func() { e.emitFileDecls(e.f.AST) })
	if e.err != nil {
		return e.err
	}

	// A channel's helpers call ogo_panic and the P2 lock/wait intrinsics, so both
	// must be requested before the include list is taken.
	if len(e.chanElems) != 0 {
		e.needPanic()
		e.includes["propeller2.h"] = true
	}
	// Assemble: header, sorted #includes, result-struct typedefs, prototypes,
	// then the definitions.
	incs := make([]string, 0, len(e.includes))
	for inc := range e.includes {
		incs = append(incs, inc)
	}
	slices.Sort(incs)

	var out bytes.Buffer
	out.WriteString("// Code generated by ogo. DO NOT EDIT.\n\n")
	for _, inc := range incs {
		fmt.Fprintf(&out, "#include <%s>\n", inc)
	}
	if len(incs) != 0 {
		out.WriteByte('\n')
	}
	// The clock the program asks for. The backend looks these two up BY NAME and
	// wants both as constants, so they are an enum rather than variables; finding
	// neither, it falls back to 160 MHz. Declared here, before anything runs, which
	// is what keeps the console readable: the serial divisor is derived from a
	// _clkfreq the backend can see, where a run-time _clkset would leave everything
	// written before it as line noise.
	if e.clock != nil {
		fmt.Fprintf(&out, "enum { _clkfreq = %d, _clkmode = %#08x };\n\n",
			e.clock.freq, e.clock.mode)
	}
	// One slice header typedef per distinct element type, and append's ok-form
	// result struct { slice, ok } per element type. Both are units of the typedef
	// section like the struct bodies and the function typedefs, and the dependency
	// order below is what places them: a header names its element, by pointer, so a
	// struct element needs only its forward declaration while a defined type needs
	// its typedef -- which is the split that used to be written out by hand here,
	// and got `[]Celsius` emitted ahead of `typedef int Celsius;` when the name in
	// hand was neither.
	for _, el := range sortedKeys(e.sliceElems) {
		e.addTypedef(sliceCName(el), sliceTypedefDef(el), el+"*")
	}
	// A channel's typedef belongs here rather than with its helpers: a struct field
	// may hold a channel, and C wants the type before the struct.
	for _, el := range sortedKeys(e.chanElems) {
		a, isArr := e.namedArrays[el]
		e.addTypedef(chanCName(el), chanTypedefDefDim(el, a, isArr), el)
	}
	// The { slice, ok } result struct is what EVERY ok form returns -- single value,
	// spread, or a string spread onto a []byte -- so it has a gate of its own. The
	// three helpers have theirs, which is what keeps a program using one of them
	// from carrying the other two as unused functions.
	for _, el := range sortedKeys(e.appendokStructs) {
		e.addTypedef(appendokCName(el),
			fmt.Sprintf("typedef struct { %s slice; int ok; } %s;\n", sliceCName(el), appendokCName(el)),
			sliceCName(el))
	}
	var typedefUnits bytes.Buffer
	for _, u := range orderTypedefs(e.typedefUnits) {
		typedefUnits.WriteString(u.text)
	}
	// The ogo_string typedef leads the typedef section (a struct, array, or result
	// field may be a string); scalar-element slice typedefs precede the struct
	// typedefs (a struct field may hold one), struct-element slices and the append
	// ok-form structs follow. The string print helpers follow the typedefs.
	if e.usesString || forwards.Len() != 0 || typedefUnits.Len() != 0 {
		if e.usesString {
			out.WriteString(stringTypedef)
		}
		out.Write(forwards.Bytes())
		out.Write(typedefUnits.Bytes())
		// The Builder typedef follows the string and byte-slice types it embeds.
		if e.usesBuilder {
			out.WriteString(builderTypedef)
		}
		out.WriteByte('\n')
	}
	if e.usesStringPrint {
		out.WriteString(stringHelpers)
		out.WriteByte('\n')
	}
	if e.usesStringPad {
		out.WriteString(stringPadHelper)
		out.WriteByte('\n')
	}
	if e.usesRunePrint {
		out.WriteString(runePrintHelper)
		out.WriteByte('\n')
	}
	// After runePrintHelper, which it calls.
	if e.usesRunePad {
		out.WriteString(runePadHelper)
		out.WriteByte('\n')
	}
	if e.usesHexPrint {
		out.WriteString(hexPrintHelper)
		out.WriteByte('\n')
	}
	if e.usesStringCmp {
		out.WriteString(stringCmpHelper)
		out.WriteByte('\n')
	}
	if e.usesRuneDecode {
		out.WriteString(runeDecodeHelper)
		out.WriteByte('\n')
	}
	if e.usesStringEq {
		out.WriteString(stringEqHelper)
		out.WriteByte('\n')
	}
	// Per-struct-type equality helpers (Go's struct ==). They follow ogo_string_eq
	// (a string field compares through it) and the struct typedefs. All are
	// forward-declared first, then defined, so a nested-struct field's helper need
	// not precede the outer one regardless of the map's iteration order.
	if len(e.eqStructs) > 0 {
		for _, ct := range sortedKeys(e.eqStructs) {
			fmt.Fprintf(&out, "static int %s(%s _ogo_l, %s _ogo_r);\n", structEqName(ct), ct, ct)
		}
		for _, ct := range sortedKeys(e.eqStructs) {
			out.WriteString(e.structEqDef(ct))
		}
		out.WriteByte('\n')
	}
	// Per-array-type equality helpers (Go's array ==). After the struct ones, which
	// an array of structs compares its elements through. Forward-declared first and
	// defined after, as the struct helpers are: a multi-dimensional array compares
	// its rows through the helper for one dimension less, and name order does not
	// put that one first ("ogo_eq_arr_2_2_int" sorts before "ogo_eq_arr_2_int").
	if len(e.eqArrays) > 0 {
		for _, name := range sortedArrayKeys(e.eqArrays) {
			out.WriteString(e.arrayEqSig(name, e.eqArrays[name]) + ";\n")
		}
		for _, name := range sortedArrayKeys(e.eqArrays) {
			out.WriteString(e.arrayEqDef(name, e.eqArrays[name]))
		}
		out.WriteByte('\n')
	}
	// Runtime helpers: the panic routine, then the per-element append helpers (the
	// trapping form and the ok form), after the typedefs they reference.
	var helperDefs bytes.Buffer
	if e.usesPanic {
		helperDefs.WriteString(ogoPanicDef(e.release))
	}
	if e.usesBound {
		helperDefs.WriteString(ogoBound)
	}
	for _, pt := range slices.Sorted(maps.Keys(e.nilHelpers)) {
		helperDefs.WriteString(nilHelperDef(pt, nilHelperName(pt)))
	}
	if e.usesNonzero64 {
		helperDefs.WriteString(ogoNonzero64)
	}
	if e.usesNonzero {
		helperDefs.WriteString(ogoNonzero)
	}
	shiftNames := make([]string, 0, len(e.shiftHelpers))
	for name := range e.shiftHelpers {
		shiftNames = append(shiftNames, name)
	}
	slices.Sort(shiftNames)
	for _, name := range shiftNames {
		oc := e.shiftHelpers[name]
		helperDefs.WriteString(shiftHelperDef(oc[0], oc[1], e.checks))
	}
	divNames := make([]string, 0, len(e.divHelpers))
	for name := range e.divHelpers {
		divNames = append(divNames, name)
	}
	slices.Sort(divNames)
	for _, name := range divNames {
		oc := e.divHelpers[name]
		helperDefs.WriteString(divHelperDef(oc[0], oc[1], e.checks))
	}
	// A channel's helpers call ogo_panic (out of locks) and the P2 lock and wait
	// intrinsics, so they follow the panic definition and pull in propeller2.h.
	for _, el := range sortedKeys(e.chanElems) {
		helperDefs.WriteString(e.chanRuntimeDefs(el))
	}
	for _, el := range sortedKeys(e.appendElems) {
		param, store := el+" v", "s.ptr[s.len] = v;"
		if a, isArr := e.namedArrays[el]; isArr {
			// NOT `%s v` for an array element: a parameter whose type is a TYPEDEF'D
			// array corrupts unrelated code on this target, silently and non-locally
			// -- doc/array-param-corrupts.c reduces it to thirty lines, and shows that
			// the spelled-out `int v[2]` and `int v[]` are both fine. A pointer to the
			// element costs nothing at the call site, an array argument decaying to
			// one anyway, and C has no array assignment so the store is a copy either
			// way.
			e.includes["string.h"] = true
			param, store = "const "+a.elem+"* v", "memcpy(s.ptr[s.len], v, sizeof(s.ptr[s.len]));"
		}
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s) {\n"+
			"\tif (s.len >= s.cap) {\n\t\togo_panic(\"append: out of capacity\");\n\t} else {\n"+
			"\t\t%s\n\t\ts.len++;\n\t}\n\treturn s;\n}\n",
			sliceCName(el), appendCName(el), sliceCName(el), param, store)
	}
	// The SPREAD forms, append(s, xs...). One memmove rather than a loop, which is
	// both shorter and correct where the two overlap -- `append(s, s...)` is legal
	// Go and copies a region onto one that may run into it. sizeof(*s.ptr) is the
	// element's size whatever it is, so an array element needs no separate form here
	// (the store is a copy either way, which is what the single-value helper spells
	// out by hand).
	for _, el := range sortedKeys(e.appendSliceElems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n"+
			"\tif (v.len > s.cap - s.len) {\n\t\togo_panic(\"append: out of capacity\");\n\t} else {\n"+
			"\t\tmemmove(s.ptr + s.len, v.ptr, (unsigned)v.len * sizeof(*s.ptr));\n"+
			"\t\ts.len += v.len;\n\t}\n\treturn s;\n}\n",
			sliceCName(el), appendSliceCName(el), sliceCName(el), sliceCName(el))
	}
	if e.usesAppendStr {
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n"+
			"\tif (v.len > s.cap - s.len) {\n\t\togo_panic(\"append: out of capacity\");\n\t} else {\n"+
			"\t\tmemmove(s.ptr + s.len, v.str, (unsigned)v.len);\n"+
			"\t\ts.len += v.len;\n\t}\n\treturn s;\n}\n",
			sliceCName("uint8_t"), appendStrCName, sliceCName("uint8_t"), cString)
	}
	// copy(dst, src): move min(len) elements and return the count. memmove, not
	// memcpy, because Go's copy allows dst and src to overlap.
	for _, el := range sortedKeys(e.copyElems) {
		fmt.Fprintf(&helperDefs, "static int %s(%s dst, %s src) {\n"+
			"\tint n = dst.len < src.len ? dst.len : src.len;\n"+
			"\tif (n > 0) { memmove(dst.ptr, src.ptr, (unsigned)n * sizeof(*dst.ptr)); }\n"+
			"\treturn n;\n}\n",
			copyCName(el), sliceCName(el), sliceCName(el))
	}
	// x[lo:hi]: check the bounds, then build the header from them. A call, rather
	// than the inline compound literal, so each bound is evaluated exactly once
	// however many of the three fields it appears in -- which is why the helper is
	// reached by a side-effecting bound even with the check itself compiled out.
	check, check3 := "", ""
	if e.checks {
		check, check3 = sliceBoundsCheck, sliceBoundsCheck3
	}
	for _, el := range sortedKeys(e.resliceElems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s* p, int c, int lo, int hi) {\n"+
			check+
			"\treturn (%s){p + lo, hi - lo, c - lo};\n}\n",
			sliceCName(el), resliceCName(el), el, sliceCName(el))
	}
	// x[lo:hi:mx]: the same, with the capacity the third bound sets.
	for _, el := range sortedKeys(e.reslice3Elems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s* p, int c, int lo, int hi, int mx) {\n"+
			check3+
			"\treturn (%s){p + lo, hi - lo, mx - lo};\n}\n",
			sliceCName(el), reslice3CName(el), el, sliceCName(el))
	}
	// The same for a string, whose header has no capacity field: c is its length.
	if e.usesResliceStr {
		helperDefs.WriteString("static ogo_string ogo_reslice_str(const char* p, int c, int lo, int hi) {\n" +
			check +
			"\treturn (ogo_string){p + lo, hi - lo};\n}\n")
	}
	// copy(dst []byte, src string): copy the string's bytes into the byte slice,
	// min(len) of them, returning the count. The bytes are text, so a plain memcpy.
	if e.usesCopyStr {
		fmt.Fprintf(&helperDefs, "static int ogo_copystr(%s dst, ogo_string src) {\n"+
			"\tint n = dst.len < src.len ? dst.len : src.len;\n"+
			"\tif (n > 0) { memcpy(dst.ptr, src.str, (unsigned)n); }\n"+
			"\treturn n;\n}\n", sliceCName("uint8_t"))
	}
	if e.usesBuilder {
		helperDefs.WriteString(builderHelpers)
	}
	// clear(s): zero every element (memset, since every zero value is all-zero
	// bytes), the length unchanged.
	for _, el := range sortedKeys(e.clearElems) {
		fmt.Fprintf(&helperDefs, "static void %s(%s s) {\n"+
			"\tif (s.len > 0) { memset(s.ptr, 0, (unsigned)s.len * sizeof(*s.ptr)); }\n"+
			"}\n",
			clearCName(el), sliceCName(el))
	}
	// The two-argument min and max helpers a variadic call folds over.
	// A string is ordered by the same helper "s < t" uses; everything else by C's
	// own comparison, which is why the helper is one line either way.
	minMaxCmp := func(ct, op string) string {
		if ct == cString {
			return "ogo_string_cmp(a, b) " + op + " 0"
		}
		return "a " + op + " b"
	}
	for _, ct := range sortedKeys(e.minElems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s a, %s b) { return %s ? a : b; }\n", ct, minCName(ct), ct, ct, minMaxCmp(ct, "<"))
	}
	for _, ct := range sortedKeys(e.maxElems) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s a, %s b) { return %s ? a : b; }\n", ct, maxCName(ct), ct, ct, minMaxCmp(ct, ">"))
	}
	for _, el := range sortedKeys(e.tryappendElems) {
		param, store := el+" v", "s.ptr[s.len] = v;" // see the append helper above
		if a, isArr := e.namedArrays[el]; isArr {
			e.includes["string.h"] = true
			param, store = "const "+a.elem+"* v", "memcpy(s.ptr[s.len], v, sizeof(s.ptr[s.len]));"
		}
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s) {\n\t%s r;\n"+
			"\tif (s.len >= s.cap) {\n\t\tr.slice = s;\n\t\tr.ok = 0;\n\t} else {\n"+
			"\t\t%s\n\t\ts.len++;\n\t\tr.slice = s;\n\t\tr.ok = 1;\n\t}\n\treturn r;\n}\n",
			appendokCName(el), tryappendCName(el), sliceCName(el), param, appendokCName(el), store)
	}
	// The ok forms of the spread. Either the whole spread fits or nothing is
	// appended: a partial append would leave the caller with no way to say what
	// happened, ok being one bool for the call.
	for _, el := range sortedKeys(e.tryappendSliceEls) {
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n\t%s r;\n"+
			"\tif (v.len > s.cap - s.len) {\n\t\tr.slice = s;\n\t\tr.ok = 0;\n\t} else {\n"+
			"\t\tmemmove(s.ptr + s.len, v.ptr, (unsigned)v.len * sizeof(*s.ptr));\n"+
			"\t\ts.len += v.len;\n\t\tr.slice = s;\n\t\tr.ok = 1;\n\t}\n\treturn r;\n}\n",
			appendokCName(el), tryappendSliceCName(el), sliceCName(el), sliceCName(el), appendokCName(el))
	}
	if e.usesTryAppendStr {
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n\t%s r;\n"+
			"\tif (v.len > s.cap - s.len) {\n\t\tr.slice = s;\n\t\tr.ok = 0;\n\t} else {\n"+
			"\t\tmemmove(s.ptr + s.len, v.str, (unsigned)v.len);\n"+
			"\t\ts.len += v.len;\n\t\tr.slice = s;\n\t\tr.ok = 1;\n\t}\n\treturn r;\n}\n",
			appendokCName("uint8_t"), tryappendStrCName, sliceCName("uint8_t"), cString, appendokCName("uint8_t"))
	}
	// The per-element slice printers render "[e0 e1 ...]"; the newline form defers
	// to the plain one and adds a trailing '\n'. They reference the slice typedef
	// emitted above and <stdio.h> (pulled in wherever a print is emitted). The plain
	// helper is emitted whenever an element is printed either way (the newline form
	// calls it); the newline helper only when a println of that element occurs, so a
	// print with no matching println leaves no unused function behind.
	printElems := map[string]bool{}
	for el := range e.printSliceElems {
		printElems[el] = true
	}
	for el := range e.printlnElems {
		printElems[el] = true
	}
	for _, el := range sortedKeys(printElems) {
		fmt.Fprintf(&helperDefs, "static void %s(%s s) {\n"+
			"\tprintf(\"[\");\n"+
			"\tfor (int _i = 0; _i < s.len; _i++) {\n"+
			"\t\tif (_i) printf(\" \");\n"+
			"\t\t%s\n"+
			"\t}\n"+
			"\tprintf(\"]\");\n}\n",
			printSliceCName(el), sliceCName(el),
			sliceElemPrintf(el))
		if e.printlnElems[el] {
			fmt.Fprintf(&helperDefs, "static void %s(%s s) { %s(s); printf(\"\\n\"); }\n",
				printlnSliceCName(el), sliceCName(el), printSliceCName(el))
		}
	}
	if helperDefs.Len() != 0 {
		out.Write(helperDefs.Bytes())
		out.WriteByte('\n')
	}
	// The prototypes precede the globals: a package variable may be initialized
	// with a function, `var tick Fn = onTick`, and C wants that function declared
	// before the initializer names it. The other way round it was "'onTick'
	// undeclared here (not in a function)" whichever order the source wrote them
	// in. Nothing in a prototype can name a global -- a signature is types only --
	// so this direction has no counterpart to break.
	if protos.Len() != 0 {
		out.Write(protos.Bytes())
		out.WriteByte('\n')
	}
	if globals.Len() != 0 {
		out.Write(globals.Bytes())
		out.WriteByte('\n')
	}
	// The thunks and the static vtables. They follow the prototypes, which is all a
	// thunk needs of the method it calls, and precede the bodies, which is where the
	// tables are taken the address of.
	if e.vtables.Len() != 0 {
		out.Write(e.vtables.Bytes())
		out.WriteByte('\n')
	}
	// The functions lifted out of function literals. Discovered while walking the
	// bodies, so they can only be written now -- and before the goroutine
	// trampolines, one of which may be the cog entry point of a lifted literal.
	if len(e.liftedDefs) != 0 {
		for _, p := range e.liftedProtos {
			out.WriteString(p + ";\n")
		}
		out.WriteByte('\n')
		for _, d := range e.liftedDefs {
			out.WriteString(d)
			out.WriteByte('\n')
		}
	}
	// The goroutine pool and trampolines go after the prototypes -- a trampoline
	// calls a user function -- and before the bodies, whose `go` statements name
	// the argument structs declared here.
	if gd := e.goDefs(); gd != "" {
		out.WriteString(gd)
		out.WriteByte('\n')
	}
	// Cells for locally declared channels. Discovered while walking the bodies, so
	// they can only be written now, and they must precede the package initializer
	// that takes their locks.
	if len(e.chanCells) != 0 {
		for _, decl := range e.chanCells {
			out.WriteString(decl + "\n")
		}
		out.WriteByte('\n')
	}
	// The package initializer likewise calls user functions, so it follows the
	// prototypes too.
	if pd := e.pkgInitDefs(); pd != "" {
		out.WriteString(pd)
		out.WriteByte('\n')
	}
	// Assembly can FAIL: the package initializer's ordering reports a cycle here,
	// and the pieces written above call into the emitter as well. The error check
	// before pass 2 covers everything up to the bodies and nothing after them, so
	// without this a diagnostic raised while assembling was computed and dropped,
	// and the program compiled as though nothing had been said.
	if e.err != nil {
		return e.err
	}
	out.Write(body.Bytes())
	_, err := w.Write(out.Bytes())
	return err
}

type emitter struct {
	w io.Writer // body buffer during the walk
	f *File     // file currently being emitted, for token access

	// pkgVarPending holds the package variables whose type has to be INFERRED from
	// their initializer, gathered before any of them is emitted so one may name
	// another declared later. Each carries the file and package it was written in,
	// since inference reads both.
	pkgVarPending []pkgVarPending
	// constPreScan runs emitConstDecl for its folded VALUES only -- no C emitted,
	// no types resolved. See collectConstValues.
	constPreScan bool
	// foldConv lets the integer fold see through a conversion, `int32(4)`. Off for
	// the fold that RENDERS an expression, where a conversion's cast is part of the
	// emitted type. See constIntValue.
	foldConv        bool
	indent          int
	includes        map[string]bool
	funcRet         map[string][]string // user function / mangled method name -> C result types (empty=void), for typing calls
	funcSliceParams map[string][]string // same key -> per parameter, its C slice type or "", so a bare nil argument knows it is a slice header
	funcVariadic    map[string]int      // same key -> the position of a "...T" parameter, for the pack a call has to build
	funcArrayRet    map[string]arrDim   // same key -> the extents of an ARRAY result, handed back through a leading out parameter
	funcArrayParams map[string][]arrDim // same key -> the extents of each ARRAY parameter, which its C type cannot carry: arrayParamCType is a pointer to the element, so a [3]int and a [2]int parameter are the same `int*`
	// A function literal has no name, and C has no nested functions, so each one is
	// LIFTED to a file-scope function of a minted name and the expression becomes
	// that name. Collected while walking a body, so they can only be written out
	// once every body has been walked -- like the channel cells.
	liftedProtos []string
	liftedDefs   []string
	liftSeq      int
	// methodValueTypes: a method's C name -> its type AS A VALUE, which is its
	// signature without the receiver. Recorded with the signatures, since the
	// declaration may be in another package's file by the time a value is made.
	methodValueTypes map[string]funcValueType
	// methodValueOf: "<global>.<method>" -> the function already lifted for it, so
	// the same method value written twice mints one function.
	methodValueOf      map[string]string
	funcParams         map[string][]string      // same key -> its parameter C types, so a value handed to it is stored as the parameter's type
	methodPtr          map[string]bool          // mangled method name -> receiver is a pointer, for &/* adjustment at the call site
	globals            map[string]string        // package-level constant/variable name -> C type, for typing `x := g`
	structs            map[string][]structField // struct type name -> its fields, for typedefs, zero-init and field typing
	namedTypes         map[string]bool          // non-struct named type (e.g. `type Celsius int`) -> emitted as a typedef; may carry methods
	typeNames          map[string]bool          // every C type name this program declares, struct or not, for fieldIdent's collision check
	interfaceTypes     map[string]bool          // source names declared as an interface type
	ifaceMethods       map[string][]ifaceMethod // mangled interface name -> its methods, in declaration order: the vtable's slot order
	ifaceVTables       map[string]bool          // "<interface>|<concrete>" pairs a static vtable has been emitted for
	vtables            bytes.Buffer             // the thunks and static vtables those pairs produced
	namedUnderlying    map[string]string        // that typedef -> the C type it stands for, so a value of it is represented as what it is
	namedArrays        map[string]arrDim        // named array type (e.g. `type Row [3]int`) -> its dimensions, resolved wherever an array type is expected (see arrayDim)
	constInt           map[string]string        // integer-constant name -> its C literal value, for array bounds
	constStr           map[string]string        // string-constant name -> its decoded value, for folding string concatenation
	constUntyped       map[string]bool          // constant name -> it is UNTYPED, so it contributes no type to an expression it appears in (see exprUntyped)
	arrays             map[string]arrDim        // local array name -> element type and bound (reset per function)
	globalArrays       map[string]arrDim        // package-level array name -> element type and bound (persists across functions)
	sliceVars          map[string]string        // local slice name -> element C type, for `xs[i]` / len(xs) (reset per function)
	globalSliceVars    map[string]string        // package-level slice name -> element C type (persists across functions)
	pkgInit            []pkgInitStep            // the synthesized package initializer, emitted in dependency order
	initFuncs          []string                 // user init() functions, called after the variable initializers
	initNames          map[string]string        // init declaration position -> its numbered C name, so both passes agree
	goSites            []goSite                 // launched goroutines, one per `go` statement: each needs an argument struct and a trampoline
	chanElems          map[string]bool          // element C types that need an ogo_chan_<T> cell and helpers
	chanInitElems      map[string]bool          // element types whose channel init helper is reached
	chanSendElems      map[string]bool          // element types whose channel send helper is reached
	chanRecvElems      map[string]bool          // element types whose blocking receive helper is reached
	chanTryRecvElems   map[string]bool          // element types whose select tryrecv helper is reached
	chanTrySendElems   map[string]bool          // element types whose select send helpers (offer/offered/withdraw) are reached
	chanElemByName     map[string]string        // ogo_chan_<T> C type name -> its element C type
	funcValueTypes     map[string]funcValueType // top-level function C name -> its type as C text, for the name used as a value
	funcTypeNames      map[string]string        // C function-pointer signature -> the typedef minted for it
	funcTypeRet        map[string][]string      // that typedef -> the result C types a call through it yields
	funcTypeParams     map[string][]string      // that typedef -> its parameter C types, for marshalling a `go` through a value
	retStructs         map[string]string        // result-struct typedef name -> the result types it stands for
	retStructByKey     map[string]string        // those result types -> the typedef name, so one list answers alike every time
	typedefUnits       []typedefUnit            // the typedef section, in the order collected; emitted in dependency order
	anonStructNames    map[string]string
	anonIfaceNames     map[string]string  // method-set shape -> the minted name of an anonymous interface
	anonIfaceMinted    map[string]bool    // the minted names, so a message says the SHAPE rather than the name
	ifaceASTs          map[string][]int32 // interface name -> its body, for resolving an EMBEDDED name whatever the declaration order        // an anonymous struct's field shape -> its minted typedef, so identical ones are one type
	sliceElems         map[string]bool    // element C types that need an ogo_slice_<T> typedef
	sliceElemByName    map[string]string  // ogo_slice_<T> C type name -> its element C type; the forward direction mangles pointers, so the reverse is recorded, not derived
	appendElems        map[string]bool    // element C types needing the trapping ogo_append_<T> helper
	tryappendElems     map[string]bool    // element C types needing the ok-form ogo_tryappend_<T> helper + ogo_appendok_<T>
	appendSliceElems   map[string]bool    // element C types needing the spread ogo_appendslice_<T> helper
	tryappendSliceEls  map[string]bool    // element C types needing the spread ok-form ogo_tryappendslice_<T>
	appendokStructs    map[string]bool    // element C types needing the { slice, ok } ogo_appendok_<T> struct
	usesAppendStr      bool               // append(bs, s...) of a string: emit ogo_appendstr
	usesTryAppendStr   bool               // the ok form of the same: emit ogo_tryappendstr
	copyElems          map[string]bool    // element C types needing the ogo_copy_<T> helper for the copy builtin
	resliceElems       map[string]bool    // element C types needing the ogo_reslice_<T> helper, a bounds-checked slice expression
	reslice3Elems      map[string]bool    // element C types needing its three-bound twin, ogo_reslice3_<T>
	usesResliceStr     bool               // a string is sliced through the helper: emit ogo_reslice_str
	resliceCalled      bool               // a reslice helper call was just emitted, so a field read off it needs a temporary (see emitHeaderField)
	usesCopyStr        bool               // copy(dst []byte, src string) is used: emit the ogo_copystr helper
	usesBuilder        bool               // the Builder type is used: emit its typedef and method helpers
	importQualifiers   map[string]string  // import qualifier -> the imported package's C symbol prefix (resolved user packages, not p2)
	curPkgPrefix       string             // the C symbol prefix of the package whose file is currently being emitted ("" for main)
	clearElems         map[string]bool    // element C types needing the ogo_clear_<T> helper for the clear builtin
	minElems           map[string]bool    // C types needing the ogo_min_<T> helper for the min builtin
	maxElems           map[string]bool    // C types needing the ogo_max_<T> helper for the max builtin
	printSliceElems    map[string]bool    // element C types printed without a newline, needing the ogo_print_slice_<T> helper
	printlnElems       map[string]bool    // element C types printed with a newline, needing ogo_println_slice_<T> (which calls ogo_print_slice_<T>)
	defers             []deferredCall     // the current function's top-level defers, in source order, replayed LIFO before each return
	switchBreak        string             // goto target for a break in the current switch case (the if/else lowering has no C switch to break); "" means a plain C break -- a loop, or outside any switch
	switchBreakSeq     int                // counter minting unique switch-end labels
	switchBreakUsed    map[string]bool    // switch-end labels a break actually jumped to, so an unreferenced label is not emitted
	labelBreak         map[string]string  // source label -> C break-target label, for "break L" (a labeled for or switch)
	labelContinue      map[string]string  // source label -> C continue-target label, for "continue L" (a labeled for)
	labelUsed          map[string]bool    // C labels a labeled break/continue jumped to, so an unreferenced one is not emitted
	labelSeq           int
	retSeq             int                     // disambiguates a result-struct name two different result lists spell alike                      // counter minting unique labeled-loop break/continue labels
	pendingContLabel   string                  // the current labeled for's C continue target, for emitLoopBody to place at the body's end
	postContLabel      string                  // the enclosing loop's post-statement label, when its post cannot fit C's third clause
	pendingPost        func()                  // that loop's post statements, emitted after the label
	pendingSwitchLabel string                  // the source label of a labeled switch, for emitSwitch to bind to its end label
	deferBlockDepth    int                     // nesting inside if/for/switch bodies; a defer at depth > 0 needs a runtime flag
	deferReplay        int                     // slot being replayed, or -1: makes emitCallArgs read the captured temporaries
	iota               int                     // the current iota value while emitting a const spec's expression, or -1 outside one
	deferReplayArgs    []deferArg              // that slot's arguments, so emitCallArgs knows which were captured
	usesPanic          bool                    // ogo_panic is called: emit its definition and pull in its includes
	testEntry          string                  // the entry point of a test binary, replacing main (see TestEntry)
	usesBound          bool                    // ogo_bound is called: emit the index bounds-check helper
	nilHelpers         map[string]bool         // pointer types whose nil-dereference guard is called
	usesNonzero        bool                    // ogo_nonzero is called: emit the divide-by-zero-check helper
	usesNonzero64      bool                    // ogo_nonzero64 (64-bit divisor guard) is called
	shiftHelpers       map[string][2]string    // guarded shift helper name -> {operator, value C type}
	divHelpers         map[string][2]string    // guarded signed division helper name -> {operator, value C type}
	clock              *clockSetting           // a clock the program asks for, instead of the backend's 160 MHz default
	release            bool                    // release build: a panic reboots (_reboot) instead of halting the cog
	checks             bool                    // emit runtime bounds / divide-by-zero checks (set by Checked; ogo build enables it by default)
	locals             map[string]string       // current function's parameter/local name -> C type, for typing `x := y`
	curFunc            string                  // name of the function whose body is being emitted (for its result-struct type)
	pkgScope           bool                    // a package variable's initializer is being emitted, where this frame's storage does not exist
	curResultNames     []string                // current function's result C-variable names, for a bare "return" (naked return)
	curResultTypes     []string                // current function's result C types, for typing a `return nil` in a slice-returning function
	tmp                int                     // per-function counter for generated temporaries (destructuring)
	makeN              int                     // translation-unit counter for make() backing arrays
	wroteDecl          bool                    // a top-level definition has been emitted (drives blank-line separators)
	mainRet            bool                    // currently emitting main's body: a bare `return` yields `return 0;`
	declInit           bool                    // emitting a static initializer: a string literal must use a brace, not a compound literal
	usesString         bool                    // an ogo_string type/literal appears: emit stringTypedef
	usesStringPrint    bool                    // a string is printed: emit stringHelpers
	usesStringPad      bool                    // printf %s with a width: emit stringPadHelper
	usesRunePrint      bool                    // printf %c is used: emit runePrintHelper
	usesRunePad        bool                    // printf %c with a width: emit runePadHelper
	usesHexPrint       bool                    // printf %x of a signed type: emit hexPrintHelper
	usesStringEq       bool                    // a string == / != appears: emit ogo_string_eq
	eqStructs          map[string]bool         // struct C types compared with == / !=: emit an ogo_eq_<T> helper
	eqArrays           map[string]arrDim       // array types compared with == / !=, keyed by helper name: emit an ogo_eq_arr_<...> helper
	prologue           []string                // lines to emit before the statement being emitted, for a temporary an expression needs hoisted out of itself (see emitStatement)
	scopeNames         []map[string]bool       // per open block, the local names visible entering it, so blockDepthOf can say which block declares a name
	curParams          map[string]bool         // the parameter names of the function being emitted. A parameter's own storage is this frame's, but what it POINTS AT is the caller's, which isFrameVar deliberately does not distinguish and the receiver-leak rule must (see checkRecvLeak).
	litPath            string                  // the C path from the composite literal being rendered to the element now being rendered -- "[1]", ".xs", "[0].xs". Empty outside one.
	litFixups          []litFixup              // that literal's elements C cannot spell in an initializer, deferred to a copy after the declaration (see recordLitFixup)
	litFixable         bool                    // the literal being rendered has an owner that will emit those copies -- one that gives it a NAME. False in the positions that have no storage to copy into.
	frameBacked        map[string]bool         // local slice variables whose backing array is storage of this frame, so returning one would dangle (see checkReturnBacking)
	crossParams        map[string][]leak       // per function, how each parameter lets a value escape the caller's frame -- a cog crossing or a store that outlives it, directly or through a call (see collectCrossParams)
	crossInto          map[string][]uint32     // per function, which PARAMETERS each parameter is stored through, as a bitmask of their indices. leakRecv answers this for a method's receiver; a plain function has no receiver and needed the general form (see storedInPointerParam)
	ifaceSummaries     map[string]ifaceSummary // "<iface>.<method>" -> the union of the summaries of every implementation, since which one a call reaches is the vtable's answer (see ifaceCallSummary)
	retParams          map[string][]bool       // per function, which parameters a RESULT derives from, so a reference handed back out is followed to the storage it came from (see frameRefOf)
	funcValueOf        map[string]string       // variable holding a function -> that function's C name, when it is known, so a call through the variable is judged by the callee's summaries (see bindFuncValue)
	crossEdges         []crossEdge             // call sites passing a parameter straight on, the graph closeCrossParams walks
	retEdges           []crossEdge             // returns of a call taking a parameter, the graph the result summary is closed over
	crossNames         map[string]string       // C function name -> the name it was declared with, for crossParams diagnostics
	frameHolder        map[string]string       // local -> the local whose storage it holds a reference to, a struct field having been given one (see noteFrameHolder)
	chanCells          []string                // file-scope static cell declarations for locally declared channels, discovered while emitting bodies (see emitLocalChanCell)
	chanCellN          int                     // counter minting unique cell names, program-wide like makeN
	usesStringCmp      bool                    // a string < <= > >= appears: emit ogo_string_cmp
	usesRuneDecode     bool                    // `for i, c := range s` appears: emit ogo_decode_rune
	err                error
}

// emit writes verbatim C text, latching the first write error. All C is written
// through emit so no source text is ever interpreted as a printf verb.
func (e *emitter) emit(s string) {
	if e.err != nil {
		return
	}
	_, e.err = io.WriteString(e.w, s)
}

// fail latches the first "not yet implemented / unsupported" emit error.
func (e *emitter) fail(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf("emit: "+format, args...)
	}
}

func (e *emitter) ind() {
	for i := 0; i < e.indent; i++ {
		e.emit("\t")
	}
}

// src returns a terminal token's source text.
func (e *emitter) src(tok int32) string { return e.f.tok(tok).Src() }

func (e *emitter) emitFileDecls(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			switch c.sym {
			case ImportDecl:
				e.addImportIncludes(c.ast)
			case TopLevelDecl:
				e.emitTopLevelDecl(c.ast)
			case 0:
				// SEMICOLON / EOF separators.
			default:
				e.fail("unsupported source-file element %v", c.sym)
			}
		}
	}
}

func (e *emitter) addImportIncludes(ast []int32) {
	for n := range it(ast) {
		if n.sym != ImportSpec {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != 0 || e.f.ch(c.tok) != STRING {
				continue
			}
			path, err := strconv.Unquote(e.src(c.tok))
			if err != nil {
				continue
			}
			// An import mapped to a C header (p2 -> propeller2.h) pulls it in. A user
			// package has no header -- its declarations are emitted inline into this
			// same translation unit (see reachableFiles) -- so it needs nothing here.
			// A genuinely unresolvable import is reported at build time, not here.
			if inc, ok := importIncludes[path]; ok {
				e.includes[inc] = true
			}
		}
	}
}

func (e *emitter) emitTopLevelDecl(ast []int32) {
	for n := range it(ast) {
		switch n.sym {
		case FuncDecl:
			e.emitFuncDecl(n.ast)
		case ConstDecl:
			// Package-level constants are emitted in an earlier pass
			// (emitPackageConsts), before the functions that reference them.
		case VarDecl:
			// Package-level variables are emitted in an earlier pass
			// (emitPackageVars).
		case TypeDecl:
			// Struct typedefs are emitted in an earlier pass (collectStructs).
		default:
			e.fail("unsupported top-level declaration %v", n.sym)
		}
	}
}

// structField is one field of a struct type: its name and C type, in declaration
// order.
// structField is one field of a struct typedef. bound is empty for a plain field;
// when set the field is a fixed-size array, declared `ctype name[bound]` -- C puts
// the extent on the declarator, not the type, so the two cannot simply be
// concatenated the way every other field is.
type structField struct {
	name, ctype string
	dim         arrDim // dim.bound != "" for a fixed-array field; ctype is then its element type
	embedded    bool   // written as a bare type name; its own fields and methods are promoted
}

// arrDim describes an array variable: its element C type and its C bound (for
// element typing and len).
// arrDim describes a fixed array: the innermost element C type, the outermost
// extent, and any further extents for a multi-dimensional one. `[2][3]int` is
// {elem: "int", bound: "2", inner: ["3"]} and declares as `int m[2][3]`.
//
// bound stays the outermost extent, which is what len/cap and slicing want (Go's
// len of a [2][3]int is 2), so the one-dimensional callers need no change. elem,
// by contrast, is only the type of an *indexed element* when inner is empty: one
// index into a [2][3]int yields a [3]int, not an int. Callers that type an element
// must check dims() == 1.
type arrDim struct {
	elem  string
	bound string
	inner []string
	// name is the DEFINED type's C name when the array was written as one -- `Row`
	// for a `type Row [2]int` -- and "" for an array written out. It is what carries
	// the type's METHODS: a variable's dimensions say nothing about which type it is,
	// and the emitter keeps arrays out of the locals map that answers that for
	// everything else.
	name string
	// elemName is the same for this array's ELEMENT: `Row` for a `[2]Row`. An array
	// of a defined array type is resolved to its extents like any other -- a `[2]Row`
	// is a [2][2]int by the time anything walks it -- and that resolution is where
	// the element's name used to be dropped, so `pool[1].Sum()` had nothing to
	// dispatch on while `h.r.Sum()` and `r.Sum()` did.
	//
	// elemDims is how many extents that name accounts for, which says how far in it
	// is: a `[2][2]Row`'s elements are `[2]Row`s and only the SECOND index reaches a
	// Row. Without it the name alone would claim the first.
	elemName string
	elemDims int
}

// dims reports the number of dimensions.
func (a arrDim) dims() int { return 1 + len(a.inner) }

// bounds returns every extent, outermost first.
func (a arrDim) bounds() []string { return append([]string{a.bound}, a.inner...) }

// row is the array one index in: the element type of a [2][3]int is a [3]int.
// Only meaningful when dims() > 1. The element NAME travels with it, so a walk that
// indexes step by step still knows what a `[2]Row`'s elements are.
func (a arrDim) row() arrDim {
	return arrDim{
		elem: a.elem, bound: a.inner[0], inner: a.inner[1:],
		name: a.rowName(), elemName: a.elemName, elemDims: a.elemDims,
	}
}

// rowName is the DEFINED name the array one index in has: the element's, when one
// index reaches exactly it. A `[2]Row` reaches a Row; a `[2][2]Row`'s first index
// reaches a `[2]Row`, which was never given a name of its own, and only its second
// reaches the Row.
func (a arrDim) rowName() string {
	if a.elemName != "" && a.dims()-1 == a.elemDims {
		return a.elemName
	}
	return ""
}

// declSuffix renders the C declarator brackets, `[2][3]`.
func (a arrDim) declSuffix() string {
	s := ""
	for _, b := range a.bounds() {
		s += "[" + b + "]"
	}
	return s
}

// deferredCall is a recorded `defer` statement: the call's head (AssignHead) and
// its suffix (Selector / Index / CallSuffix), replayed before the function returns.
//
// Arguments are captured into function-scope temporaries where the defer is
// written, which is where Go evaluates them. That is not merely closer to Go: a
// defer in a nested block may name a variable of that block, which no longer
// exists at the return the call is replayed from.
//
// cond marks a defer written inside a nested block, where whether it ran is a
// runtime question and needs a flag. A defer cannot appear in a loop -- the
// checker rejects that -- so the number of sites in a function is fixed at compile
// time and the flags are plain stack locals. This is Go's open-coded defer without
// the heap fallback Go keeps for the loop case OctoGo does not admit.
type deferredCall struct {
	head   Node
	suffix []Node
	args   []deferArg
	cond   bool
	slot   int
	// A method call's receiver, captured at the defer statement like an argument --
	// which is what it is: Go evaluates it there, so a value receiver keeps the
	// value it had then. Empty for a plain function call.
	recvCType  string // the temporary's type, already pointer-adjusted for the method
	cname      string // the method's C name, empty when the temporary IS the callee
	callsValue bool   // the temporary holds a function value, so it is called rather than passed
	// litName is the file-scope function a deferred FUNCTION LITERAL was lifted to.
	// There is no head node naming it -- the source wrote no name -- so the replay
	// calls it directly.
	litName string
}

// deferArg is one argument of a deferred call. A literal needs no temporary --
// re-evaluating it at the return yields the same value -- so it is left inline,
// which matters on a target with 512 longs of cog RAM per cog.
type deferArg struct {
	ctype  string
	expr   []int32
	inline bool
}

// deferFlagName and deferArgName name the temporaries backing a defer slot.
func deferFlagName(slot int) string { return fmt.Sprintf("_ogo_defer%d", slot) }

func deferArgName(slot, arg int) string { return fmt.Sprintf("_ogo_defer%d_a%d", slot, arg) }

func deferRecvName(slot int) string { return fmt.Sprintf("_ogo_defer%d_r", slot) }

// collectStructs records each package-level struct type's fields in the struct
// environment and emits a C typedef -- `typedef struct { <t0> f0; ... } T;`.
// Only structs with explicitly-typed, non-embedded fields are modelled.
func (e *emitter) collectStructs(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == TypeDecl {
					e.collectTypeDecl(d.ast)
				}
			}
		}
	}
}

// collectStructForwards emits a forward declaration `typedef struct T T;` for every
// package-level struct type and registers its name, ahead of any struct body (see
// collectStructs). With every struct tag already known, a struct field may point to
// a struct declared later in source, or to a mutually-recursive one (A holds *B, B
// holds *A). A by-value field still needs the referenced struct's body first, which
// remains source order.
func (e *emitter) collectStructForwards(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym != TypeDecl {
					continue
				}
				for ts := range it(d.ast) {
					if ts.sym != TypeSpec {
						continue
					}
					var name string
					var typeAST []int32
					for s := range it(ts.ast) {
						switch s.sym {
						case 0:
							if e.f.ch(s.tok) == IDENT && name == "" {
								name = e.src(s.tok)
							}
						case Type:
							typeAST = s.ast
						}
					}
					if name == "" || typeAST == nil {
						continue
					}
					// Every type name is registered, struct or not: fieldIdent needs
					// the whole set, and this pass is the one that has seen every file
					// before a struct body is emitted.
					mn := mangle(e.curPkgPrefix, name)
					e.typeNames[mn] = true
					if ifaceAST := e.interfaceTypeAST(typeAST); ifaceAST != nil {
						// Recorded so an EMBEDDED name can be resolved wherever it is
						// written: type declarations are collected in source order,
						// and an interface may embed one declared after it. This pass
						// has already seen every file.
						e.ifaceASTs[mn] = ifaceAST
					}
					if e.isInterfaceTypeAST(typeAST) {
						// Recorded by SOURCE name: a use of it reaches cType as the
						// name written. Both the value and its table are tagged
						// structs, so both take a forward declaration -- the value
						// names the table by pointer.
						e.interfaceTypes[name] = true
						e.emit("typedef struct " + mn + " " + mn + ";\n")
						e.emit("typedef struct " + ifaceVTName(mn) + " " + ifaceVTName(mn) + ";\n")
						e.structs[mn] = nil
						e.structs[ifaceVTName(mn)] = nil
					}
					if e.structTypeAST(typeAST) == nil {
						continue
					}
					e.structs[mn] = nil
					e.emit("typedef struct " + mn + " " + mn + ";\n")
				}
			}
		}
	}
}

// collectTypeDecl records a type declaration (single or grouped) and emits its
// typedef: a `type Name struct { ... }` as `typedef struct { ... } Name;`, and a
// non-struct named type `type Name <underlying>` (e.g. `type Celsius int`) as
// `typedef <underlying> Name;`. The named type may then back variables and carry
// methods. An underlying type outside the modelled subset fails honestly.
func (e *emitter) collectTypeDecl(ast []int32) {
	for n := range it(ast) {
		if n.sym != TypeSpec {
			continue
		}
		var name string
		var typeAST []int32
		for s := range it(n.ast) {
			switch s.sym {
			case 0:
				if e.f.ch(s.tok) == IDENT && name == "" {
					name = e.src(s.tok)
				}
			case Type:
				typeAST = s.ast
			}
		}
		if name == "" || typeAST == nil {
			e.fail("malformed type declaration")
			return
		}
		// The C type name is the source name mangled into this package's namespace,
		// so it is a valid C identifier and cannot collide with another package's
		// type. cType returns the same mangled name for a reference to it, so every
		// use resolves to the same typedef and the same structs/namedTypes map key.
		mn := mangle(e.curPkgPrefix, name)
		if ifaceAST := e.interfaceTypeAST(typeAST); ifaceAST != nil {
			e.collectInterfaceType(mn, ifaceAST)
			continue
		}
		if structAST := e.structTypeAST(typeAST); structAST != nil {
			// The name was registered and forward-declared by collectStructForwards,
			// so a self-referential or forward/mutually-referential pointer field
			// resolves. Emit only the body here, tagged (`struct N { ... N* next; };`)
			// rather than as an anonymous `typedef struct { ... } N;`, because C cannot
			// name a type inside its own anonymous typedef.
			fields := e.structFieldsOf(structAST)
			e.structs[mn] = fields
			deps := make([]string, 0, len(fields))
			text := e.captureC(func() {
				e.emit("struct " + mn + " {")
				for _, fld := range fields {
					// A field name may be Unicode; cIdent it in the typedef and, to match,
					// wherever a field is selected (see fieldAccessC and the chain/selector
					// paths). The structs map still stores the source name, so the type
					// lookups (structFieldType etc.) compare source names.
					deps = append(deps, fld.ctype)
					if fld.dim.bound != "" {
						e.emit(" " + fld.ctype + " " + e.fieldIdent(fld.name) + fld.dim.declSuffix() + ";")
						continue
					}
					e.emit(" " + fld.ctype + " " + e.fieldIdent(fld.name) + ";")
				}
				if len(fields) == 0 {
					// C rejects a struct with no members; Go's empty struct is a
					// legal, zero-information type (markers, chan struct{} signals).
					// Give it one hidden byte so the C type is well-formed. OctoGo
					// code cannot name the field, so it stays invisible.
					e.emit(" char _ogo_empty;")
				}
				e.emit(" };\n")
			})
			e.addTypedef(mn, text, deps...)
			continue
		}
		// A named array type: `type Row [3]int` -> `typedef int Row[3];` (the extent
		// rides the declarator, C-style), the array counterpart of `type Celsius int`.
		// Recording it is what does the work: arrayDim resolves the name to these
		// dimensions wherever an array is expected -- a variable of the type, a field,
		// a parameter -- so each such site renders the underlying `int[3]` declarator
		// directly. The typedef documents the type in the output and keeps the name a
		// valid C type should anything refer to it by name.
		if a, ok := e.arrayDim(typeAST); ok {
			// The name goes IN the shape, not only in the map key. arrayDim already
			// puts it there when it resolves the name itself, so this is the map
			// agreeing with it -- and it is what lets a reader of the map tell a
			// DEFINED array from one the compiler minted a name for, which decides
			// whether there are methods to look for.
			a.name = mn
			e.namedArrays[mn] = a
			e.addTypedef(mn, "typedef "+a.elem+" "+mn+a.declSuffix()+";\n", a.elem)
			continue
		}
		// A non-struct named type: `type Celsius int` -> `typedef int Celsius;`. The
		// underlying must be a modelled scalar (or other cType-resolvable) type.
		underlying := e.cType(typeAST)
		if underlying == "" {
			return // cType has latched the failure
		}
		e.namedTypes[mn] = true
		e.namedUnderlying[mn] = underlying
		// A defined type over a channel takes a typedef like any other defined type,
		// which is what gives it a name to hang a method on. It could not until the
		// channel's own typedef moved into this section (it used to be emitted with
		// the helpers, after it, so a typedef naming it here named a type C had not
		// seen); the dependency is what orders the two now.
		e.addTypedef(mn, "typedef "+underlying+" "+mn+";\n", underlying)
	}
}

// ifaceMethod is one method of an interface, as the vtable slot it becomes: the
// source name, the C result type, and the C parameter types beside the receiver.
// Declaration order is slot order, which is why this is a slice and not a map.
type ifaceMethod struct {
	name   string
	res    string
	params []string
}

// vtTypeField names the vtable member holding the dynamic type's name, which "%T"
// reads. It leads the table so its offset does not depend on the method set.
const vtTypeField = "_ogo_type"

// ifaceVTName names the vtable STRUCT of an interface -- the table's shape, one
// function pointer per method.
func ifaceVTName(iface string) string { return iface + "_vt" }

// ifaceVTVar names the static vtable of a concrete type viewed as an interface, and
// ifaceThunkName the function that adapts one of its methods to the slot's
// signature. Both are keyed by the pair, since the same method may fill a slot in
// more than one interface and each slot has its own position.
func ifaceVTVar(iface, concrete string) string { return iface + "_vt_" + concrete }

func ifaceThunkName(iface, concrete, method string) string {
	return iface + "_" + concrete + "_" + userIdent(method)
}

// ifaceMethodsOf reads an InterfaceType's MethodSpecs into vtable slots, in
// declaration order -- which is slot order, and why this is a slice.
//
// It is separate from collectInterfaceType because an ANONYMOUS interface needs the
// slots before it has a name: the shape is what its name is derived from, so the
// methods have to be read first and registered second.
func (e *emitter) ifaceMethodsOf(structAST []int32) ([]ifaceMethod, bool) {
	return e.ifaceMethodsSeen(structAST, map[string]bool{})
}

// ifaceMethodsSeen is ifaceMethodsOf with the set of interfaces already expanded,
// so an embedding cycle stops instead of recurring. The cycle itself is the
// checker's to report; here it only has to terminate.
func (e *emitter) ifaceMethodsSeen(structAST []int32, seen map[string]bool) ([]ifaceMethod, bool) {
	var methods []ifaceMethod
	// One slot per NAME. Two embedded interfaces may declare the same method, which
	// Go allows and which must not become two slots -- a table has one pointer per
	// method, and a call looks it up by position.
	add := func(m ifaceMethod) {
		for _, have := range methods {
			if have.name == m.name {
				return
			}
		}
		methods = append(methods, m)
	}
	for n := range it(structAST) {
		if n.sym != MethodSpec {
			continue
		}
		name := e.soleIdent(n.ast)
		if name == "" {
			continue
		}
		// An EMBEDDED interface, written as a bare name: it contributes its methods
		// to this one, which is the whole of what embedding is here.
		if methodSpecEmbedded(e, n.ast) {
			mn := mangle(e.curPkgPrefix, name)
			if seen[mn] {
				continue
			}
			seen[mn] = true
			inner, ok := e.ifaceASTs[mn]
			if !ok {
				e.fail("cannot embed %s in an interface: it is not an interface type", name)
				return nil, false
			}
			ms, ok := e.ifaceMethodsSeen(inner, seen)
			if !ok {
				return nil, false
			}
			for _, m := range ms {
				add(m)
			}
			continue
		}
		_, resTypes := e.cSig(n.ast)
		res := "void"
		switch len(resTypes) {
		case 0:
		case 1:
			res = resTypes[0]
		default:
			e.fail("an interface method with more than one result is not supported yet")
			return nil, false
		}
		params, _ := e.cParamTypes(n.ast)
		add(ifaceMethod{name: name, res: res, params: params})
	}
	return methods, true
}

// methodSpecEmbedded reports a MethodSpec that is an EMBEDDED interface name rather
// than a method: the production left-factors to `identifier [ "(" ... ]`, so the
// parenthesis is what tells a method from a name standing alone.
func methodSpecEmbedded(e *emitter, ast []int32) bool {
	for n := range it(ast) {
		if n.sym == 0 && e.f.ch(n.tok) == LPAREN {
			return false
		}
	}
	return true
}

// anonInterfaceType mints (or reuses) the name of an interface type written where a
// type is wanted rather than declared with one of its own, `interface{ foo() }` and
// the empty `interface{}` that `any` stands for.
//
// Everything the interface machinery does is keyed by a NAME -- the method table,
// the vtable struct, the one static table per (concrete type, interface) pair -- so
// giving the shape a name is the whole of what it needed. It is the same move
// anonStructType makes, and the same rule: a generated identifier that unblocks a
// construct beats readable C (see octogo.go).
//
// Keyed by the METHOD SET, sorted by name, so identity does not depend on the order
// the methods were written in: Go's method sets are unordered, and `interface{ a();
// b() }` is `interface{ b(); a() }`. Sorting the slots too is what makes the two
// share one table rather than merely one name.
func (e *emitter) anonInterfaceType(ifaceAST []int32) string {
	methods, ok := e.ifaceMethodsOf(ifaceAST)
	if !ok {
		return ""
	}
	return e.anonInterfaceOf(methods)
}

// anonInterfaceOf is anonInterfaceType from the method set itself, for the caller
// that has no AST to read one from: `any`, which is the empty one spelled as a name.
func (e *emitter) anonInterfaceOf(methods []ifaceMethod) string {
	slices.SortFunc(methods, func(a, b ifaceMethod) int { return strings.Compare(a.name, b.name) })
	var key strings.Builder
	for _, m := range methods {
		key.WriteString(m.res + " " + m.name + "(" + strings.Join(m.params, ",") + ");")
	}
	if name, ok := e.anonIfaceNames[key.String()]; ok {
		return name
	}
	name := mangle(e.curPkgPrefix, fmt.Sprintf("ogo_anonface%d", len(e.anonIfaceNames)))
	e.anonIfaceNames[key.String()] = name
	e.anonIfaceMinted[name] = true
	e.interfaceTypes[name] = true
	e.typeNames[name] = true
	e.registerInterface(name, methods, true)
	return name
}

// collectInterfaceType records an interface's methods in declaration order and
// emits the two typedefs it becomes: the value -- a data pointer beside a pointer
// to a table -- and the table's shape. The value is what a variable of the type is,
// and the table is what tells a call which function to make.
func (e *emitter) collectInterfaceType(mn string, structAST []int32) {
	methods, ok := e.ifaceMethodsOf(structAST)
	if !ok {
		return
	}
	e.registerInterface(mn, methods, false)
}

// registerInterface records an interface's slots under a name and emits the two
// typedefs the name becomes. Shared by the declared form and the anonymous one.
//
// forward asks for the two tagged structs to be FORWARD-DECLARED here as well. A
// declared interface already has both from collectStructForwards, which runs before
// any body; an anonymous one is discovered while a body is being emitted, long after
// that pass, so it has to carry its own -- and both go in ONE typedef unit, since a
// forward and the definition that needs it cannot be ordered apart.
func (e *emitter) registerInterface(mn string, methods []ifaceMethod, forward bool) {
	e.ifaceMethods[mn] = methods

	vt := ifaceVTName(mn)
	var b strings.Builder
	// The dynamic type's NAME leads every table. It is what "%T" reads: a value
	// carries its table, and one table per (concrete type, interface) pair means the
	// table identifies the type exactly. It also gives an empty interface's table a
	// member, which C requires and which used to be a filler byte.
	fmt.Fprintf(&b, "struct %s { const char* %s;", vt, vtTypeField)
	for _, m := range methods {
		fmt.Fprintf(&b, " %s (*%s)(void*", m.res, userIdent(m.name))
		for _, p := range m.params {
			b.WriteString(", " + p)
		}
		b.WriteString(");")
	}
	b.WriteString(" };\n")
	var deps []string
	for _, m := range methods {
		deps = append(deps, m.res)
		deps = append(deps, m.params...)
	}
	value := "struct " + mn + " { void* data; const " + vt + "* vt; };\n"
	if forward {
		// Registered as structs so a POINTER to either needs only the forward
		// declaration, which is the rule typedefDeps follows for every other struct.
		e.structs[mn] = nil
		e.structs[vt] = nil
		e.addTypedef(mn, "typedef struct "+vt+" "+vt+";\n"+b.String()+
			"typedef struct "+mn+" "+mn+";\n"+value, deps...)
		return
	}
	e.addTypedef(vt, b.String(), deps...)
	e.addTypedef(mn, value, vt+"*")
}

// ifaceStoreC renders writing a concrete value into an interface value named by
// target: the data pointer is the value's address, and the table is the one for that
// pair. It returns "" when the right-hand side is not something whose address can be
// taken -- a literal, a call's result -- which is refused rather than copied to a
// temporary, there being nowhere for the interface to own a copy.
//
// The value's storage is the caller's, which is what makes an interface a REFERENCE:
// the variable it was made from must outlive it. That is recorded where the ordinary
// provenance mark is, so every sink already asks about it.
func (e *emitter) ifaceStoreC(target, iface string, rhs []int32) string {
	if e.isNilExpr(rhs) {
		// The ZERO interface: no table and no data. It is written as two stores
		// rather than as a compound literal, which this target's C compiler refuses
		// on the right of an assignment (see emitGo). Without this case nil fell
		// through to "an interface holds a pointer: write the address of a
		// variable", which is true of a concrete value and not of nil.
		return target + ".data = 0; " + target + ".vt = 0;\n"
	}
	if ct, ok := e.inferCType(rhs); ok && ct == iface {
		// Already an interface value: the same two words, copied. A variable's
		// provenance travels with them, since what the copy points at is what the
		// original pointed at.
		if name, isName := e.exprIdent(rhs); isName {
			if origin := e.frameHolder[name]; origin != "" {
				e.frameHolder[target] = origin
			}
		}
		return target + " = " + e.captureC(func() { e.emitExpr(rhs) }) + ";\n"
	}
	// A value of ANOTHER interface type. Go allows it when the target's method set
	// is a subset of the source's -- widening; narrowing is what an assertion is
	// for. What is stored is the same data word beside a different table, which is
	// the rebind a type switch's binding and an assertion both make.
	if src, ok := e.exprIdent(rhs); ok {
		if from, ok := e.varType(src); ok && e.isIfaceCType(from) && from != iface {
			return e.ifaceWidenC(target, iface, src, from)
		}
	}
	concrete, data, temp, ok := e.ifaceOperand(rhs)
	if !ok {
		if e.pkgScope {
			// A frame temporary is what gives `&T{...}` storage, and a package
			// variable's initializer has no frame to put one in.
			e.fail("a package variable of interface type needs a variable to point at: declare one and use &it")
			return ""
		}
		e.fail("an interface holds a pointer: write the address of a variable, or &T{...}")
		return ""
	}
	if !e.needVTable(iface, concrete) {
		return ""
	}
	if temp {
		// The value lives in a temporary of this frame, so what holds it holds a
		// reference into this frame, exactly as the address of a local does.
		e.frameHolder[target] = tempOrigin
	}
	if root, isRoot := e.addrOfRoot(rhs); isRoot && e.isFrameVar(root) {
		e.frameHolder[target] = "local " + root
	} else if name, isName := e.exprIdent(rhs); isName {
		if e.isFrameVar(name) {
			e.frameHolder[target] = "local " + name
		} else if origin := e.frameHolder[name]; origin != "" {
			e.frameHolder[target] = origin
		}
	}
	return target + ".data = " + data + "; " + target + ".vt = &" + ifaceVTVar(iface, concrete) + ";\n"
}

// ifaceWidenC stores an interface value in a variable of ANOTHER interface type.
// It is allowed exactly when every method the target declares is one the source
// declares too -- Go's assignability for interfaces, and the direction that needs no
// check at run time, since anything in the source already has the target's methods.
//
// PROVENANCE TRAVELS WITH IT. The two words are the same pointer viewed through a
// different table, so what the copy reaches is what the original reached: a value
// holding a reference into this frame still does after being widened, and storing it
// where it outlives the frame is refused as before. Losing that here would have made
// widening the way to launder a dangling pointer into a package variable.
func (e *emitter) ifaceWidenC(target, iface, src, from string) string {
	for _, m := range e.ifaceMethods[iface] {
		if !e.hasIfaceMethod(from, m.name) {
			e.fail("cannot use %s as %s: missing method %s", e.goTypeName(from), e.goTypeName(iface), m.name)
			return ""
		}
	}
	if origin := e.frameHolder[src]; origin != "" {
		e.frameHolder[target] = origin
	}
	var types []string
	for _, ct := range e.ifaceImplementors(from) {
		if !e.needVTable(iface, ct) {
			return ""
		}
		types = append(types, ct)
	}
	text, ok := e.ifaceRebindC(target, iface, src, from, types, 0)
	if !ok {
		return ""
	}
	return text
}

// hasIfaceMethod reports whether an interface declares a method by that name.
func (e *emitter) hasIfaceMethod(iface, method string) bool {
	for _, m := range e.ifaceMethods[iface] {
		if m.name == method {
			return true
		}
	}
	return false
}

// ifaceOperand resolves what is being put into an interface: the concrete type it
// is a value of, and the C expression for the data pointer.
//
// What an interface holds is a POINTER, so the operand is one: `&x`, a variable
// already of pointer type, or `&T{...}`. A value is refused -- the checker says so
// first, in Go's words and with the fix; this is the backstop.
func (e *emitter) ifaceOperand(rhs []int32) (concrete, data string, temp, ok bool) {
	// `&T{...}` is asked first: addrOfRoot takes the literal's TYPE name for the
	// root of the address, there being an identifier in front of the brace, and
	// would answer for a variable that does not exist.
	if concrete, data, temp, ok := e.ifaceAddrLitOperand(rhs); ok {
		return concrete, data, temp, ok
	}
	if root, isAddr := e.addrOfRoot(rhs); isAddr {
		ct, ok := e.varType(root)
		if !ok {
			return "", "", false, false
		}
		return ct, "&" + e.varRef(root), false, true
	}
	if name, isName := e.exprIdent(rhs); isName {
		// A variable already of pointer type points at the value itself.
		if ct, ok := e.varType(name); ok && e.isPointer(ct) {
			return e.elemType(ct), e.varRef(name), false, true
		}
		// A bare name that is NOT a pointer is the value form, which this target
		// refuses on purpose -- there is nowhere to copy the value to. Answering no
		// here is what makes that refusal reach the reader.
		return "", "", false, false
	}
	// Any OTHER expression of pointer type: a call's result, a pointer field, an
	// element of an array of pointers, a chain reaching one. The value already points
	// at the concrete value, so the two words are its own text beside the table its
	// pointee names -- there is nothing to address and nothing to copy.
	//
	// Only `&x`, `&T{...}` and a bare name of pointer type could reach an interface
	// before, so `var s Shape = get()` was refused though Go accepts it -- and as an
	// ARGUMENT it was not refused at all: the raw pointer went where the two words
	// belong and the C compiler reported "expected _struct__Shape but got pointer to
	// _struct__Quad" about generated code.
	if ct, isTyped := e.inferCType(rhs); isTyped && e.isPointer(ct) {
		return e.elemType(ct), e.captureC(func() { e.emitExpr(rhs) }), false, true
	}
	return "", "", false, false
}

// ifaceAddrLitOperand gives storage to `&T{...}`, a fresh value with no variable of
// its own. Go allocates one; there is no heap here, so it is a temporary of this
// frame -- which is exactly what a local is, so the lifetime rules already cover
// it: an interface made from it may not outlive the frame.
//
// A package variable's initializer has no frame to put one in (its temporaries are
// locals of ogo_pkg_init), so there it is refused rather than pointed at.
func (e *emitter) ifaceAddrLitOperand(rhs []int32) (concrete, data string, temp, ok bool) {
	ct, lit, isLit := e.addrOfCompositeLit(rhs)
	if !isLit || e.pkgScope || !e.isStruct(ct) {
		return "", "", false, false
	}
	// The literal's own copies go into the prologue behind the declaration hoist put
	// there, which is where the temporary comes into existence.
	name := ""
	fixups := e.captureLitFixups(func() {
		name = e.hoist(ct, func() { e.emitCompositeLit(ct, lit, true) })
	})
	stmts, okCopies := e.litFixupCopies(name, fixups)
	if !okCopies {
		return "", "", false, false
	}
	for _, stmt := range stmts {
		e.prologue = append(e.prologue, stmt+"\n")
	}
	return ct, "&" + name, true, true
}

// addrOfCompositeLit reports whether an expression is the address of a composite
// literal, `&T{...}`, and returns the literal. addrOfRoot answers the same question
// for a variable and takes only a named root, which is why this exists beside it.
func (e *emitter) addrOfCompositeLit(ast []int32) (ctype string, lit Node, ok bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != UnaryExpr {
		return "", Node{}, false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) < 2 || kids[0].sym != UnaryOp {
		return "", Node{}, false
	}
	if tok, ok := e.unaryOpTok(kids[0].ast); !ok || e.f.ch(tok) != AND {
		return "", Node{}, false
	}
	fac := kids[len(kids)-1]
	if fac.sym != Factor {
		return "", Node{}, false
	}
	return e.factorCompositeLit(slices.Collect(it(fac.ast)))
}

// ifaceValueC renders a concrete value as an interface value, for the positions
// that need one expression rather than two statements: an argument, and a return.
// An operand that is already of the interface type is itself.
func (e *emitter) ifaceValueC(iface string, rhs []int32) (string, bool) {
	if e.isNilExpr(rhs) {
		// The ZERO interface, wherever a VALUE is wanted: a field assignment, an
		// argument, a return. ifaceStoreC is the sibling for a TARGET being written
		// member by member, which is what a plain variable assignment does.
		return "(" + iface + "){0}", true
	}
	// Already an interface value -- a variable of one, or a call returning one --
	// so it is itself: the two words, copied.
	if ct, ok := e.inferCType(rhs); ok && ct == iface {
		return e.captureC(func() { e.emitExpr(rhs) }), true
	}
	// A value of ANOTHER interface, widened. Building one is statements rather than
	// an expression -- the table is chosen per concrete type -- so it goes to a
	// temporary declared before the statement, which is what stands here.
	if src, isName := e.exprIdent(rhs); isName {
		if from, ok := e.varType(src); ok && e.isIfaceCType(from) && from != iface {
			name := e.newTmp()
			e.prologue = append(e.prologue, iface+" "+name+" = {0};\n")
			text := e.ifaceWidenC(name, iface, src, from)
			if text == "" {
				return "", false
			}
			e.prologue = append(e.prologue, text)
			return name, true
		}
	}
	concrete, data, _, ok := e.ifaceOperand(rhs)
	if !ok || !e.needVTable(iface, concrete) {
		return "", false
	}
	return "(" + iface + "){" + data + ", &" + ifaceVTVar(iface, concrete) + "}", true
}

// ifaceBraceC is ifaceValueC for a position INSIDE a brace initializer -- a struct
// literal's interface-typed field, an interface array's element -- where a compound
// literal is the wrong shape: the initializer wants the members braced, "{ptr, &vt}",
// not a cast-and-braces value. Without it a literal put the raw pointer where the
// two words go, which the C compiler refused ("expected _struct__Shape but got
// pointer to _struct__Rect") rather than miscompiling, so `Box{&gr}` and
// `[2]Shape{&gq, &gr}` were both rejected though Go accepts them.
func (e *emitter) ifaceBraceC(iface string, rhs []int32) (string, bool) {
	if e.isNilExpr(rhs) {
		return "{0}", true // the zero interface: no data, no table
	}
	// A conversion to the interface this position already has, `[]Shape{Shape(&q)}`.
	// Its VALUE form is a compound literal, which the target's C compiler refuses
	// inside an array initializer -- so the operand is unwrapped and braced here,
	// which is the same two words by the spelling this position takes. The
	// conversion says nothing the position does not, so dropping it loses nothing.
	if operand, ok := e.ifaceConvOperand(iface, rhs); ok {
		return e.ifaceBraceC(iface, operand)
	}
	// Already an interface value: its two words, copied as they stand.
	if ct, ok := e.inferCType(rhs); ok && ct == iface {
		return e.captureC(func() { e.emitExpr(rhs) }), true
	}
	concrete, data, _, ok := e.ifaceOperand(rhs)
	if !ok || !e.needVTable(iface, concrete) {
		return "", false
	}
	return "{" + data + ", &" + ifaceVTVar(iface, concrete) + "}", true
}

// ifaceConvOperand recognises a conversion to the given interface type -- `Shape(&q)`,
// or the qualified `geo.Shape(&q)` -- and returns the operand. It exists for the brace
// positions, where the conversion's own value form -- a compound literal -- is the
// wrong shape; the sibling arrayConvOperand does the same for a defined array type.
func (e *emitter) ifaceConvOperand(iface string, ast []int32) ([]int32, bool) {
	recv, suffix, ok := e.directCall(ast)
	if !ok {
		return nil, false
	}
	ct, used, isConv := e.convChainHead(recv, suffix)
	if !isConv || used != len(suffix) || ct != iface {
		return nil, false
	}
	args := e.callArgExprs(suffix[used-1].ast)
	if len(args) != 1 {
		return nil, false
	}
	return args[0].ast, true
}

// typeAssertion recognises "x.(T)" and resolves the three names its lowering needs:
// the operand, the interface type it holds, and the asserted concrete type. Only
// "*T" is asserted, an interface holding a pointer and nothing else, so the star is
// required here as it is at every other position.
//
// The grammar admits the same Selector for ".(type)", which carries no Type child;
// that is a type switch and is not this.
func (e *emitter) typeAssertion(ast []int32) (operand, iface, target string, targetIsIface, ok bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term || nodes[0].sym == UnaryExpr) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != Factor {
		return "", "", "", false, false
	}
	return e.typeAssertionKids(slices.Collect(it(nodes[0].ast)))
}

// typeAssertionKids is typeAssertion given a Factor's children, which is what the
// expression emitter and the type inference each already hold.
func (e *emitter) typeAssertionKids(kids []Node) (operand, iface, target string, targetIsIface, ok bool) {
	operand, iface, target, targetIsIface, rest, ok := e.typeAssertionPrefix(kids)
	if !ok || len(rest) != 0 {
		return "", "", "", false, false // a suffix beyond the assertion is a chain
	}
	return operand, iface, target, targetIsIface, true
}

// typeAssertionPrefix is typeAssertionKids for an assertion that may be FOLLOWED by
// more of a suffix, `e.(*P).foo()`. It answers with the steps after it, which apply
// to what the assertion yielded rather than to the operand -- reading them against
// the operand is what "type any has no method foo" was.
func (e *emitter) typeAssertionPrefix(kids []Node) (operand, iface, target string, targetIsIface bool, rest []Node, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", "", "", false, nil, false
	}
	operand = e.src(kids[0].tok)
	iface, target, targetIsIface, rest, ok = e.assertionSteps(operand, slices.Collect(it(kids[1].ast)))
	return operand, iface, target, targetIsIface, rest, ok
}

// assertionSteps is typeAssertionPrefix from an operand NAME and the steps applied
// to it, for the statement path, whose call is not a Factor: `e.(*P).m()` on a line
// of its own arrives as a head and a postfix rather than as one node.
func (e *emitter) assertionSteps(operand string, allSteps []Node) (iface, target string, targetIsIface bool, rest []Node, ok bool) {
	steps := allSteps
	if len(steps) == 0 || steps[0].sym != Selector {
		return "", "", false, nil, false
	}
	rest = steps[1:]
	if iface, ok = e.varType(operand); !ok || !e.isIfaceCType(iface) {
		return "", "", false, nil, false
	}
	target, targetIsIface, ok = e.assertedTargetC(steps[0])
	if !ok {
		return "", "", false, nil, false
	}
	return iface, target, targetIsIface, rest, true
}

// assertedTargetC reads the type a ".(T)" selector asserts, as the C type the
// assertion yields. It is the half of an assertion that does not depend on the
// operand, which is what lets a bound operand share it.
func (e *emitter) assertedTargetC(sel Node) (target string, targetIsIface, ok bool) {
	var typeAST []int32
	for c := range it(sel.ast) {
		if c.sym == Type {
			typeAST = c.ast
		}
	}
	if typeAST == nil {
		return "", false, false
	}
	ct := e.cType(typeAST)
	// An INTERFACE target, `v.(T)`. Written without a star -- `*T` would be a
	// pointer TO an interface -- so it is the unpointered case, and the one whose
	// result is another interface value rather than the pointer that went in.
	if e.isIfaceCType(ct) {
		return ct, true, true
	}
	if !e.isPointer(ct) {
		return "", false, false
	}
	return e.elemType(ct), false, true
}

// assertionSplit finds a type assertion that is the LAST step of a Factor's suffix
// and splits the suffix around it: the steps BEFORE it reach the operand, and the
// selector itself names the asserted type. It answers only when there is at least
// one step before -- an assertion directly on the name is the plain path's, and
// this is the shape that path cannot read.
func (e *emitter) assertionSplit(kids []Node) (base string, prefix []Node, sel Node, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, Node{}, false
	}
	steps := slices.Collect(it(kids[1].ast))
	last := len(steps) - 1
	if last < 1 || steps[last].sym != Selector {
		return "", nil, Node{}, false
	}
	for c := range it(steps[last].ast) {
		if c.sym == Type {
			return e.src(kids[0].tok), steps[:last], steps[last], true
		}
	}
	return "", nil, Node{}, false
}

// boundAssertionKids is bindAssertionOperand for a Factor in EXPRESSION position:
// the binding goes to the statement prologue rather than being written where it
// stands, since an expression has nowhere to put a declaration. The prologue is
// carried into a loop body by capturePrologue where that matters, so an operand
// that changes per iteration is still bound per iteration.
func (e *emitter) boundAssertionKids(kids []Node) (operand, iface, target string, targetIsIface, ok bool) {
	base, prefix, sel, ok := e.assertionSplit(kids)
	if !ok {
		return "", "", "", false, false
	}
	text, ctype, _, okChain := e.chainCText(base, prefix)
	if !okChain || !e.isIfaceCType(ctype) {
		return "", "", "", false, false
	}
	if target, targetIsIface, ok = e.assertedTargetC(sel); !ok {
		return "", "", "", false, false
	}
	tmp := e.newTmp()
	e.prologue = append(e.prologue, ctype+" "+tmp+" = "+text+";\n")
	e.locals[tmp] = ctype
	return tmp, ctype, target, targetIsIface, true
}

// bindAssertionOperand lowers "<expr>.(T)" whose operand is NOT a plain name --
// "rs[i].(T)", "b.r.(T)", "mk().(T)" -- by binding the operand to a temporary and
// answering with that temporary's name. Everything below an assertion reads the
// operand by name and reads it TWICE (once to test the table, once to take the
// data word), so a name is what it needs; binding is also what makes an operand
// with a side effect evaluated once.
//
// It EMITS, so it may be called only from a statement context, and only once the
// caller knows the expression really is an assertion.
func (e *emitter) bindAssertionOperand(ast []int32) (operand, iface, target string, targetIsIface, ok bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term || nodes[0].sym == UnaryExpr) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != Factor {
		return "", "", "", false, false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", "", "", false, false
	}
	steps := slices.Collect(it(kids[1].ast))
	last := len(steps) - 1
	if last < 1 || steps[last].sym != Selector {
		return "", "", "", false, false // no operand steps: the plain-name path owns it
	}
	text, ctype, _, okChain := e.chainCText(e.src(kids[0].tok), steps[:last])
	if !okChain || !e.isIfaceCType(ctype) {
		return "", "", "", false, false
	}
	if target, targetIsIface, ok = e.assertedTargetC(steps[last]); !ok {
		return "", "", "", false, false
	}
	tmp := e.newTmp()
	e.ind()
	e.emit(ctype + " " + tmp + " = " + text + ";\n")
	e.locals[tmp] = ctype
	return tmp, ctype, target, targetIsIface, true
}

// assertOKC renders the test an assertion asks: the value carries this concrete
// type exactly when its table is the one emitted for that (type, interface) pair.
// One table per pair is what makes a pointer comparison the whole of it -- there is
// no type id to read and no name to compare.
func (e *emitter) assertOKC(operand, iface, concrete string) string {
	return e.varRef(operand) + ".vt == &" + ifaceVTVar(iface, concrete)
}

// assertValueC renders the asserted value: the data word, read back as the pointer
// that was put in.
func (e *emitter) assertValueC(operand, concrete string) string {
	return "(" + concrete + "*)" + e.varRef(operand) + ".data"
}

// isIfaceCType reports whether a C type name is one of the interface value structs.
func (e *emitter) isIfaceCType(ct string) bool {
	_, ok := e.ifaceMethods[ct]
	return ok
}

// needVTable emits, once per (interface, concrete type) pair, the static table that
// makes a value of that type usable as that interface: one thunk per method, and the
// table naming them.
//
// A thunk exists because the slot's receiver is a void* -- the table has to have one
// shape for every concrete type -- while the method's is its own type, by value or
// by pointer. The thunk is where that difference is spent, so the call site does not
// have to know which kind of receiver it reached.
func (e *emitter) needVTable(iface, concrete string) bool {
	key := iface + "|" + concrete
	if e.ifaceVTables[key] {
		return true
	}
	methods, ok := e.ifaceMethods[iface]
	if !ok {
		return false
	}
	e.ifaceVTables[key] = true
	var b strings.Builder
	for _, m := range methods {
		// Resolved through the embedding chain, not read off the concrete type: a
		// PROMOTED method is in the type's method set, so it satisfies an interface
		// as a declared one does, and the same lookup the direct call path uses
		// answers both. The path it returns is empty for a method the type declares
		// itself, so that case is untouched.
		cname, path, _, has := e.promotedMethod(concrete, m.name)
		if !has {
			e.fail("%s does not implement %s: missing method %s", concrete, iface, m.name)
			return false
		}
		thunk := ifaceThunkName(iface, concrete, m.name)
		fmt.Fprintf(&b, "static %s %s(void* _ogo_r", m.res, thunk)
		var args []string
		for i, p := range m.params {
			fmt.Fprintf(&b, ", %s _ogo_a%d", p, i)
			args = append(args, fmt.Sprintf("_ogo_a%d", i))
		}
		recv := "*(" + concrete + "*)_ogo_r"
		if e.methodPtr[cname] {
			recv = "(" + concrete + "*)_ogo_r"
		}
		if len(path) != 0 {
			// A promoted method takes the EMBEDDED sub-object as its receiver rather
			// than the whole struct. embeddedPathC renders the way in, which is the
			// same helper the direct call path uses -- the C field is not spelt the
			// way the source spells it, and writing the path out by hand here got
			// "->A" where the field is named "A_".
			sub := "(*(" + concrete + "*)_ogo_r)" + e.embeddedPathC(concrete, path)
			if recv = sub; e.methodPtr[cname] {
				recv = "&" + sub
			}
		}
		call := cname + "(" + strings.Join(append([]string{recv}, args...), ", ") + ")"
		switch {
		case m.res == "void":
			b.WriteString(") { " + call + "; }\n")
		case e.isStruct(m.res):
			// Bound, not returned where it stands: the target loses a struct member
			// narrower than a machine word out of `return f();`. The thunk is a
			// return of a call by construction, so every struct-result method
			// reached through an interface hit it. See doc/return-nonword-struct.c.
			b.WriteString(") { " + m.res + " _ogo_v = " + call + "; return _ogo_v; }\n")
		default:
			b.WriteString(") { return " + call + "; }\n")
		}
	}
	fmt.Fprintf(&b, "static const %s %s = { %q", ifaceVTName(iface), ifaceVTVar(iface, concrete),
		"*"+e.goTypeName(concrete)) // what goes in is a POINTER, so that is the dynamic type
	for _, m := range methods {
		b.WriteString(", " + ifaceThunkName(iface, concrete, m.name))
	}
	b.WriteString(" };\n")
	e.vtables.WriteString(b.String())
	return true
}

// isInterfaceTypeAST reports whether a Type subtree is an interface type.
func (e *emitter) isInterfaceTypeAST(typeAST []int32) bool {
	return e.interfaceTypeAST(typeAST) != nil
}

// interfaceTypeAST returns the InterfaceType node's children within a Type subtree,
// or nil if the type is not an interface.
func (e *emitter) interfaceTypeAST(typeAST []int32) []int32 {
	for n := range it(typeAST) {
		if n.sym == InterfaceType {
			return n.ast
		}
	}
	return nil
}

// structTypeAST returns the StructType node's children within a Type subtree, or
// nil if the type is not a struct.
func (e *emitter) structTypeAST(typeAST []int32) []int32 {
	for n := range it(typeAST) {
		if n.sym == StructType {
			return n.ast
		}
	}
	return nil
}

// structFieldsOf reads a StructType's FieldDecls into ordered fields. A field
// group `x, y int` yields one field per name. An embedded or untyped field fails.
func (e *emitter) structFieldsOf(structAST []int32) []structField {
	var out []structField
	for n := range it(structAST) {
		if n.sym != FieldDecl {
			continue
		}
		var names []string
		ctype := ""
		star := false
		var dim arrDim // dim.bound non-empty for a fixed-size array field
		for c := range it(n.ast) {
			if c.sym == 0 && e.f.ch(c.tok) == MUL {
				star = true
			}
			switch c.sym {
			case Type:
				if elem, ok := e.sliceType(c.ast); ok {
					// A slice field is a header held by value, so this struct's
					// typedef depends on the header's, which in turn names the element
					// -- by pointer, so a struct element needs only its forward
					// declaration. Both are units of the typedef section and the
					// dependency order places them; a struct-element slice used to be
					// emitted inline here, between the element's typedef and this
					// one's, because the fixed groups could not express that.
					e.needSlice(elem)
					ctype = sliceCName(elem)
					break
				}
				// A fixed-size array field (`data [3]int`, `pts [3]Point`). Unlike a
				// slice it needs no header typedef -- the storage is inline -- but C
				// puts the extent on the declarator, so the bound travels beside the
				// element type and is applied when the typedef is written.
				if a, ok := e.arrayDim(c.ast); ok {
					ctype, dim = a.elem, a
					break
				}
				ctype = e.cType(c.ast)
			case 0:
				if e.f.ch(c.tok) == IDENT {
					names = append(names, e.src(c.tok))
				}
			}
		}
		// An EMBEDDED field is written as a bare type name and nothing else. In C it
		// is an ordinary member named after that type; what makes it embedding is
		// that its own fields and methods are reachable without naming it (fieldPath,
		// promotedMethod).
		if ctype == "" && len(names) == 1 && !star {
			mn := mangle(e.curPkgPrefix, names[0])
			if _, isStruct := e.structs[mn]; isStruct {
				out = append(out, structField{name: names[0], ctype: mn, embedded: true})
				continue
			}
		}
		if star {
			// "*T" embedded: Go promotes through the pointer, and a nil one panics at
			// the selector. Refused rather than silently embedded by value, which is
			// what treating the name as the type would have done.
			e.fail("an embedded pointer field is not supported yet; embed %s by value", strings.Join(names, ", "))
			return out
		}
		if ctype == "" || len(names) == 0 {
			e.fail("an embedded field must be a struct type of this package, and an untyped field is not a field")
			return out
		}
		for _, nm := range names {
			out = append(out, structField{name: nm, ctype: ctype, dim: dim})
		}
	}
	return out
}

// emitPackageConsts emits the file's package-level constant declarations as C
// file-scope `static const` definitions and records each in the global type
// environment.
func (e *emitter) emitPackageConsts(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == ConstDecl {
					e.emitConstDecl(d.ast, true)
				}
			}
		}
	}
}

// collectConstValues records every package-level constant that folds to an integer,
// before any type is collected.
//
// A struct field's array bound may name a constant -- `buf [maxFrame]uint8` -- and C
// cannot use a `static const` as a bound, so the emitter has to spell the value.
// Struct typedefs are emitted first of all (a signature or a variable of struct type
// has to resolve), which put the bound's constant out of reach: the field fell
// through to the plain named-type path and failed as `unsupported type ""`. A local
// or package-level array of the same shape worked, being emitted after the constants.
//
// Only values are collected here. Resolving a constant's TYPE cannot move this early
// -- `const c Celsius = 5` names a type this pass runs ahead of -- and an array bound
// has no use for one.
func (e *emitter) collectConstValues(ast []int32) {
	e.constPreScan = true
	defer func() { e.constPreScan = false }()
	e.emitPackageConsts(ast)
}

// pkgVarPending is one package variable whose C type must be inferred from its
// initializer, with the file and package prefix that inference reads.
type pkgVarPending struct {
	name   string
	init   []int32
	file   *File
	prefix string
}

// collectPackageVarTypes registers every package variable whose type is WRITTEN and
// gathers the ones whose type must be inferred, for resolvePkgVarTypes to settle.
//
// It exists because an initializer may name a package variable declared BELOW it, or
// in another file of the same package -- Go's package block has no order -- while the
// pass that emits them walks the file in source order and typed each variable as it
// arrived. So `var b = a + 1` above `var a = 5` was refused as "cannot infer a type
// for the package variable b", of a program Go compiles.
//
// It is BEST EFFORT and emits nothing. A shape it does not model is simply not
// registered here, and the emitting pass then behaves exactly as it did before --
// which is what keeps this from being a second implementation of that pass.
func (e *emitter) collectPackageVarTypes(ast []int32) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym != VarDecl {
					continue
				}
				e.collectVarDeclTypes(d.ast)
			}
		}
	}
}

// collectVarDeclTypes is collectPackageVarTypes for one declaration.
func (e *emitter) collectVarDeclTypes(ast []int32) {
	for n := range it(ast) {
		if n.sym != VarSpec {
			continue
		}
		var names []string
		var typeAST []int32
		var initExprs [][]int32
		for s := range it(n.ast) {
			switch s.sym {
			case IdentifierList:
				for id := range it(s.ast) {
					if id.sym == 0 && e.f.ch(id.tok) == IDENT {
						names = append(names, e.src(id.tok))
					}
				}
			case Type:
				typeAST = s.ast
			case ExpressionList:
				for _, x := range expressionListItems(s) {
					initExprs = append(initExprs, x.ast)
				}
			}
		}
		switch {
		case typeAST != nil:
			// An ARRAY lives in an environment of its own -- the emitter keeps arrays
			// apart from every other variable -- so registering only its C type would
			// leave `var s = pool[:]` above `var pool [3]int` still unable to say what
			// it views.
			if a, isArr := e.arrayDim(typeAST); isArr {
				for _, nm := range names {
					if nm != "_" {
						e.globalArrays[e.globalC(nm)] = a
					}
				}
				continue
			}
			ct := e.cType(typeAST)
			if ct == "" {
				continue // a type this pass cannot name: left to the emitting pass
			}
			for _, nm := range names {
				if nm != "_" {
					e.globals[e.globalC(nm)] = ct
				}
			}
		case len(names) == 1 && names[0] != "_" && len(initExprs) == 1 && e.isArrayLitInit(initExprs[0]):
			// `var pool = [3]int{...}`: the array's extents come from the LITERAL's
			// written type, inference having no array value type to give.
			if litType, _, isLit := e.soleArrayLit(initExprs[0]); isLit {
				if a, isArr := e.arrayDim(litType); isArr {
					e.globalArrays[e.globalC(names[0])] = a
				}
			}
		case len(names) == 1 && len(initExprs) == 1 && names[0] != "_":
			e.pkgVarPending = append(e.pkgVarPending, pkgVarPending{
				name: names[0], init: initExprs[0], file: e.f, prefix: e.curPkgPrefix,
			})
		}
	}
}

// isArrayLitInit reports whether an initializer is an array literal, the one shape
// whose type is read off the literal rather than inferred from the value.
func (e *emitter) isArrayLitInit(initExpr []int32) bool {
	_, _, ok := e.soleArrayLit(initExpr)
	return ok
}

// resolvePkgVarTypes settles the inferred package-variable types, retrying until a
// round adds nothing: an inferred type comes from its own initializer, which may name
// another inferred variable, so one pass is not enough for a chain.
//
// What is left when a round makes no progress is an initialization CYCLE, or a shape
// inference does not model. Either way it says nothing: the emitting pass reports it
// in its own words, and a second message about the same declaration would only
// compete with that one.
func (e *emitter) resolvePkgVarTypes() {
	// Emits nothing and reports nothing, so whatever inference provoked on the way is
	// discarded -- the emitting pass runs after this and says what is really wrong.
	savedF, savedPrefix, savedErr, savedPro := e.f, e.curPkgPrefix, e.err, e.prologue
	defer func() { e.f, e.curPkgPrefix, e.err, e.prologue = savedF, savedPrefix, savedErr, savedPro }()
	pending := e.pkgVarPending
	e.pkgVarPending = nil
	for len(pending) != 0 {
		var next []pkgVarPending
		for _, p := range pending {
			e.f, e.curPkgPrefix, e.prologue = p.file, p.prefix, nil
			ct, ok := e.inferCType(p.init)
			if !ok {
				next = append(next, p)
				continue
			}
			e.globals[e.globalC(p.name)] = ct
		}
		if len(next) == len(pending) {
			return
		}
		pending = next
	}
}

// emitPackageVars emits the file's package-level variable declarations as C
// file-scope `static` definitions and records each in the global type environment.
func (e *emitter) emitPackageVars(ast []int32) {
	// A package variable's initializer runs in ogo_pkg_init, so a temporary hoisted
	// out of it is a local of THAT function -- storage a package variable must not
	// be left pointing at. What needs one is refused here instead (ifaceOperand).
	e.pkgScope = true
	defer func() { e.pkgScope = false }()
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == VarDecl {
					e.emitPackageVarDecl(d.ast)
				}
			}
		}
	}
}

// emitPackageVarDecl emits a package-level variable declaration (one VarSpec or a
// parenthesized group). Each variable is a file-scope `static`, so an uninitialized
// one is zeroed by C and needs no initializer; an array is a plain C array; an
// initializer must be a constant expression (emitGlobalInit). A blank name and an
// inferred (typeless) variable are not modelled.
func (e *emitter) emitPackageVarDecl(ast []int32) {
	for n := range it(ast) {
		if n.sym != VarSpec {
			continue
		}
		var names []string
		var typeAST []int32
		var initExprs [][]int32
		for s := range it(n.ast) {
			switch s.sym {
			case IdentifierList:
				for id := range it(s.ast) {
					if id.sym == 0 && e.f.ch(id.tok) == IDENT {
						names = append(names, e.src(id.tok))
					}
				}
			case Type:
				typeAST = s.ast
			case ExpressionList:
				for _, x := range expressionListItems(s) {
					initExprs = append(initExprs, x.ast)
				}
			case 0:
				// The "=" separator.
			default:
				e.fail("unsupported var-spec element %v", s.sym)
			}
		}
		// As for locals: a value list is that many independent single-name
		// declarations; one value (or none) keeps the single-spec paths below.
		if len(names) > 1 && len(initExprs) == len(names) {
			e.emitPackageVarList(names, typeAST, initExprs)
			continue
		}
		var initExpr []int32
		if len(initExprs) != 0 {
			initExpr = initExprs[0]
		}
		if typeAST == nil {
			// Type-inferred package variable `var x = expr`. C requires a constant
			// initializer at file scope (emitGlobalInit), so a single named variable
			// with an inferable type is modelled; a make/slice initializer still needs
			// an explicit type and fails honestly through inference.
			if len(names) != 1 {
				if len(initExprs) == 1 {
					// `var a, b = f()` at package scope: a multi-result call bound to
					// several variables. C cannot call in a file-scope initializer, so
					// the call and its distribution are deferred to the synthesized
					// package init.
					e.emitPackageDestructure(names, initExprs[0])
					continue
				}
				e.fail("a type-inferred package variable must be a single name (var x = expr)")
				return
			}
			if names[0] == "_" {
				continue // a blank package variable declares nothing
			}
			// `var g = [N]T{...}` or `var g = []T{...}` at package scope: a
			// file-scope static table, the global counterpart of a local
			// array-literal variable. inferCType can type neither -- an array has no
			// assignable C value type, and a slice literal needs a hoisted backing --
			// so this precedes the general inference path below.
			if litType, lit, isLit := e.soleArrayLit(initExpr); isLit {
				// A literal whose elements are not all constant cannot BE a static
				// initializer: C evaluates one at compile time, and the target's
				// compiler says so ("global initializers ... must be constant") of
				// generated code the program never wrote. The table is emitted
				// zeroed and filled at package initialization instead, which is
				// what the scalar and struct forms already did -- staticInitOK
				// answered for an array literal all along and only this position
				// never asked it.
				if !e.staticLitElementsOK(lit) {
					if a, isArr := e.pkgArrayInit(initExpr); isArr {
						e.emitPkgArrayVar(e.globalC(names[0]), names[0], a, initExpr)
						continue
					}
				}
				e.emitArrayLitVar(e.globalC(names[0]), litType, lit, true)
				continue
			}
			// `var g = mk()` / `var g = src` / `var g = h.f`: an array from something
			// that is not a literal. An array has no assignable C value type, so
			// inferCType answers no for every one of them and the variable was
			// refused for want of a type -- of a value whose own is in hand. A local
			// takes all three.
			if a, isArr := e.pkgArrayInit(initExpr); isArr {
				e.emitPkgArrayVar(e.globalC(names[0]), names[0], a, initExpr)
				continue
			}
			ct, ok := e.inferCType(initExpr)
			if !ok {
				e.fail("cannot infer a type for the package variable %q", names[0])
				return
			}
			gn := e.globalC(names[0])
			e.globals[gn] = ct
			e.bindFuncValue(names[0], initExpr)
			if e.isSliceCType(ct) {
				e.globalSliceVars[gn] = sliceElemFromCName(ct)
			}
			if e.staticInitOK(initExpr) {
				e.emit("static " + ct + " " + gn + " = ")
				e.emitGlobalInit(initExpr)
				e.emit(";\n")
				continue
			}
			e.emit("static " + ct + " " + gn + " = " + e.zeroInitC(ct) + ";\n")
			e.pkgInitAssign(gn, names[0], initExpr)
			continue
		}
		if len(names) != 1 && initExpr != nil {
			e.fail("multi-name package variable with an initializer is not supported yet")
			return
		}
		// A package-level fixed array `var a [N]T` -> `static T a[N];`, of any rank:
		// arrayDim carries every extent, where arrayType reports only the outermost
		// (a `var m [2][3]int` read through that one lost its inner extent and
		// failed as a nameless "unsupported type").
		if a, ok := e.arrayDim(typeAST); ok {
			// `var a [N]T = [N]T{...}`: the same static table as the inferred form,
			// after checking the literal's type against the declared one.
			if initExpr != nil && len(names) == 1 && names[0] != "_" {
				litType, lit, isLit := e.soleArrayLit(initExpr)
				if !isLit {
					// Not a literal but still an array -- a call's result, another
					// array, a field. The declared extents are what it is checked
					// against and filled as.
					if _, isArr := e.pkgArrayInit(initExpr); isArr {
						e.emitPkgArrayVar(e.globalC(names[0]), names[0], a, initExpr)
						continue
					}
					e.fail("a package array initializer must be an array literal, another array, or a call returning one")
					return
				}
				if !e.sameArrayType(a, litType) {
					return
				}
				e.emitArrayLitVar(e.globalC(names[0]), litType, lit, true)
				continue
			}
			if initExpr != nil {
				e.fail("array variable initializers are not supported yet")
				return
			}
			for _, nm := range names {
				if nm != "_" {
					gn := e.globalC(nm)
					e.globalArrays[gn] = a
					e.emit("static " + a.elem + " " + gn + a.declSuffix() + ";\n")
					// An array of structs holding channels owns a cell per ELEMENT per
					// channel field: `var ws [8]worker` is eight workers with a channel
					// each, which is the shape this target is for -- one per cog.
					e.emitChanFieldCellsArray(gn, a)
				}
			}
			continue
		}
		// A package-level slice `var xs []T` -> `static ogo_slice_T xs;` (BSS-zeroed
		// to {NULL, 0}); its element type is recorded for `xs[i]` / len(xs) in bodies.
		if elem, ok := e.sliceType(typeAST); ok {
			e.needSlice(elem)
			cname := sliceCName(elem)
			if initExpr != nil {
				// `var s []T = []T{...}`: a static backing array plus a header over
				// it, the same lowering the local form uses.
				if litType, lit, isLit := e.soleArrayLit(initExpr); isLit {
					if me, isSlice := e.sliceType(litType); !isSlice || me != elem {
						e.fail("a %s literal cannot initialize a variable declared []%s", e.litTypeName(litType), elem)
						return
					}
					if len(names) != 1 || names[0] == "_" {
						e.fail("a slice literal initializer needs a single named variable")
						return
					}
					e.emitArrayLitVar(e.globalC(names[0]), litType, lit, true)
					continue
				}
				me, lenAST, capAST, ok := e.makeSliceInit(initExpr)
				if !ok {
					e.fail("a package slice initializer must be make([]T, ...) or a []T literal")
					return
				}
				if me != elem {
					e.fail("make element type %q does not match the declared slice element type %q", me, elem)
					return
				}
				if len(names) != 1 || names[0] == "_" {
					e.fail("a make slice initializer needs a single named variable")
					return
				}
				gn := e.globalC(names[0])
				e.globalSliceVars[gn] = elem
				e.globals[gn] = cname
				e.emitMakeSliceVar(gn, cname, elem, lenAST, capAST, true)
				continue
			}
			for _, nm := range names {
				if nm != "_" {
					gn := e.globalC(nm)
					e.globalSliceVars[gn] = elem
					e.globals[gn] = cname
					e.emit("static " + cname + " " + gn + ";\n")
				}
			}
			continue
		}
		ctype := e.cType(typeAST)
		if ctype == "" {
			return
		}
		for _, nm := range names {
			if nm == "_" {
				continue // a blank package variable declares nothing
			}
			gn := e.globalC(nm)
			e.globals[gn] = ctype
			if initExpr != nil {
				e.bindFuncValue(nm, initExpr)
			}
			e.emit("static " + ctype + " " + gn)
			switch {
			case initExpr == nil:
			case e.isIfaceCType(ctype):
				// An interface value is an address beside a table pointer, and an
				// address is not a C constant expression, so the two words are
				// written at package initialization. What it points at has to be a
				// package variable too: a frame temporary would be a local of
				// ogo_pkg_init, which is not storage a package variable may keep.
				e.emit(" = " + e.zeroInitC(ctype))
				if stmt := e.ifaceStoreC(gn, ctype, initExpr); stmt != "" {
					defer e.deferPkgInit(strings.TrimSuffix(stmt, "\n"))
				}
			case e.staticInitOK(initExpr):
				e.emit(" = ")
				e.emitGlobalInit(initExpr)
			default:
				// C evaluates a file-scope initializer at compile time, so anything
				// that is not a constant expression is done at package
				// initialization instead -- which is where the inferred-type form
				// beside this one already puts it. Written out, the variable used to
				// keep the initializer and the backend refused the program: "global
				// initializers are evaluated at compile time and therefore must be
				// constant", about C the reader never wrote.
				e.emit(" = " + e.zeroInitC(ctype))
				defer e.pkgInitAssign(gn, nm, initExpr)
			}
			e.emit(";\n")
			if e.isChanCType(ctype) {
				// The cell is a file-scope object like the variable pointing at it;
				// acquiring its lock is a call, so it waits for package init.
				elem := e.chanElemOfCType(ctype)
				cell := gn + "_cell"
				e.emit("static " + chanCellCName(elem) + " " + cell + ";\n")
				e.deferPkgInit(gn + " = &" + cell + ";")
				e.chanInitElems[elem] = true
				e.deferPkgInit(chanInitCName(elem) + "(" + gn + ");")
			}
			// A struct variable owns a cell per CHANNEL FIELD, on the same rule: the
			// declaration owns the cell, the field is a reference to it. Declaring the
			// struct TYPE allocates nothing, and a copy of the variable shares the
			// channel, which is what a copy of a channel does in Go too.
			e.emitChanFieldCells(gn, ctype)
		}
	}
}

// emitChanFieldCells mints a rendezvous cell for every channel field of a struct
// variable and wires the field to it at package initialization, which is where a
// channel variable's own cell is wired for the same reason: acquiring the lock is a
// call, and C has no calls in a file-scope initializer.
//
// The rule is the one a channel variable already obeys -- the DECLARATION owns the
// cell -- so a struct type declares nothing, two variables of one type have a
// channel each, and a copy of a variable shares the channel it was copied from.
// That last is not a compromise: a channel value is a reference in Go as well.
func (e *emitter) emitChanFieldCells(gn, ctype string) {
	for _, fld := range e.structs[ctype] {
		if !e.isChanCType(fld.ctype) {
			if _, nested := e.structs[fld.ctype]; nested && fld.dim.bound == "" {
				// A struct field holding a struct with channel fields of its own.
				e.emitChanFieldCells(gn+"."+e.fieldIdent(fld.name), fld.ctype)
			}
			continue
		}
		if fld.dim.bound != "" {
			e.fail("a channel field that is an array is not supported yet")
			return
		}
		elem := e.chanElemOfCType(fld.ctype)
		cell := userIdent(strings.NewReplacer(".", "_").Replace(gn)) + "_" + e.fieldIdent(fld.name) + "_cell"
		e.emit("static " + chanCellCName(elem) + " " + cell + ";\n")
		e.deferPkgInit(gn + "." + e.fieldIdent(fld.name) + " = &" + cell + ";")
		e.chanInitElems[elem] = true
		e.deferPkgInit(chanInitCName(elem) + "(" + gn + "." + e.fieldIdent(fld.name) + ");")
	}
}

// emitChanFieldCellsArray mints the cells for an ARRAY of structs holding channels,
// one per element, and wires each element's field at package initialization. A
// multi-dimensional array is walked in row-major order, so the index expression
// matches the C declarator.
func (e *emitter) emitChanFieldCellsArray(gn string, a arrDim) {
	if !e.hasChanField(a.elem) {
		return
	}
	bounds := a.bounds()
	counts := make([]int, len(bounds))
	total := 1
	for i, b := range bounds {
		n, err := strconv.Atoi(b)
		if err != nil || n < 0 {
			e.fail("a channel field in an array needs a constant bound, got %q", b)
			return
		}
		counts[i], total = n, total*n
	}
	idx := make([]int, len(counts))
	for k := 0; k < total; k++ {
		sub := gn
		for _, i := range idx {
			sub += "[" + strconv.Itoa(i) + "]"
		}
		e.emitChanFieldCells(sub, a.elem)
		for d := len(idx) - 1; d >= 0; d-- {
			idx[d]++
			if idx[d] < counts[d] {
				break
			}
			idx[d] = 0
		}
	}
}

// hasChanField reports whether a struct type holds a channel, at any depth. It is
// what keeps an ordinary array of ordinary structs from walking its elements.
func (e *emitter) hasChanField(ctype string) bool {
	for _, fld := range e.structs[ctype] {
		if e.isChanCType(fld.ctype) {
			return true
		}
		if _, nested := e.structs[fld.ctype]; nested && fld.dim.bound == "" && e.hasChanField(fld.ctype) {
			return true
		}
	}
	return false
}

// emitLocalChanFieldCells is emitChanFieldCells for a local struct: the cells are
// still file-scope objects (their locks are taken once, before main), but the field
// is wired at the declaration rather than at package initialization, because that is
// where the variable comes into existence.
// lit is the composite literal the declaration was initialized with, or nil. A field
// that literal fills already refers to whatever the value written there does, so no
// cell is minted over it: doing so replaced the channel the program wrote with a
// private one nobody ever sends to, and the first receive on it blocked for ever --
// `var w W = W{ch}` hung where `w := W{ch}`, which takes another path, did not.
//
// A nested struct filled by a nested LITERAL is walked against that literal, so the
// rule reaches all the way down and an empty one (`W{In{}}`) still gets its cells.
// One filled by anything else is not walked at all: the value copied in brings
// whatever channels it has.
func (e *emitter) emitLocalChanFieldCells(nm, ctype string, lit *Node) {
	var values []*Node
	var fields []structField
	if lit != nil {
		values, fields, _ = e.litFieldValues(ctype, *lit)
	}
	filled := func(name string) (*Node, bool) {
		for i, f := range fields {
			if f.name == name && i < len(values) && values[i] != nil {
				return values[i], true
			}
		}
		return nil, false
	}
	for _, fld := range e.structs[ctype] {
		v, has := filled(fld.name)
		if !e.isChanCType(fld.ctype) {
			if _, nested := e.structs[fld.ctype]; nested && fld.dim.bound == "" {
				switch sub, isLit := e.litValueNode(v); {
				case !has:
					e.emitLocalChanFieldCells(nm+"."+e.fieldIdent(fld.name), fld.ctype, nil)
				case isLit:
					e.emitLocalChanFieldCells(nm+"."+e.fieldIdent(fld.name), fld.ctype, sub)
				}
			}
			continue
		}
		if has {
			continue
		}
		if fld.dim.bound != "" {
			e.fail("a channel field that is an array is not supported yet")
			return
		}
		e.ind()
		e.emit(nm + "." + e.fieldIdent(fld.name) + " = &" + e.localChanCell(e.chanElemOfCType(fld.ctype)) + ";\n")
	}
}

// litValueNode reads a literal's element as a composite literal of its own, in both
// spellings: written with its type, `In{ch}`, and type-elided, `{ch}`.
func (e *emitter) litValueNode(v *Node) (*Node, bool) {
	if v == nil {
		return nil, false
	}
	if v.sym == CompositeLit {
		return v, true
	}
	if _, lit, ok := e.soleCompositeLit(v.ast); ok {
		return &lit, true
	}
	return nil, false
}

// declLitNode reads a declaration's initializer as a composite literal, for the
// channel-cell walk, which must know which fields the literal fills itself.
func (e *emitter) declLitNode(initExpr []int32) *Node {
	if initExpr == nil {
		return nil
	}
	if _, lit, ok := e.soleCompositeLit(initExpr); ok {
		return &lit
	}
	return nil
}

// emitChanSend emits one send on an already-rendered channel, whatever named it: a
// variable, a field, or an element's field. op is the PostfixOp's children, whose
// second is the value.
func (e *emitter) emitChanSend(ch, elem string, op []Node) {
	if len(op) != 2 || op[1].sym != Expression {
		e.fail("unsupported send statement")
		return
	}
	if x, r, bad := e.frameRefIn([]Node{op[1]}); bad {
		e.fail("%v: cannot send %s: its storage does not outlive the function, and the receiver keeps "+
			"the value; %s",
			e.f.tok(x.Pos()).Position(), r.what, r.advice())
		return
	}
	// `ch <- mk(3)`: the send helper takes the element by value, so a struct-
	// returning call handed to it is the same shape hoistStructCallArg binds for an
	// ordinary call -- and the send does not go through emitCallArgs, so it is bound
	// here. See doc/return-nonword-struct.c.
	sent, hoisted := e.hoistStructCallArg(op[1])
	e.ind()
	e.chanSendElems[elem] = true
	e.emit(chanSendCName(elem) + "(" + ch + ", ")
	// A concrete value sent on a channel of interface type is wrapped, the element
	// type being the target here.
	if hoisted {
		e.emit(sent)
	} else if text, ok := e.ifaceValueC(elem, op[1].ast); ok && e.isIfaceCType(elem) {
		e.emit(text)
	} else {
		e.emitExpr(op[1].ast)
	}
	e.emit(");\n")
}

// emitGlobalInit emits a package variable's initializer, which C requires to be a
// constant expression. A bare integer-constant reference is folded to its value
// (flexcc rejects a `static const` in a global initializer); a string or struct
// literal uses the brace form (declInit).
func (e *emitter) emitGlobalInit(initExpr []int32) {
	if tok, ok := e.soleToken(initExpr); ok && e.f.ch(tok) == IDENT {
		if v, ok := e.foldedInt(e.src(tok)); ok {
			e.emit(v)
			return
		}
	}
	// A constant of an imported package, `geo.MaxPoints`, folds to its value like a
	// constant of this one. It has to: C evaluates a file-scope initializer at
	// compile time, and the other package's `static const` is a symbol, not a
	// literal ("global initializers ... must be constant").
	if v, ok := e.foldedQualifiedInt(initExpr); ok {
		e.emit(v)
		return
	}
	e.declInit = true
	e.emitExpr(initExpr)
	e.declInit = false
}

// emitConstDecl emits a constant declaration -- one ConstSpec or a parenthesized
// group -- as C const definitions. A package-level constant becomes a file-scope
// `static const`; a local one a block-scope `const`. An untyped constant's C type
// is inferred from its initializer, defaulting to int. The name is recorded in the
// global or local type environment so its later uses can be typed.
func (e *emitter) emitConstDecl(ast []int32, pkg bool) {
	// iota counts specs across the group; lastExpr and lastType carry the previous
	// spec's expression and type forward for a spec that omits its own, mirroring
	// the checker's declareConst.
	iotaVal := 0
	var lastExprs [][]int32
	var lastType string
	haveLastType := false
	for n := range it(ast) {
		if n.sym != ConstSpec {
			continue
		}
		var names []string
		var initExprs [][]int32
		var ownType string
		hasType := false
		for s := range it(n.ast) {
			switch s.sym {
			case Type:
				// The pre-scan runs before any type is collected, so a named type
				// would fail to resolve. It records values, not types: hasType still
				// carries forward to the next spec, ownType is simply unused.
				if e.constPreScan {
					ownType, hasType = "", true
					continue
				}
				ownType, hasType = e.cType(s.ast), true
			case IdentifierList:
				for d := range it(s.ast) {
					if d.sym == 0 && e.f.ch(d.tok) == IDENT {
						names = append(names, e.src(d.tok))
					}
				}
			case ExpressionList:
				for d := range it(s.ast) {
					if d.sym == Expression {
						initExprs = append(initExprs, d.ast)
					}
				}
			}
		}
		if len(names) == 0 {
			e.fail("malformed const declaration")
			return
		}
		// A spec omitting its expression list repeats the previous spec's,
		// positionally, together with its type; one with a list of its own carries
		// that list forward.
		if len(initExprs) != 0 {
			lastExprs = initExprs
			lastType, haveLastType = ownType, hasType
		} else {
			initExprs = lastExprs
			ownType, hasType = lastType, haveLastType
		}
		if len(initExprs) != len(names) {
			e.fail("malformed const declaration")
			return
		}
		// iota counts specs, not names: every name on one line sees the same value.
		curIota := iotaVal
		iotaVal++
		for i, name := range names {
			initExpr := initExprs[i]
			if name == "_" {
				continue // a blank const declares nothing
			}
			e.emitConstSpecName(name, ownType, hasType, initExpr, curIota, pkg)
		}
	}
}

// emitConstSpecName emits one name of a const spec. A spec binds a list, and every
// name on it shares the spec's iota and its written type while taking the
// expression standing in its own position.
func (e *emitter) emitConstSpecName(name, ownType string, hasType bool, initExpr []int32, curIota int, pkg bool) {
	{
		// A package-level constant is namespaced by its package, exactly like a
		// package variable (see globalC), so same-named constants in different
		// packages neither collide in the single translation unit nor cross-pollute
		// the constInt/constStr fold maps. A block-scope constant keeps its own name.
		cname := name
		if pkg {
			cname = mangle(e.curPkgPrefix, name)
		}
		if e.constPreScan {
			// Values only: what an array bound needs, and all it can use.
			e.iota = curIota
			if v, ok := e.constIntValue(initExpr); ok {
				e.constInt[cname] = intCLit(v)
			}
			e.iota = -1
			return
		}
		ctype := ownType
		if !hasType {
			e.iota = curIota // so inference sees iota as an int
			ct, ok := e.inferCType(initExpr)
			e.iota = -1
			if !ok {
				ct = "int" // an untyped constant defaults to int
			}
			// An untyped constant whose value does not fit an int takes the width it
			// needs: Go's default type for it is int, which is 64-bit there, and
			// "static const int" would store 1 << 40 as 0.
			e.iota = curIota
			if v, ok := e.foldConstInt(initExpr); ok && ct == "int" && !fitsCInt(v) {
				ct = "int64_t"
				e.includes["stdint.h"] = true
			}
			e.iota = -1
			ctype = ct
		}
		if pkg {
			e.globals[cname] = ctype
		} else {
			e.locals[cname] = ctype
		}
		// A constant written with no type and built only from untyped constants is
		// itself untyped: it has no type to contribute to an expression it appears
		// in, and takes the type of whatever it meets. Recorded so inferNodes can
		// look past it -- "fracBits * one" is an int32 because one is, whichever
		// operand comes first.
		if !hasType && e.exprUntyped(initExpr) {
			e.constUntyped[cname] = true
		}
		// A constant that folds to an integer -- a literal, iota, or a constant
		// expression like "2 + 1" or "W * H" -- can serve as an array bound (flexcc
		// rejects a `static const` there); record its value. iota is visible to the
		// fold as this spec's index for the duration.
		e.iota = curIota
		if v, ok := e.constIntValue(initExpr); ok {
			e.constInt[cname] = intCLit(v)
		}
		e.iota = -1
		// A constant string -- a literal or a concatenation of constants -- is
		// recorded decoded and emitted at each use as the folded literal, rather
		// than as a C variable. A Go constant has no address, so inlining it is
		// correct, and it avoids an unused-variable warning when the constant is
		// only ever folded into a concatenation (which does not name it).
		if v, ok := e.foldConstString(initExpr); ok {
			e.constStr[cname] = v
			return
		}
		e.iota = curIota // substitute iota with its value while emitting the expression
		e.ind()
		storage := "const "
		if pkg {
			storage = "static const "
		}
		e.emit(storage + ctype + " " + cname + " = ")
		switch v, folded := e.constInt[cname]; {
		case folded:
			// A folded integer constant emits its literal value, so a constant that
			// references another ("const M = N + 1") does not become the C
			// initializer "N + 1" -- not a constant expression at file scope.
			e.emit(v)
		case pkg:
			// A file-scope constant has static storage, so a string initializer
			// must be a brace, not a compound literal (see emitStringLit).
			e.declInit = true
			e.emitExpr(initExpr)
			e.declInit = false
		default:
			e.emitExpr(initExpr)
		}
		e.emit(";\n")
		e.iota = -1
	}
}

// foldedInt returns a folded integer constant's C literal by its source name,
// trying the block-scope (unmangled) key first, then the current package's mangled
// global key. A package constant's fold maps are keyed by its mangled name (see
// emitConstDecl) to keep same-named constants in different packages distinct, so a
// same-package read resolves through globalC just as a package variable does.
func (e *emitter) foldedInt(name string) (string, bool) {
	if v, ok := e.constInt[name]; ok {
		return v, true
	}
	v, ok := e.constInt[e.globalC(name)]
	return v, ok
}

// foldedQualifiedInt folds a reference to an imported package's integer constant,
// `geo.MaxPoints`, to its literal value. The fold maps are keyed by the mangled
// name, which is what that package emitted the constant under.
func (e *emitter) foldedQualifiedInt(ast []int32) (string, bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term || nodes[0].sym == UnaryExpr) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	kids := nodes
	if len(nodes) == 1 && nodes[0].sym == Factor {
		kids = slices.Collect(it(nodes[0].ast))
	}
	return e.foldedQualifiedIntKids(kids)
}

// foldedQualifiedIntKids is foldedQualifiedInt given a Factor's children, which is
// what the constant folder holds.
func (e *emitter) foldedQualifiedIntKids(kids []Node) (string, bool) {
	base, fields, ok := e.factorFieldAccess(kids)
	if !ok || len(fields) != 1 {
		return "", false
	}
	prefix, isImport := e.importQualifiers[base]
	if !isImport {
		return "", false
	}
	// An intrinsic package's constants are the compiler's own table: nothing was
	// emitted for them, so there is no mangled name to look up.
	if base == "p2" {
		v, ok := p2Constants[fields[0]]
		return v, ok
	}
	v, ok := e.constInt[mangle(prefix, fields[0])]
	return v, ok
}

// foldedStr is foldedInt's string-constant counterpart.
func (e *emitter) foldedStr(name string) (string, bool) {
	if v, ok := e.constStr[name]; ok {
		return v, true
	}
	v, ok := e.constStr[e.globalC(name)]
	return v, ok
}

// collectResults records every user function's C result types in funcRet and,
// for a function with more than one result, emits a result-struct typedef —
// `typedef struct { <t0> _0; <t1> _1; ... } ogo_ret_<name>;` — that its C
// signature returns in place of C's absent multiple-return.
// collectResults records every user function's and method's C result types in
// funcRet (keyed by the plain name for a function, the mangled `<T>_<method>` for a
// method) and, for a multi-result callee, emits its result-struct typedef. A
// method's receiver pointer-ness is recorded in methodPtr for the call site.
func (e *emitter) collectResults(ast []int32) {
	e.eachFuncDeclAST(ast, func(d []int32) {
		name, sig, _, recv, ok := e.funcParts(d)
		if !ok || name == "" {
			return
		}
		cname := mangle(e.curPkgPrefix, name)
		if recv != nil {
			_, rct, _ := e.receiverInfo(recv)
			cname = methodCName(methodBaseType(rct), name)
			e.methodPtr[cname] = e.isPointer(rct)
		}
		_, resTypes := e.resultInfo(sig)
		e.funcRet[cname] = resTypes
		if a, arrRet := e.arrayResultOf(sig); arrRet {
			// The extents are recorded HERE and not only where the C signature is
			// rendered, which happens with the prototypes -- after the package
			// variables. A package variable filled by such a call had nothing to read
			// until then, so `var g = mk()` was refused for want of a type.
			e.funcArrayRet[cname] = a
			// A function with an array result cannot be used as a VALUE: its C
			// signature has an out parameter the type would have to describe, and a
			// function type here is the signature the source wrote. Recorded as
			// returning nothing so the call sites that ask find no value type, and
			// skipped here so cSig is never asked about the array result.
			e.funcRet[cname] = nil
			e.funcSliceParams[cname] = e.paramSliceTypes(sig)
			e.funcParams[cname], e.funcArrayParams[cname] = e.cParamTypes(sig)
			return
		}
		if recv == nil {
			// Kept so the name used as a value can mint its function type on demand.
			// Minting here instead would emit a typedef for every function in the
			// program, nearly all of them unused. Rendered now, while this package's
			// file is still the current one, since the use may be in another.
			e.funcValueTypes[cname] = e.funcSigCParts(sig)
		} else {
			// A METHOD's type as a value is its signature without the receiver, which
			// is what sig already is -- the receiver is a production of its own.
			e.methodValueTypes[cname] = e.funcSigCParts(sig)
		}
		e.funcSliceParams[cname] = e.paramSliceTypes(sig)
		if _, at := e.variadicElem(sig); at >= 0 {
			e.funcVariadic[cname] = at
		}
		e.funcParams[cname], e.funcArrayParams[cname] = e.cParamTypes(sig)
		if len(resTypes) > 1 {
			e.retStructNameOf(resTypes)
		}
	})
}

// leak says how a parameter lets what it was given escape the frame that chose the
// storage. The kinds are bits so a parameter can do both and the propagation can
// simply OR them together; the diagnostic names one, the cog crossing first,
// because it is the one a reader is least likely to have thought about.
type leak uint8

const (
	// leakCog: the value reaches another cog, which may outlive this function.
	leakCog leak = 1 << iota
	// leakGlobal: the value is stored where it outlives every frame -- a package
	// variable, or a field or element of one.
	leakGlobal
	// leakRecv: the value is stored into the RECEIVER, whose lifetime the callee
	// does not know. Whether that outlives the caller's frame is the CALL SITE's
	// question -- `h.set(a[:])` leaks for a package-level h and is fine for a local
	// one -- which is why it is a flag of its own rather than folded into leakGlobal.
	leakRecv
)

// intoBits is how many parameters a crossInto mask can name. A store through
// parameter 32 or later is not summarised; the mask is a word because a function
// with that many parameters is not the shape this rule exists for, and a wider one
// would cost every function to serve none.
const intoBits = 32

// crossEdge records that a call passes the caller's parameter `from` straight into
// the callee's parameter `to`, so whatever the callee does with it, the caller does.
//
// recv says whose storage the callee's RECEIVER is, when the call is a method's. That
// decides what the callee's leakRecv means here: a store into the receiver is a leak
// to whoever owns it, and the same callee is harmless on a scratch struct and fatal
// on a package one.
type crossEdge struct {
	caller string
	from   int
	callee string
	to     int
	recv   recvKind
	// recvAt is which of the CALLER's parameters the receiver is, for recvParam.
	recvAt int
	// argOwner says, per argument position, what the caller passed there: one of
	// its own parameters by index, argOutlives for a package variable, argLocal for
	// anything else. It is what turns the callee's "stored through my parameter 2"
	// into a fact about the caller, whose parameter 2 is not the callee's.
	argOwner []int
}

const (
	// argLocal: an argument this cannot follow -- a local, or an expression with no
	// name to take. Nothing propagates through it; the call site's own check is
	// what covers the storage chosen there.
	argLocal = -1
	// argOutlives: a package variable, which outlives every frame. A store through
	// it is leakGlobal for the caller, which needs no further question asked.
	argOutlives = -2
)

// owner names what the caller passed at argument position j.
func (g crossEdge) owner(j int) int {
	if j < 0 || j >= len(g.argOwner) {
		return argLocal
	}
	return g.argOwner[j]
}

// recvKind classifies a method edge's receiver.
type recvKind uint8

const (
	// recvNone: not a method call. The callee's flags mean here what they mean there.
	recvNone recvKind = iota
	// recvOwn: the caller's OWN receiver, `t.inner(d)`. What the callee stores in it,
	// the caller stores in the receiver it was handed -- so leakRecv travels as
	// leakRecv and the question is asked again one call further out.
	recvOwn
	// recvOutlives: a package variable, or one the caller was handed as a parameter.
	// Either outlives the caller's frame, so a store into it is a store into storage
	// that outlives -- leakGlobal, which the call sites already understand.
	recvOutlives
	// recvParam: one of the CALLER's own `*T` parameters, `h.inner(d)` in a function
	// taking h. Where that points is the caller's caller's business, so the callee's
	// leakRecv becomes the caller's "stored through parameter recvAt" -- the same
	// fact crossInto carries for a plain store, asked one call further out.
	recvParam
)

// collectCrossParams seeds the per-parameter crossing summary for the functions of
// one file, and records the call edges the fixed point later walks.
//
// The question it answers is the one increment 3 could not: a `go` statement or a
// send is refused an argument backed by *this* frame, but a parameter is accepted,
// its storage belonging to a caller this cannot see. Now it can. A function that
// lets one of its parameters reach another cog imposes a requirement on everyone who
// calls it -- that the argument outlive the goroutine -- and the summary is what
// carries that requirement back to the call sites.
//
//	func spawn(p []int) { go work(p) }   // parameter 0 crosses
//	func setup() {
//		var local [4]int
//		spawn(local[:])              // ... so this is refused, here
//	}
//
// Seeding is direct: a parameter named as a go argument or a send value crosses.
// Passing it on to another function is the indirect case, and cannot be settled
// until that function's own summary is known -- hence the edge, and hence a fixed
// point rather than a single walk. A cycle among mutually recursive functions
// converges because a parameter only ever goes from not-crossing to crossing.
func (e *emitter) collectCrossParams(ast []int32) {
	e.eachFuncDeclAST(ast, func(d []int32) {
		fi, ok := e.funcParamNames(d)
		if !ok {
			return
		}
		cname, srcName, params, body, recvName := fi.cname, fi.srcName, fi.params, fi.body, fi.recvName
		// A type switch BINDS a new name to the value it switched on, so a store of
		// that name is a store of whatever the operand was. Without following it, a
		// parameter reached a package variable through `switch x := p.(type)` with
		// no summary recorded -- and interface widening then made that the way to
		// launder a reference into a global.
		//
		// Collected for the whole body rather than per clause: the name is scoped to
		// its switch anyway, and merging two switches' bindings can only refuse more.
		alias := e.typeSwitchAliases(body)
		at := func(name string) int {
			for range 16 { // bounded; an alias chain cannot outlive the body
				if i := slices.Index(params, name); i >= 0 {
					return i
				}
				next, ok := alias[name]
				if !ok {
					return -1
				}
				name = next
			}
			return -1
		}
		// owners says what this call passed at each position, in the terms the
		// CALLER's summary is written in: its own parameters by index, or a package
		// variable, or something not to be followed. A callee's "stored through my
		// parameter j" means nothing until it is read through this.
		owners := func(args []Node) []int {
			out := make([]int, len(args))
			for j, a := range args {
				root := e.crossRoot(a.ast)
				switch i := at(root); {
				case i >= 0:
					out[j] = i
				case root != "" && e.isPackageVar(root):
					out[j] = argOutlives
				default:
					out[j] = argLocal
				}
			}
			return out
		}
		if _, seen := e.crossParams[cname]; !seen {
			e.crossParams[cname] = make([]leak, len(params))
		}
		if _, seen := e.crossInto[cname]; !seen {
			e.crossInto[cname] = make([]uint32, len(params))
		}
		if _, seen := e.retParams[cname]; !seen {
			e.retParams[cname] = make([]bool, len(params))
		}
		e.crossNames[cname] = srcName
		e.eachStmt(body, func(nodes []Node) {
			switch {
			case len(nodes) != 0 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == GO:
				for _, a := range e.goStmtArgs(nodes) {
					if i := at(e.crossRoot(a.ast)); i >= 0 {
						e.crossParams[cname][i] |= leakCog
					}
				}
			default:
				if v, ok := e.sendValue(nodes); ok {
					if i := at(e.crossRoot(v)); i >= 0 {
						e.crossParams[cname][i] |= leakCog
					}
				}
				// A store into a package variable, `g = p` or `g.f = p`: whatever
				// the caller chose the storage for, it now outlives every frame.
				for _, v := range e.storedInPackageVar(nodes) {
					if i := at(e.leakRoot(v)); i >= 0 {
						e.crossParams[cname][i] |= leakGlobal
					}
				}
				// A store into the RECEIVER, `t.d = p` -- the setter every struct
				// with a buffer has. How long that lives is not knowable here: the
				// receiver belongs to whoever called, so the flag travels to the
				// call site, which knows whether it picked storage that outlives
				// its own frame.
				for _, v := range e.storedInReceiver(recvName, nodes) {
					if i := at(e.leakRoot(v)); i >= 0 {
						e.crossParams[cname][i] |= leakRecv
					}
				}
				// A store through a POINTER PARAMETER, `h.d = p` -- the same
				// setter, written as a plain function rather than a method. WHICH
				// parameter it reaches is what has to be carried: the call site
				// decides by the lifetime of the argument at that position, and
				// `fill(&g, a[:])` and `fill(&local, a[:])` differ in nothing else.
				if vs, slot := e.storedInPointerParam(fi, nodes); slot >= 0 {
					for _, v := range vs {
						if i := at(e.leakRoot(v)); i >= 0 {
							e.crossInto[cname][i] |= 1 << slot
						}
					}
				}
			}
			// A return hands the value back to the caller: which parameter it came
			// from is what lets the caller follow it to the storage it chose.
			if len(nodes) != 0 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == RETURN {
				for _, v := range e.returnedExprs(nodes) {
					if i := at(e.leakRoot(v)); i >= 0 {
						e.retParams[cname][i] = true
					}
				}
				// `return g(p)`: whatever g hands back of its own parameter, this
				// function hands back of the parameter it passed there.
				for _, c := range e.stmtCalls(nodes) {
					for j, a := range c.args {
						if i := at(e.leakRoot(a.ast)); i >= 0 {
							e.retEdges = append(e.retEdges, crossEdge{caller: cname, from: i, callee: c.callee, to: j})
						}
					}
				}
			}
			// Any call in the statement, go statement and send included: an
			// argument that is one of this function's parameters ties the two
			// together.
			for _, c := range e.stmtCalls(nodes) {
				owner := owners(c.args)
				for j, a := range c.args {
					if i := at(e.crossRoot(a.ast)); i >= 0 {
						e.crossEdges = append(e.crossEdges,
							crossEdge{caller: cname, from: i, callee: c.callee, to: j, recvAt: argLocal, argOwner: owner})
					}
				}
			}
			// The same for a METHOD call, which stmtCalls cannot name. The edge
			// carries whose receiver it is, because that is what says whether the
			// callee's leakRecv is a leak here too or the end of the matter.
			for _, c := range e.stmtMethodCalls(nodes, fi) {
				owner := owners(c.args)
				for j, a := range c.args {
					if i := at(e.crossRoot(a.ast)); i >= 0 {
						e.crossEdges = append(e.crossEdges, crossEdge{caller: cname, from: i,
							callee: c.callee, to: j, recv: c.recv, recvAt: c.recvAt, argOwner: owner})
					}
				}
			}
		})
	})
}

// closeCrossParams propagates the crossing summary along the recorded call edges
// until it stops changing. Every pass over the edges can only set flags, and there
// are finitely many, so it terminates.
func (e *emitter) closeCrossParams() {
	for changed := true; changed; {
		changed = false
		for _, g := range e.crossEdges {
			callee, caller := e.crossParams[g.callee], e.crossParams[g.caller]
			if g.to >= len(callee) || g.from >= len(caller) {
				continue
			}
			// A store into the callee's RECEIVER is a leak to whoever owns that
			// receiver, so what it means here depends on which one the call named.
			flags := callee[g.to]
			callerInto := e.crossInto[g.caller]
			into := func(slot int) {
				if slot < 0 || slot >= intoBits || g.from >= len(callerInto) || callerInto[g.from]&(1<<slot) != 0 {
					return
				}
				callerInto[g.from] |= 1 << slot
				changed = true
			}
			if flags&leakRecv != 0 {
				switch {
				case g.recv == recvOutlives:
					flags = flags&^leakRecv | leakGlobal
				case g.recv == recvParam:
					// The receiver is the caller's own parameter, so the callee's
					// store into it is the caller's store THROUGH that parameter:
					// the same fact, one call further out, where crossInto says it.
					into(g.recvAt)
					flags &^= leakRecv
				}
			}
			// And what the callee stores through ITS parameters, the caller stores
			// through whatever it passed at those positions -- a package variable
			// settling the matter here, its own parameter carrying it on.
			if calleeInto := e.crossInto[g.callee]; g.to < len(calleeInto) {
				for j := 0; j < intoBits; j++ {
					if calleeInto[g.to]&(1<<j) == 0 {
						continue
					}
					if owner := g.owner(j); owner == argOutlives {
						flags |= leakGlobal
					} else {
						into(owner)
					}
				}
			}
			if flags&^caller[g.from] == 0 {
				continue
			}
			caller[g.from] |= flags
			changed = true
		}
		for _, g := range e.retEdges {
			callee, caller := e.retParams[g.callee], e.retParams[g.caller]
			if g.to >= len(callee) || g.from >= len(caller) || !callee[g.to] || caller[g.from] {
				continue
			}
			caller[g.from] = true
			changed = true
		}
	}
}

// returnedExprs returns the operands of a return statement.
func (e *emitter) returnedExprs(nodes []Node) [][]int32 {
	var out [][]int32
	for _, n := range nodes {
		if n.sym != ExpressionList {
			continue
		}
		for c := range it(n.ast) {
			if c.sym == Expression {
				out = append(out, c.ast)
			}
		}
	}
	return out
}

// funcParamNames returns a function or method declaration's C name, the name it was
// declared with, its parameter names in order, its body, and -- for a method -- the
// name its receiver was declared with. The receiver is not among the parameters: it
// is not one at the call sites this feeds, which name arguments positionally. Its
// NAME is answered separately because a store into it is a leak whose lifetime only
// the call site knows (see leakRecv).
func (e *emitter) funcParamNames(d []int32) (funcInfo, bool) {
	name, sig, body, recv, ok := e.funcParts(d)
	if !ok || name == "" || body == nil {
		return funcInfo{}, false
	}
	fi := funcInfo{cname: mangle(e.curPkgPrefix, name), srcName: name, body: body}
	if recv != nil {
		rn, rct, _ := e.receiverInfo(recv)
		fi.cname = methodCName(methodBaseType(rct), name)
		fi.recvName, fi.recvCType = rn, rct
	}
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue // parameters are the only ParameterList; results are ResultList/Type
		}
		e.forEachParam(n.ast, func(nm string, ta []int32, _ bool) {
			fi.params = append(fi.params, nm)
			fi.ptrParam = append(fi.ptrParam, e.isPtrParam(ta))
			fi.ptrBase = append(fi.ptrBase, e.ptrParamBase(ta))
		})
	}
	return fi, true
}

// ptrParamBase names the type a parameter written `*T` points at, mangled, or ""
// for any other parameter. Two things want it, and both are why this reads the
// WRITTEN type rather than asking cType: a store through such a parameter reaches
// the caller's storage, and a method called on one is named after that type.
//
// cType is what must not be called here. It REFUSES an array by latching the
// emitter's error state, so asking it about every parameter -- from a pass that is
// only gathering facts, before any body is emitted -- failed the build of programs
// that were never in question. Reading the type as written asks nothing of it.
//
// Only `*T` for a plain declared T answers. `*pkg.T`, `**T` and a pointer to a
// slice or an array all yield "", having no name to be called after -- which is why
// "is this a pointer at all" is isPtrParam's separate question: a store through
// `*[]int` reaches the caller just as far, and answers no method.
// isPtrParam says whether a parameter's written type is a pointer, whatever it
// points at. That is the whole question for a STORE through it -- `*p = d` reaches
// the caller's storage for `*[]int` exactly as `p.d = d` does for `*H`.
func (e *emitter) isPtrParam(ta []int32) bool {
	nodes := slices.Collect(it(ta))
	return len(nodes) == 2 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == MUL && nodes[1].sym == Type
}

func (e *emitter) ptrParamBase(ta []int32) string {
	nodes := slices.Collect(it(ta))
	if len(nodes) != 2 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != MUL || nodes[1].sym != Type {
		return ""
	}
	tok, ok := e.soleToken(nodes[1].ast)
	if !ok || e.f.ch(tok) != IDENT {
		return ""
	}
	name := mangle(e.curPkgPrefix, e.src(tok))
	if !e.typeNames[name] {
		return "" // not a type declared here: nothing to be named after
	}
	return name
}

// funcInfo is what the crossing summary needs of one declaration: how it is named,
// its parameters and their types, its body, and -- for a method -- the receiver it
// was declared with. The receiver is not among the parameters: it is not one at the
// call sites this feeds, which name arguments positionally. Its NAME and TYPE are
// carried because a store into it is a leak whose lifetime only the call site knows
// (see leakRecv), and because a method it calls is named after that type.
type funcInfo struct {
	cname     string
	srcName   string
	params    []string
	ptrParam  []bool
	ptrBase   []string
	body      []int32
	recvName  string
	recvCType string
}

// eachStmt calls fn with the children of every Statement in ast, at any depth, so a
// go statement or a send nested in a loop or a block is seen like a top-level one.
func (e *emitter) eachStmt(ast []int32, fn func(nodes []Node)) {
	for n := range it(ast) {
		if n.sym == 0 {
			continue
		}
		if n.sym == Statement {
			fn(slices.Collect(it(n.ast)))
		}
		e.eachStmt(n.ast, fn)
	}
}

// crossRoot names the variable whose storage a value about to cross is a view of, or
// "" when there is none to name. A bare identifier and a re-slice of one both answer
// with the base variable -- sliceBackingIsFrame already resolves that shape, and only
// its verdict about the frame is unwanted here -- and `&x` answers with x.
func (e *emitter) crossRoot(ast []int32) string {
	if name, ok := e.addrOfRoot(ast); ok {
		return name
	}
	name, _ := e.sliceBackingIsFrame(ast)
	return name
}

// storedInReceiver returns the values a statement stores into the METHOD's receiver,
// `t.d = v` -- storedInPackageVar's counterpart for the storage a method is handed
// rather than the storage it can see. A bare `t = v` is not one: that rebinds the
// receiver variable itself, which dies with the call.
func (e *emitter) storedInReceiver(recvName string, nodes []Node) [][]int32 {
	base, values, suffixed, deref := e.assignThrough(nodes)
	// A selector, an index or a written-out dereference before the operator: a
	// store THROUGH the receiver, not to it.
	if recvName == "" || base != recvName || !(suffixed || deref) {
		return nil
	}
	return values
}

// storedInPointerParam returns the values a statement stores through one of the
// function's `*T` PARAMETERS, `h.d = v`, and which parameter that is.
//
// It is storedInReceiver's general form, and the one the summary was missing. A
// receiver is only the parameter a method call writes to the left of the dot; the
// same store through the same struct, in a plain function taking it as an argument,
// escapes exactly as far -- and was accepted, because leakRecv had no way to say
// "into parameter 2" and nothing else did either.
//
//	func fill(h *H, d []int) { h.d = d }   // d is stored through parameter 0
//
// Whether that outlives the caller's frame is the CALL SITE's question, as it is
// for a receiver: `fill(&g, a[:])` leaks and `fill(&local, a[:])` does not.
func (e *emitter) storedInPointerParam(fi funcInfo, nodes []Node) ([][]int32, int) {
	base, values, suffixed, deref := e.assignThrough(nodes)
	if base == "" || !(suffixed || deref) {
		return nil, -1
	}
	for i, nm := range fi.params {
		if nm == base && i < len(fi.ptrParam) && fi.ptrParam[i] && i < intoBits {
			return values, i
		}
	}
	return nil, -1
}

// assignThrough reads a statement of the form `base<suffix> = v...` and answers
// with the base identifier, the values assigned, and whether any selector or index
// stood between them -- which is what separates a store INTO a name from a store
// THROUGH it. The three callers differ only in what they ask of the base.
//
// Only a plain "=" qualifies. A ":=" declares a local, and a compound assignment
// reads what is there rather than storing what it is given.
func (e *emitter) assignThrough(nodes []Node) (base string, values [][]int32, suffixed, deref bool) {
	if len(nodes) != 2 || nodes[0].sym != AssignHead || nodes[1].sym != Postfix {
		return "", nil, false, false
	}
	if base = e.soleIdent(nodes[0].ast); base == "" {
		return "", nil, false, false
	}
	postfix := slices.Collect(it(nodes[1].ast))
	if len(postfix) == 0 || postfix[len(postfix)-1].sym != PostfixOp {
		return "", nil, false, false
	}
	op := slices.Collect(it(postfix[len(postfix)-1].ast))
	if len(op) != 2 || op[0].sym != 0 || e.f.ch(op[0].tok) != ASSIGN {
		return "", nil, false, false
	}
	for n := range it(op[1].ast) {
		if n.sym == Expression {
			values = append(values, n.ast)
		}
	}
	return base, values, len(postfix) > 1, e.derefStars(nodes[0].ast) != ""
}

// storedInPackageVar returns the values a statement stores into a package variable,
// `g = v`, `g.f = v` or `g[i] = v`. The target's ROOT is what decides: a field or an
// element of a package variable outlives every frame exactly as the variable does --
// so unlike the two above, no selector need stand between them.
func (e *emitter) storedInPackageVar(nodes []Node) [][]int32 {
	base, values, _, _ := e.assignThrough(nodes)
	if base == "" || !e.isPackageVar(base) {
		return nil
	}
	return values
}

// leakRoot names the variable a stored value came from: the expression itself when
// it is a bare name, and otherwise whatever crossRoot finds behind an address-of or
// a slice.
//
// The bare name is what the cog seeds do not need and this one does: `go f(p)`
// passes a slice or an address, while `g = p` stores a pointer parameter as it
// stands. Naming a parameter that holds no reference at all costs nothing -- the
// call site only refuses an argument that IS a frame reference.
func (e *emitter) leakRoot(ast []int32) string {
	if name, ok := e.exprIdent(ast); ok {
		return name
	}
	return e.crossRoot(ast)
}

// typeSwitchAliases maps each name a type switch binds to the operand it switched
// on, for every type switch in a body. What the binding names is the same value the
// operand named, so a question asked about one is asked about the other.
func (e *emitter) typeSwitchAliases(body []int32) map[string]string {
	alias := map[string]string{}
	var walk func(ast []int32)
	walk = func(ast []int32) {
		for n := range it(ast) {
			if n.sym == SwitchGuard {
				if name, operand, ok := e.typeSwitchNames(n.ast); ok {
					alias[name] = operand
				}
			}
			walk(n.ast)
		}
	}
	walk(body)
	return alias
}

// typeSwitchNames reads a type switch guard's bound name and its operand, by SHAPE
// alone. typeSwitchGuard answers the same question but resolves the operand's type
// and reports when it is not an interface -- which this caller runs too early to
// ask, the summaries being computed before any body has declared a local.
func (e *emitter) typeSwitchNames(guardAST []int32) (name, operand string, ok bool) {
	g, ok := e.f.switchGuardParts(guardAST)
	if !ok || !g.hasName {
		return "", "", false
	}
	// The BASE answers for an operand that is not a plain name: a reference into
	// the frame through `rs[i]` or `b.r` is one through `rs` or `b`, so asking the
	// base is right where it matters and conservative everywhere else. Answering
	// "no operand" instead would drop the binding out of the leak summary, which is
	// the hole that let a widened interface launder a pointer to a local.
	if operand, _, ok = e.typeSwitchOperand(g.value.ast); !ok {
		return "", "", false
	}
	if name, ok = e.exprIdent(g.name.ast); !ok || name == "" || name == "_" {
		return "", "", false
	}
	return name, operand, true
}

// goStmtArgs returns the argument expressions of a go statement.
func (e *emitter) goStmtArgs(nodes []Node) []Node {
	for _, n := range nodes {
		if n.sym == CallSuffix {
			return e.callArgExprs(n.ast)
		}
	}
	return nil
}

// sendValue returns the value expression of a send statement, `ch <- v`.
func (e *emitter) sendValue(nodes []Node) ([]int32, bool) {
	if len(nodes) != 2 || nodes[0].sym != AssignHead || nodes[1].sym != Postfix {
		return nil, false
	}
	postfix := slices.Collect(it(nodes[1].ast))
	if len(postfix) == 0 || postfix[len(postfix)-1].sym != PostfixOp {
		return nil, false
	}
	op := slices.Collect(it(postfix[len(postfix)-1].ast))
	if len(op) != 2 || op[0].sym != 0 || e.f.ch(op[0].tok) != ARROW || op[1].sym != Expression {
		return nil, false
	}
	return op[1].ast, true
}

// stmtCall is one call found in a statement: the callee's C name and its arguments.
type stmtCall struct {
	callee string
	args   []Node
}

// methodCall is one `recv.m(args)` a statement makes: the method's C name, whose
// storage the receiver is, and the arguments.
type methodCall struct {
	callee string
	recv   recvKind
	recvAt int
	args   []Node
}

// stmtMethodCalls finds the METHOD calls a statement makes, which stmtCalls does not:
// it resolves a callee by name, and a method has none until the receiver's type is
// known. Without them a parameter handed to a method went unfollowed entirely, so
// `func (t *H) set(d []int) { t.inner(d) }` -- one method delegating to another --
// carried no requirement back to its callers and the leak inner made was invisible.
//
// Three receivers are resolvable here, and they are the three that leak. The
// enclosing method's OWN receiver, whose type this declaration states; a PACKAGE
// variable, whose type is known because package variables are emitted before this
// pass runs; and a `*T` PARAMETER, whose type ptrParamBase reads as written.
//
// That last one was skipped while resolving it meant asking cType -- which REFUSES
// an array by latching the emitter's error state, from a pass that is only
// gathering facts. Reading the written type asks nothing of cType, so the case
// costs nothing it used to.
//
// What is still unfollowed is a method called on a LOCAL. Naming it means finding
// the declaration in the body, and the direct store through such a receiver is
// checkRecvLeak's, which sees it.
func (e *emitter) stmtMethodCalls(nodes []Node, fi funcInfo) []methodCall {
	var out []methodCall
	// The statement-level call, `t.inner(d)`. It is an AssignHead beside a Postfix
	// and carries no Factor at all, which is why the walk below -- which reads
	// Factors -- sees nothing of it. stmtCalls makes the same distinction.
	if len(nodes) == 2 && nodes[0].sym == AssignHead && nodes[1].sym == Postfix {
		if recv := e.soleIdent(nodes[0].ast); recv != "" {
			suffix := slices.Collect(it(nodes[1].ast))
			if len(suffix) == 2 && suffix[0].sym == Selector && suffix[1].sym == CallSuffix {
				if c, isM := e.methodCallOf(recv, suffix, fi); isM {
					out = append(out, c)
				}
			}
		}
	}
	var walk func(ast []int32)
	walk = func(ast []int32) {
		for n := range it(ast) {
			if n.sym == 0 {
				continue
			}
			if n.sym == Factor {
				kids := slices.Collect(it(n.ast))
				if recv, suffix, ok := e.factorCall(kids); ok && len(suffix) == 2 &&
					suffix[0].sym == Selector && suffix[1].sym == CallSuffix {
					if c, isM := e.methodCallOf(recv, suffix, fi); isM {
						out = append(out, c)
					}
				}
			}
			walk(n.ast)
		}
	}
	for _, n := range nodes {
		walk(n.ast)
	}
	return out
}

// methodCallOf resolves one `recv.m(args)` against the enclosing declaration.
func (e *emitter) methodCallOf(recv string, suffix []Node, fi funcInfo) (methodCall, bool) {
	method := e.soleIdent(suffix[0].ast)
	if method == "" {
		return methodCall{}, false
	}
	ct, kind, at := "", recvNone, argLocal
	switch i := slices.Index(fi.params, recv); {
	case recv == fi.recvName && fi.recvCType != "":
		ct, kind = fi.recvCType, recvOwn
	case e.isPackageVar(recv):
		ct, kind = e.globals[e.globalC(recv)], recvOutlives
	case i >= 0 && i < len(fi.ptrBase) && fi.ptrBase[i] != "" && i < intoBits:
		// A `*T` PARAMETER. ptrBase read the type as written, so naming the method
		// costs nothing and risks nothing -- which is what used to leave this case
		// out. The base is already mangled and starless, as methodBaseType wants.
		ct, kind, at = fi.ptrBase[i], recvParam, i
	}
	if ct == "" || kind == recvNone {
		return methodCall{}, false
	}
	cname := methodCName(methodBaseType(ct), method)
	if _, isMethod := e.funcRet[cname]; !isMethod {
		return methodCall{}, false
	}
	return methodCall{callee: cname, recv: kind, recvAt: at, args: e.callArgExprs(suffix[1].ast)}, true
}

// stmtCalls finds the calls a statement makes, so that an argument which is one of
// the enclosing function's parameters can be tied to the callee's parameter in that
// position. Two shapes are recognised: the statement-level call, `f(x)`, and a call
// on a plain name anywhere inside an expression, `y := f(x)` or `return f(x)`.
//
// A callee this does not resolve to a name -- a method, or a call through a
// selector -- yields no edge, so the requirement simply is not propagated through
// it. That errs the way the rest of the analysis does: towards accepting.
func (e *emitter) stmtCalls(nodes []Node) []stmtCall {
	var out []stmtCall
	add := func(name string, suffix []int32) {
		if _, ok := e.userFunc(name); ok {
			out = append(out, stmtCall{callee: e.funcCallC(name), args: e.callArgExprs(suffix)})
		}
	}
	// The statement-level call and the go statement: the callee is in the
	// AssignHead, a sibling of the Postfix holding the CallSuffix.
	if len(nodes) != 0 {
		if head := nodes[0]; head.sym == AssignHead || (head.sym == 0 && e.f.ch(head.tok) == GO) {
			if name := e.soleIdent(headOf(nodes).ast); name != "" {
				for _, n := range nodes {
					for c := range it(n.ast) {
						if c.sym == CallSuffix {
							add(name, c.ast)
						}
					}
					if n.sym == CallSuffix {
						add(name, n.ast)
					}
				}
			}
		}
	}
	// A call inside an expression: the callee is the identifier before the CallSuffix.
	// It is carried into a nested level because the two are not always siblings --
	// `f(x)` in an expression is an identifier followed by a Postfix that holds the
	// CallSuffix -- but never past the call it belongs to, so `f(g(x))` pairs g with
	// its own suffix and not with f's.
	var walk func(ast []int32, outer string)
	walk = func(ast []int32, outer string) {
		last := outer
		for n := range it(ast) {
			switch {
			case n.sym == 0:
				if e.f.ch(n.tok) == IDENT {
					last = e.src(n.tok)
					continue
				}
			case n.sym == CallSuffix:
				if last != "" {
					add(last, n.ast)
				}
				walk(n.ast, "")
			default:
				walk(n.ast, last)
			}
			last = ""
		}
	}
	for _, n := range nodes {
		walk(n.ast, "")
	}
	return out
}

// headOf returns the AssignHead of a statement's children, which a go statement
// carries after its keyword and a call statement first.
func headOf(nodes []Node) Node {
	for _, n := range nodes {
		if n.sym == AssignHead {
			return n
		}
	}
	return Node{}
}

// addrOfRoot reports whether an expression is the address of a variable -- `&x`,
// `&x.f`, `&x[i]` -- and names the variable.
func (e *emitter) addrOfRoot(ast []int32) (string, bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != UnaryExpr {
		return "", false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) < 2 || kids[0].sym != UnaryOp {
		return "", false
	}
	if tok, ok := e.unaryOpTok(kids[0].ast); !ok || e.f.ch(tok) != AND {
		return "", false
	}
	// The operand is a Factor: its base identifier is the variable whose storage the
	// address reaches, whatever field or index suffix follows.
	fac := kids[len(kids)-1]
	suffixed := containsSym(slices.Collect(it(fac.ast)), FactorSuffix)
	for n := range it(fac.ast) {
		if n.sym == 0 && e.f.ch(n.tok) == IDENT {
			// "&p.f" and "&p[i]" through a POINTER reach what the pointer points at,
			// which is the caller's storage, not this frame's. Only "&p" itself takes
			// this frame's -- the pointer variable's own cell. A slice root is not a
			// frame reference either; where its backing came from is what decides
			// that, and sliceBackingIsFrame has already asked.
			name := e.src(n.tok)
			if suffixed {
				if ct, ok := e.varType(name); ok && (e.isPointer(ct) || e.isSliceCType(ct)) {
					return "", false
				}
			}
			return name, true
		}
	}
	return "", false
}

// paramSliceTypes returns one entry per declared parameter: the C slice type when
// that parameter is a slice, "" otherwise. It answers the single question a call
// site asks of a parameter -- whether a bare `nil` argument there is a slice
// header rather than a null pointer -- so only the slice shape is resolved. A
// parameter of any other type never reaches cType here, which keeps this pass from
// reporting a type the signature's own emission reports anyway, earlier and
// against the wrong position.
func (e *emitter) paramSliceTypes(sig []int32) []string {
	var out []string
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue // parameters are the only ParameterList; results are ResultList/Type
		}
		e.forEachParamV(n.ast, func(_ string, ta []int32, _, variadic bool) {
			if variadic {
				out = append(out, sliceCName(e.cType(ta)))
				return
			}
			elem, ok := e.sliceType(ta)
			if !ok {
				out = append(out, "")
				return
			}
			out = append(out, sliceCName(elem))
		})
	}
	return out
}

// eachFuncDeclAST calls fn with the AST of each FuncDecl (function or method) in a
// file's AST.
func (e *emitter) eachFuncDeclAST(ast []int32, fn func(d []int32)) {
	for n := range it(ast) {
		if n.sym != SourceFile {
			continue
		}
		for c := range it(n.ast) {
			if c.sym != TopLevelDecl {
				continue
			}
			for d := range it(c.ast) {
				if d.sym == FuncDecl {
					fn(d.ast)
				}
			}
		}
	}
}

// funcDefCName is the C name a top-level function is defined and prototyped under.
//
// Every function but init takes its mangled source name. A package may declare
// several init functions, and Go treats each as its own -- so they cannot all be
// called init in C, which would be a redefinition. Each takes a numbered name from
// the reserved prefix instead, assigned when the prototype pass first sees the
// declaration and looked up by that declaration's position afterwards, so the two
// passes agree on which init is which.
func (e *emitter) funcDefCName(name string, decl []int32) string {
	if name != "init" {
		return mangle(e.curPkgPrefix, name)
	}
	key := e.declKey(decl)
	if cname, ok := e.initNames[key]; ok {
		return cname
	}
	cname := fmt.Sprintf("ogo_init%d", len(e.initNames))
	e.initNames[key] = cname
	return cname
}

// declKey identifies a declaration by where its first token stands, which is the
// one thing the prototype pass and the definition pass see alike.
func (e *emitter) declKey(decl []int32) string {
	for n := range it(decl) {
		return e.f.tok(n.Pos()).Position().String()
	}
	return ""
}

// retStructName is the C typedef name of a multi-result function's result struct.
func (e *emitter) retStructName(fn string) string { return e.retStructNameOf(e.funcRet[fn]) }

// retStructNameOf is that name keyed by the RESULT TYPES rather than by the
// function, and mints the typedef on first sight.
//
// Naming it after the function was what kept a multi-result function from being a
// value: `divmod` and `split`, both `func(int, int) (int, int)`, returned
// ogo_ret_divmod and ogo_ret_split, so there was no single C type for a variable of
// that function type to return, and taking either as a value was refused. Keyed by
// shape they return the same struct and the function type is spellable -- the same
// reasoning anonStructType already uses for an anonymous struct, where Go gives two
// of them one identity when their fields match.
func (e *emitter) retStructNameOf(res []string) string {
	if len(res) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString("ogo_ret")
	for _, ct := range res {
		b.WriteByte('_')
		b.WriteString(cTypeIdent(ct))
	}
	// The readable name is the result types run together, which two DIFFERENT lists
	// can spell the same way once a type name contains an underscore: `(a_b, int)`
	// and `(a, b_int)` both give ogo_ret_a_b_int. That silently gave the second
	// function the first one's struct -- an int64 result came back truncated. So the
	// name is checked against the list it already stands for and numbered apart when
	// they differ; the common case keeps the name it always had.
	// Looked up by the result LIST, not by the name: the same list must answer with
	// the same struct every time it is asked, and asking by name would mint a fresh
	// one on each call once a collision had pushed it off the base name.
	key := strings.Join(res, ",")
	if had, ok := e.retStructByKey[key]; ok {
		return had
	}
	name := b.String()
	for {
		if _, taken := e.retStructs[name]; !taken {
			break
		}
		e.retSeq++
		name = fmt.Sprintf("%s_%d", b.String(), e.retSeq)
	}
	e.retStructByKey[key] = name
	e.retStructs[name] = key
	text := e.captureC(func() {
		e.emit("typedef struct { ")
		for i, ct := range res {
			fmt.Fprintf(e.w, "%s _%d; ", ct, i)
		}
		e.emit("} " + name + ";\n")
	})
	// Each result is held by value, so every one of their typedefs comes first.
	e.addTypedef(name, text, res...)
	return name
}

// cTypeIdent folds a C type into the identifier text a generated name is built
// from: a pointer becomes a "_p" suffix, and anything else that is not an
// identifier character becomes an underscore.
func cTypeIdent(ct string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(ct) {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '*':
			b.WriteString("_p")
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// emitPrototypes emits a forward prototype for every user function and method in a
// file (all but main, which C declares implicitly). Run before the definitions so a
// call need not follow its callee's definition.
func (e *emitter) emitPrototypes(ast []int32) {
	e.eachFuncDeclAST(ast, func(d []int32) {
		name, sig, _, recv, ok := e.funcParts(d)
		if !ok || name == "" || (recv == nil && name == "main") {
			return
		}
		var proto string
		if recv == nil {
			proto = e.funcSignatureC(e.funcDefCName(name, d), sig)
		} else {
			rn, rct, _ := e.receiverInfo(recv)
			proto = e.methodSignatureC(methodCName(methodBaseType(rct), name), rn, rct, sig)
		}
		if proto != "" {
			e.emit(proto + ";\n")
		}
		// Go runs each package's init() before main, imports first. Recorded here, in
		// the prototype pass, in the order they will be called.
		if recv == nil && name == "init" {
			e.initFuncs = append(e.initFuncs, e.funcDefCName(name, d))
		}
	})
}

func (e *emitter) emitFuncDecl(ast []int32) {
	name, sig, body, recv, ok := e.funcParts(ast)
	if !ok {
		return
	}
	if body == nil {
		// A function declared without a body is implemented elsewhere -- which the
		// grammar provides for, and which the p2 package is entirely made of: each of
		// its functions is one C intrinsic the call site substitutes. There is
		// nothing to emit, and emitting a prototype would name a symbol that does not
		// exist.
		return
	}

	if e.wroteDecl {
		e.emit("\n")
	}
	e.wroteDecl = true

	switch {
	case e.testEntry != "":
		// A test binary is entered through its generated runner. The package's own
		// main, if it has one, is not part of it -- go test does not run it either.
		if recv == nil && name == e.testEntry {
			e.emitMain(sig, body)
			return
		}
		if recv == nil && name == "main" {
			return
		}
	case recv == nil && name == "main":
		e.emitMain(sig, body)
		return
	}
	e.locals = map[string]string{}
	e.curParams = map[string]bool{}
	e.arrays = map[string]arrDim{}
	e.sliceVars = map[string]string{}
	e.frameBacked = map[string]bool{}
	e.frameHolder = map[string]string{}
	e.tmp = 0
	e.defers = nil
	e.deferReplay = -1
	e.curResultNames = nil
	e.curResultTypes = nil
	// A method is a function with a mangled name and its receiver as the first
	// parameter, bound in the local environment so the body reads it like any local
	// (a pointer receiver's field access is then `->`, exactly as for a `*T` param).
	var proto string
	var emptyRecvName string // receiver of an empty-struct method: nothing to access, so (void) it
	var recvName, recvCType string
	if recv == nil {
		cname := e.funcDefCName(name, ast)
		proto = e.funcSignatureC(cname, sig)
		e.curFunc = cname
	} else {
		var recvNamed bool
		recvName, recvCType, recvNamed = e.receiverInfo(recv)
		cname := methodCName(methodBaseType(recvCType), name)
		proto = e.methodSignatureC(cname, recvName, recvCType, sig)
		e.curFunc = cname
		if a, isArr := e.namedArrays[recvCType]; isArr {
			// Recorded as an ARRAY, not a plain local: that is what makes `r[i]`
			// index it, `len(r)` fold to its extent and a bounds check apply.
			e.arrays[recvName] = a
		} else {
			e.locals[recvName] = recvCType
		}
		// (void) the receiver when the body does not use it: an unnamed receiver
		// `(T)` (the source never names it), a receiver whose type is an empty struct
		// (nothing to access), or a named one the body simply never mentions -- a
		// method that answers the same thing for every value of its type. All three
		// would otherwise draw -Wunused-parameter, which the run harness fails on.
		//
		// The mention test is by name, so a local of the same name in a nested scope
		// counts as a use. That over-emits a no-op and never under-emits, which is
		// the safe direction and the one declareNamedResults takes for the same
		// reason.
		flds, isStruct := e.structs[methodBaseType(recvCType)]
		if !recvNamed || (isStruct && len(flds) == 0) || !e.bodyMentions(body, recvName) {
			emptyRecvName = recvName
		}
	}
	if proto == "" {
		return
	}
	e.bindParams(sig)
	e.emit(proto + " {\n")
	e.indent++
	e.emitRecvCopy(recvName, recvCType)
	e.emitParamCopies(sig)
	e.emitParamVoids(sig, body)
	if emptyRecvName != "" {
		e.ind()
		e.emit("(void)" + emptyRecvName + ";\n")
	}
	e.declareNamedResults(sig, body)
	// A bare "return" (legal only when every result is named) returns these. A
	// blank result "_" has no C variable, so it contributes its zero value.
	e.curResultNames, e.curResultTypes = e.resultInfo(sig)
	for i, nm := range e.curResultNames {
		if nm == "" || nm == "_" {
			e.curResultNames[i] = "0"
		}
	}
	// The body goes to a buffer so the defer temporaries can be declared ahead of
	// it. They must be at function scope -- a defer in a nested block captures its
	// arguments there, but the call is replayed at a return that block has exited
	// -- and the full set is only known once the body has been walked.
	saved := e.w
	var bodyBuf bytes.Buffer
	e.w = &bodyBuf
	e.emitBlockStmts(body)
	// A body that falls off the end (no trailing return) runs its deferred calls
	// here; one ending in a return already replayed them at that return.
	if len(e.defers) != 0 && !e.bodyEndsInReturn(body) {
		e.emitDeferred()
	}
	e.w = saved
	e.emitDeferDecls()
	e.w.Write(bodyBuf.Bytes())
	e.indent--
	e.emit("}\n")
}

// liftFuncLit emits a function literal as a file-scope function of a minted name
// and returns that name, which is what the expression becomes. C has no nested
// functions, and this language has no closures to need one: a literal captures
// nothing (the checker says so), so lifting it changes nothing about what it means.
//
// Everything the body emitter keeps about the function being emitted is saved and
// restored around it, since the literal is met in the middle of another body.
func (e *emitter) liftFuncLit(lit Node) (string, bool) {
	var sig, body []int32
	for n := range it(lit.ast) {
		switch n.sym {
		case Signature:
			sig = n.ast
		case Block:
			body = n.ast
		}
	}
	if sig == nil || body == nil {
		e.fail("a function literal needs a signature and a body")
		return "", false
	}
	// Named without a leading underscore, unlike the statement temporaries: the
	// backend reserves that spelling for its own intrinsics at file scope, and a
	// function taking it produced a binary the loader would not accept.
	cname := mangle(e.curPkgPrefix, fmt.Sprintf("ogo_lit%d", e.liftSeq))
	e.liftSeq++

	// Recorded before the body is walked, so a literal that calls itself through a
	// variable, or another literal lifted after it, resolves.
	e.funcRet[cname] = nil
	e.funcValueTypes[cname] = e.funcSigCParts(sig)
	e.funcSliceParams[cname] = e.paramSliceTypes(sig)
	if _, at := e.variadicElem(sig); at >= 0 {
		e.funcVariadic[cname] = at
	}
	e.funcParams[cname], e.funcArrayParams[cname] = e.cParamTypes(sig)

	type state struct {
		locals                         map[string]string
		arrays                         map[string]arrDim
		sliceVars                      map[string]string
		frameBacked                    map[string]bool
		frameHolder                    map[string]string
		tmp, indent, deferReplay       int
		defers                         []deferredCall
		curFunc                        string
		curResultNames, curResultTypes []string
		prologue                       []string
		w                              io.Writer
	}
	saved := state{
		locals: e.locals, arrays: e.arrays, sliceVars: e.sliceVars,
		frameBacked: e.frameBacked, frameHolder: e.frameHolder,
		tmp: e.tmp, indent: e.indent, deferReplay: e.deferReplay, defers: e.defers,
		curFunc: e.curFunc, curResultNames: e.curResultNames, curResultTypes: e.curResultTypes,
		prologue: e.prologue, w: e.w,
	}
	e.locals = map[string]string{}
	e.curParams = map[string]bool{}
	e.arrays = map[string]arrDim{}
	e.sliceVars = map[string]string{}
	e.frameBacked = map[string]bool{}
	e.frameHolder = map[string]string{}
	e.tmp, e.indent, e.deferReplay, e.defers, e.prologue = 0, 0, -1, nil, nil
	e.curFunc = cname

	var def bytes.Buffer
	e.w = &def
	proto := e.funcSignatureC(cname, sig)
	if proto != "" {
		e.bindParams(sig)
		e.emit(proto + " {\n")
		e.indent++
		e.emitParamCopies(sig)
		e.emitParamVoids(sig, body)
		e.declareNamedResults(sig, body)
		e.curResultNames, e.curResultTypes = e.resultInfo(sig)
		for i, nm := range e.curResultNames {
			if nm == "" || nm == "_" {
				e.curResultNames[i] = "0"
			}
		}
		var bodyBuf bytes.Buffer
		inner := e.w
		e.w = &bodyBuf
		e.emitBlockStmts(body)
		if len(e.defers) != 0 && !e.bodyEndsInReturn(body) {
			e.emitDeferred()
		}
		e.w = inner
		e.emitDeferDecls()
		e.w.Write(bodyBuf.Bytes())
		e.indent--
		e.emit("}\n")
	}
	_, resTypes := e.cSig(sig)
	e.funcRet[cname] = resTypes

	e.locals, e.arrays, e.sliceVars = saved.locals, saved.arrays, saved.sliceVars
	e.frameBacked, e.frameHolder = saved.frameBacked, saved.frameHolder
	e.tmp, e.indent, e.deferReplay, e.defers = saved.tmp, saved.indent, saved.deferReplay, saved.defers
	e.curFunc, e.curResultNames, e.curResultTypes = saved.curFunc, saved.curResultNames, saved.curResultTypes
	e.prologue, e.w = saved.prologue, saved.w
	if proto == "" {
		return "", false
	}
	e.liftedProtos = append(e.liftedProtos, proto)
	e.liftedDefs = append(e.liftedDefs, def.String())
	return cname, true
}

// factorMethodValue recognises "x.M" standing as a VALUE: an identifier and a
// single Selector, with no call after it. A call is factorCall's, and a field read
// resolves to a field rather than to a method, so neither reaches here.
func (e *emitter) factorMethodValue(kids []Node) (base, method string, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", "", false
	}
	steps := slices.Collect(it(kids[1].ast))
	if len(steps) != 1 || steps[0].sym != Selector {
		return "", "", false
	}
	base = e.src(kids[0].tok)
	method = e.soleIdent(steps[0].ast)
	if base == "" || method == "" {
		return "", "", false
	}
	rct, isVar := e.varType(base)
	if !isVar || !e.isUserType(methodBaseType(rct)) {
		return "", "", false
	}
	if _, isMethod := e.methodValueTypes[methodCName(methodBaseType(rct), method)]; !isMethod {
		return "", "", false
	}
	return base, method, true
}

// liftMethodValue emits a method value as a function of its own with the receiver
// bound, and returns that function's name -- which is what the expression becomes,
// an ordinary one-word function pointer like any other function value.
//
// This is why a method value costs nothing that anything else pays. Go's
// representation -- a value pointing at a struct whose first word is the code
// pointer -- would carry ANY receiver, and was measured to cost about a quarter of
// the time of every call through a function value on this part
// (doc/funcval-cost.c). Binding the receiver at compile time instead needs no
// representation at all, and the checker refuses what it cannot bind.
func (e *emitter) liftMethodValue(base, method string) (string, bool) {
	rct, _ := e.varType(base)
	bt := methodBaseType(rct)
	mcname := methodCName(bt, method)
	if !e.methodPtr[mcname] {
		e.fail("cannot take %s.%s as a value: only a pointer-receiver method may be taken here", base, method)
		return "", false
	}
	key := e.varRef(base) + "." + method
	if cn, done := e.methodValueOf[key]; done {
		return cn, true
	}
	fv := e.methodValueTypes[mcname]
	if len(fv.res) > 1 {
		e.fail("a method with more than one result cannot be used as a value yet")
		return "", false
	}
	ret := "void"
	if len(fv.res) == 1 {
		ret = fv.res[0]
	}
	cname := mangle(e.curPkgPrefix, fmt.Sprintf("ogo_mv%d", e.liftSeq))
	e.liftSeq++

	var params, args []string
	for i, pt := range fv.params {
		nm := fmt.Sprintf("p%d", i)
		params = append(params, pt+" "+nm)
		args = append(args, nm)
	}
	sigText := "void"
	if len(params) != 0 {
		sigText = strings.Join(params, ", ")
	}
	call := mcname + "(&" + e.varRef(base)
	if len(args) != 0 {
		call += ", " + strings.Join(args, ", ")
	}
	call += ")"
	proto := ret + " " + cname + "(" + sigText + ")"
	body := "\t" + call + ";\n"
	if ret != "void" {
		body = "\treturn " + call + ";\n"
	}
	e.liftedProtos = append(e.liftedProtos, proto)
	e.liftedDefs = append(e.liftedDefs, proto+" {\n"+body+"}\n")
	e.funcValueTypes[cname] = fv
	e.funcRet[cname] = fv.res
	e.funcParams[cname] = fv.params
	e.methodValueOf[key] = cname
	return cname, true
}

// factorFuncLit returns the FuncLiteral a Factor begins with, and the suffix that
// follows it -- which is a call, "func() int { ... }()", and nothing else the
// grammar admits there.
func (e *emitter) factorFuncLit(kids []Node) (lit Node, suffix []Node, ok bool) {
	if len(kids) == 0 || len(kids) > 2 || kids[0].sym != FuncLiteral {
		return Node{}, nil, false
	}
	if len(kids) == 2 {
		if kids[1].sym != FactorSuffix {
			return Node{}, nil, false
		}
		suffix = slices.Collect(it(kids[1].ast))
	}
	return kids[0], suffix, true
}

// bodyEndsInReturn reports whether a function body's last top-level statement is a
// return, so deferred calls need not be replayed again at the closing brace.
func (e *emitter) bodyEndsInReturn(body []int32) bool {
	var lastAST []int32
	for n := range it(body) {
		if n.sym == Statement {
			lastAST = n.ast
		}
	}
	nodes := slices.Collect(it(lastAST))
	return len(nodes) != 0 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == RETURN
}

// declareNamedResults declares a function's named result parameters as zero-
// initialized locals (P2 stack locals are not auto-zeroed) and binds them in the
// local type environment, so the body may assign and read them like Go's named
// results. Unnamed and blank results declare nothing.
//
// A named result that the body never reads or writes and that no naked return
// hands back is not emitted as a C local at all: it would only draw an
// unused-variable warning ("(q, r int) { return a, b }" is idiomatic Go). Its
// values are supplied directly by each explicit return.
func (e *emitter) declareNamedResults(sig, body []int32) {
	names, types := e.resultInfo(sig)
	naked := e.bodyHasNakedReturn(body)
	for i, nm := range names {
		if nm == "" || nm == "_" {
			continue
		}
		e.locals[nm] = types[i]
		if !naked && !e.bodyMentions(body, nm) {
			continue
		}
		e.ind()
		// A struct, a string or a slice result zeroes with braces: C has no scalar
		// zero for an aggregate, and "= 0" is an invalid initializer there.
		e.emit(types[i] + " " + nm + " = " + e.zeroInitC(types[i]) + ";\n")
	}
}

// bodyHasNakedReturn reports whether ast contains a bare "return" -- a return
// statement with no ExpressionList -- anywhere, including in nested blocks. A
// naked return reads the named result variables, so their declaration cannot be
// elided when one is present.
func (e *emitter) bodyHasNakedReturn(ast []int32) bool {
	for n := range it(ast) {
		if n.sym == 0 {
			continue
		}
		hasRet, hasExpr := false, false
		for c := range it(n.ast) {
			switch {
			case c.sym == 0 && e.f.ch(c.tok) == RETURN:
				hasRet = true
			case c.sym == ExpressionList:
				hasExpr = true
			}
		}
		if hasRet && !hasExpr {
			return true
		}
		if e.bodyHasNakedReturn(n.ast) {
			return true
		}
	}
	return false
}

// bodyMentions reports whether name appears as an identifier anywhere in ast,
// used to decide whether a named result is actually read or written by the body.
func (e *emitter) bodyMentions(ast []int32, name string) bool {
	for n := range it(ast) {
		if n.sym == 0 {
			if e.f.ch(n.tok) == IDENT && e.src(n.tok) == name {
				return true
			}
			continue
		}
		if e.bodyMentions(n.ast, name) {
			return true
		}
	}
	return false
}

// funcParts pulls the name, signature subtree, body subtree and receiver subtree
// from a FuncDecl AST. recv is non-nil for a method (a Receiver is present); body
// is nil for a bodyless declaration. ok is false only if the walk hit an
// unexpected element.
func (e *emitter) funcParts(ast []int32) (name string, sig, body, recv []int32, ok bool) {
	ok = true
	for n := range it(ast) {
		switch n.sym {
		case 0:
			if e.f.ch(n.tok) == IDENT {
				name = e.src(n.tok)
			}
		case Receiver:
			recv = n.ast
		case Signature:
			sig = n.ast
		case Block:
			body = n.ast
		default:
			e.fail("unsupported in function declaration: %v", n.sym)
			ok = false
		}
	}
	return name, sig, body, recv, ok
}

// recvSynthName is the C name given to an unnamed method receiver `(T)`. flexcc
// drops the argument slot of an unnamed parameter (see unnamedParamName), so the
// receiver always needs a name in the emitted C even when the source omits one;
// the body cannot refer to it, so it is (void)-cast like any synthetic parameter.
const recvSynthName = "_ogo_recv"

// receiverInfo parses a Receiver subtree `"(" ParamDecl ")"`, returning the
// receiver's name, its C type (e.g. "Point" or "Point*"), and whether the source
// named it. An unnamed or blank receiver reports named=false and takes the
// synthetic recvSynthName so the emitted C parameter still has an identifier.
func (e *emitter) receiverInfo(recv []int32) (name, ctype string, named bool) {
	decls := e.f.paramDecls(recv)
	if len(decls) == 0 {
		return recvSynthName, "", false
	}
	d := decls[0]
	// An ARRAY receiver is asked for BEFORE cType, which models no array type and
	// latches "unsupported type" when asked about one. The DEFINED name stands for
	// it: that is what methodCName wants for the method's namespace, and what the
	// pointer form `*Row` already yields.
	if a, isArr := e.arrayDim(d.TypeAST.ast); isArr && a.name != "" {
		ctype = a.name
	} else {
		ctype = e.cType(d.TypeAST.ast)
	}
	if len(d.Names) != 0 && d.Names[0].Src() != "_" {
		return d.Names[0].Src(), ctype, true
	}
	return recvSynthName, ctype, false
}

// methodBaseType is a receiver C type without any pointer star: a type's value and
// pointer methods share this base, so `T` and `*T` methods live in one C namespace,
// as the checker requires (a value/pointer method-name collision is an error).
func methodBaseType(recvCType string) string { return strings.TrimSuffix(recvCType, "*") }

// methodCName mangles a method to its C function name `<BaseType>_<method>`.
// methodCName mangles a method to its C name, baseType_method. baseType is already
// the receiver type's mangled C name (package-namespaced), so a method is namespaced
// by its type and cannot collide across packages; the method name is passed through
// cIdent so a Unicode method name is a valid C identifier too.
func methodCName(baseType, method string) string { return baseType + "_" + userIdent(method) }

// emitMain emits `func main()` as `int main(void)`; main takes no parameters or
// results.
func (e *emitter) emitMain(sig, body []int32) {
	params, resTypes := e.cSig(sig)
	if params != "" || len(resTypes) != 0 {
		e.fail("func main must have no parameters or results")
		return
	}
	e.locals = map[string]string{}
	e.curParams = map[string]bool{}
	e.arrays = map[string]arrDim{}
	e.sliceVars = map[string]string{}
	e.frameBacked = map[string]bool{}
	e.frameHolder = map[string]string{}
	e.tmp = 0
	e.defers = nil
	e.deferReplay = -1
	e.curFunc = "main"
	e.emit("int main(void) {\n")
	e.indent++
	if e.needsPkgInit() {
		// Package initialization runs before anything in main, as in Go.
		e.ind()
		e.emit(pkgInitCName + "();\n")
	}
	// The body goes to a buffer so the defer temporaries can be declared ahead of
	// it, exactly as emitFunc does. Without this main was the one function whose
	// deferred call could not capture an argument: the capture was emitted and the
	// temporary it assigned to never declared, so any `defer f(x)` in main failed
	// to build with "Unknown symbol '_ogo_defer0_a0'". The goldens missed it because
	// a literal argument needs no temporary and stays inline.
	e.mainRet = true
	saved := e.w
	var bodyBuf bytes.Buffer
	e.w = &bodyBuf
	e.emitBlockStmts(body)
	if len(e.defers) != 0 && !e.bodyEndsInReturn(body) {
		e.emitDeferred()
	}
	e.w = saved
	e.mainRet = false
	e.emitDeferDecls()
	e.w.Write(bodyBuf.Bytes())
	e.ind()
	e.emit("return 0;\n")
	e.indent--
	e.emit("}\n")
}

// funcSignatureC builds the C signature `<ret> name(params)` for a user function,
// e.g. `int add(int a, int b)`, `void run(void)`, or -- for more than one result
// -- `ogo_ret_divmod divmod(int a, int b)`.
func (e *emitter) funcSignatureC(name string, sig []int32) string {
	// An ARRAY result is handed back through an out parameter, which leads the
	// list: the function returns void and writes what the caller gave it. Asked
	// before cSig, which refuses an array result -- rightly, for every shape but
	// this one.
	if a, ok := e.arrayResultOf(sig); ok {
		e.funcArrayRet[name] = a
		out := arrayResultCType(a) + " " + arrayResultParam
		if params, _ := e.cParams(sig); params != "" {
			out += ", " + params
		}
		return "void " + name + "(" + out + ")"
	}
	params, resTypes := e.cSig(sig)
	if params == "" {
		params = "void"
	}
	return e.cReturnType(name, resTypes) + " " + name + "(" + params + ")"
}

// cParams renders a Signature's parameters alone, without asking about its
// results, which is what the array-result path needs: cSig refuses an array result
// before it gets to the parameters.
func (e *emitter) cParams(sig []int32) (string, bool) {
	for n := range it(sig) {
		if n.sym == ParameterList {
			return strings.Join(e.cParamList(n.ast), ", "), true
		}
	}
	return "", false
}

// arrayResultOf reports whether a signature's single result is a fixed array, and
// what its extents are. Two results one of which is an array is not this: it would
// need a struct holding an array, which this backend cannot assign.
func (e *emitter) arrayResultOf(sig []int32) (arrDim, bool) {
	var out []arrDim
	n := 0
	for k := range it(sig) {
		switch k.sym {
		case ResultList:
			for _, d := range e.f.paramDecls(k.ast) {
				c := len(d.Names)
				if c == 0 {
					c = 1
				}
				for range c {
					n++
					if a, ok := e.arrayDim(d.TypeAST.ast); ok {
						out = append(out, a)
					}
				}
			}
		case Type:
			n++
			if a, ok := e.arrayDim(k.ast); ok {
				out = append(out, a)
			}
		}
	}
	if n != 1 || len(out) != 1 {
		return arrDim{}, false
	}
	return out[0], true
}

// methodSignatureC builds a method's C signature with the receiver as the leading
// parameter, e.g. `int Point_Sum(Point p)` or `void Point_Scale(Point* p, int f)`.
func (e *emitter) methodSignatureC(cname, recvName, recvCType string, sig []int32) string {
	// A VALUE receiver is passed by value, so it meets the same ABI wall a parameter
	// does. It reached the backend instead, which said "Internal error, couldn't
	// find object variable with offset 4". A pointer receiver is unaffected.
	if !strings.HasSuffix(recvCType, "*") {
		e.refuseArrayStructABI(recvCType, "receiver "+recvName)
	}
	recvParam := recvCType + " " + userIdent(recvName)
	// An ARRAY value receiver is received as a POINTER and copied in the body,
	// exactly as an array PARAMETER is: a parameter of array type corrupts unrelated
	// code on this target (doc/array-param-corrupts.c), and `Row r` is one. It is
	// still the value Go passes -- emitRecvCopy makes the copy the caller cannot see
	// past.
	if a, isArr := e.namedArrays[recvCType]; isArr {
		recvParam = e.arrayParamCType(a) + " " + paramArgName(recvName)
	}
	if a, ok := e.arrayResultOf(sig); ok {
		// As for a function: the out parameter leads, after the receiver.
		e.funcArrayRet[cname] = a
		out := recvParam + ", " + arrayResultCType(a) + " " + arrayResultParam
		if params, _ := e.cParams(sig); params != "" {
			out += ", " + params
		}
		return "void " + cname + "(" + out + ")"
	}
	params, resTypes := e.cSig(sig)
	if params == "" {
		params = recvParam
	} else {
		params = recvParam + ", " + params
	}
	return e.cReturnType(cname, resTypes) + " " + cname + "(" + params + ")"
}

// cReturnType is a function's C return type: void for no results, the type itself
// for one, and the result struct for more than one (C has no multiple return).
func (e *emitter) cReturnType(name string, resTypes []string) string {
	switch len(resTypes) {
	case 0:
		return "void"
	case 1:
		return resTypes[0]
	default:
		return e.retStructName(name)
	}
}

// cSig renders a Signature's parameters as a C parameter list ("int a, int b")
// and returns its result C types (empty for none). Parameters are always named
// (the grammar requires it); results are a single unnamed type or, for one or
// more, a named parameter list.
func (e *emitter) cSig(sig []int32) (params string, resTypes []string) {
	var parts []string
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			// Parameters are the only ParameterList; results are ResultList/Type.
			parts = e.cParamList(n.ast)
		case ResultList:
			for _, d := range e.f.paramDecls(n.ast) {
				ct := e.resultCType(d.TypeAST.ast)
				k := len(d.Names)
				if k == 0 {
					k = 1 // an unnamed result is one value
				}
				for range k {
					resTypes = append(resTypes, ct)
				}
			}
		case Type:
			// A single unnamed result: Signature = "(" [...] ")" Type .
			resTypes = append(resTypes, e.resultCType(n.ast))
		case 0:
			// structural "(" / ")"
		default:
			e.fail("unsupported signature element %v", n.sym)
		}
	}
	return strings.Join(parts, ", "), resTypes
}

// resultInfo returns a function's result names and C types (one entry per result
// value). An unnamed result has an empty name; a named result contributes its
// name (a shared "(a, b int)" yields one entry per name).
func (e *emitter) resultInfo(sig []int32) (names, types []string) {
	// A single ARRAY result is not a returned value at all: it is written through an
	// out parameter, so the function returns nothing and has no result type to name.
	// Asked first, because resultCType refuses an array -- rightly, for every other
	// shape.
	if _, ok := e.arrayResultOf(sig); ok {
		return nil, nil
	}
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ResultList:
			for _, d := range e.f.paramDecls(n.ast) {
				ct := e.resultCType(d.TypeAST.ast)
				if len(d.Names) == 0 {
					names = append(names, "")
					types = append(types, ct)
					continue
				}
				for _, nm := range d.Names {
					names = append(names, nm.Src())
					types = append(types, ct)
				}
			}
		case Type:
			if seenRPar {
				names = append(names, "")
				types = append(types, e.resultCType(n.ast))
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
	return names, types
}

// cParamList renders one ParameterList's `IdentifierList Type` groups to C
// parameters, expanding a shared type ("a, b int" -> "int a, int b"). A fixed-
// array parameter is received by pointer (Go passes arrays by value, but C cannot,
// and flexcc cannot wrap them in a struct either): the C parameter is
// `<elem>* _ogo_<name>`, and the function copies it into a same-named local on
// entry (see emitParamCopies) to restore the value semantics.
func (e *emitter) cParamList(ast []int32) []string {
	var out []string
	e.forEachParamV(ast, func(name string, ta []int32, _, variadic bool) {
		if variadic {
			// "...T" is received as the []T it means; the caller builds the header.
			elem := e.cType(ta)
			e.needSlice(elem)
			out = append(out, sliceCName(elem)+" "+userIdent(name))
			return
		}
		if a, ok := e.arrayDim(ta); ok {
			out = append(out, e.arrayParamCType(a)+" "+paramArgName(name))
			return
		}
		ct := e.cType(ta)
		e.refuseArrayStructABI(ct, "parameter "+name)
		out = append(out, ct+" "+userIdent(name)) // a parameter name may be Unicode
	})
	return out
}

// cParamTypes renders a Signature's parameters as C types alone, with no names.
// That is what a function-type typedef wants: the names are not part of the type,
// so writing them would make `func(a int)` and `func(b int)` mint two typedefs for
// what is one type.
func (e *emitter) cParamTypes(sig []int32) ([]string, []arrDim) {
	var out []string
	var dims []arrDim
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue
		}
		e.forEachParamV(n.ast, func(name string, ta []int32, _, variadic bool) {
			if variadic {
				elem := e.cType(ta)
				e.needSlice(elem)
				out = append(out, sliceCName(elem))
				dims = append(dims, arrDim{})
				return
			}
			if a, ok := e.arrayDim(ta); ok {
				// The extents are answered separately because the C type cannot carry
				// them: an array parameter is a pointer to the element, so every
				// [N]int parameter is the same `int*` and a call has nothing to check
				// its argument against.
				out = append(out, e.arrayParamCType(a))
				dims = append(dims, a)
				return
			}
			ct := e.cType(ta)
			e.refuseArrayStructABI(ct, "parameter "+name)
			out = append(out, ct)
			dims = append(dims, arrDim{})
		})
	}
	return out, dims
}

// refuseArrayStructABI rejects passing or returning a struct that holds an array.
// A copy elsewhere is lowered to memcpy (see emitStructCopy), but a parameter or a
// result is the C calling convention itself, and flexcc gets that wrong in a way
// no lowering here can reach: it drops the argument slot ("Internal error,
// couldn't find object variable with offset 4") or fails to assign the result.
// Reported where the signature is written, so the message names the declaration
// rather than every call of it. Passing a pointer is the way to write this.
func (e *emitter) refuseArrayStructABI(ctype, what string) {
	if e.hasArrayField(ctype) {
		e.fail("%s: %s holds an array, which the target's C compiler cannot pass or return by value; use a pointer", what, ctype)
	}
}

// resultCType maps a result Type to its C type, refusing a fixed-array result. C
// cannot return an array by value, and a result -- unlike a parameter, which decays
// to a pointer (cParamList) -- has nowhere to decay to, so it is refused with an
// actionable message. cType would instead fail with an empty, nameless "unsupported
// type", the array's element being a nested node it finds no identifier for. arrayDim
// catches every rank, a multi-dimensional array included. A struct that merely holds
// an array is refused by refuseArrayStructABI for the same ABI reason.
func (e *emitter) resultCType(ta []int32) string {
	if _, ok := e.arrayDim(ta); ok {
		// A SINGLE array result is handed back through an out parameter, and
		// funcSignatureC takes that path before this one is reached. Anything else
		// -- an array beside another result -- would need a struct holding an array,
		// which this backend cannot assign ("Unable to multiply assign this
		// target"), and returning a pointer instead would name the callee's dead
		// frame. So it is still refused, and the message still names the way out.
		e.fail("cannot return an array beside another result; return a slice or a pointer to it")
		return ""
	}
	ct := e.cType(ta)
	e.refuseArrayStructABI(ct, "result")
	return ct
}

// arrayCountC renders an array's element count as a C factor chain, "*3" for a
// [3]int and "*2*3" for a [2][3]int, so a copy sizes a multi-dimensional array as
// one block.
func arrayCountC(a arrDim) string {
	s := ""
	for _, b := range a.bounds() {
		s += "*" + b
	}
	return s
}

// arrayResultCall reports whether an expression is exactly a call to a function
// with an ARRAY result, and names it. Such a call is a statement rather than a
// value: what it yields has no C value type, so it is written into storage the
// caller supplies.
func (e *emitter) arrayResultCall(ast []int32) (string, arrDim, bool) {
	recv, suffix, ok := e.directCall(ast)
	if !ok || len(suffix) == 0 || suffix[len(suffix)-1].sym != CallSuffix {
		return "", arrDim{}, false
	}
	return e.arrayResultCallOf(recv, suffix)
}

// arrayResultCallOf is arrayResultCall for a callee and suffix already in hand,
// which is what a caller that has stripped trailing steps off the factor has.
func (e *emitter) arrayResultCallOf(recv string, suffix []Node) (string, arrDim, bool) {
	cname := e.funcCallC(recv)
	if len(suffix) == 2 && suffix[0].sym == Selector {
		// A method, `b.triple()`: its C name is the receiver type's, and the
		// receiver leads the argument list ahead of the out parameter.
		//
		// methodRecvCType rather than varType-plus-isUserType, the fifth place that
		// pair was spelled by hand: an ARRAY variable has no C type, so a method with
		// an array RESULT on an array RECEIVER -- `g.doubled()` for a `type Row
		// [2]int` -- was not recognised as a call at all.
		rct, isVar := e.methodRecvCType(recv)
		if !isVar {
			return "", arrDim{}, false
		}
		cname = methodCName(methodBaseType(rct), e.soleIdent(suffix[0].ast))
	} else if len(suffix) != 1 {
		return "", arrDim{}, false
	}
	a, isArr := e.funcArrayRet[cname]
	return cname, a, isArr
}

// emitArrayResultCall emits the call itself, with dst as the out parameter it
// writes through.
func (e *emitter) emitArrayResultCall(dst, cname string, ast []int32) {
	recv, suffix, _ := e.directCall(ast)
	e.emitArrayResultCallOf(dst, cname, recv, suffix)
}

// emitArrayResultCallOf is emitArrayResultCall for a callee and suffix in hand.
func (e *emitter) emitArrayResultCallOf(dst, cname, recv string, suffix []Node) {
	a := e.funcArrayRet[cname]
	e.ind()
	e.emit(cname + "(")
	// A method's receiver leads, ahead of the out parameter.
	if len(suffix) == 2 && suffix[0].sym == Selector {
		rct, _ := e.varType(recv)
		r, ok := e.chainReceiver(e.varRef(recv), rct, true, e.methodPtr[cname])
		if !ok {
			e.fail("cannot take the address of %s for a pointer-receiver method", recv)
			return
		}
		e.emit(r + ", ")
	}
	// The out parameter is a pointer to the ELEMENT, so a multi-dimensional
	// destination is cast: `int (*)[3]` and `int*` name the same storage and C will
	// not convert between them silently.
	e.emit("(" + arrayResultCType(a) + ")" + dst)
	if args := e.argsCText(cname, suffix[len(suffix)-1].ast); args != "" {
		e.emit(", " + args)
	}
	e.emit(");\n")
}

// arrayReturnOperand names an expression that IS an array -- today a variable --
// and its extents. An array has no C value type, so what this returns is the
// storage's name, which is what a copy needs.
func (e *emitter) arrayReturnOperand(ast []int32) (string, arrDim, bool) {
	// Whatever the copy can read from, the return can: an array variable, a
	// dereferenced pointer to one, an array reached through a chain of fields and
	// indexes, and a literal, which is bound to a temporary of this frame. Nothing
	// here outlives the copy -- the memcpy IS the return -- so a source local to
	// this frame, the literal's temporary included, costs nothing.
	a, ok := e.arrayShapeOf(ast)
	if !ok {
		return "", arrDim{}, false
	}
	text, ok := e.arraySourceC(ast)
	if !ok {
		return "", arrDim{}, false
	}
	return text, a, true
}

// arrayResultCType is the C type of the out parameter an array result is passed
// back through: a pointer to the array's element. A multi-dimensional result is a
// pointer to its innermost element too -- the caller's storage is contiguous either
// way, and the copy is by size.
func arrayResultCType(a arrDim) string { return a.elem + "*" }

// arrayResultParam is the name of that out parameter, in the callee.
const arrayResultParam = "_ogo_ret"

// forEachParam walks a ParameterList's `IdentifierList Type` groups, calling fn
// with each parameter's name and C type (a shared type "a, b int" yields two
// calls). It underlies both the C parameter rendering (cParamList) and the local
// type environment (bindParams).
func (e *emitter) forEachParam(ast []int32, fn func(name string, typeAST []int32, synthetic bool)) {
	e.forEachParamV(ast, func(name string, typeAST []int32, synthetic, _ bool) {
		fn(name, typeAST, synthetic)
	})
}

// forEachParamV is forEachParam with the fourth thing a caller may need: whether
// the entry was written "...T". Its type AST is T, since that is what the source
// wrote; what the parameter IS, in the signature and in the body alike, is a []T.
func (e *emitter) forEachParamV(ast []int32, fn func(name string, typeAST []int32, synthetic, variadic bool)) {
	i := 0
	for _, d := range e.f.paramDecls(ast) {
		if len(d.Names) == 0 {
			fn(unnamedParamName(i), d.TypeAST.ast, true, d.Variadic)
			i++
			continue
		}
		for _, nm := range d.Names {
			name := nm.Src()
			if name == "_" {
				fn(unnamedParamName(i), d.TypeAST.ast, true, d.Variadic)
			} else {
				fn(name, d.TypeAST.ast, false, d.Variadic)
			}
			i++
		}
	}
}

// variadicElem names the element type of a signature's variadic parameter, and
// which position it is at, or -1. The pack a call builds is a slice over that
// element, so both are needed at the call site.
func (e *emitter) variadicElem(sig []int32) (elem string, at int) {
	at = -1
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue
		}
		i := 0
		e.forEachParamV(n.ast, func(_ string, ta []int32, _, variadic bool) {
			if variadic {
				elem, at = e.cType(ta), i
			}
			i++
		})
		break
	}
	return elem, at
}

// unnamedParamName is the synthetic C name of the i-th parameter when it is
// unnamed or blank ("_"). flexcc miscompiles a definition that leaves a parameter
// unnamed -- it drops that parameter's argument slot and shifts every following
// argument -- so each such parameter is given a name (and a "(void)" reference in
// the body, since the source never uses it, to stay -Wunused-parameter clean).
func unnamedParamName(i int) string { return "_ogo_unused" + strconv.Itoa(i) }

// arrayParamCType is the C type an array parameter of shape a is RECEIVED as: a
// pointer to its element, the callee copying from it into a local of its own. A
// parameter of array type miscompiles on this target (doc/array-param-corrupts.c),
// which is what the pointer is for.
//
// A rank above one has no element type C can write inline -- `int (*)[2]` would put
// the parameter's name in the middle of the declarator -- so the ROW's generated
// typedef names it, the same one a `[][2]int` element is given. Without it,
// `func take(x [3][2]int)` was refused as an unsupported type though the
// one-dimensional form had always worked.
func (e *emitter) arrayParamCType(a arrDim) string { return e.sliceElemOfArray(a) + "*" }

// paramArgName is the C name of a value-array parameter as it is received (a
// pointer), distinct from the local copy the body sees under the source name.
func paramArgName(name string) string { return "_ogo_" + userIdent(name) }

// bindParams records the current function's parameters in the local type
// environment, so a `x := p` short declaration can be typed from a parameter p. A
// fixed-array parameter is recorded as an array (its body sees a same-named local
// copy). It reads only the parameter list (before the signature's closing ")"),
// not the results.
func (e *emitter) bindParams(sig []int32) {
	e.curParams = map[string]bool{}
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParamV(n.ast, func(name string, ta []int32, synthetic, variadic bool) {
					if synthetic {
						return // an unnamed parameter binds nothing; the body cannot name it
					}
					e.curParams[name] = true
					if variadic {
						elem := e.cType(ta)
						e.needSlice(elem)
						e.sliceVars[name] = elem
						e.locals[name] = sliceCName(elem)
						return
					}
					if a, ok := e.arrayDim(ta); ok {
						e.arrays[name] = a
						return
					}
					if elem, ok := e.sliceType(ta); ok {
						e.sliceVars[name] = elem
					}
					e.locals[name] = e.cType(ta)
				})
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
}

// emitParamCopies emits, at a function's entry, a local copy of each fixed-array
// parameter — `<elem> <name>[<N>]; memcpy(<name>, _ogo_<name>, sizeof(<name>));` —
// so the body mutates a copy and the caller's array is untouched (Go's array
// value semantics). The parameter itself arrives by pointer (see cParamList).
func (e *emitter) emitParamCopies(sig []int32) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParam(n.ast, func(name string, ta []int32, synthetic bool) {
					if synthetic {
						return // an unnamed array parameter has no in-body copy
					}
					if a, ok := e.arrayDim(ta); ok {
						e.includes["string.h"] = true
						e.ind()
						e.emit(a.elem + " " + name + a.declSuffix() + ";\n")
						e.ind()
						e.emit("memcpy(" + name + ", " + paramArgName(name) + ", sizeof(" + name + "));\n")
					}
				})
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
}

// emitParamVoids emits a "(void)name;" for every unused (unnamed, blank, or named
// and never mentioned)
// parameter, so the names forced on them for flexcc (see unnamedParamName) do not
// trip -Wunused-parameter. It reads only the parameter list, not the results.
func (e *emitter) emitParamVoids(sig, body []int32) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParam(n.ast, func(name string, ta []int32, synthetic bool) {
					// A parameter the body never uses, whether the source left it
					// unnamed or named it and ignored it. Go allows both -- an unused
					// parameter is not an unused variable -- and C warns about both.
					if !synthetic && e.bodyMentions(body, name) {
						return
					}
					cname := name
					if _, ok := e.arrayDim(ta); ok {
						cname = paramArgName(name) // an array parameter is received by pointer
					}
					e.ind()
					e.emit("(void)" + cname + ";\n")
				})
			}
		case 0:
			if e.f.ch(n.tok) == RPAREN {
				seenRPar = true
			}
		}
	}
}

// emitBlockStmts emits the statements of a Block (skipping its braces).
// enterScope snapshots the per-name state a block may shadow, returning the restore
// that ends the scope. The emitter has no scopes of its own -- it records a
// variable's type, extents and provenance in maps keyed by SOURCE name -- so a
// declaration inside a block used to outlive it: after `{ s := 5 }` shadowing a
// package-level string, s was still recorded as an int, and the next read of the
// real s printed the first word of its header as a number.
//
// The maps are small (one entry per name in scope), so copying them per block costs
// nothing measurable and needs no analysis of what the block declares.
func (e *emitter) enterScope() func() {
	// The names visible on the way IN, so a later question can tell which block
	// declared a variable: one declared here is absent from this snapshot and from
	// every outer one (see blockDepthOf).
	e.scopeNames = append(e.scopeNames, e.visibleLocals())
	locals, arrays, sliceVars := maps.Clone(e.locals), maps.Clone(e.arrays), maps.Clone(e.sliceVars)
	frameBacked, frameHolder := maps.Clone(e.frameBacked), maps.Clone(e.frameHolder)
	constInt, constStr := maps.Clone(e.constInt), maps.Clone(e.constStr)
	constUntyped := maps.Clone(e.constUntyped)
	funcValueOf := maps.Clone(e.funcValueOf)
	return func() {
		// The frame marks are MERGED back rather than replaced. They are monotone --
		// a variable given a reference to this frame keeps it, which is what makes
		// the analysis sound without per-field tracking -- and restoring the snapshot
		// wholesale un-marked a variable a nested block had marked: `if c { v = s }`
		// followed by `g = v` was accepted, a dangling slice stored silently.
		//
		// A mark survives only for a name declared OUTSIDE the block, which is what
		// the snapshot's own environments say. Dropping the rest with the names is
		// not conservatism lost: nothing can refer to them afterwards, and keeping
		// them would make a later sibling block's same-named variable inherit a mark
		// it never earned.
		outer := func(n string) bool {
			if _, ok := locals[n]; ok {
				return true
			}
			_, ok := arrays[n]
			return ok
		}
		for n, marked := range e.frameBacked {
			if marked && outer(n) {
				frameBacked[n] = true
			}
		}
		for n, origin := range e.frameHolder {
			if origin != "" && outer(n) {
				frameHolder[n] = origin
			}
		}
		e.scopeNames = e.scopeNames[:len(e.scopeNames)-1]
		e.locals, e.arrays, e.sliceVars = locals, arrays, sliceVars
		e.frameBacked, e.frameHolder = frameBacked, frameHolder
		e.constInt, e.constStr = constInt, constStr
		e.constUntyped = constUntyped
		e.funcValueOf = funcValueOf
	}
}

func (e *emitter) emitBlockStmts(ast []int32) {
	defer e.enterScope()()
	for n := range it(ast) {
		switch n.sym {
		case 0:
			// LBRACE / RBRACE / SEMICOLON.
		case Statement:
			e.emitStatement(n.ast)
		default:
			e.fail("unsupported block element %v", n.sym)
		}
	}
}

// stmtLabelParts reports whether the statement is a labeled one, "L: Stmt" -- an
// AssignHead identifier followed by a Postfix that is a ":" continuation -- and
// returns the label name and the labeled statement's AST.
func (e *emitter) stmtLabelParts(nodes []Node) (label string, inner []int32, ok bool) {
	if len(nodes) < 2 || nodes[0].sym != AssignHead || nodes[1].sym != Postfix {
		return "", nil, false
	}
	label, isID := e.exprIdent(nodes[0].ast)
	if !isID {
		return "", nil, false
	}
	for n := range it(nodes[1].ast) {
		if n.sym != PostfixOp {
			continue
		}
		sawColon := false
		for c := range it(n.ast) {
			switch {
			case c.sym == 0 && e.f.ch(c.tok) == COLON:
				sawColon = true
			case c.sym == Statement && sawColon:
				return label, c.ast, true
			}
		}
	}
	return "", nil, false
}

// stmtLabelOperand returns the label a "break"/"continue" names, from the optional
// identifier token after the keyword.
func (e *emitter) stmtLabelOperand(nodes []Node) (string, bool) {
	if len(nodes) >= 2 && nodes[1].sym == 0 && e.f.ch(nodes[1].tok) == IDENT {
		return e.src(nodes[1].tok), true
	}
	return "", false
}

// emitLabeledStatement emits "L: Stmt". A labeled "for" gets a break-target label
// after the loop and a continue-target label at the end of its body (a fall-through
// there is exactly C's continue); "break L"/"continue L" become gotos to those. A
// labeled "switch" binds L to the end label the switch already mints. A label on
// anything else has no break/continue target (there is no goto), so its statement is
// emitted plainly.
func (e *emitter) emitLabeledStatement(label string, inner []int32) {
	switch e.stmtKind(inner) {
	case FOR:
		e.labelSeq++
		brk := fmt.Sprintf("ogo_lbreak_%d", e.labelSeq)
		cont := fmt.Sprintf("ogo_lcont_%d", e.labelSeq)
		e.labelBreak[label] = brk
		e.labelContinue[label] = cont
		e.pendingContLabel = cont
		e.emitStatement(inner)
		if e.labelUsed[brk] {
			e.ind()
			e.emit(brk + ":;\n")
		}
		delete(e.labelBreak, label)
		delete(e.labelContinue, label)
	case SwitchStmt:
		e.pendingSwitchLabel = label
		e.emitStatement(inner) // emitSwitch binds and unbinds labelBreak[label]
	default:
		e.emitStatement(inner)
	}
}

// stmtKind classifies a statement node for labeling: the "for" keyword symbol, the
// SwitchStmt node symbol, or 0 for neither.
func (e *emitter) stmtKind(inner []int32) Symbol {
	for n := range it(inner) {
		switch {
		case n.sym == 0 && e.f.ch(n.tok) == FOR:
			return FOR
		case n.sym == SwitchStmt:
			return SwitchStmt
		}
	}
	return 0
}

func (e *emitter) emitStatement(ast []int32) {
	nodes := slices.Collect(it(ast))
	if len(nodes) == 0 {
		return // EmptyStatement
	}
	// The statement is emitted into a buffer so anything inside it can ask for a
	// line to be placed *before* it -- a temporary an expression needs hoisted out
	// of itself, which C has nowhere to put mid-expression. With no such request
	// this is a copy and nothing else, so every statement that needs no prologue
	// emits exactly what it did before.
	savedW, savedPro := e.w, e.prologue
	var buf bytes.Buffer
	e.w, e.prologue = &buf, nil
	e.emitStatementInner(nodes, ast)
	pro := e.prologue
	e.w, e.prologue = savedW, savedPro
	for _, line := range pro {
		e.ind()
		e.emit(line)
	}
	e.w.Write(buf.Bytes())
}

// hoist requests a line before the statement being emitted and returns the name of
// a fresh temporary it declares. It is how an expression gets a temporary when C
// gives it nowhere to put one.
func (e *emitter) hoist(ctype string, emitValue func()) string {
	tmp := e.newTmp()
	savedW := e.w
	var buf bytes.Buffer
	e.w = &buf
	emitValue()
	e.w = savedW
	e.prologue = append(e.prologue, ctype+" "+tmp+" = "+buf.String()+";\n")
	return tmp
}

// exprHasEffect reports whether an expression can change state when evaluated: it
// calls something, or receives from a channel. Nothing else in an expression can.
//
// A call to len, cap, min or max, or a type conversion, is spelled like a call but
// cannot change anything, so it does not count -- its arguments still do, and are
// reached by walking into it. Treating every call shape as an effect was the first
// version and it was wrong in a visible way: `println(r[0], len(r))` stopped being
// one printf.
func (e *emitter) exprHasEffect(ast []int32) bool {
	for n := range it(ast) {
		switch {
		case n.sym == 0:
			if e.f.ch(n.tok) == ARROW {
				return true // a channel receive
			}
		case n.sym == Factor:
			if recv, _, isCall := e.factorCall(slices.Collect(it(n.ast))); isCall && !e.pureCall(recv) {
				return true
			}
			// A method called on a PARENTHESISED expression is a call like any
			// other, and factorCall does not see it -- that one wants a bare
			// identifier for a base. Without this the arguments of
			// `println((&v).Sum(), (&v).Bump())` were left to C's unspecified
			// order, and came out right to left.
			if _, _, isParenCall := e.parenMethodSteps(slices.Collect(it(n.ast))); isParenCall {
				return true
			}
			if e.exprHasEffect(n.ast) {
				return true // its arguments may still have effects
			}
		default:
			if e.exprHasEffect(n.ast) {
				return true
			}
		}
	}
	return false
}

// pureCall reports whether a call to recv cannot change state: the builtins that
// only read their arguments, and a type conversion, which is a cast.
func (e *emitter) pureCall(recv string) bool {
	if _, isUser := e.userFunc(recv); isUser {
		return false // a user function of that name shadows the builtin
	}
	switch recv {
	case "len", "cap", "min", "max":
		return true
	}
	_, isConv := e.convType(recv)
	return isConv
}

func (e *emitter) emitStatementInner(nodes []Node, ast []int32) {
	if label, inner, ok := e.stmtLabelParts(nodes); ok {
		e.emitLabeledStatement(label, inner)
		return
	}
	switch first := nodes[0]; {
	case first.sym == VarDecl:
		e.emitVarDecl(first.ast)
	case first.sym == ConstDecl:
		e.emitConstDecl(first.ast, false)
	case first.sym == 0 && e.f.ch(first.tok) == FOR:
		e.emitFor(nodes)
	case first.sym == IfStmt:
		e.emitIf(first.ast)
	case first.sym == SwitchStmt:
		e.emitSwitch(first.ast)
	case first.sym == SelectStmt:
		e.emitSelect(first.ast)
	case first.sym == Block:
		// A bare block statement "{ ... }": a nested C compound statement, which
		// gives its declarations their own scope. break and continue pass through a
		// block unchanged (it is not a loop or switch), so switchBreak is left as
		// is; a defer inside needs the depth flag, as in any nested body.
		e.ind()
		e.emit("{\n")
		e.indent++
		e.deferBlockDepth++
		e.emitBlockStmts(first.ast)
		e.deferBlockDepth--
		e.indent--
		e.ind()
		e.emit("}\n")
	case first.sym == 0 && e.f.ch(first.tok) == RETURN:
		e.emitReturn(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == DEFER:
		e.emitDefer(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == GO:
		e.emitGo(nodes)
	case first.sym == 0 && e.f.ch(first.tok) == BREAK:
		// A labeled break jumps to the named loop's or switch's break-target label.
		// An unlabeled break in a switch -- lowered to a chain of conditionals, not
		// a C switch -- jumps past the chain to the switch's end label (a C break
		// would leave an enclosing loop); in a loop, where switchBreak is "" (cleared
		// by emitLoopBody), it stays a plain C break. Each end label is emitted only
		// once a break has referenced it.
		e.ind()
		if lbl, ok := e.stmtLabelOperand(nodes); ok {
			target := e.labelBreak[lbl]
			e.emit("goto " + target + ";\n")
			e.labelUsed[target] = true
		} else if e.switchBreak != "" {
			e.emit("goto " + e.switchBreak + ";\n")
			e.switchBreakUsed[e.switchBreak] = true
		} else {
			e.emit("break;\n")
		}
	case first.sym == 0 && e.f.ch(first.tok) == CONTINUE:
		// A labeled continue jumps to the named loop's continue-target label at the
		// end of its body (a fall-through there re-runs the loop's post and test). An
		// unlabeled continue is a plain C continue, which names the enclosing loop
		// either way, exactly as Go's does.
		e.ind()
		switch lbl, ok := e.stmtLabelOperand(nodes); {
		case ok:
			target := e.labelContinue[lbl]
			e.emit("goto " + target + ";\n")
			e.labelUsed[target] = true
		case e.postContLabel != "":
			// This loop's post statements live at the end of its body, not in C's
			// third clause, so a plain `continue` would skip them.
			e.emit("goto " + e.postContLabel + ";\n")
			e.labelUsed[e.postContLabel] = true
		default:
			e.emit("continue;\n")
		}
	case first.sym == 0 && e.f.ch(first.tok) == ARROW:
		e.emitRecvStmt(nodes)
	case first.sym == AssignHead:
		e.emitAssignHeadStmt(nodes)
	case first.sym == 0:
		e.fail("%v statement is not supported yet", e.f.ch(first.tok))
	default:
		e.fail("statement %v is not supported yet", first.sym)
	}
}

// emitVarDecl handles `var name Type` (zero-initialized; P2 stack locals are not
// auto-zeroed, so the initializer is required), `var name Type = expr`, and fixed
// arrays `var a [N]T`.
func (e *emitter) emitVarDecl(ast []int32) {
	for n := range it(ast) {
		if n.sym != VarSpec {
			continue
		}
		var names []string
		var typeAST []int32
		var initExprs [][]int32
		for s := range it(n.ast) {
			switch s.sym {
			case IdentifierList:
				for id := range it(s.ast) {
					if id.sym == 0 && e.f.ch(id.tok) == IDENT {
						names = append(names, e.src(id.tok))
					}
				}
			case Type:
				typeAST = s.ast
			case ExpressionList:
				for _, x := range expressionListItems(s) {
					initExprs = append(initExprs, x.ast)
				}
			case 0:
				// The "=" separator between the type and the initializer.
			default:
				e.fail("unsupported var-spec element %v", s.sym)
			}
		}
		// A value list gives every name its own initializer, so the spec is that
		// many independent single-name declarations. One value (or none) keeps the
		// single-spec paths below, which is where destructuring a call lives.
		if len(names) > 1 && len(initExprs) == len(names) {
			e.emitVarList(names, typeAST, initExprs)
			continue
		}
		var initExpr []int32
		if len(initExprs) != 0 {
			initExpr = initExprs[0]
		}
		if typeAST == nil {
			// Type-inferred `var x = expr` (the var form of `x := expr`) or
			// `var a, b = f()` (destructuring). The grammar guarantees an initializer
			// when the type is omitted.
			if initExpr == nil {
				e.fail("var declaration needs a type or an initializer")
				return
			}
			if len(names) != 1 {
				e.emitDestructure(plainTargets(names), allTrue(len(names)), initExpr)
				continue
			}
			if names[0] == "_" {
				e.emitDiscard(initExpr)
				continue
			}
			e.emitInferredLocal(names[0], initExpr)
			continue
		}
		// A fixed array `var a [N]T` -> `T a[N] = {0};`. Its name maps to the
		// element type for `x := a[i]` typing. An initializer copies another array of
		// the same type by value (`var b [N]T = a`); C cannot assign arrays, so the
		// array is declared uninitialized and filled with memcpy.
		if a, ok := e.arrayDim(typeAST); ok {
			elem := a.elem
			if len(names) != 1 && initExpr != nil {
				e.fail("multi-name var with initializer is not supported yet")
				return
			}
			for _, nm := range names {
				if nm == "_" {
					if initExpr != nil {
						e.emitDiscard(initExpr)
					}
					continue
				}
				e.arrays[nm] = a
				if initExpr == nil {
					e.ind()
					// A zero-length array has nothing to zero, and "{0}" names an
					// element it does not have -- valid to the target's C compiler,
					// which says nothing, and a warning from the host's. `[0]T` is a
					// legal Go type, so it is declared without an initializer instead.
					if a.bound == "0" {
						e.emit(elem + " " + nm + a.declSuffix() + ";\n")
						continue
					}
					e.emit(elem + " " + nm + a.declSuffix() + " = {0};\n")
					continue
				}
				// A literal initializer is aggregate initialization, not a copy.
				if litType, lit, ok := e.soleArrayLit(initExpr); ok {
					if !e.sameArrayType(a, litType) {
						return
					}
					e.emitArrayLitVar(nm, litType, lit, false)
					continue
				}
				// `var c [3]int = mk()`: the declaration is the storage the callee
				// fills, so there is nothing to copy afterwards.
				if cname, ra, isArrCall := e.arrayResultCall(initExpr); isArrCall {
					if ra.elem != a.elem || ra.declSuffix() != a.declSuffix() {
						e.fail("cannot use %s as %s in variable declaration", e.goArrayTypeName(ra), e.goArrayTypeName(a))
						return
					}
					e.ind()
					e.emit(elem + " " + nm + a.declSuffix() + ";\n")
					e.emitArrayResultCall(nm, cname, initExpr)
					continue
				}
				if !e.checkArrayShape(a, initExpr, "variable declaration") {
					return
				}
				e.includes["string.h"] = true
				// A self-referential shadowing copy (`var a [N]T = a` with an outer a)
				// means the outer array; capture it before the new one shadows it, or
				// the memcpy reads the uninitialized destination (see emitVarDeclInit).
				if e.initRefsName(initExpr, nm) {
					tmp := e.newTmp()
					e.ind()
					e.emit(elem + " " + tmp + a.declSuffix() + ";\n")
					e.ind()
					e.emit("memcpy(" + tmp + ", ")
					e.emitExpr(initExpr)
					e.emit(", sizeof(" + tmp + "));\n")
					e.ind()
					e.emit(elem + " " + nm + a.declSuffix() + ";\n")
					e.ind()
					e.emit("memcpy(" + nm + ", " + tmp + ", sizeof(" + nm + "));\n")
					continue
				}
				e.ind()
				e.emit(elem + " " + nm + a.declSuffix() + ";\n")
				e.ind()
				e.emit("memcpy(" + nm + ", ")
				e.emitExpr(initExpr)
				e.emit(", sizeof(" + nm + "));\n")
			}
			continue
		}
		// A slice `var xs []T` -> `ogo_slice_T xs = {0};` (a { pointer, length }
		// header, zero value {NULL, 0}); its name maps to the element type for `xs[i]`
		// and len(xs). An initializer is a plain slice-header value copy.
		if elem, ok := e.sliceType(typeAST); ok {
			if len(names) != 1 && initExpr != nil {
				e.fail("multi-name var with initializer is not supported yet")
				return
			}
			cname := sliceCName(elem)
			e.needSlice(elem)
			// A make([]T, ...) or a literal initializer synthesises a backing array
			// + header, rather than copying an existing header.
			if initExpr != nil && len(names) == 1 && names[0] != "_" {
				if litType, lit, ok := e.soleArrayLit(initExpr); ok {
					if me, isSlice := e.sliceType(litType); !isSlice || me != elem {
						e.fail("a %s literal cannot initialize a variable declared []%s", e.litTypeName(litType), elem)
						return
					}
					e.emitArrayLitVar(names[0], litType, lit, false)
					continue
				}
				if me, lenAST, capAST, ok := e.makeSliceInit(initExpr); ok {
					if me != elem {
						e.fail("make element type %q does not match the declared slice element type %q", me, elem)
						return
					}
					e.sliceVars[names[0]] = elem
					e.locals[names[0]] = cname
					e.emitMakeSliceVar(names[0], cname, elem, lenAST, capAST, false)
					continue
				}
			}
			for _, nm := range names {
				if nm == "_" {
					if initExpr != nil {
						e.emitDiscard(initExpr)
					}
					continue
				}
				e.sliceVars[nm] = elem
				e.locals[nm] = cname
				// A self-referential shadowing copy (`var xs []T = xs` with an outer
				// xs) means the outer header; capture it before the new one shadows it
				// (see emitVarDeclInit).
				if initExpr != nil && e.initRefsName(initExpr, nm) {
					tmp := e.newTmp()
					e.ind()
					e.emit(cname + " " + tmp + " = ")
					e.emitExpr(initExpr)
					e.emit(";\n")
					e.ind()
					e.emit(cname + " " + nm + " = " + tmp + ";\n")
					continue
				}
				e.ind()
				e.emit(cname + " " + nm + " = ")
				// An explicit `= nil`, like no initializer at all, is the zero header
				// {0} -- nil's integer form 0 is not a slice value.
				if initExpr != nil && !e.isNilExpr(initExpr) {
					e.emitExpr(initExpr)
				} else {
					e.emit("{0}")
				}
				e.emit(";\n")
			}
			continue
		}
		ctype := e.cType(typeAST)
		if ctype == "" {
			return
		}
		if len(names) != 1 && initExpr != nil {
			// A multi-name initializer destructures a multi-result call, declaring
			// each name -- the var form of `a, b := f()`.
			e.emitDestructure(plainTargets(names), allTrue(len(names)), initExpr)
			continue
		}
		for _, nm := range names {
			if nm == "_" {
				// A blank var declares nothing. With an initializer, its side
				// effects still run and the value is discarded; without one, it
				// emits nothing at all.
				if initExpr != nil {
					e.emitDiscard(initExpr)
				}
				continue
			}
			e.locals[nm] = ctype
			// `var d List = make(List, n, c)` over `type List []int`. The branch
			// above takes the "[]T" spelling; a DEFINED slice type arrives here
			// instead, because its declared type is a name. It keeps that name as
			// its C type -- resolving it to the header's own would cost the variable
			// its methods, which is what litSliceType's comment warns about -- while
			// the backing array and the { ptr, len, cap } it points at are built
			// exactly as for the unnamed spelling.
			if elem, lenAST, capAST, ok := e.makeSliceInit(initExpr); ok && e.isSliceCType(e.underlyingCType(ctype)) {
				e.needSlice(elem)
				e.sliceVars[nm] = elem
				e.emitMakeSliceVar(nm, ctype, elem, lenAST, capAST, false)
				continue
			}
			// A DEFINED slice type declared from an existing header views the same
			// storage, so it inherits where that storage lives -- the entry the short
			// form records for itself. Without it `var s L = a[:]` over a local a was
			// a frame-backed slice no sink could see, and storing s in a package
			// variable was accepted where the "[]int" spelling of the same two lines
			// was refused.
			if u := e.underlyingCType(ctype); initExpr != nil && e.isSliceCType(u) {
				e.sliceVars[nm] = sliceElemFromCName(u)
				if e.initViewsFrame(initExpr) {
					e.frameBacked[nm] = true
				}
			}
			if initExpr != nil {
				// The provenance the SHORT form records for itself. Without these
				// `var b B = B{a[:]}` marked nothing where `b := B{a[:]}` marked b,
				// so storing that struct, or reading the field back out of it, was
				// accepted -- the same declaration one spelling apart.
				e.noteDeclFrameHolder(ctype, nm, initExpr)
				e.bindLitFuncFields(nm, initExpr)
				e.emitVarDeclInit(ctype, nm, initExpr)
			} else {
				e.ind()
				e.emit(ctype + " " + nm + " = " + e.zeroInitC(ctype) + ";\n")
			}
			// A channel is storage, not a handle: the checker rejects make() for one
			// ("dynamic allocation not supported"), so the declaration is what
			// creates it. Acquiring the hardware lock here is what makes the cell
			// usable, and ties the lock's lifetime to the variable's.
			//
			// Only a declaration with NO initializer creates one. `var c chan int = ch`
			// names the channel ch already is -- which is Go's reading too, a channel
			// value being copied so that the two then refer to one channel -- and the
			// cell was minted for it anyway, overwriting the alias one line after it
			// was written with a private cell nobody ever sends to. The first receive
			// then blocked for ever. `c := ch` and `c = ch` always aliased, so this
			// was the one spelling of three that did not.
			if initExpr == nil && e.isChanCType(ctype) {
				// The declaration owns the cell; the variable is a reference to it.
				e.ind()
				e.emit(nm + " = &" + e.localChanCell(e.chanElemOfCType(ctype)) + ";\n")
			}
			// A local struct owns a cell per channel field, on the same rule as a
			// local channel: the declaration owns it. Without this the field would be
			// a null pointer that builds and then faults at the first send, which is
			// the worst way for a feature to be missing.
			e.emitLocalChanFieldCells(nm, ctype, e.declLitNode(initExpr))
		}
	}
}

// factorCompositeLit matches a Factor of the shape "T{...}": an identifier naming
// the type, followed by the literal. Nothing else may follow, so a suffixed factor
// (a call, selector or index) is not one.
func (e *emitter) factorCompositeLit(kids []Node) (name string, lit Node, ok bool) {
	if len(kids) < 2 || len(kids) > 3 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT ||
		kids[len(kids)-1].sym != CompositeLit {
		return "", Node{}, false
	}
	// The name returned is the mangled C one, so that the value's inferred type and
	// its emission -- the (T){...} cast and the structs lookup -- use the same
	// package-namespaced typedef.
	if len(kids) == 2 {
		return mangle(e.curPkgPrefix, e.src(kids[0].tok)), kids[1], true // a type of this package
	}
	// A qualified type, "pkg.T{...}": the leading identifier is the import qualifier,
	// so the typedef carries the prefix of the package it names rather than this
	// one's. Everything else in front of a literal -- an index, a call, a longer
	// selector run -- names no type, and the checker has said so already.
	if kids[1].sym != FactorSuffix {
		return "", Node{}, false
	}
	prefix, isImport := e.importQualifiers[e.src(kids[0].tok)]
	fields, okFields := e.selectorFields(slices.Collect(it(kids[1].ast)))
	if !isImport || !okFields || len(fields) != 1 {
		return "", Node{}, false
	}
	return mangle(prefix, fields[0]), kids[2], true
}

// soleCompositeLit reports whether an expression is nothing but a composite
// literal -- no operator, no unary prefix, no call or index around it -- and
// returns the type name and the literal. This is the shape that may be spelled as
// a brace initializer rather than a compound literal; "f(P{1})" may not, because
// the literal there is an argument, not the initializer.
func (e *emitter) soleCompositeLit(ast []int32) (name string, lit Node, ok bool) {
	kids, ok := e.soleFactor(ast)
	if !ok {
		return "", Node{}, false
	}
	return e.factorCompositeLit(kids)
}

// soleFactor returns the children of the Factor an expression consists of, if that
// is all it is: no operator, no unary prefix, just the one operand.
func (e *emitter) soleFactor(ast []int32) (kids []Node, ok bool) {
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return nil, false
	}
	return slices.Collect(it(fac.ast)), true
}

// soleFactorNode is soleFactor returning the Factor node itself, for a caller that
// needs its AST slice rather than its children.
func (e *emitter) soleFactorNode(ast []int32) (Node, bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && nodes[0].sym != 0 {
		if n := nodes[0]; n.sym == Factor {
			return n, true
		}
		nodes = slices.Collect(it(nodes[0].ast))
	}
	return Node{}, false
}

// emitCompositeLit emits "T{a, b}" as the C compound literal "(T){a, b}", or, with
// brace set, as the plain initializer "{a, b}". "T{}" zeroes every field.
//
// Braces are what C spells in a declarator ("P p = {1, 2}") and what a file-scope
// initializer requires, a compound literal not being a constant expression. They
// are also the only form flexcc can lower for a struct that has an array field:
// given a compound literal of one it fails with "Unable to multiply assign this
// target", naming C the user never wrote. So the brace form propagates into an
// element that is itself a literal, which is C's own spelling for a nested
// aggregate initializer anyway.
func (e *emitter) emitCompositeLit(name string, lit Node, brace bool) {
	// A literal of an ARRAY type -- a defined one, `Row{1, 2}`, or the typedef minted
	// for an unnamed one -- is not a struct's braces however alike the two read: its
	// elements are indexed rather than named, and its rows nest. Sent through the
	// struct walk, two things went wrong here and nowhere else, because the
	// DECLARATION form of the same literal goes to emitArrayLitVar and was always
	// right: an INDEXED literal came out as {0}, the struct walk having found no field
	// of that name, and a nested row was refused as "a type-elided composite literal
	// element is only supported for a struct element type yet".
	//
	// So `ch <- Row{1: 5}` sent zeros and `append(xs, Row{2: 7})` appended them, both
	// silently, while `r := Row{1: 5}` was right -- the positions that hoist nothing
	// to point at are exactly the ones that come here. It is also what puts an
	// element that is an array VALUE in front of the fixup guard rather than in front
	// of the struct walk, which had no element type to recognise one by.
	if a, isArr := e.namedArrays[name]; isArr {
		values, length, ok := e.litPositions(lit)
		if !ok {
			return
		}
		if n, err := strconv.Atoi(a.bound); err == nil && length > n {
			e.fail("too many values in %s literal: %s but the length is %s",
				e.goArrayTypeName(a), countUnits(length, "value"), a.bound)
			return
		}
		if !brace {
			e.emit("(" + name + ")")
		}
		e.emitArrayValues(values, a)
		return
	}
	values, fields, ok := e.litFieldValues(name, lit)
	if !ok {
		return
	}
	if !brace {
		e.emit("(" + name + ")")
	}
	if len(values) == 0 {
		e.emit("{0}") // no values: zero every field
		return
	}
	e.emit("{")
	// The path each element sits at, for anything this literal has to defer to a
	// copy after the declaration (see recordLitFixup). It grows as the walk descends
	// and is put back on the way out, so a nested literal names the whole route.
	litPath := e.litPath
	for i, v := range values {
		if i != 0 {
			e.emit(", ")
		}
		if v == nil {
			e.emit(e.zeroFieldC(fields[i])) // a field this keyed literal omits
			continue
		}
		var expect structField
		if i < len(fields) {
			expect = fields[i] // the field, for a type-elided or nested element
			e.litPath = litPath + "." + e.fieldIdent(expect.name)
		}
		e.emitLitElement(*v, expect, brace)
	}
	e.litPath = litPath
	e.emit("}")
}

// litFixup is one element of a composite literal that C cannot put in an
// initializer: an ARRAY, which C will not copy there at all. The position is filled
// with zeros and the value copied in afterwards, once the storage the literal fills
// has a name -- which is the only point at which the copy can be written, and the
// reason this is collected rather than emitted where it is found.
type litFixup struct {
	path string  // from the literal's own name: "[1]", ".xs", "[0].xs"
	src  []int32 // the expression to copy from
	// ctype is the STRUCT type being copied, for the other element C will not take:
	// one that HOLDS an array, which the target's compiler cannot copy by assignment
	// anywhere. Empty for an array element, which is copied by its own size.
	ctype string
}

// captureLitFixups renders a literal with a fixup list of its own and answers what
// it deferred, leaving whatever an enclosing literal had collected untouched.
func (e *emitter) captureLitFixups(render func()) []litFixup {
	savedFixups, savedPath, savedOK := e.litFixups, e.litPath, e.litFixable
	e.litFixups, e.litPath, e.litFixable = nil, "", true
	render()
	fixups := e.litFixups
	e.litFixups, e.litPath, e.litFixable = savedFixups, savedPath, savedOK
	return fixups
}

// recordLitFixup defers one element of a composite literal to a copy after the
// declaration, and reports whether it could. The caller writes the zeros that stand
// in for it meanwhile.
//
// The element's position is e.litPath, built up as the literal is walked, so the
// copy names the storage the literal fills as soon as that storage has a name.
// want is what the position declares, and is checked against the value's own shape
// for the reason every other array copy checks it: the copy is sized by the
// destination.
func (e *emitter) recordLitFixup(v Node, want arrDim) bool {
	if !e.litFixable {
		// A literal standing where nothing gives it a name -- a compound literal in
		// expression position. There is no storage to copy into and no statement to
		// copy in, so this is refused rather than zeroed: a fixup nobody emits is a
		// literal that silently loses an element.
		e.fail("a %s value cannot be an element of a literal written here; bind the literal "+
			"to a variable first", e.goArrayTypeName(want))
		return false
	}
	a, ok := e.arrayShapeOf(v.ast)
	if !ok {
		// A CALL returning an array is not a value to copy FROM -- the result
		// travels through an out parameter -- so arrayShapeOf, which answers for
		// values, does not see it. The copy is the call itself, writing into the
		// element, so the shape comes off the callee's result.
		if _, ra, isCall := e.arrayResultCall(v.ast); isCall {
			a, ok = ra, true
		}
	}
	if !ok {
		e.fail("an element of a %s literal must be a literal, an array value or a call returning one",
			e.goArrayTypeName(want))
		return false
	}
	if a.elem != want.elem || a.declSuffix() != want.declSuffix() {
		e.fail("cannot use %s as %s in a literal", e.goArrayTypeName(a), e.goArrayTypeName(want))
		return false
	}
	e.litFixups = append(e.litFixups, litFixup{path: e.litPath, src: v.ast})
	return true
}

// litFixupCopies renders the deferred copies as C statements, into the storage now
// named by dst. Rendering the sources here rather than where they were found is
// what puts them in the order the statements run in.
func (e *emitter) litFixupCopies(dst string, fixups []litFixup) ([]string, bool) {
	out := make([]string, 0, len(fixups))
	for _, f := range fixups {
		at := dst + f.path
		if f.ctype != "" {
			// A struct that holds an array takes the memcpy every copy of one takes.
			stmt := strings.TrimSuffix(e.captureC(func() { e.emitStructCopy(at, f.ctype, f.src) }), "\n")
			if stmt == "" {
				return nil, false // emitStructCopy has said why
			}
			out = append(out, stmt)
			continue
		}
		// `[]Row{mk()}`: the callee fills storage the caller owns, and the element IS
		// that storage, so the call writes through it directly -- no copy, which is
		// what the out-parameter ABI is for.
		if cname, _, isCall := e.arrayResultCall(f.src); isCall {
			stmt := strings.TrimSuffix(e.captureC(func() { e.emitArrayResultCall(at, cname, f.src) }), "\n")
			if stmt == "" {
				return nil, false // emitArrayResultCall has said why
			}
			out = append(out, stmt)
			continue
		}
		src, ok := e.arraySourceC(f.src)
		if !ok {
			e.fail("cannot copy this into a literal's element: it is not an array this can read from")
			return nil, false
		}
		e.includes["string.h"] = true
		out = append(out, "memcpy("+at+", "+src+", sizeof("+at+"));")
	}
	return out, true
}

// recordStructLitFixup defers the other element C will not take: a STRUCT that HOLDS
// an array, which the target's C compiler cannot copy by assignment anywhere -- so
// no more here than at a plain `s = t`. It is zeroed in place and memcpy'd in after
// the declaration, which is what every other copy of such a struct does.
func (e *emitter) recordStructLitFixup(v Node, ctype string) bool {
	if !e.litFixable {
		e.fail("a value of %s cannot be an element of a literal written here: it holds an array, "+
			"which the target's C compiler cannot copy; bind the literal to a variable first", ctype)
		return false
	}
	if !e.checkStructCopySrc(ctype, v.ast) {
		return false
	}
	e.litFixups = append(e.litFixups, litFixup{path: e.litPath, src: v.ast, ctype: ctype})
	return true
}

// flushLitFixups emits the copies a rendered literal deferred. It must run after the
// declaration the literal initialized and before anything reads it.
func (e *emitter) flushLitFixups(dst string, fixups []litFixup) {
	stmts, ok := e.litFixupCopies(dst, fixups)
	if !ok {
		return
	}
	for _, stmt := range stmts {
		e.ind()
		e.emit(stmt + "\n")
	}
}

// pkgArrayInit resolves the shape of a package variable's ARRAY initializer: any
// array a value names, or the result of a call that returns one.
func (e *emitter) pkgArrayInit(initExpr []int32) (arrDim, bool) {
	if a, ok := e.arrayShapeOf(initExpr); ok {
		return a, true
	}
	if _, a, isCall := e.arrayResultCall(initExpr); isCall {
		return a, true
	}
	return arrDim{}, false
}

// emitPkgArrayVar declares a package ARRAY variable whose initializer is not a
// literal -- `var g = mk()`, `var g = src`, `var g = h.f` -- and fills it at package
// initialization. C admits neither a call nor an array copy in a static initializer,
// and the zero the declaration gets is the right starting value either way, so the
// storage is a file-scope table exactly as a literal's is and only the fill moves.
//
// The fill is emitArrayTargetAssign, which is what every other whole-array write
// goes through, so a receive and an out-parameter call come with it. Its statements
// become one step, ordered against the package variables the initializer reads.
func (e *emitter) emitPkgArrayVar(gn, srcName string, a arrDim, initExpr []int32) {
	// Asked here rather than left to the fill, which reports it as an assignment:
	// this position is a declaration and Go names it one. fail keeps the first
	// error, so saying it here is what decides the wording.
	if !e.checkArrayShape(a, initExpr, "variable declaration") {
		return
	}
	e.globalArrays[gn] = a
	e.emit("static " + a.elem + " " + gn + a.declSuffix() + ";\n")
	// A temporary the fill hoists out of itself has no enclosing statement here to
	// go before, so it becomes a step statement of its own -- the care pkgInitAssign
	// takes, for the same reason.
	saved, savedIndent := e.prologue, e.indent
	e.prologue, e.indent = nil, 0
	text := e.captureC(func() { e.emitArrayTargetAssign(gn, a, initExpr) })
	pro := e.prologue
	e.prologue, e.indent = saved, savedIndent
	if text == "" {
		return // emitArrayTargetAssign has said why
	}
	step := pkgInitStep{
		target:  gn,
		deps:    e.globalRefs(initExpr),
		srcName: srcName,
		pos:     e.astPos(initExpr),
	}
	for _, line := range pro {
		step.stmts = append(step.stmts, strings.TrimSuffix(line, "\n"))
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		step.stmts = append(step.stmts, line)
	}
	e.pkgInit = append(e.pkgInit, step)
}

// pkgInitLitFixups defers a file-scope literal's copies to the synthesized package
// initializer: C admits no call in a static initializer, and the values copied are
// other package variables, so the step carries their names as dependencies and is
// ordered against them exactly as an assigned initializer is.
func (e *emitter) pkgInitLitFixups(target string, fixups []litFixup) {
	// A source that hoists a temporary out of itself has no enclosing statement here
	// to put it before, so the temporary becomes a step statement of its own ahead of
	// the copies -- the same care pkgInitAssign takes.
	saved := e.prologue
	e.prologue = nil
	stmts, ok := e.litFixupCopies(target, fixups)
	pro := e.prologue
	e.prologue = saved
	if !ok {
		return
	}
	step := pkgInitStep{target: target}
	for _, line := range pro {
		step.stmts = append(step.stmts, strings.TrimSuffix(line, "\n"))
	}
	step.stmts = append(step.stmts, stmts...)
	for _, f := range fixups {
		step.deps = append(step.deps, e.globalRefs(f.src)...)
	}
	e.pkgInit = append(e.pkgInit, step)
}

// emitLitElement emits one element of a composite literal. Inside a brace
// initializer an element that is itself a literal is written with braces too,
// which is C's spelling for a nested aggregate and the only one flexcc lowers for
// a struct that holds an array (see emitCompositeLit).
//
// expect describes what this element's position implies -- the array/slice element
// type, or the struct field. A type-elided element (`{1}` for `P{1}`) arrives as a
// bare CompositeLit node with no type of its own, so it is emitted against expect,
// which must name something a brace can fill.
func (e *emitter) emitLitElement(v Node, expect structField, brace bool) {
	expectType := expect.ctype
	// An INTERFACE-typed position takes the two words, not whatever was written: a
	// pointer standing here has to become {data, table} the way it does at an
	// assignment or an argument. Put in raw, the C compiler refused the literal --
	// "expected _struct__Shape but got pointer to _struct__Rect" -- so `Box{&gr}`
	// did not compile though Go accepts it.
	if e.isIfaceCType(expectType) {
		text, ok := e.ifaceBraceC(expectType, v.ast)
		if !ok {
			e.fail("cannot use this value as %s in a literal: an interface holds a pointer here, "+
				"so write the address of a variable", e.goTypeName(expectType))
			return
		}
		e.emit(text)
		return
	}
	// An element of a struct type that holds an array is assigned by value into the
	// literal's storage, which is the same copy the backend cannot make: it reported
	// "incompatible types" rather than anything a reader could act on. A whole
	// literal of such a struct is fine -- that is C's own aggregate initialization,
	// which emitStructLit writes -- so this refuses only an element COPIED from
	// something else.
	if e.hasArrayField(expectType) && !e.isCompositeLitExpr(v) {
		if !e.recordStructLitFixup(v, expectType) {
			return
		}
		e.emit(e.zeroBraceC(expectType))
		return
	}
	// An array-typed field takes a nested aggregate, `P{1, [2]int{2, 3}}` or its
	// type-elided form `P{1, {2, 3}}`. Both are the same values in braces, and the
	// field's own extents say how deep they nest -- the literal's written type, when
	// there is one, describes what the field already declares.
	if expect.dim.bound != "" {
		if lit, ok := e.arrayLitElement(v); ok {
			values, _, ok := e.litPositions(lit)
			if !ok {
				return
			}
			e.emitArrayValues(values, expect.dim)
			return
		}
		// The position takes an array and the value is not a literal but a VALUE --
		// `[][2]int{a, b}`, `B{a}`. C cannot copy an array in an initializer at all,
		// so the position is zeroed and the copy deferred to after the declaration.
		if !e.recordLitFixup(v, expect.dim) {
			return
		}
		e.emit("{0}")
		return
	}
	if v.sym == CompositeLit {
		if !e.isStruct(expectType) {
			e.fail("a type-elided composite literal element is only supported for a struct element type yet")
			return
		}
		if len(compositeLitElements(v)) == 0 {
			e.emit(e.zeroBraceC(expectType)) // "{}" -> full zeros; see zeroBraceC
			return
		}
		e.emitCompositeLit(expectType, v, true)
		return
	}
	// A SLICE element is not a nested aggregate, whatever it is named: it is a header
	// that has to point at storage, so it takes the expression path, which hoists a
	// backing array and a temporary ahead of the literal and puts the temporary here.
	// A []int element already went that way, its literal not being a named one; a
	// `type List []int` element did not, and was brace-filled -- "Box b = {{1, 2,
	// 3}}" set the header's own pointer, length and capacity to 1, 2 and 3.
	if nm, sub, ok := e.soleCompositeLit(v.ast); brace && ok && !e.isSliceCType(e.underlyingCType(nm)) {
		if len(compositeLitElements(sub)) == 0 {
			e.emit(e.zeroBraceC(nm)) // "{0}" does not nest; see zeroBraceC
			return
		}
		e.emitCompositeLit(nm, sub, true)
		return
	}
	// A string is a { pointer, length } struct, so an element that is one is a
	// nested aggregate and takes braces here for the same reason a literal does.
	//
	// What qualifies is an element that FOLDS to a constant string, not merely one
	// written as a bare literal. The two are the same thing to C -- both reach the
	// output as the bytes and their length -- and testing the spelling instead left
	// a constant CONCATENATION, `[2]string{pre + "b", "c"}`, emitting the compound
	// literal form, which the target's compiler rejects at file scope as "Bad
	// constant expression". A constant rune conversion, `string('a')`, is the same
	// case and is how this was found. A call that genuinely returns a string does
	// not fold, and is left to the paths below: bracing what it contains would not
	// be C, which is what the spelling test was reaching for.
	if _, isConst := e.foldConstString(v.ast); brace && isConst {
		saved := e.declInit
		e.declInit = true
		e.emitExpr(v.ast)
		e.declInit = saved
		return
	}
	// A struct VALUE standing as an element -- a variable, a call's result, a
	// conversion, anything that is not a literal. The target's C compiler refuses a
	// non-braced aggregate inside an ARRAY initializer: `[]B{b}` was reported as
	// "expected int but got _struct__B", about generated code the program never
	// wrote, though the same C is valid and the host compiler takes it. It is the
	// same limit that already makes a slice element and a string element brace here.
	//
	// The value is bound to a name first so it is evaluated once however it was
	// written. A struct holding an ARRAY never reaches this: it is refused above.
	if brace && e.isStruct(expectType) {
		base, isName := e.exprIdent(v.ast)
		if isName {
			base = e.varRef(base)
		} else {
			base = e.hoist(expectType, func() { e.emitExpr(v.ast) })
		}
		e.emit(e.structBraceC(base, expectType))
		return
	}
	e.emitExpr(v.ast)
}

// structBraceC renders a struct VALUE reached by base as the brace initializer an
// array literal's element position takes: its members in order, each braced in turn
// where it is itself an aggregate. See emitLitElement for why the braces are needed.
//
// A string, a slice and an interface are structs the emitter mints rather than
// declares, so their members are named here; everything else reads its fields off
// the registry. A member that is neither is a scalar and stands as it is.
func (e *emitter) structBraceC(base, ctype string) string {
	u := e.underlyingCType(ctype)
	switch {
	case u == cString:
		return "{" + base + ".str, " + base + ".len}"
	case e.isSliceCType(u):
		return "{" + base + ".ptr, " + base + ".len, " + base + ".cap}"
	case e.isIfaceCType(u):
		// Asked before the registry, which holds an interface's name with no fields
		// under it -- so the generic path would have written "{0}" and zeroed the
		// value instead of copying it.
		return "{" + base + ".data, " + base + ".vt}"
	}
	fields, ok := e.structs[u]
	if !ok {
		return base // a scalar member is itself
	}
	if len(fields) == 0 {
		return "{0}" // an empty struct: its one hidden byte, not "{}" (invalid C)
	}
	var parts []string
	for _, f := range fields {
		// The C member name, not the Go one: a field whose name collides with a type
		// takes a trailing underscore, so reading `b.I` for a field named after the
		// type I produced "unknown identifier I in class _struct__B".
		parts = append(parts, e.structBraceC(base+"."+e.fieldIdent(f.name), f.ctype))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// valueIsSliceExpr reports whether a value is a slice EXPRESSION -- `pool[:]`,
// `b.arr[:]`, `(*p)[:]` -- rather than a literal, a call or a name. It is the shape
// that renders as a compound literal, and so the only one that needs the brace
// spelling inside an initializer.
func (e *emitter) valueIsSliceExpr(v Node) bool {
	// A CONVERSION to a slice type renames the same header, so it renders whatever
	// its operand renders: `L(a[:])` is a compound literal exactly as `a[:]` is.
	if recv, suffix, isCall := e.directCall(v.ast); isCall && len(suffix) != 0 &&
		suffix[len(suffix)-1].sym == CallSuffix && e.convToSliceType(recv, suffix) {
		if args := e.callArgExprs(suffix[len(suffix)-1].ast); len(args) == 1 {
			return e.valueIsSliceExpr(args[0])
		}
	}
	fac, ok := e.soleFactorNode(v.ast)
	if !ok {
		return false
	}
	kids := slices.Collect(it(fac.ast))
	if _, steps, isDeref := e.factorDerefChain(kids); isDeref {
		return e.endsInSliceStep(steps)
	}
	_, steps, isChain := e.factorAccessChain(kids)
	return isChain && e.endsInSliceStep(steps)
}

// litKeyIndex evaluates an array or slice literal's element index -- the "2" in
// "[]int{2: 5}" -- to a non-negative integer. Only a constant is admitted, which is
// Go's rule and the spec's; a non-constant or negative key yields ok=false.
func (e *emitter) litKeyIndex(keyAST []int32) (int, bool) {
	// The whole constant folder rather than a token switch. An index is any constant
	// expression Go accepts -- `geo.K`, `K + 1`, `1 << 2` -- and reading a SOLE token
	// answered only for a literal or a bare name, so a qualified constant was refused
	// as "not a non-negative integer constant" about one that is.
	n, ok := e.foldConstInt(keyAST)
	if !ok || n < 0 {
		return 0, false
	}
	return int(n), true
}

// litPositions expands an array or slice literal's elements into a positional list,
// a nil entry marking an index the literal skips (and so zeroes). It resolves Go's
// indexed form: a keyed element places its value at a constant index, and a later
// positional element continues from that index plus one ("[]int{1, 4: 9, 5}" fills
// 0, 4 and 5). length is the highest index used plus one -- the array's declared or
// backing-slice length. A non-constant, negative, or repeated index is refused.
func (e *emitter) litPositions(lit Node) (values []*Node, length int, ok bool) {
	elements := compositeLitElements(lit)
	byIndex := map[int]*Node{}
	cur, maxIdx := 0, -1
	for i := range elements {
		el := &elements[i]
		idx := cur
		if el.keyed {
			k, ok := e.litKeyIndex(el.key.ast)
			if !ok {
				e.fail("an array or slice literal index must be a non-negative integer constant")
				return nil, 0, false
			}
			idx = k
		}
		if _, dup := byIndex[idx]; dup {
			e.fail("duplicate index %d in an array or slice literal", idx)
			return nil, 0, false
		}
		byIndex[idx] = &el.value
		if idx > maxIdx {
			maxIdx = idx
		}
		cur = idx + 1
	}
	length = maxIdx + 1
	values = make([]*Node, length)
	for idx, v := range byIndex {
		values[idx] = v
	}
	return values, length, true
}

// emitPositionalValues emits a positional value list as a braced C initializer.
// A nil entry -- an index the literal skips -- is written as the element type's
// zero, because C cannot leave a gap in a positional list the way "[i]=v" could
// (and flexcc mishandles designated initializers anyway; see litFieldValues).
func (e *emitter) emitPositionalValues(values []*Node, elemCType string) {
	if len(values) == 0 {
		e.emit("{0}") // no values: zero every element
		return
	}
	e.emit("{")
	litPath := e.litPath
	for i, v := range values {
		if i != 0 {
			e.emit(", ")
		}
		if v == nil {
			e.emit(e.zeroInitC(elemCType))
			continue
		}
		e.litPath = litPath + "[" + strconv.Itoa(i) + "]"
		// A named ARRAY element carries its extents, so a `{1, 2}` written for one is
		// read as a value OF that element rather than as a nested extent of the outer
		// array -- which is what `[][2]int{{1, 2}, {3, 4}}` needs.
		fld := structField{ctype: elemCType}
		if a, isArr := e.namedArrays[elemCType]; isArr {
			fld.dim = a
		}
		// A slice EXPRESSION standing as an ARRAY's element renders as a compound
		// literal, `(ogo_slice_int){pool, 4, 4}`, and the target's compiler refuses
		// one inside an ARRAY initializer -- "expected pointer to int but got
		// __anon_..." -- while accepting it inside a STRUCT initializer, which is
		// why only this path spells it the other way. So `[2][]int{r0[:], r1[:]}`,
		// a table of rows, did not compile at all. declInit braces the header, and
		// only a slice expression needs it: a slice LITERAL element must hoist a
		// backing array first, which is exactly what declInit turns off.
		if e.isSliceCType(e.underlyingCType(elemCType)) && e.valueIsSliceExpr(*v) {
			saved := e.declInit
			e.declInit = true
			e.emitLitElement(*v, fld, true)
			e.declInit = saved
			continue
		}
		e.emitLitElement(*v, fld, true)
	}
	e.litPath = litPath
	e.emit("}")
}

// emitArrayLitVar declares a local initialized from an array or slice literal.
//
// An array is C's own aggregate initialization, `int a[3] = {1, 2, 3};` -- which is
// also why an array literal is only ever a declaration's initializer: C cannot
// assign an array, so there is nowhere else to put one.
//
// A slice literal has no such spelling. It lowers the way make does, to a backing
// array plus a { pointer, len, cap } header, the difference being that the backing
// array carries the values and its length is the number of them.
// emitArrayCopy declares array local dst with the same dimensions as source array
// src (an already-declared array's C name) and copies it by value with memcpy, the
// lowering of Go's `dst := src` / `var dst [N]T = src` array-value copy (C forbids
// array assignment). dst is registered so later dst[i] / len(dst) resolve.
// arrayFieldOperand recognises a read of an ARRAY through a field chain, `h.f` or
// `b.inner.grid`, answering with the C text naming it and its shape. It is the field
// form of arrayDerefOperand, for the positions that copy an array by value.
//
// The shape comes from the field's own declaration, so a field of a DEFINED array
// type keeps that type's name -- which is what lets the copy carry its method set.
func (e *emitter) arrayFieldOperand(ast []int32) (string, arrDim, bool) {
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return "", arrDim{}, false
	}
	base, fields, isField := e.factorFieldAccess(slices.Collect(it(fac.ast)))
	if !isField {
		return "", arrDim{}, false
	}
	a, isArr := e.fieldArray(base, fields)
	if !isArr {
		return "", arrDim{}, false
	}
	return e.fieldAccessC(base, fields), a, true
}

// arrayChainOperand recognises a read of an ARRAY through a chain that includes an
// index, `pool[1]` or `b.rows[0]`, answering with the C text naming it and its shape.
// It complements arrayFieldOperand: a pure field chain goes there because the field's
// declaration still knows the type's NAME, which this cannot recover.
func (e *emitter) arrayChainOperand(ast []int32) (string, arrDim, bool) {
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return "", arrDim{}, false
	}
	base, steps, isChain := e.factorAccessChain(slices.Collect(it(fac.ast)))
	if !isChain {
		return "", arrDim{}, false
	}
	cur, walked := e.accessChainType(base, steps)
	if !walked || len(cur.dims) == 0 {
		return "", arrDim{}, false
	}
	text, okText := e.accessChainCText(base, steps)
	if !okText {
		return "", arrDim{}, false
	}
	// cur.name is what an INDEX now hands on -- `pool[1]` over a `[2]Row` reaches a
	// Row -- so a copy of it keeps the type's method set. Dropping it here is what
	// left `r := pool[1]; r.Sum()` reading the call as a package qualification.
	return text, curArrDim(cur), true
}

// arrayOperandOf resolves the shape of an ARRAY a factor names through a chain --
// `h.f`, `h.inner.g`, `pool[1]`, `h.rows[1]` -- for the callers that want the extents
// rather than the C text arrayFieldOperand and arrayChainOperand also render.
//
// The field form is asked first because it keeps the type's NAME, so a pointer to it
// is spelled `Row*` rather than by a minted name; a route through an index has no
// name to keep.
func (e *emitter) arrayOperandOf(n Node) (arrDim, bool) {
	kids := slices.Collect(it(n.ast))
	if base, fields, isField := e.factorFieldAccess(kids); isField {
		if a, isArr := e.fieldArray(base, fields); isArr {
			return a, true
		}
	}
	if base, steps, isChain := e.factorAccessChain(kids); isChain {
		if cur, walked := e.accessChainType(base, steps); walked && len(cur.dims) != 0 {
			return arrDim{elem: cur.elem, bound: cur.dims[0], inner: cur.dims[1:], name: cur.name}, true
		}
	}
	return arrDim{}, false
}

func (e *emitter) emitArrayCopy(dst, src string, a arrDim) {
	e.arrays[dst] = a
	e.includes["string.h"] = true
	e.ind()
	e.emit(a.elem + " " + dst + a.declSuffix() + ";\n")
	e.ind()
	e.emit("memcpy(" + dst + ", " + src + ", sizeof(" + dst + "));\n")
}

// emitArrayValues emits an array literal's values as a braced C initializer,
// descending a multi-dimensional array one extent at a time. C spells a nested
// array exactly this way -- `int m[2][2] = {{1, 2}, {3, 4}}` -- so an element of a
// rank > 1 array is itself a braced list of that row's values.
//
// It takes the arrDim rather than the element type string emitPositionalValues
// works from because an element of a multi-dimensional array has no C value type
// to name: cType models no array type at all, so only the innermost level has a
// type to emit against, and it is the row's own extent that bounds the level above.
func (e *emitter) emitArrayValues(values []*Node, a arrDim) {
	if a.dims() == 1 {
		e.emitPositionalValues(values, a.elem)
		return
	}
	if len(values) == 0 {
		e.emit("{0}") // no values: zero every element, at every depth
		return
	}
	row := a.row()
	e.emit("{")
	litPath := e.litPath
	for i, v := range values {
		if i != 0 {
			e.emit(", ")
		}
		if v == nil {
			e.emit("{0}") // an index the literal skips: the whole row is zero
			continue
		}
		e.litPath = litPath + "[" + strconv.Itoa(i) + "]"
		// A row written as a VALUE rather than a literal, `[2][2]int{a, b}` -- a
		// table built from named rows. C cannot copy an array in an initializer, so
		// the row is zeroed here and copied in after the declaration.
		if _, isLit := e.arrayLitElement(*v); !isLit {
			if !e.recordLitFixup(*v, row) {
				return
			}
			e.emit("{0}")
			continue
		}
		rowValues, ok := e.rowValues(*v, row)
		if !ok {
			return
		}
		e.emitArrayValues(rowValues, row)
	}
	e.litPath = litPath
	e.emit("}")
}

// rowValues reads one element of a multi-dimensional array literal as the values
// of a row. The element is written either type-elided (`{1, 2}`, the usual form)
// or carrying its own type (`[2]int{1, 2}`), which must then be the row's type.
func (e *emitter) rowValues(v Node, row arrDim) ([]*Node, bool) {
	lit := v
	if v.sym != CompositeLit {
		litType, sub, ok := e.soleArrayLit(v.ast)
		if !ok {
			e.fail("an element of a %s literal must itself be a literal", e.goArrayTypeName(row))
			return nil, false
		}
		if !e.sameArrayType(row, litType) {
			return nil, false
		}
		lit = sub
	}
	values, length, ok := e.litPositions(lit)
	if !ok {
		return nil, false
	}
	if n, err := strconv.Atoi(row.bound); err == nil && length > n {
		e.fail("too many values in %s literal: %s but the length is %s", e.goArrayTypeName(row), countUnits(length, "value"), row.bound)
		return nil, false
	}
	return values, true
}

// emitArrayLitVar emits a variable initialized by an array or slice literal --
// "[N]T{...}" as a C aggregate, "[]T{...}" as a backing array plus a header over
// it. static drives file-scope emission: "static", no indent, and the package
// rather than the local type environment. name is the emitted C name, already
// mangled for a package variable.
//
// A file-scope slice header initialized with the backing array's name is a valid
// C static initializer -- an address constant -- so a package-level slice literal
// needs no run-time init step, unlike a package variable whose initializer is a
// call.
func (e *emitter) emitArrayLitVar(name string, typeAST []int32, lit Node, static bool) {
	values, length, ok := e.litPositions(lit)
	if !ok {
		return
	}
	// lead opens a declaration: "static " at file scope, the current indent inside
	// a function.
	lead := func() {
		if static {
			e.emit("static ")
			return
		}
		e.ind()
	}
	if a, ok := e.arrayDim(typeAST); ok {
		// Fewer values than the length is legal and zeroes the rest, as in Go; more
		// is not. C only warns about the excess, and the extra values are dropped,
		// so saying so here is the difference between a diagnostic and a surprise.
		// An index-form literal spans highest-index+1 positions, so this also
		// catches "[3]int{5: 1}", whose index lies past the declared length.
		if n, err := strconv.Atoi(a.bound); err == nil && length > n {
			e.fail("too many values in %s literal: %s but the length is %s", e.goArrayTypeName(a), countUnits(length, "value"), a.bound)
			return
		}
		if static {
			e.globalArrays[name] = a
		} else {
			e.arrays[name] = a
		}
		// An element that is an array VALUE cannot go in the initializer -- C copies
		// no array there -- so it is zeroed and copied in afterwards. At file scope
		// there is no "afterwards" in C, and the copy becomes a step of the package
		// initializer, ordered against the variables it reads like any other.
		fixups := e.captureLitFixups(func() {
			lead()
			e.emit(a.elem + " " + name + a.declSuffix() + " = ")
			e.emitArrayValues(values, a)
			e.emit(";\n")
		})
		switch {
		case len(fixups) == 0:
		case static:
			e.pkgInitLitFixups(name, fixups)
		default:
			e.flushLitFixups(name, fixups)
		}
		return
	}
	elem, ok := e.litSliceType(typeAST)
	if !ok {
		e.fail("unsupported array or slice literal type")
		return
	}
	e.needSlice(elem)
	cname := sliceCName(elem)
	// When the literal NAMES a defined slice type, the variable takes that name and
	// not the header's. litSliceType above answers with the element, which is what
	// indexing and len want; the name is what a METHOD wants, and dropping it left
	// `d := List{1, 2, 3}` with nothing for `d.sum()` to hang off -- the emitter read
	// the call as a package qualification and said `unknown package "d"`. The
	// declared forms, `var d List = make(...)` and `var l List = back[:]`, always
	// kept it, so this is the short form catching up with them.
	if nm, ok := e.namedSliceLitType(typeAST); ok {
		cname = nm
	}
	if static {
		e.globalSliceVars[name] = elem
		e.globals[name] = cname
	} else {
		e.sliceVars[name] = elem
		e.locals[name] = cname
		e.frameBacked[name] = true // the backing array is a local of this frame
	}
	if length == 0 {
		// "[]T{}" is an empty slice, not a slice of one zero element. C has no
		// zero-length array to point it at, and it needs none: the header is the
		// zero value, whose pointer is never dereferenced because the length is 0.
		lead()
		e.emit(cname + " " + name + " = {0};\n")
		return
	}
	backing := e.newBacking()
	n := strconv.Itoa(length)
	// An ARRAY element is declared through the element's own extents rather than
	// through its typedef: `int b[2][2] = {{1, 2}, {3, 4}}`, not
	// `ogo_arr_2_int b[2] = {...}`, which the target's compiler refuses ("expected
	// pointer to int but got list of 2 values"). The header stores the same thing
	// either way -- `int[2][2]` decays to `int (*)[2]`, which IS ogo_arr_2_int*.
	decl, suffix := elem, ""
	if a, isArr := e.namedArrays[elem]; isArr {
		decl, suffix = a.elem, a.declSuffix()
	}
	// A file-scope backing array whose elements are not all constant cannot be
	// written as a static initializer -- C evaluates one at compile time. The ARRAY
	// forms are zeroed and filled at package initialization instead; a slice's
	// backing has no such fill yet, so it is refused HERE with the shape that does
	// work, rather than emitted and left to the backend to reject in words about
	// generated C the program never wrote.
	if static && !e.staticLitElementsOK(lit) {
		e.fail("a package slice literal's elements must be constant: declare the values as an "+
			"array and slice it, `var back = [%s]%s{...}` and `var %s = back[:]`", n, e.goTypeName(elem), name)
		return
	}
	fixups := e.captureLitFixups(func() {
		lead()
		e.emit(decl + " " + backing + "[" + n + "]" + suffix + " = ")
		e.emitPositionalValues(values, elem)
		e.emit(";\n")
	})
	// The copies fill the BACKING array, which is what holds the elements; the header
	// below only points at it, so it may be built either side of them.
	switch {
	case len(fixups) == 0:
	case static:
		e.pkgInitLitFixups(backing, fixups)
	default:
		e.flushLitFixups(backing, fixups)
	}
	lead()
	e.emit(cname + " " + name + " = {" + backing + ", " + n + ", " + n + "};\n")
}

// sameArrayType reports whether a literal's bracketed type is the array type
// required where it stands, and reports the mismatch by name if not. The checker
// does not compare composite types yet, so this is where "var a [3]int =
// [2]int{}" is caught -- without it the literal's own extent would silently win.
// The same comparison bounds a row of a multi-dimensional literal that carries its
// own type, so the message names the expected type rather than the position.
func (e *emitter) sameArrayType(want arrDim, litType []int32) bool {
	lit, ok := e.arrayDim(litType)
	if ok && lit.elem == want.elem && slices.Equal(lit.bounds(), want.bounds()) {
		return true
	}
	e.fail("cannot use a %s literal as %s", e.litTypeName(litType), e.goArrayTypeName(want))
	return false
}

// hoistLit binds a slice literal to a local declared before the statement and
// returns its name. It is emitArrayLitVar's output moved into the prologue, which is
// where the backing array has to go: that declaration is two lines, so each becomes
// a prologue line of its own rather than one hoist.
//
// It declines an array literal and a static initializer, whose refusals the caller
// keeps -- an array has no C value type to bind, and a static initializer has no
// statement to hang the declarations off.
func (e *emitter) hoistLit(typeAST []int32, lit Node) (string, bool) {
	if e.declInit {
		return "", false
	}
	// Only a slice literal. A slice is a header, an ordinary C value that can stand
	// wherever one can; an array is not -- C cannot assign it, and binding one here
	// would turn a refusal into "assignment to expression with array type", which
	// is the emitter writing C the user never did.
	if _, ok := e.litSliceType(typeAST); !ok {
		return "", false
	}
	return e.hoistLitVar(typeAST, lit)
}

// hoistArrayLitExpr binds an ARRAY literal standing in expression position to a
// temporary of this frame, declared before the statement, and answers with its name.
//
// It is separate from hoistLit, which handles a slice literal and refuses an array
// on purpose: a slice is a header, an ordinary C value that can stand wherever one
// can, while an array is not, so a name bound here cannot simply be emitted where the
// literal was. What it CAN do is serve the two positions that have a lowering of
// their own -- an argument, where the parameter's C form is a pointer the callee
// memcpys from, and an assignment, which copies. Both are given the name; neither
// makes C assign an array.
//
// The temporary is this frame's, so the lifetime rules that already see an array
// literal as frame storage (frameRefOf) still refuse returning or storing one.
func (e *emitter) hoistArrayLitExpr(ast []int32) (string, bool) {
	if e.declInit || e.deferReplay >= 0 {
		return "", false
	}
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return "", false
	}
	typeAST, lit, ok := e.factorArrayLit(fac)
	if !ok {
		return "", false
	}
	if _, isArray := e.arrayDim(typeAST); !isArray {
		return "", false
	}
	return e.hoistLitVar(typeAST, lit)
}

// unwrapArrayConv sees through a conversion to an ARRAY type, `Col(r)`, answering
// with the operand it converts. The conversion changes nothing about the value -- the
// typedef stands for the same storage -- and Go admits one between array types only
// where the underlying types are identical, so the operand IS the array, of the same
// shape, and every position that copies or compares an array may read it as one.
//
// Without this a conversion was not an array value at all: `c = Col(r)` fell past
// every copy path and emitted `c = r;`, which is not C, and the return, the
// comparison and a literal's element each refused it for want of an array they were
// looking straight at. The chain walk has seen through it since factorAccessChain
// gained arrayConvChain, which is why the DECLARATION form always worked.
//
// Anything that is not such a conversion comes back unchanged.
func (e *emitter) unwrapArrayConv(ast []int32) []int32 {
	if operand, ok := e.arrayConvOperand(ast); ok {
		return operand
	}
	// `([2]int)(r)` -- the same conversion to an UNNAMED array type, which the
	// grammar can only spell parenthesised. Only with nothing after it: a suffix
	// makes it a chain, which the chain walk reads for itself.
	if fac, isFac := e.soleFactorNode(ast); isFac {
		if typeAST, arg, steps, isConv := e.factorBracketConv(fac); isConv && len(steps) == 0 {
			if _, isArray := e.arrayDim(typeAST); isArray {
				return arg
			}
		}
	}
	return ast
}

// arrayShapeOf resolves the SHAPE of an array-valued expression, where the program
// wrote enough to read one off: a variable, a dereferenced pointer to one, an array
// reached through a chain of fields and indexes, or a literal, which carries its own
// type. It is arraySourceC's pure twin -- same shapes, no text and no temporary --
// so a caller may ask what it is about to copy before deciding to copy it.
//
// It answers false for anything else, and a caller must read that as "not known",
// never as "not an array": a shape this cannot see is not a mismatch.
func (e *emitter) arrayShapeOf(ast []int32) (arrDim, bool) {
	ast = e.unwrapArrayConv(ast)
	if name, ok := e.exprIdent(ast); ok {
		return e.arrayVar(name)
	}
	if name, ok := e.derefOperand(ast); ok {
		return e.arrayPtrVar(name)
	}
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return arrDim{}, false
	}
	if a, ok := e.arrayOperandOf(fac); ok {
		return a, true
	}
	if typeAST, _, ok := e.factorArrayLit(fac); ok {
		return e.arrayDim(typeAST)
	}
	return arrDim{}, false
}

// checkArrayShape refuses a copy whose source array is not the same shape as the
// destination -- a different element type, or different extents. Go rejects such a
// program ("cannot use s (variable of type [3]int) as [2]int value in assignment"),
// and every copy here is sized by the DESTINATION, so one let through read past the
// end of a shorter source or dropped what did not fit -- silently, with the program
// running and printing a wrong answer.
//
// A source whose shape arrayShapeOf cannot read is passed. This is a check on the
// shapes that are known, not a new restriction on what may be copied.
func (e *emitter) checkArrayShape(dst arrDim, ast []int32, what string) bool {
	src, ok := e.arrayShapeOf(ast)
	if !ok || src.elem == dst.elem && src.declSuffix() == dst.declSuffix() {
		return true
	}
	e.fail("cannot use %s as %s in %s", e.goArrayTypeName(src), e.goArrayTypeName(dst), what)
	return false
}

// arraySourceC names what an array-valued right-hand side is, for a copy to read
// from: another array variable by its C name, a dereferenced pointer to one, an
// array reached through a chain of fields and indexes, or an array literal by a
// temporary bound ahead of the statement. Anything else is not something this can
// copy from and is left to the paths that report it.
func (e *emitter) arraySourceC(ast []int32) (string, bool) {
	ast = e.unwrapArrayConv(ast)
	if name, ok := e.exprIdent(ast); ok {
		if _, isArray := e.arrayVar(name); isArray {
			return e.varRef(name), true
		}
		return "", false
	}
	// `b = *p`: what is copied is the array the pointer names, and copying it is
	// what Go's dereference of a pointer to an array does.
	if text, _, ok := e.arrayDerefOperand(ast); ok {
		return text, true
	}
	// `d = h.f` / `d = pool[1]`: an array reached through a chain rather than named
	// directly. These are the shapes a DECLARATION already copies (`x := h.f`); an
	// assignment fell past every copy path to the ordinary one, which emits
	// `d = h.f;` -- not C, however willingly flexcc takes it and copies. That is the
	// same reason the plain `a = b` shape is a memcpy and not an assignment.
	//
	// The field form is asked first for the reason it is asked first everywhere: it
	// keeps the type's NAME, which a walk through an index cannot.
	if text, _, ok := e.arrayFieldOperand(ast); ok {
		return text, true
	}
	if text, _, ok := e.arrayChainOperand(ast); ok {
		return text, true
	}
	return e.hoistArrayLitExpr(ast)
}

// derefOperand recognises `*p` where p is a pointer VARIABLE, answering with its
// name. One star only: `**p` reaches a pointer, not a value with fields or
// elements, and nothing here models the second level.
func (e *emitter) derefOperand(ast []int32) (string, bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != UnaryExpr {
		return "", false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) != 2 || kids[0].sym != UnaryOp {
		return "", false
	}
	if tok, ok := e.unaryOpTok(kids[0].ast); !ok || e.f.ch(tok) != MUL {
		return "", false
	}
	name, ok := e.exprIdent(e.unparenExpr(kids[1].ast))
	if !ok {
		return "", false
	}
	if ct, ok := e.varType(name); !ok || !e.isPointer(ct) {
		return "", false
	}
	return name, true
}

// addrOperand recognises `&v` where v is a VARIABLE the emitter knows, answering
// with its name. It is derefOperand's mirror, and like it takes one operator only.
func (e *emitter) addrOperand(ast []int32) (string, bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != UnaryExpr {
		return "", false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) != 2 || kids[0].sym != UnaryOp {
		return "", false
	}
	if tok, ok := e.unaryOpTok(kids[0].ast); !ok || e.f.ch(tok) != AND {
		return "", false
	}
	name, ok := e.exprIdent(e.unparenExpr(kids[1].ast))
	if !ok {
		return "", false
	}
	if _, isVar := e.varType(name); isVar {
		return name, true
	}
	// An array variable carries no C type here -- its extents live in e.arrays --
	// so it has to be asked for separately or `(&rows).m()` would read `rows` as a
	// package qualifier.
	_, isArray := e.arrays[name]
	return name, isArray
}

// factorAddrCall matches a parenthesised ADDRESS carrying a method call,
// `(&v).m(...)`, returning the variable's name and the steps after it.
//
// Go admits it for any addressable v, and it means what `v.m()` means: a value
// receiver copies what the pointer points at, and a pointer receiver is what `v.m()`
// already takes the address for. So the shorthand IS the lowering, exactly as
// `(*p).m()` is emitted as `p.m()` (see derefCallSteps).
//
// Only the CALL form is matched. `(&v)[i]` is NOT `v[i]` -- for a slice v the first
// is illegal Go and the second is not -- so accepting the general suffix here would
// let through a program Go refuses.
func (e *emitter) factorAddrCall(kids []Node) (string, []Node, bool) {
	if len(kids) != 4 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[3].sym != FactorSuffix {
		return "", nil, false
	}
	name, ok := e.addrOperand(kids[1].ast)
	if !ok {
		return "", nil, false
	}
	steps := slices.Collect(it(kids[3].ast))
	if len(steps) == 0 || !derefCallSteps(steps) {
		return "", nil, false
	}
	return name, steps, true
}

// unparenExpr peels redundant parentheses from an expression, `(p)` and `((p))`
// alike, returning what they enclose. It is the operand-level twin of unparenKids,
// which peels a parenthesised factor that carries a SUFFIX; there is nothing to
// splice here, so the enclosed expression stands on its own.
func (e *emitter) unparenExpr(ast []int32) []int32 {
	for {
		kids := e.factorKids(ast)
		if len(kids) != 3 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
			kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[1].sym != Expression {
			return ast
		}
		ast = kids[1].ast
	}
}

// arrayDerefOperand recognises `*p` where p is a pointer to an ARRAY, answering
// with the C text for the array it points at and its extents. It is the whole-value
// half of the dereference surface: the index, length and range paths reach the
// array through a base they were given, while a copy is handed the dereference
// itself and has to see through it.
func (e *emitter) arrayDerefOperand(ast []int32) (string, arrDim, bool) {
	name, ok := e.derefOperand(ast)
	if !ok {
		return "", arrDim{}, false
	}
	a, ok := e.arrayPtrVar(name)
	if !ok {
		return "", arrDim{}, false
	}
	return "(*" + e.nilCheckedPtrVar(name) + ")", a, true
}

// hoistLitVar binds a composite literal to a temporary of this frame, declared
// before the statement, and answers with its name. It is the shared body of the
// slice and array hoists, which differ only in which literals they will take.
func (e *emitter) hoistLitVar(typeAST []int32, lit Node) (string, bool) {
	name := e.newTmp()
	saved := e.indent
	e.indent = 0
	text := e.captureC(func() { e.emitArrayLitVar(name, typeAST, lit, false) })
	e.indent = saved
	if text == "" {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		e.prologue = append(e.prologue, line+"\n")
	}
	return name, true
}

// factorLitIndexed recognises a literal of a BRACKETED type with an index or
// selector run after it -- `[]int{1, 2, 3}[0]`, `[2]P{{1, 2}, {3, 4}}[1].x`. The
// literal is bound to a temporary and the steps read that, which is the only way an
// ARRAY literal can be indexed at all: C has no array value for the steps to apply
// to. A named type's literal, `Row{1, 2, 3}[0]`, is not this shape -- it goes
// through the identifier alternative, whose suffix comes BEFORE the literal.
func (e *emitter) factorLitIndexed(fac Node) (typeAST []int32, lit Node, steps []Node, ok bool) {
	kids := slices.Collect(it(fac.ast))
	if len(kids) < 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LBRACK {
		return nil, Node{}, nil, false
	}
	if kids[len(kids)-1].sym != FactorSuffix || kids[len(kids)-2].sym != CompositeLit {
		return nil, Node{}, nil, false
	}
	if steps = slices.Collect(it(kids[len(kids)-1].ast)); !isAccessChain(steps) {
		return nil, Node{}, nil, false
	}
	// The Factor's own nodes are the bracketed type: arrayDim and sliceType look
	// only for the length Expression and the element Type, and ignore the rest.
	return fac.ast, kids[len(kids)-2], steps, true
}

// factorBracketConv recognises a conversion whose target is an UNNAMED composite
// type, `([]int)(xs)` / `([3]int)(q)`, reached here through unparenKids -- the
// parentheses are what let an LL(1) grammar spell it (see "Parentheses where the
// parser needs them" in specs.go). It answers with the target type, the operand and
// whatever follows the call.
func (e *emitter) factorBracketConv(fac Node) (typeAST []int32, arg []int32, steps []Node, ok bool) {
	kids := slices.Collect(it(fac.ast))
	if len(kids) != 4 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[3].sym != FactorSuffix {
		return nil, nil, nil, false
	}
	// The parenthesised type: its own factor's AST is what arrayDim and sliceType
	// read, so it is kept whole rather than reduced to nodes.
	inner, isFac := e.soleFactorNode(kids[1].ast)
	if !isFac {
		return nil, nil, nil, false
	}
	innerKids := slices.Collect(it(inner.ast))
	if len(innerKids) == 0 || innerKids[0].sym != 0 || e.f.ch(innerKids[0].tok) != LBRACK {
		return nil, nil, nil, false
	}
	for _, k := range innerKids {
		if k.sym == FactorSuffix || k.sym == CompositeLit {
			return nil, nil, nil, false
		}
	}
	typeAST = inner.ast
	steps = slices.Collect(it(kids[3].ast))
	if len(steps) == 0 || steps[0].sym != CallSuffix {
		return nil, nil, nil, false
	}
	args := e.callArgExprs(steps[0].ast)
	if len(args) != 1 {
		return nil, nil, nil, false
	}
	return typeAST, args[0].ast, steps[1:], true
}

// bracketConvOperand answers with the C text of a bracketed conversion whose target
// has the operand's own representation, and with whether it is one at all.
//
// A conversion between an unnamed composite type and a defined type over it changes
// nothing: `type Row [3]int` and `[3]int` are the same storage, as are `type Nums
// []int` and `[]int`. So the operand IS the answer. A conversion that would change
// the representation -- `([]byte)(s)` from a string -- is not identity and is left
// to the refusal below, which says what is actually wrong with it.
func (e *emitter) bracketConvOperand(typeAST []int32, arg []int32) (string, bool) {
	name, isName := e.exprIdent(arg)
	if !isName {
		return "", false
	}
	if a, isArray := e.arrayDim(typeAST); isArray {
		if v, isVar := e.arrayVar(name); isVar && v.elem == a.elem && v.declSuffix() == a.declSuffix() {
			return e.varRef(name), true
		}
		return "", false
	}
	if elem, isSlice := e.litSliceType(typeAST); isSlice {
		if el, isVar := e.sliceElem(name); isVar && el == elem {
			return e.varRef(name), true
		}
	}
	return "", false
}

// hoistArrayResultCall recognises a call whose result is an ARRAY with steps after
// it, `mk()[1]` or `mk()[i].x`, binds the result to a temporary of this frame and
// answers with that temporary and the steps.
//
// An array result travels through an out parameter -- C cannot return one -- so the
// call is a statement and has no expression to index. Binding it gives the steps
// something to read, which is what a declaration of the result already did; this is
// the same move without the user having to write the variable.
func (e *emitter) hoistArrayResultCall(ast []int32) (string, []Node, bool) {
	if e.declInit || e.deferReplay >= 0 {
		return "", nil, false
	}
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return "", nil, false
	}
	return e.hoistArrayResultCallKids(slices.Collect(it(fac.ast)))
}

// hoistArrayResultCallKids is hoistArrayResultCall for a factor's children already
// in hand, which is what the expression and typing walks hold.
func (e *emitter) hoistArrayResultCallKids(kids []Node) (string, []Node, bool) {
	if e.declInit || e.deferReplay >= 0 {
		return "", nil, false
	}
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	steps := slices.Collect(it(kids[1].ast))
	// The call, then the steps that read what it returned. A method is `recv.m()`,
	// two steps before the rest.
	call := 1
	if len(steps) >= 2 && steps[0].sym == Selector && steps[1].sym == CallSuffix {
		call = 2
	}
	if len(steps) <= call || steps[call-1].sym != CallSuffix || !isAccessChain(steps[call:]) {
		return "", nil, false
	}
	recv := e.src(kids[0].tok)
	cname, a, isArr := e.arrayResultCallOf(recv, steps[:call])
	if !isArr {
		return "", nil, false
	}
	name := e.newTmp()
	saved := e.indent
	e.indent = 0
	text := e.captureC(func() {
		e.emit(a.elem + " " + name + a.declSuffix() + ";\n")
		e.emitArrayResultCallOf(name, cname, recv, steps[:call])
	})
	e.indent = saved
	if text == "" {
		return "", nil, false
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		e.prologue = append(e.prologue, line+"\n")
	}
	e.arrays[name] = a
	return name, steps[call:], true
}

// litSliceType is sliceType for the type of a composite LITERAL, where a defined
// slice type stands in for what it is defined over: `type List []int` used as
// `List{1, 2, 3}` is the same literal as `[]int{1, 2, 3}`.
//
// It is deliberately not sliceType itself. That one also answers for the type of a
// VARIABLE, and a variable of a defined slice type is not a plain slice: it keeps
// its own name, which is what its methods hang off. Teaching sliceType to resolve
// the name cost `var l List = back[:]` its `l.total()`.
func (e *emitter) litSliceType(typeAST []int32) (elem string, ok bool) {
	if elem, ok := e.sliceType(typeAST); ok {
		return elem, true
	}
	nodes := slices.Collect(it(typeAST))
	if len(nodes) != 1 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != IDENT {
		return "", false
	}
	// Through a chain of definitions: `type Alias List` over `type List []int` is
	// still a slice literal's type.
	u, ok := e.namedUnderlying[mangle(e.curPkgPrefix, e.src(nodes[0].tok))]
	if !ok {
		return "", false
	}
	if u = e.underlyingCType(u); !e.isSliceCType(u) {
		return "", false
	}
	return sliceElemFromCName(u), true
}

// isNamedLitType reports whether a factor's children are a bare type name followed
// by a composite literal -- `Row{1, 2, 3}` rather than `[3]int{1, 2, 3}`.
func (e *emitter) isNamedLitType(kids []Node) bool {
	return len(kids) == 2 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == IDENT && kids[1].sym == CompositeLit
}

// litTypeName renders a literal's bracketed type for a diagnostic, as the source
// spells it rather than as C would.
func (e *emitter) litTypeName(litType []int32) string {
	if a, ok := e.arrayDim(litType); ok {
		return e.goArrayTypeName(a)
	}
	if elem, ok := e.sliceType(litType); ok {
		return "[]" + e.goTypeName(elem)
	}
	return "array or slice"
}

// soleArrayLit matches an initializer that is exactly an array or slice literal,
// "[N]T{...}" or "[]T{...}". The bracketed type the grammar already allows as a
// value (so "make([]int, n)" parses) carries the composite literal as its tail, so
// the Factor's own nodes are the type -- which is why arrayDim and sliceType, both
// of which read that shape, can be handed them unchanged.
func (e *emitter) soleArrayLit(initExpr []int32) (typeAST []int32, lit Node, ok bool) {
	fac, ok := e.soleFactorNode(initExpr)
	if !ok {
		return nil, Node{}, false
	}
	return e.factorArrayLit(fac)
}

// namedSliceLitType reports the C name of a literal's type when that type is a
// DEFINED slice type written by name, "List{1, 2, 3}" over "type List []int".
// litSliceType answers such a literal with its ELEMENT, which is what the backing
// array and the indexing need; this is the other half, the name a method hangs off.
func (e *emitter) namedSliceLitType(typeAST []int32) (string, bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) != 1 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != IDENT {
		return "", false
	}
	nm := mangle(e.curPkgPrefix, e.src(nodes[0].tok))
	if !e.isSliceCType(e.underlyingCType(nm)) {
		return "", false
	}
	return nm, true
}

// arrayLitElement matches a composite-literal element that fills an array-typed
// position: a written array literal, `[2]int{2, 3}`, or the type-elided form the
// position allows, `{2, 3}`, which arrives as a bare CompositeLit.
func (e *emitter) arrayLitElement(v Node) (Node, bool) {
	if v.sym == CompositeLit {
		return v, true
	}
	_, lit, ok := e.soleArrayLit(v.ast)
	return lit, ok
}

// factorArrayLit matches a Factor that is an array or slice literal: the bracketed
// type followed by a composite literal.
func (e *emitter) factorArrayLit(fac Node) (typeAST []int32, lit Node, ok bool) {
	kids := slices.Collect(it(fac.ast))
	// `Row{1, 2, 3}` for a defined array or slice type is the same literal as the
	// bracketed form it is defined over. The type is the bare name, which arrayDim
	// and sliceType both resolve, so the rest of the path needs no further help.
	if len(kids) == 2 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == IDENT && kids[1].sym == CompositeLit {
		name := []int32{kids[0].tok}
		if _, ok := e.arrayDim(name); ok {
			return name, kids[1], true
		}
		if _, ok := e.litSliceType(name); ok {
			return name, kids[1], true
		}
		return nil, Node{}, false
	}
	if len(kids) == 0 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LBRACK {
		return nil, Node{}, false
	}
	last := kids[len(kids)-1]
	if last.sym != CompositeLit {
		return nil, Node{}, false
	}
	// The Factor's own nodes are the bracketed type, so they are what arrayDim and
	// sliceType read; the trailing CompositeLit is not part of the type and both
	// ignore it, looking only for the length Expression and the element Type.
	return fac.ast, last, true
}

// litFieldValues returns a composite literal's values in field order, and the
// fields they belong to. A positional literal is in that order already, one value
// per field, and needs no field list. A keyed one is rewritten into it, with a nil
// for every field the literal omits, so that both forms emit through the one path
// above.
//
// The rewrite is why keyed literals do not become C designated initializers, which
// look like the obvious lowering: flexcc mishandles those. "(P){.n = 5}" skipping a
// struct-typed field fails with "Expected multiple values", and so does naming the
// fields out of declaration order. Written out positionally, a keyed literal is
// exactly as compilable as the positional one it is equivalent to.
func (e *emitter) litFieldValues(name string, lit Node) (values []*Node, fields []structField, ok bool) {
	elements := compositeLitElements(lit)
	if len(elements) == 0 || !elements[0].keyed {
		for i := range elements {
			values = append(values, &elements[i].value)
		}
		// Positional: pair each value with the field at its position, so a
		// type-elided element (`{1}`) can be emitted against that field's type.
		return values, e.structs[name], true
	}
	fields = e.structs[name]
	values = make([]*Node, len(fields))
	for i := range elements {
		el := &elements[i]
		key, ok := e.soleToken(el.key.ast)
		if !ok || e.f.ch(key) != IDENT {
			// The checker refuses these, so reaching here means one got past it.
			e.fail("a composite literal key must be a field name")
			return nil, nil, false
		}
		nm := e.src(key)
		at := slices.IndexFunc(fields, func(f structField) bool { return f.name == nm })
		if at < 0 {
			e.fail("unknown field %s in a %s literal", nm, name)
			return nil, nil, false
		}
		values[at] = &el.value
	}
	return values, fields, true
}

// emitVarInit emits a variable declaration's initializer. A composite literal that
// is the whole of one is emitted as a brace initializer; see emitCompositeLit.
//
// EXCEPT WHEN THE TYPE IS A SLICE, which a brace cannot fill. A slice is a header
// pointing at storage, so its literal has to put the elements somewhere first and
// point at that -- which is what emitExpr does. A NAMED slice type reached here and
// was brace-filled like a struct, writing the elements into the header's own fields:
// `var l List = List{10, 20, 30}` over `type List []int` gave a length of 20, a
// capacity of 30 and a data pointer of 10, so len(l) answered 20 and l[0] read
// whatever lives at address 10. The C compiler did say "mixing pointer and integer
// types in assignment", which is how it was found, but `ogo build` still wrote a
// binary and the program still ran.
//
// Only this spelling was wrong. `l := List{...}`, `var l = List{...}` with no type
// written, a literal as an argument and one at package scope all took other paths
// and were always right, which is why it survived: the form that names the type
// twice is the one nobody writes twice.
func (e *emitter) emitVarInit(initExpr []int32) {
	if name, lit, ok := e.soleCompositeLit(initExpr); ok && !e.isSliceCType(e.underlyingCType(name)) {
		e.emitCompositeLit(name, lit, true)
		return
	}
	e.emitExpr(initExpr)
}

// emitVarList emits a local `var a, b = e0, e1` (typed or inferred): each name is
// an independent declaration taking its own value, so this is the single-name path
// repeated. A declared array type is refused -- C cannot initialize an array from
// an expression, and copying one needs the single-name path's memcpy.
func (e *emitter) emitVarList(names []string, typeAST []int32, inits [][]int32) {
	if typeAST != nil {
		if _, ok := e.arrayDim(typeAST); ok {
			e.fail("a multi-name array var with an initializer is not supported yet")
			return
		}
	}
	for i, nm := range names {
		if nm == "_" {
			e.emitDiscard(inits[i]) // declares nothing; the value's effects still run
			continue
		}
		if typeAST == nil {
			e.emitInferredLocal(nm, inits[i])
			continue
		}
		ctype := e.cType(typeAST)
		if ctype == "" {
			return
		}
		if elem, ok := e.sliceType(typeAST); ok {
			e.sliceVars[nm] = elem
		}
		e.locals[nm] = ctype
		e.emitVarDeclInit(ctype, nm, inits[i])
	}
}

// emitPackageVarList is emitVarList for package scope: each name becomes its own
// file-scope static. A constant initializer is emitted in place; anything else is
// zero-initialized and assigned in the synthesized package init, exactly as the
// single-name path does.
func (e *emitter) emitPackageVarList(names []string, typeAST []int32, inits [][]int32) {
	for i, nm := range names {
		if nm == "_" {
			continue // a blank package variable declares nothing
		}
		ctype := ""
		switch {
		case typeAST != nil:
			ctype = e.cType(typeAST)
		default:
			var ok bool
			if ctype, ok = e.inferCType(inits[i]); !ok {
				e.fail("cannot infer a type for the package variable %q", nm)
				return
			}
		}
		if ctype == "" {
			return
		}
		gn := e.globalC(nm)
		e.globals[gn] = ctype
		if e.isSliceCType(ctype) {
			e.globalSliceVars[gn] = sliceElemFromCName(ctype)
		}
		// A package variable of interface type is zero until package init, where the
		// two words are written -- an address is not a C constant expression, so it
		// cannot be a file-scope initializer. What it points at has to be a package
		// variable too, this function's temporaries being locals of ogo_pkg_init.
		if e.isIfaceCType(ctype) && inits[i] != nil {
			e.emit("static " + ctype + " " + gn + " = " + e.zeroInitC(ctype) + ";\n")
			if stmt := e.ifaceStoreC(gn, ctype, inits[i]); stmt != "" {
				e.deferPkgInit(strings.TrimSuffix(stmt, "\n"))
			}
			continue
		}
		if e.staticInitOK(inits[i]) {
			e.emit("static " + ctype + " " + gn + " = ")
			e.emitGlobalInit(inits[i])
			e.emit(";\n")
			continue
		}
		e.emit("static " + ctype + " " + gn + " = " + e.zeroInitC(ctype) + ";\n")
		e.pkgInitAssign(gn, nm, inits[i])
	}
}

// emitPackageDestructure lowers a package-scope `var a, b = f()` that distributes a
// multi-result call across several package variables. It is emitDestructure split
// across two locations: C forbids a call in a file-scope initializer, so each
// variable is declared static and zero-initialized here, while the call binds to a
// temporary and each variable reads its field in the synthesized package init.
func (e *emitter) emitPackageDestructure(names []string, rhs []int32) {
	callee, suffix, ok := e.directCall(rhs)
	if !ok {
		e.fail("destructuring into package variables requires a single function call on the right-hand side")
		return
	}
	resTypes, ok := e.userFunc(callee)
	if !ok {
		e.fail("destructuring into package variables requires a call to a function, not %q", callee)
		return
	}
	if len(resTypes) != len(names) {
		e.fail("assignment mismatch: %d variables but %s returns %d values", len(names), callee, len(resTypes))
		return
	}
	call := e.captureC(func() { e.emitCallExpr(callee, suffix) })
	// An all-blank `var _, _ = f()` keeps the call for its side effects but binds
	// nothing, so no result temporary is emitted -- an unused one would warn.
	if !slices.ContainsFunc(names, func(nm string) bool { return nm != "_" }) {
		e.deferPkgInit(call + ";")
		return
	}
	for i, nm := range names {
		if nm == "_" {
			continue // a blank package variable declares nothing
		}
		gn := e.globalC(nm)
		e.globals[gn] = resTypes[i]
		if e.isSliceCType(resTypes[i]) {
			e.globalSliceVars[gn] = sliceElemFromCName(resTypes[i])
		}
		e.emit("static " + resTypes[i] + " " + gn + " = " + e.zeroInitC(resTypes[i]) + ";\n")
	}
	tmp := e.newTmp()
	e.deferPkgInit(e.retStructName(e.funcCallC(callee)) + " " + tmp + " = " + call + ";")
	for i, nm := range names {
		if nm == "_" {
			continue // its value is produced but bound to nothing
		}
		e.deferPkgInit(fmt.Sprintf("%s = %s._%d;", nm, tmp, i))
	}
}

// cType maps a Type subtree that names a single predeclared or struct type to its
// C type, recording any needed <stdint.h> include. A struct type's C name is the
// type name itself (its typedef); a pointer type "*T" maps to "<T>*". Other
// composite types (array, slice, channel, ...) are not modelled and fail honestly.
func (e *emitter) cType(ast []int32) string {
	nodes := slices.Collect(it(ast))
	// Pointer type: "*" Type -> "<elem>*".
	if len(nodes) == 2 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == MUL && nodes[1].sym == Type {
		// A pointer to an ARRAY, `*[3]int`. C spells one `int (*p)[3]` -- the name
		// in the middle of the declarator -- so the pointee takes the same generated
		// typedef a slice's array element does and the name comes back out in front
		// of it. A pointer to a typedef'd array is sound on this target; it is a
		// PARAMETER of one that is not (doc/array-param-corrupts.c), which is why
		// nothing here passes the array itself by value.
		if name, isArr := e.arrayElemTypedef(nodes[1].ast); isArr {
			return name + "*"
		}
		if elem := e.cType(nodes[1].ast); elem != "" {
			return elem + "*"
		}
		return ""
	}
	// Slice type: "[" "]" Type -> the ogo_slice_<elem> header.
	if elem, ok := e.sliceType(ast); ok {
		e.needSlice(elem)
		return sliceCName(elem)
	}
	// Channel type: "chan" Type -> the ogo_chan_<elem> rendezvous cell.
	if elem, ok := e.chanType(ast); ok {
		e.needChan(elem)
		return chanCName(elem)
	}
	// Function type: "func" Signature -> the typedef standing for that signature.
	if len(nodes) == 2 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == FUNC {
		name, _ := e.funcType(ast)
		return name
	}
	// An anonymous struct type, `struct{ x, y int }`, written where a type is
	// wanted rather than declared with a name of its own.
	if structAST := e.structTypeAST(ast); structAST != nil {
		return e.anonStructType(structAST)
	}
	// An interface type written out rather than declared with a name of its own,
	// `interface{ foo() }` and the empty `interface{}` that `any` stands for. It
	// gets a minted name, which is all the interface machinery ever wanted.
	//
	// The comment that stood here said this was refused BY NAME, and the code that
	// would have done so was not below it: the tokens carry no identifier, so the
	// generic refusal named the empty string -- "unsupported type" with nothing in
	// it -- which is what a reader of `var e interface{}` actually got.
	if ifaceAST := e.interfaceTypeAST(ast); ifaceAST != nil {
		return e.anonInterfaceType(ifaceAST)
	}
	// `any`, which is that empty interface spelled as a name. It reaches here as a
	// bare identifier and resolves to no declared type, the universe holding it
	// rather than any file -- so it is answered here, where the spelling it stands
	// for is answered.
	if tok, ok := e.soleToken(ast); ok && e.f.ch(tok) == IDENT && e.src(tok) == "any" && !e.typeNames[mangle(e.curPkgPrefix, "any")] {
		return e.anonInterfaceOf(nil)
	}

	var toks []int32
	nonTerminal := false
	for _, n := range nodes {
		if n.sym != 0 {
			nonTerminal = true // a StructType body, etc.
			continue
		}
		toks = append(toks, n.tok)
	}
	// A qualified type "pkg.T" -> the imported package's mangled typedef, matching
	// how collectTypeDecl named it while emitting that package's files.
	if len(toks) == 3 && e.f.ch(toks[0]) == IDENT && e.f.ch(toks[1]) == PERIOD && e.f.ch(toks[2]) == IDENT {
		if prefix, ok := e.importQualifiers[e.src(toks[0])]; ok {
			mn := mangle(prefix, e.src(toks[2]))
			if _, ok := e.structs[mn]; ok {
				return mn
			}
			if e.namedTypes[mn] {
				return mn
			}
		}
	}
	// A simple named type is exactly one IDENT token; anything else -- a pointer
	// "*T", an array "[N]T", a slice, a channel, a qualified name -- carries extra
	// tokens or nodes and is not modelled yet.
	name := ""
	if len(toks) == 1 && e.f.ch(toks[0]) == IDENT {
		name = e.src(toks[0])
	}
	if nonTerminal || name == "" {
		for _, t := range toks {
			if e.f.ch(t) == IDENT {
				name = e.src(t)
			}
		}
		e.fail("unsupported type %q", name)
		return ""
	}
	if ct, ok := cTypes[name]; ok {
		if strings.HasSuffix(ct, "_t") {
			e.includes["stdint.h"] = true
		}
		if ct == cString {
			e.usesString = true
		}
		if name == "Builder" {
			// Naming the type is enough to need its typedef and helpers: they used to
			// be pulled in only by a NewBuilder call, so `var b Builder` -- a zero
			// Builder, writing nowhere until it is given a backing -- emitted a
			// variable of a type nothing declared.
			e.needBuilder()
		}
		return ct
	}
	// A user type resolves to its mangled C name, matching how collectTypeDecl
	// recorded it and named its typedef; the mangled string is then the key for
	// every downstream structs/namedTypes lookup.
	mn := mangle(e.curPkgPrefix, name)
	if _, ok := e.structs[mn]; ok {
		return mn
	}
	if e.namedTypes[mn] {
		return mn
	}
	e.fail("unsupported type %q", name)
	return ""
}

// underlyingCType resolves a named type to the C type it stands for, following a
// chain of them ("type A B; type B int" -> int). Anything else is returned as it is.
//
// A named type is a distinct type to the checker but the same representation to C,
// and every decision about how a value is REPRESENTED -- how it prints, whether it
// carries a length, whether it is a bool word -- has to see through the name. Only
// the decisions about identity (which method set, what to call the C variable) use
// the name itself.
func (e *emitter) underlyingCType(ctype string) string {
	// Bounded rather than cycle-tracked: a type cycle is refused by the checker
	// long before this runs, and the bound costs nothing.
	for range 16 {
		u, ok := e.namedUnderlying[ctype]
		if !ok {
			return ctype
		}
		ctype = u
	}
	return ctype
}

// exprReprCType is inferCType with the name resolved away: the C type that decides
// how a value is represented, rather than the one it is declared as.
func (e *emitter) exprReprCType(ast []int32) (string, bool) {
	ct, ok := e.inferCType(ast)
	if !ok {
		return "", false
	}
	return e.underlyingCType(ct), true
}

// isUserType reports whether a C type name denotes a user-defined type that may
// carry methods -- a struct or a non-struct named type -- as opposed to a
// predeclared type or an imported package qualifier.
func (e *emitter) isUserType(ctype string) bool { return e.isStruct(ctype) || e.namedTypes[ctype] }

// isMethodBase reports whether a C type name can carry a method set. It is
// isUserType plus a defined ARRAY type, which takes a typedef of its own and so
// never reaches the namedTypes registry the other two are in. A MINTED array name
// lands here too and costs nothing: it has no methods, so the funcRet lookup beside
// this one comes up empty and the call is not read as a dispatch.
func (e *emitter) isMethodBase(ctype string) bool {
	if e.isUserType(ctype) {
		return true
	}
	_, isArr := e.namedArrays[ctype]
	return isArr
}

// convType reports the C type of a conversion `T(x)` when recv names a type usable
// in one: a predeclared numeric type, or a named type over such a type. A cast
// `(T)(x)` expresses it. bool and string are not numeric conversions -- bool has no
// arithmetic source and a string conversion would need a copy -- so they are left
// to the generic call path, which fails honestly.
func (e *emitter) convType(recv string) (string, bool) {
	if recv == "bool" {
		return cBool, true
	}
	if recv == "string" {
		return cString, true
	}
	if ct, ok := cTypes[recv]; ok {
		if strings.HasSuffix(ct, "_t") {
			e.includes["stdint.h"] = true // a fixed-width target needs its header
		}
		return ct, true // int, uint, byte, rune, the fixed-width names
	}
	// `any(x)`, the empty interface spelled as a name. It is a conversion like any
	// other interface's, and the only one whose name is not a declaration -- the
	// universe holds it -- so it is answered here rather than found in a registry,
	// exactly as cType answers it. Guarded on nothing having declared that name,
	// which is what makes it the universe's and not the program's.
	if recv == "any" && !e.typeNames[mangle(e.curPkgPrefix, "any")] {
		return e.anonInterfaceOf(nil), true
	}
	mn := mangle(e.curPkgPrefix, recv)
	if e.namedTypes[mn] {
		return mn, true // `type Celsius int` used as Celsius(x)
	}
	// A struct type names a conversion too: `Point(n)` for a Named defined over
	// Point, which is how a value comes back from the defined type. Only the name
	// changes -- the representation is the same struct -- so it is the cast C
	// already writes for the other direction.
	if _, ok := e.structs[mn]; ok {
		return mn, true
	}
	// A defined ARRAY type, `row(a)` for `type row [3]int`. It was the one kind of
	// defined type whose name did not name a conversion, so the name reached the
	// output as though it were a function and the C compiler reported a syntax
	// error about generated code.
	if _, ok := e.namedArrays[mn]; ok {
		return mn, true
	}
	return "", false
}

// qualConvType is convType for a QUALIFIED name, `geo.Celsius(20)`. A conversion
// spelled that way was refused for every type -- not just an interface -- because
// convType takes one identifier and the call machinery, finding an import qualifier,
// went looking for a function of that name: the declaration reported "cannot infer a
// type" and the emitted call named a C function the program never defines.
//
// The lookup is cType's qualified branch, which has answered this question for a type
// POSITION all along; only the conversion position was never given it.
func (e *emitter) qualConvType(qualifier, name string) (string, bool) {
	prefix, ok := e.importQualifiers[qualifier]
	if !ok {
		return "", false
	}
	mn := mangle(prefix, name)
	if _, isArr := e.namedArrays[mn]; isArr || e.namedTypes[mn] || e.isStruct(mn) {
		return mn, true
	}
	return "", false
}

// convChainHead recognises a conversion opening an access chain and says which type
// it makes and how many steps it consumes. The unqualified `C(5).twice()` is a call
// of the type's own name and takes one step; the qualified `geo.Celsius(20).Double()`
// is a selector naming the type and then the call, and takes two. Either leaves a
// value of that type, which the steps after it walk like any other.
//
// The qualified form is asked first because both shapes start with an identifier that
// is not a variable: the unqualified test would read `geo` as a type name and answer
// no, which is right, but only by accident of the order.
func (e *emitter) convChainHead(base string, steps []Node) (ct string, used int, ok bool) {
	if len(steps) >= 2 && steps[0].sym == Selector && steps[1].sym == CallSuffix {
		if ct, ok := e.qualConvType(base, e.soleIdent(steps[0].ast)); ok {
			return ct, 2, true
		}
	}
	if len(steps) >= 1 && steps[0].sym == CallSuffix {
		if ct, ok := e.convType(base); ok {
			return ct, 1, true
		}
	}
	return "", 0, false
}

// arrayConvOperand recognises a conversion to a defined ARRAY type -- `row(a)`, or
// the qualified `geo.Row(a)` -- and returns the operand. Such a conversion is the
// operand itself: the two types have one representation, so there is nothing to
// convert. Reading only the unqualified shape is what made `geo.Row(a)` copy one
// element and leave the rest garbage, the generic path treating it as a scalar.
func (e *emitter) arrayConvOperand(ast []int32) ([]int32, bool) {
	recv, suffix, ok := e.directCall(ast)
	if !ok {
		return nil, false
	}
	ct, used, isConv := e.convChainHead(recv, suffix)
	if !isConv || used != len(suffix) {
		return nil, false
	}
	if _, isArray := e.namedArrays[ct]; !isArray {
		return nil, false
	}
	args := e.callArgExprs(suffix[used-1].ast)
	if len(args) != 1 {
		return nil, false
	}
	return args[0].ast, true
}

// emitConversion emits a conversion `T(x)`.
//
// A scalar target is a C cast, which is what makes a narrowing one truncate as Go
// says. A target that is not scalar -- a string, a slice, a struct, or a named type
// over one of them -- cannot be cast in C at all (a cast names a scalar type), and
// needs no conversion either: a named type is a typedef of what it stands for, so
// the value is already of the target's representation and the operand alone is the
// conversion. What is left is a conversion that would have to BUILD a value --
// string(rune), string([]byte) -- which needs the allocation this target does not
// have, and is refused.
func (e *emitter) emitConversion(ct string, arg Node) {
	if isScalarCType(e.underlyingCType(ct)) {
		// The target's C compiler miscompiles a cast to a 64-bit type applied to a
		// 64-bit EXPRESSION, yielding a value that varies from run to run; the same
		// cast of a variable is right. So the operand is bound to one first. Only
		// the board shows this -- gcc computes either form correctly.
		if src, ok := e.exprReprCType(arg.ast); ok && cIntWidths[e.underlyingCType(ct)] == 64 && cIntWidths[src] == 64 {
			if _, isName := e.exprIdent(arg.ast); !isName {
				e.emit("(" + ct + ")" + e.hoist(src, func() { e.emitExpr(arg.ast) }))
				return
			}
		}
		// A COMPOUND LITERAL inside a cast is the second thing that target's C
		// compiler cannot do. `(int)(f((S){1, 2, 3}))` warns "Bad number of
		// parameters in call to f: expected 3 found 1" and generates a call that
		// does not pass the value; through a function pointer, or with a slice
		// header for the literal, it refuses the program outright, and
		// `(int)((S){1, 2, 3}.a)` crashes it. The literal alone is fine, the cast
		// alone is fine. Binding the operand to a temporary puts the literal outside
		// the cast, which is all it takes.
		//
		// The test is on the emitted text rather than on the source shape: a "){" in
		// an expression is a compound literal and nothing else, and it catches every
		// way one can get there -- a slice expression handed to a call, a composite
		// literal argument, or one standing on its own. `int(total(xs[:]))` is the
		// ordinary spelling that hits it.
		text := e.captureC(func() { e.emitExpr(arg.ast) })
		if strings.Contains(text, "){") {
			if src, ok := e.exprReprCType(arg.ast); ok && src != "" {
				e.emit("(" + ct + ")" + e.hoist(src, func() { e.emit(text) }))
				return
			}
		}
		e.emit("(" + ct + ")(" + text + ")")
		return
	}
	if _, isArray := e.namedArrays[ct]; isArray {
		// A defined array type and its underlying are one representation, so the
		// conversion is the operand. Without this the type NAME reached the output
		// as though it were a function, and the C compiler reported a syntax error
		// about generated code.
		e.emitExpr(arg.ast)
		return
	}
	// A conversion to an INTERFACE type -- `Shape(&q)`, or the `any(x)` that spells
	// the empty one -- builds the two words exactly as an assignment to a variable
	// of that interface does: the target names the interface, the operand names the
	// concrete type, and the pair chooses the table. Without it the conversion fell
	// through to the representation test below, where a `Quad*` operand does not
	// match the interface struct, and every position was refused ("cannot convert to
	// Shape") though Go accepts them all. A source that does not implement the
	// target is reported by the table lookup, which is where the same mistake
	// written as an assignment is reported.
	if e.isIfaceCType(ct) {
		if text, ok := e.ifaceValueC(ct, arg.ast); ok {
			e.emit(text)
			return
		}
		// The generic "cannot convert to Shape" is no help here: what is wrong is
		// almost always that a VALUE was written where the interface takes a
		// pointer, which the assignment form says in those words. A conversion is
		// how a program gets past the checker's rule -- it checks assignments, not
		// conversions -- so the same mistake arrives here and deserves the same
		// answer. Left alone if the table lookup already named a missing method,
		// which is the other way in.
		if e.err == nil {
			e.fail("cannot convert to %s: an interface holds a pointer here, so write "+
				"the address of a variable, or &T{...}", e.goTypeName(ct))
		}
		return
	}
	src, ok := e.exprReprCType(arg.ast)
	if !ok || src != e.underlyingCType(ct) {
		if e.underlyingCType(ct) == cString {
			// A CONSTANT operand converts at COMPILE TIME: Go makes string('A') a
			// constant string, and there is nothing to allocate or copy. The
			// refusal below is about the runtime conversion, which does need
			// storage this target has no way to choose; it had been refusing both,
			// so `println(string('A'))` was an error about allocation for an
			// expression that allocates nothing.
			if v, isConst := e.constIntValue(arg.ast); isConst {
				e.emitFoldedString(runeString(v))
				return
			}
			e.fail("a string conversion needs allocation, which the target does not have")
			return
		}
		// Two DISTINCT struct types, `B(a)`. Reached only when the representations
		// differ, so the same-representation case -- a defined type over the struct,
		// either direction -- is already answered above and this is the other one:
		// two names for one shape, neither defined over the other, which Go converts
		// between and this refused as "cannot convert to B".
		if ok && e.isStruct(src) && e.isStruct(e.underlyingCType(ct)) {
			e.emitStructConv(ct, src, arg)
			return
		}
		e.fail("cannot convert to %s", ct)
		return
	}
	e.emitExpr(arg.ast)
}

// emitStructConv emits a conversion between two struct types of different C names,
// `B(a)`. Go allows one exactly when the underlying types are IDENTICAL -- the same
// fields, in the same order, with the same names and types -- and the two are then
// one shape under two names, so nothing about the value changes.
//
// C has no cast between struct types, so it is a COPY: a temporary of the target's
// type filled from the operand's bytes. memcpy rather than a memberwise literal
// because identical layouts is exactly what makes copying the bytes right, and
// because it is the one form flexcc lowers for a struct holding an array -- a
// memberwise literal cannot initialize an array member from another array at all.
func (e *emitter) emitStructConv(ct, src string, arg Node) {
	target := e.underlyingCType(ct)
	if !e.sameStructLayout(target, src) {
		// Go's own wording, so what a reader knows from Go carries over. Reported
		// here rather than by the checker because the field lists the answer needs
		// are the emitter's.
		e.fail("cannot convert %s (variable of struct type %s) to type %s",
			e.f.exprSource(arg), e.goTypeName(src), e.goTypeName(ct))
		return
	}
	if e.pkgScope {
		// A package variable's initializer has no frame to put the temporary in, and
		// C evaluates a file-scope initializer before any statement runs.
		e.fail("a package variable cannot be initialized by a conversion between struct types: " +
			"it is a copy, and there is no frame here to make it in; assign it in a function")
		return
	}
	from := ""
	if name, isName := e.exprIdent(arg.ast); isName {
		from = e.varRef(name)
	} else {
		// Evaluated once, and addressable: memcpy takes the address of its source,
		// which a call's result does not have.
		from = e.hoist(src, func() { e.emitExpr(arg.ast) })
	}
	tmp := e.newTmp()
	e.includes["string.h"] = true
	e.prologue = append(e.prologue, target+" "+tmp+";\n")
	e.prologue = append(e.prologue, "memcpy(&"+tmp+", &"+from+", sizeof("+target+"));\n")
	e.locals[tmp] = ct
	e.emit(tmp)
}

// sameStructLayout reports whether two struct C types have identical layouts in Go's
// sense: the same fields, in the same order, each with the same name and the same
// type. That is what makes a conversion between them legal, and it is compared by
// the field's C type NAME -- two structurally alike fields of different named types
// are different types in Go too, so string equality is the right test rather than a
// weaker one.
func (e *emitter) sameStructLayout(a, b string) bool {
	fa, okA := e.structs[a]
	fb, okB := e.structs[b]
	if !okA || !okB || len(fa) != len(fb) {
		return false
	}
	for i := range fa {
		x, y := fa[i], fb[i]
		switch {
		case x.name != y.name, x.ctype != y.ctype, x.embedded != y.embedded:
			return false
		case x.dim.bound != y.dim.bound, x.dim.name != y.dim.name:
			return false
		case !slices.Equal(x.dim.inner, y.dim.inner):
			return false
		}
	}
	return true
}

// isScalarCType reports whether a C type is one a cast may name: the arithmetic
// types and a pointer. A string, a slice, a channel and a struct are not, C having
// no cast to a non-scalar type.
func isScalarCType(ct string) bool {
	switch {
	case ct == cBool, ct == "double", ct == "float":
		return true
	case isIntCType(ct):
		return true
	case strings.HasSuffix(ct, "*"):
		return true
	}
	return false
}

// arrayDim recognises a fixed array type `[N]T`, including a multi-dimensional
// `[N][M]T`, returning its element type and every extent. cType models no array
// type, so a nested element is resolved by recursing here rather than through it
// -- which is why `[2][3]int` used to fail as `unsupported type ""`.
func (e *emitter) arrayDim(typeAST []int32) (arrDim, bool) {
	nodes := slices.Collect(it(typeAST))
	// A named array type stands in for its dimensions: `type Row [3]int` used as
	// `var r Row` resolves here so every array site (declaration, indexing, len,
	// a struct field, a parameter) treats r as its `[3]int`. Only a bare name that
	// was declared as an array type matches -- `type Celsius int` is not here.
	if len(nodes) == 1 && nodes[0].sym == 0 && e.f.ch(nodes[0].tok) == IDENT {
		nm := mangle(e.curPkgPrefix, e.src(nodes[0].tok))
		a, ok := e.namedArrays[nm]
		if ok {
			a.name = nm // resolved away everywhere else; kept here for the method set
		}
		return a, ok
	}
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != LBRACK {
		return arrDim{}, false
	}
	var sizeAST, elemAST []int32
	for _, n := range nodes {
		switch n.sym {
		case Expression:
			sizeAST = n.ast
		case Type:
			elemAST = n.ast
		}
	}
	if sizeAST == nil || elemAST == nil {
		return arrDim{}, false // a slice, or a malformed array
	}
	bound, ok := e.arrayBoundC(sizeAST)
	if !ok {
		return arrDim{}, false
	}
	if inner, ok := e.arrayDim(elemAST); ok {
		// The element's own name, when it has one -- `[2]Row` -- and otherwise
		// whatever the element carried for ITS element, so a rank above two keeps it
		// too: the elements of a `[2][2]Row` are `[2]Row`s, which are not named, but
		// two indexes in there is still a Row.
		en, ed := inner.name, inner.dims()
		if en == "" {
			en, ed = inner.elemName, inner.elemDims
		}
		return arrDim{elem: inner.elem, bound: bound, inner: inner.bounds(), elemName: en, elemDims: ed}, true
	}
	elem := e.cType(elemAST)
	if elem == "" {
		return arrDim{}, false
	}
	return arrDim{elem: elem, bound: bound}, true
}

// sliceType recognises a slice type `[]T`, returning its element C type. It is a
// bracketed type with no length -- as opposed to a fixed array `[N]T`, which
// carries a size expression (handled by arrayType).
func (e *emitter) sliceType(typeAST []int32) (elem string, ok bool) {
	nodes := slices.Collect(it(typeAST))
	if len(nodes) == 0 || nodes[0].sym != 0 || e.f.ch(nodes[0].tok) != LBRACK {
		return "", false
	}
	var elemAST []int32
	for _, n := range nodes {
		switch n.sym {
		case Expression:
			return "", false // a sized array, not a slice
		case Type:
			elemAST = n.ast
		}
	}
	if elemAST == nil {
		return "", false
	}
	// An ARRAY element, `[][2]int`: C cannot spell one inline where the header's
	// pointer goes -- `int (*ptr)[2]` puts the name in the middle of the declarator --
	// so a typedef moves it out of the way, the same move a function pointer needs.
	// The helpers that would take such an element BY VALUE take a pointer instead;
	// see the append helper, and doc/array-param-corrupts.c for why.
	if name, isArr := e.arrayElemTypedef(elemAST); isArr {
		return name, true
	}
	if elem = e.cType(elemAST); elem == "" {
		return "", false
	}
	return elem, true
}

// arrayElemTypedef mints (or reuses) the typedef standing for an ARRAY used as an
// element type, `typedef int ogo_arr_2_int[2];`, and registers it as a named array
// so indexing a value of it reads through the same path a defined array type does.
//
// Keyed by the shape rather than by where it was written, so `[][2]int` written
// twice mints once -- the same rule anonStructType and the result structs follow.
func (e *emitter) arrayElemTypedef(typeAST []int32) (string, bool) {
	a, ok := e.arrayDim(typeAST)
	if !ok {
		return "", false
	}
	return e.arrayTypedef(a), true
}

// arrayTypedef is arrayElemTypedef from the extents themselves, for a caller that
// has already resolved them -- the address of an array variable, whose type is
// known from the variable rather than from a written one.
func (e *emitter) arrayTypedef(a arrDim) string {
	// A DEFINED array type already HAS a C name and a typedef of its own, so it
	// stands for itself. Minting a second one emitted `typedef int ogo_arr_2_int[2]`
	// beside `typedef int Row[2]` for the same type, and named a method on Row after
	// the mint -- `ogo_arr_2_int_set` -- which no call site would ever look for.
	if a.name != "" {
		return a.name
	}
	name := "ogo_arr"
	for _, b := range a.bounds() {
		name += "_" + cTypeIdent(b)
	}
	name += "_" + cTypeIdent(a.elem)
	if _, seen := e.namedArrays[name]; !seen {
		e.namedArrays[name] = a
		e.typeNames[name] = true
		e.addTypedef(name, "typedef "+a.elem+" "+name+a.declSuffix()+";\n", a.elem)
	}
	return name
}

// arrayPtrCType reports the array a C pointer type points at, `ogo_arr_3_int*` ->
// the [3]int it stands for. A pointer to an array is the one pointer an index
// applies to, Go's `p[i]` there meaning `(*p)[i]`, and this is what tells it from
// every other pointer.
//
// The pointee's typedef is the registry: `namedArrays` holds both the arrays a
// program defined a name for and the ones the compiler minted a name for, and a
// pointer to either behaves the same way.
func (e *emitter) arrayPtrCType(ctype string) (arrDim, bool) {
	if !strings.HasSuffix(ctype, "*") {
		return arrDim{}, false
	}
	a, ok := e.namedArrays[strings.TrimSuffix(ctype, "*")]
	return a, ok
}

// arrayPtrVar reports the array a pointer VARIABLE points at.
func (e *emitter) arrayPtrVar(name string) (arrDim, bool) {
	ct, ok := e.varType(name)
	if !ok {
		return arrDim{}, false
	}
	return e.arrayPtrCType(ct)
}

// indexableBase reports whether a base that is not an array, a slice or a string
// may be indexed, refusing it by name when it is not. It backs the checker's own
// refusal (indexingPointer) at the one place C would not: an index on a pointer is
// pointer arithmetic there, so an unrefused one compiles and reads storage the
// program never named. The checker refuses what its type model can prove; this
// refuses what reaches here anyway, which is a pointer to a type it could not
// resolve -- a slice, a channel, a function value.
func (e *emitter) indexableBase(base string) bool {
	ct, ok := e.varType(base)
	if !ok || !e.isPointer(ct) {
		return true
	}
	e.fail("cannot index %s: only a pointer to an array is indexable", base)
	return false
}

// arrayBase names the storage an array-valued base denotes and its extents: an
// array variable is its own name, and a POINTER to an array is that name
// dereferenced, since Go's `p[i]`, `len(p)` and `range p` all mean the array.
//
// It is the one place the dereference is written. Every path that indexes,
// assigns through, measures, ranges or slices an array base goes through it, so a
// pointer reaches each of them as the array it points at rather than as a pointer
// each would have to know about separately.
func (e *emitter) arrayBase(name string) (string, arrDim, bool) {
	if a, ok := e.arrayVar(name); ok {
		return e.varRef(name), a, true
	}
	if a, ok := e.arrayPtrVar(name); ok {
		return "(*" + e.nilCheckedPtrVar(name) + ")", a, true
	}
	return "", arrDim{}, false
}

// arrayBoundC renders a fixed-array bound as a C integer constant: a single
// integer literal directly, or a single integer-constant name folded to its value
// (flexcc rejects a `static const` as an array bound). Anything else is unmodelled.
func (e *emitter) arrayBoundC(sizeAST []int32) (string, bool) {
	if tok, ok := e.soleToken(sizeAST); ok {
		switch e.f.ch(tok) {
		case INT:
			return normalizeIntLit(e.src(tok)), true
		case IDENT:
			if v, ok := e.foldedInt(e.src(tok)); ok {
				return v, true
			}
		}
	}
	// Not a bare literal or named constant: a constant expression like "[2+1]int"
	// or "[W*H]int". Fold it -- C cannot use a `const int` (or a nested const) as an
	// array bound, so the value must be spelled as a literal.
	if v, ok := e.constIntValue(sizeAST); ok && v >= 0 {
		return strconv.FormatInt(v, 10), true
	}
	return "", false
}

// foldConstInt folds a constant integer expression -- integer literals, integer
// constants, and the arithmetic and bitwise operators over them -- to its value.
// It mirrors the grammar levels emitExprNode walks: SimpleExpr and Term are flat,
// left-associative operand/operator lists and precedence lives in the nesting, so
// the result matches the C the emitter emits for the same expression. It reports
// ok=false for a non-constant operand, a comparison or logical operator (not an
// integer), or an operator it does not fold, leaving the caller its prior behavior.
func (e *emitter) foldConstInt(ast []int32) (int64, bool) {
	return e.foldIntSeq(slices.Collect(it(ast)))
}

// fitsCInt reports whether a constant value fits C's int, which is 32 bits on this
// target. One that does not may not be written as a plain decimal: C would compute
// and store it in int.
func fitsCInt(v int64) bool { return v >= math.MinInt32 && v <= math.MaxInt32 }

// intCLit renders a folded integer constant as a C literal, widening the ones that
// do not fit an int. Go computes a constant expression in arbitrary precision and
// then converts; C computes it in the type of its operands, so "1 << 40" written out
// as C source is a shift of an int by 40 -- undefined, and 0 in practice. The folded
// value with a width suffix is what the expression actually denotes.
//
// A negative wide value is spelled as its bit pattern rather than as a negation.
// The target's C compiler folds no unary minus in a global initializer at all
// ("global initializers ... must be constant" on "-1099511627776LL"), and the most
// negative value has no negation that is a literal in any C: 9223372036854775808
// does not fit the signed type the minus would apply to. The hex form says the value
// exactly, on a two's-complement target, and folds wherever a literal does.
// emitComplement writes the bitwise complement of an expression: `-1 ^ (x)`, with
// the -1 cast to the operand's own type, rather than C's `~(x)`.
//
// The two mean the same thing on a two's-complement target -- C defines ~ as XOR
// with all ones -- and the cast keeps the operand's type, so the usual arithmetic
// conversions land where they did before. The long spelling routes around a
// backend bug: flexcc miscompiles
//
//	x = <anything> & ~x
//
// -- an AND with the complement of the very variable being assigned -- to a
// constant 0, whatever the operands are. It computes the left operand into the
// destination register and then reads that register back as the complemented
// operand, so what it evaluates is `A & ~A`. Found by the smith oracle running on
// a real P2 (seed 10), where `a[0] = 96 &^ a[0]` gave 0 instead of 96 while the
// host C compiler gave the right answer.
//
// A local, a parameter, a struct field and an array element indexed by a constant
// all reproduce it; an array element indexed by a variable, and a package-level
// variable, do not. The whole family is emitted the long way regardless: the
// distinction is the backend's, not the language's.
//
// The cast is not cosmetic either: a bare `-1 ^ (x)` would widen an unsigned
// operand's expression to int, which is not what C's ~ does.
// factorKids descends an expression through its single-child levels to the
// children of the Factor it consists of, for a caller that matches a Factor shape
// (factorCall, factorFieldAccess). An expression that is not a single factor
// yields whatever level it stops at, which those matchers then reject.
// unparenKids reduces a Factor written `(x) suffix` to the kids of `x suffix`, so
// every recogniser below sees the shape it already knows. `(a)[1]`, `(s).v` and
// `(dbl)(21)` are ordinary Go and mean exactly what the unparenthesised forms mean,
// and a parenthesised TYPE is how a conversion the LL(1) grammar cannot spell
// directly is written -- `([]byte)(s)`, `([3]int)(q)`. See "Parentheses where the
// parser needs them" in specs.go.
//
// Only a parenthesised expression with NO suffix of its own is unwrapped. Splicing
// two suffix runs together -- `(a[0])[1]` -- would hand the recognisers a shape with
// two FactorSuffix nodes, which none of them match, so that keeps its refusal rather
// than becoming a silently different tree.
// unparenKids peels every layer, `((a))[1]` as readily as `(a)[1]`: one call removes
// one pair, and the result of removing one is the shape the next call matches.
func (e *emitter) unparenKids(kids []Node) []Node {
	// Peeling `((a))[1]` leaves `(a)[1]`, which has the same NUMBER of nodes, so the
	// peel reports whether it did anything rather than being compared by shape.
	for {
		next, peeled := e.unparenKidsOnce(kids)
		if !peeled {
			return next
		}
		kids = next
	}
}

func (e *emitter) unparenKidsOnce(kids []Node) ([]Node, bool) {
	if len(kids) != 4 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[3].sym != FactorSuffix {
		return kids, false
	}
	inner := e.factorKids(kids[1].ast)
	if len(inner) == 0 {
		return kids, false
	}
	// The inner factor must carry no suffix of its own.
	for _, k := range inner {
		if k.sym == FactorSuffix || k.sym == CompositeLit {
			return kids, false
		}
	}
	// A leading UNARY OPERATOR makes the parentheses load-bearing, and peeling them
	// changes what the program says: `(*p).x` is not `*p.x`, which Go reads as
	// `*(p.x)`, and `(<-ch).x` is not `<-ch.x`. The peel used to take them anyway,
	// leaving a node sequence no case matched -- which is why every `(*p).x` failed
	// with "unsupported expression node FactorSuffix", naming a node the source does
	// not contain. factorDerefChain handles the shape this now leaves alone.
	if inner[0].sym == UnaryOp {
		return kids, false
	}
	// A parenthesised composite TYPE is a conversion, and factorBracketConv reads it
	// from the unreduced node -- the type it needs is the inner factor's own AST,
	// which splicing the nodes out here would lose.
	if inner[0].sym == 0 && e.f.ch(inner[0].tok) == LBRACK {
		return kids, false
	}
	return append(slices.Clone(inner), kids[3]), true
}

func (e *emitter) factorKids(ast []int32) []Node {
	kids := slices.Collect(it(ast))
	for len(kids) == 1 && kids[0].sym != 0 {
		kids = slices.Collect(it(kids[0].ast))
	}
	return kids
}

// constExpr reports whether an expression is a compile-time constant. It sees
// through a conversion of one, `uint32(0xFF00FF00)`, which foldConstInt does not:
// the fold works on operator sequences, and a conversion is a call shape.
func (e *emitter) constExpr(ast []int32) bool {
	if _, ok := e.foldConstInt(ast); ok {
		return true
	}
	recv, suffix, ok := e.factorCall(e.factorKids(ast))
	if !ok || len(suffix) != 1 {
		return false
	}
	if _, isConv := e.convType(recv); !isConv {
		return false
	}
	args := e.callArgExprs(suffix[0].ast)
	return len(args) == 1 && e.constExpr(args[0].ast)
}

func (e *emitter) emitComplement(ast []int32, ct string, emitOperand func()) {
	// A constant operand is never the destination of the assignment it sits in, so
	// the bug below cannot reach it and it keeps the short spelling. That is not
	// only tidiness: written the long way, a wide constant made flexcc type the
	// whole expression 64 bits wide and then REFUSE the printf it fed, so the
	// workaround for a miscompile broke a build that had worked.
	if e.constExpr(ast) {
		e.emit("~(")
		emitOperand()
		e.emit(")")
		return
	}
	ones := "-1" // an unknown type keeps the plain int -1, which is what ~ promotes to
	if ct != "" {
		ones = "(" + ct + ")-1"
	}
	e.emit("(" + ones + " ^ (")
	emitOperand()
	e.emit("))")
}

func intCLit(v int64) string {
	switch {
	case fitsCInt(v):
		return strconv.FormatInt(v, 10)
	case v > 0 && v <= math.MaxUint32:
		// It does not fit a signed int but does fit an UNSIGNED one, which is 32
		// bits on this target, so a U suffix is both exact and keeps the constant
		// -- and the expression around it -- 32 bits wide.
		//
		// Spelling it LL instead made "m ^ 0xFFFFFFFF" for a uint32 m a long long,
		// and the target's C compiler REFUSES the printf that feeds: "Bad number of
		// parameters in call to _basic_print_unsigned: expected 4 found 5", a
		// 64-bit argument taking two slots where %u wants one. gcc accepts the same
		// C, so nothing off-target saw it. The comment on constIntValue below
		// describes this same hazard reached through a conversion.
		return strconv.FormatUint(uint64(v), 10) + "U"
	case v >= 0:
		return strconv.FormatInt(v, 10) + "LL"
	default:
		return fmt.Sprintf("0x%016XULL", uint64(v))
	}
}

// constIntValue is foldConstInt for a caller that wants a constant's VALUE rather
// than a rendering of it: it also sees through a conversion, `int32(4)`, whose
// value is the operand's -- the checker has already range-checked it against the
// target.
//
// The fold used for EMISSION deliberately does not do this. A conversion's cast is
// part of the emitted expression's type, and dropping it retypes what surrounds it:
// `u &^ uint32(0xFF00FF00)` written without the cast is a long long to the target's
// C compiler, which then refuses the printf it feeds.
func (e *emitter) constIntValue(ast []int32) (int64, bool) {
	prev := e.foldConv
	e.foldConv = true
	defer func() { e.foldConv = prev }()
	return e.foldConstInt(ast)
}

// convFold folds `T(x)` to x's value when T is an integer type, for the value-only
// walk constIntValue turns on. Being part of the walk rather than wrapped around
// it, a conversion nested anywhere in the expression is reached -- which is what
// `const one = int32(1) << 16` needs.
func (e *emitter) convFold(kids []Node) (int64, bool) {
	recv, suffix, ok := e.factorCall(kids)
	if !ok || len(suffix) != 1 {
		return 0, false
	}
	ct, isConv := e.convType(recv)
	if !isConv {
		return 0, false
	}
	if _, isInt := cIntWidths[e.underlyingCType(ct)]; !isInt {
		return 0, false
	}
	args := e.callArgExprs(suffix[0].ast)
	if len(args) != 1 {
		return 0, false
	}
	return e.foldConstInt(args[0].ast)
}

// wideConstLit renders an expression that folds to a constant too wide for a C int,
// or reports false for anything else -- an expression that does not fold, or one
// whose value C computes the same way Go does, which is left as written.
func (e *emitter) wideConstLit(ast []int32) (string, bool) {
	v, ok := e.foldConstInt(ast)
	if !ok || fitsCInt(v) {
		return "", false
	}
	return intCLit(v), true
}

// foldIntSeq folds a flat "operand (op operand)*" list left-associatively.
func (e *emitter) foldIntSeq(kids []Node) (int64, bool) {
	if len(kids) == 0 {
		return 0, false
	}
	acc, ok := e.foldIntNode(kids[0])
	if !ok {
		return 0, false
	}
	for i := 1; i+1 < len(kids); i += 2 {
		op := kids[i]
		if op.sym != AddOp && op.sym != MulOp {
			return 0, false // a RelOp (comparison or logical) is not an integer
		}
		rhs, ok := e.foldIntNode(kids[i+1])
		if !ok {
			return 0, false
		}
		if acc, ok = foldIntOp(acc, e.opText(op.ast), rhs); !ok {
			return 0, false
		}
	}
	return acc, true
}

func (e *emitter) foldIntNode(n Node) (int64, bool) {
	switch n.sym {
	case Expression, SimpleExpr, Term:
		return e.foldIntSeq(slices.Collect(it(n.ast)))
	case UnaryExpr, Factor:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 3 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == LPAREN {
			return e.foldIntNode(kids[1]) // "(" Expression ")"
		}
		if e.foldConv {
			if v, ok := e.convFold(kids); ok {
				return v, true
			}
		}
		// A constant of an imported package, `geo.MaxPoints`. It folds like a
		// constant of this one; the token switch below sees only a bare name.
		if v, ok := e.foldedQualifiedIntKids(kids); ok {
			n, err := strconv.ParseInt(v, 0, 64)
			return n, err == nil
		}
		// A prefix operator, either as a bare token or wrapped in a UnaryOp node --
		// the shape the parser actually builds for "-1", and the reason "-1 << 40"
		// used to reach the C compiler as written and shift a negative int.
		op, hasOp := int32(0), false
		if len(kids) >= 2 && kids[0].sym == 0 {
			op, hasOp = kids[0].tok, true
		} else if len(kids) >= 2 && kids[0].sym == UnaryOp {
			op, hasOp = e.unaryOpTok(kids[0].ast)
		}
		if hasOp {
			switch e.f.ch(op) {
			case SUB:
				v, ok := e.foldIntSeq(kids[1:])
				return -v, ok
			case ADD:
				return e.foldIntSeq(kids[1:])
			case XOR:
				v, ok := e.foldIntSeq(kids[1:])
				return ^v, ok
			default:
				return 0, false
			}
		}
		if len(kids) == 1 {
			return e.foldIntNode(kids[0])
		}
		return 0, false // a call, index or selector -- not a constant integer
	case 0:
		return e.foldIntToken(n.tok)
	default:
		return 0, false
	}
}

func (e *emitter) foldIntToken(tok int32) (int64, bool) {
	switch e.f.ch(tok) {
	case INT:
		v, err := strconv.ParseInt(normalizeIntLit(e.src(tok)), 0, 64)
		return v, err == nil
	case CHAR:
		// A rune literal is an untyped integer constant, worth the same as the INT
		// spelling of its code point -- so `string('A')` and `[3 * 'A']int` fold
		// alike. Emission was already numeric (emitOperandToken), so nothing that
		// used to reach the C compiler changes shape.
		r, ok := runeLitValue(e.src(tok))
		return int64(r), ok
	case IDENT:
		switch s := e.src(tok); s {
		case "true":
			return 1, true
		case "false":
			return 0, true
		case "iota":
			if e.iota >= 0 {
				return int64(e.iota), true
			}
			return 0, false
		default:
			if v, ok := e.foldedInt(s); ok {
				n, err := strconv.ParseInt(v, 0, 64)
				return n, err == nil
			}
			return 0, false
		}
	default:
		return 0, false
	}
}

// foldIntOp applies one integer operator during constant folding. A division or
// shift the fold cannot perform (a zero divisor, an out-of-range shift) yields
// ok=false rather than a wrong value.
func foldIntOp(a int64, op string, b int64) (int64, bool) {
	switch op {
	case "+":
		return a + b, true
	case "-":
		return a - b, true
	case "*":
		return a * b, true
	case "/":
		if b == 0 {
			return 0, false
		}
		return a / b, true
	case "%":
		if b == 0 {
			return 0, false
		}
		return a % b, true
	case "<<":
		if b < 0 || b >= 64 {
			return 0, false
		}
		return a << uint(b), true
	case ">>":
		if b < 0 || b >= 64 {
			return 0, false
		}
		return a >> uint(b), true
	case "&":
		return a & b, true
	case "|":
		return a | b, true
	case "^":
		return a ^ b, true
	case "&^":
		return a &^ b, true
	default:
		return 0, false
	}
}

// soleToken returns the single terminal token of an expression subtree, if it has
// exactly one (a bare literal or identifier), descending non-terminal wrappers.
func (e *emitter) soleToken(ast []int32) (int32, bool) {
	var tok int32
	count := 0
	var walk func([]int32)
	walk = func(a []int32) {
		for n := range it(a) {
			if n.sym == 0 {
				tok, count = n.tok, count+1
			} else {
				walk(n.ast)
			}
		}
	}
	walk(ast)
	return tok, count == 1
}

// factorIndex recognises a Factor that is a single index `base[i]` -- an
// identifier followed by a FactorSuffix of exactly one Index -- returning the base
// name and the index expression.
func (e *emitter) factorIndex(kids []Node) (base string, indexAST []int32, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	suffix := slices.Collect(it(kids[1].ast))
	if len(suffix) != 1 || suffix[0].sym != Index {
		return "", nil, false
	}
	return e.src(kids[0].tok), suffix[0].ast, true
}

// factorFieldIndex recognises `base.f...[i]` -- a field-access chain followed by a
// single trailing index -- returning the base name, the field chain and the index
// expression. It is the field-base counterpart of factorIndex, letting a slice
// struct field be indexed directly (`b.data[i]`).
func (e *emitter) factorFieldIndex(kids []Node) (base string, fields []string, indexAST []int32, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, nil, false
	}
	suffix := slices.Collect(it(kids[1].ast))
	if len(suffix) < 2 || suffix[len(suffix)-1].sym != Index {
		return "", nil, nil, false
	}
	for _, n := range suffix[:len(suffix)-1] {
		if n.sym != Selector {
			return "", nil, nil, false
		}
		fld := e.soleIdent(n.ast)
		if fld == "" {
			return "", nil, nil, false
		}
		fields = append(fields, fld)
	}
	return e.src(kids[0].tok), fields, suffix[len(suffix)-1].ast, true
}

// accessCur is the value reached partway along an access chain: a plain C value, a
// slice header, or a fixed array with the extents it has left. Exactly one of the
// three holds at a time -- dims non-empty means an array, slice means a header,
// otherwise ctype is a plain value.
type accessCur struct {
	ctype string   // the plain value's C type
	elem  string   // a slice's or array's element type
	dims  []string // an array's remaining extents, outermost first
	slice bool
	// name is the DEFINED name of an ARRAY the chain has reached, when it was
	// declared as one. An array has no ctype -- C models no array value type -- so
	// this is the only thing that says which type it is, and therefore which methods
	// it has. Empty for an array written out and for every other shape.
	name string
	// elemName and elemDims are arrDim's, carried through the walk so that an INDEX
	// can hand the name on: `pool[1]` over a `[2]Row` reaches a Row, and the walk is
	// where that has to be worked out -- the extents alone say nothing about it.
	elemName string
	elemDims int
}

// curArray describes an array the chain has reached, from its shape.
func curArray(a arrDim) accessCur {
	return accessCur{elem: a.elem, dims: a.bounds(), name: a.name, elemName: a.elemName, elemDims: a.elemDims}
}

// curArrDim is curArray's inverse: the shape of the array a chain has reached.
func curArrDim(cur accessCur) arrDim {
	if len(cur.dims) == 0 {
		return arrDim{}
	}
	return arrDim{
		elem: cur.elem, bound: cur.dims[0], inner: cur.dims[1:],
		name: cur.name, elemName: cur.elemName, elemDims: cur.elemDims,
	}
}

// accessBase resolves the start of a chain: a slice variable, an array variable, a
// pointer to an array, or a plain local/global.
func (e *emitter) accessBase(base string) (accessCur, bool) {
	// A folded string constant is not a variable: there is nothing named to walk a
	// chain from, and the chain's steps read ".str"/".len" off the name. Refusing
	// it here sends `lit[i]` and `lit[i:j]` to the single-step shapes, which stand
	// the literal in for the variable (stringConstParts).
	if e.isStringConstName(base) {
		return accessCur{}, false
	}
	if el, ok := e.sliceElem(base); ok {
		return accessCur{elem: el, slice: true}, true
	}
	// A pointer to an array enters the chain AS the array: `p[i].f` is
	// `(*p)[i].f`, and every step after the first is then the array's. The
	// dereference is in the text accessBaseText renders, not here.
	if _, a, ok := e.arrayBase(base); ok {
		return curArray(a), true
	}
	if ct, ok := e.varType(base); ok {
		return accessCur{ctype: ct}, true
	}
	return accessCur{}, false
}

// accessBaseText is the C text a chain starts from: a variable's name, or a
// pointer to an array dereferenced, since what the chain walks is the array.
func (e *emitter) accessBaseText(base string) string {
	if text, _, ok := e.arrayBase(base); ok {
		return text
	}
	// A chain that starts at a POINTER dereferences it -- every step through one is
	// a "->" or an index -- so the base takes the nil check here, once, rather than
	// at each step. arrayBase above has already applied it to a pointer to an array.
	if ct, ok := e.varType(base); ok && e.isPointer(ct) {
		return e.nilCheckedC(e.varRef(base), ct)
	}
	return e.varRef(base)
}

// accessSelect advances the chain by a field selector.
func (e *emitter) accessSelect(cur accessCur, field string) (accessCur, bool) {
	if cur.slice || len(cur.dims) != 0 || cur.ctype == "" {
		return accessCur{}, false // only a plain struct value has fields
	}
	if a, ok := e.structFieldArray(cur.ctype, field); ok {
		// a.name carries the field's DEFINED array type, which is what a method on
		// it dispatches through: `h.f.sum()` for a `type Row [2]int` field. a.elemName
		// carries the same for its ELEMENTS, so `h.rows[1].sum()` has it too.
		return curArray(a), true
	}
	ct, ok := e.structFieldType(cur.ctype, field)
	if !ok {
		return accessCur{}, false
	}
	// Through the underlying type, so a field of a DEFINED slice type is a slice:
	// the table is keyed by the header's own C name, which `type List []int` does
	// not share, and reading it by the written name alone refused `b.in[0]` with
	// "cannot index b.in" for a field Go indexes happily.
	if el, ok := e.sliceElemByName[e.underlyingCType(ct)]; ok {
		return accessCur{elem: el, slice: true}, true
	}
	return accessCur{ctype: ct}, true
}

// accessIndex advances the chain by one index, reporting the type reached and the
// bound to check against. A slice's bound is a length expression built from the
// prefix, so it is returned separately and is only usable while the prefix is
// still available as C text.
func (e *emitter) accessIndex(cur accessCur, prefix string) (next accessCur, lenExpr string, ok bool) {
	switch {
	case cur.slice:
		if prefix == "" {
			// The prefix has already been emitted, so ".len" cannot be formed from
			// it. Indexing a slice this deep in a chain is therefore not modelled.
			return accessCur{}, "", false
		}
		return e.plainOrSlice(cur.elem), prefix + ".len", true
	case cur.ctype == cString:
		if prefix == "" {
			return accessCur{}, "", false
		}
		return accessCur{ctype: "uint8_t"}, prefix + ".len", true // s[i] is a byte, as in Go
	case len(cur.dims) != 0:
		if len(cur.dims) > 1 {
			// One index in. The row takes the element's NAME when one index reaches
			// exactly it, which is what a method on `pool[1]` dispatches through.
			return curArray(curArrDim(cur).row()), cur.dims[0], true
		}
		return e.plainOrSlice(cur.elem), cur.dims[0], true
	}
	// A POINTER to an array reached part-way along a chain -- a struct field of one,
	// `b.p[i]` -- is indexed as the array it points at, which is what accessDeref
	// writes. A pointer BASE never arrives here: accessBase already entered the
	// chain as the array, so there is no second dereference to apply.
	if a, ok := e.arrayPtrCType(cur.ctype); ok {
		rest := a.bounds()[1:]
		if len(rest) != 0 {
			return accessCur{elem: a.elem, dims: rest}, a.bound, true
		}
		return e.plainOrSlice(a.elem), a.bound, true
	}
	return accessCur{}, "", false
}

// accessDeref renders the base an index or a slice step applies to: a pointer to
// an ARRAY is dereferenced, everything else is itself. It pairs with the pointer
// case of accessIndex, which types what this writes.
func (e *emitter) accessDeref(cur accessCur, prefix string) string {
	if _, ok := e.arrayPtrCType(cur.ctype); ok {
		return "(*" + prefix + ")"
	}
	return prefix
}

// accessSlice advances the chain by a slice step, `[l:h]`. Slicing an array or a
// slice yields a slice of the same element; slicing a string yields a string. A
// multi-dimensional array cannot be sliced here for the reason it cannot be
// elsewhere: a slice of arrays has no element type C can name.
func (e *emitter) accessSlice(cur accessCur) (accessCur, bool) {
	switch {
	case cur.slice:
		return accessCur{elem: cur.elem, slice: true}, true
	case len(cur.dims) >= 1:
		return accessCur{elem: e.accessSliceElem(cur), slice: true}, true
	case cur.ctype == cString:
		return accessCur{ctype: cString}, true
	}
	return accessCur{}, false
}

// accessSliceElem names the C element type slicing cur yields, for the chain form
// the way sliceElemOfArray does for the arrDim one: cur's element where the array
// has one extent left, and otherwise the typedef standing for its ROW. A [2][3]int
// slices to a slice of [3]int, whose element C names as ogo_arr_3_int.
//
// This is what carried the refusal into a chain: `d[0][:]` over a [3][2][2]int is
// still a slice of arrays after the index, so the advice the old diagnostic gave --
// slice a row instead -- did not hold for a rank above two.
func (e *emitter) accessSliceElem(cur accessCur) string {
	if len(cur.dims) < 2 {
		return cur.elem
	}
	return e.arrayTypedef(arrDim{elem: cur.elem, bound: cur.dims[1], inner: cur.dims[2:]})
}

// accessSliceSource describes what a slice step slices -- sliceableVar's form for a
// position part-way along a chain, named by the prefix reached so far. An emitted
// prefix leaves nothing to build a header's pointer and length from, so a slice step
// after an index is not modelled. Pure: the caller registers what the header needs.
func (e *emitter) accessSliceSource(cur accessCur, prefix string) (sliceSource, bool) {
	if prefix == "" {
		return sliceSource{}, false
	}
	switch {
	case cur.slice:
		return sliceSource{sliceCName(cur.elem), prefix + ".ptr", prefix + ".len", prefix + ".cap"}, true
	case len(cur.dims) >= 1:
		// An array decays to a pointer to its first element and its OUTERMOST extent
		// is both its length and its capacity. Where extents remain past that one the
		// element is a row, and decaying names it: an `int m[2][3]` decays to
		// `int(*)[3]`, which is exactly the ogo_arr_3_int* the header holds.
		return sliceSource{sliceCName(e.accessSliceElem(cur)), prefix, cur.dims[0], cur.dims[0]}, true
	case cur.ctype == cString:
		return sliceSource{cString, prefix + ".str", prefix + ".len", ""}, true
	}
	return sliceSource{}, false
}

// byteReadOpen opens the cast every read of a string byte carries, `s[i]`, and
// records the header the cast's type needs. Go's byte is unsigned, while the string
// header carries `const char*`, whose signedness C leaves to the implementation: on
// one where char is signed, reading a byte over 127 without this yields a negative
// number. The runtime helpers cast for the same reason -- ogo_decode_rune and
// ogo_string_cmp both do -- and these two index sites were what did not.
//
// The caller closes it with "])" in place of the "]" an unread cast would have.
func (e *emitter) byteReadOpen() string {
	e.includes["stdint.h"] = true
	return "(uint8_t)("
}

// plainOrSlice classifies an element C type as a nested slice header or a plain
// value.
func (e *emitter) plainOrSlice(elem string) accessCur {
	if el, ok := e.sliceElemByName[elem]; ok {
		return accessCur{elem: el, slice: true}
	}
	// A DEFINED slice type is a slice here too. Only the header's own C name was
	// recognised, so `[2]L` over a `type L []int` reached its element as a plain
	// value and `named[0][0]` was refused -- where the unnamed `[2][]int` spelling
	// of the same thing indexed twice without trouble. Every other way of reaching
	// it worked (len, a copy into a local, a range), which is what made the shape
	// look supported.
	if u := e.underlyingCType(elem); u != elem {
		if el, ok := e.sliceElemByName[u]; ok {
			return accessCur{elem: el, slice: true}
		}
	}
	// A named ARRAY type carries its extents, so a further index has something to
	// consume: `[][2]int` reaches its element as ogo_arr_2_int, and `xs[0][1]` is
	// that element indexed once more.
	if a, ok := e.namedArrays[elem]; ok {
		return curArray(a)
	}
	return accessCur{ctype: elem}
}

// emitAccessChain emits `base` followed by an arbitrary run of selectors and
// indexes, returning the type reached. It is the general form of the four fixed
// shapes above (field access, index, field-then-index, index-then-select), and
// reaches chains none of them can express -- `s[i].v[j]`, where an index, a
// selector and another index alternate.
//
// The prefix is accumulated as C text until an index is emitted; after that,
// selectors are emitted directly, since the text is no longer a string that can be
// concatenated or used to build a ".len".
func (e *emitter) emitAccessChain(base string, steps []Node) (accessCur, bool) {
	cur, ok := e.accessBase(base)
	if !ok {
		return accessCur{}, false
	}
	return e.emitAccessChainAt(e.accessBaseText(base), cur, steps, false)
}

// emitAccessChainAt emits a chain from a value already reached and named by prefix.
// claimed says the chain has done something no fixed shape can, so a trailing slice
// step belongs here rather than being handed back to them.
//
// A step may need a base it can name -- an index into a slice or a string wants its
// ".len", a slice step its ".ptr" -- at a point where the chain has already emitted
// the value and left nothing to name. What the chain reaches by then is bound to a
// temporary, which is that name, and the rest goes on from there; the tail is
// emitted through this same function, so a chain needing a second such binding gets
// one. Rendering the prefix once into the temporary is also what keeps the indexes
// inside it evaluated once, and for a slice it is the header that is copied, so a
// write through the temporary still lands in the storage the field names.
func (e *emitter) emitAccessChainAt(prefix string, cur accessCur, steps []Node, claimed bool) (accessCur, bool) {
	// Type the whole chain before emitting any of it, so an unsupported step fails
	// without leaving a half-written expression behind.
	if _, ok := e.accessChainTypeAt(cur, steps, claimed); !ok {
		return accessCur{}, false
	}
	if k, ctype, ok := e.chainHoistPointAt(cur, steps); ok {
		text, ok := e.accessChainCTextAt(prefix, cur, steps[:k], claimed)
		if !ok {
			return accessCur{}, false
		}
		at, ok := e.accessChainTypeAt(cur, steps[:k], claimed)
		if !ok {
			return accessCur{}, false
		}
		return e.emitAccessChainAt(e.hoist(ctype, func() { e.emit(text) }), at, steps[k:], true)
	}
	sliced := claimed
	for i, n := range steps {
		last := i == len(steps)-1
		switch n.sym {
		case Selector:
			f := e.soleIdent(n.ast)
			next, ok := e.accessSelect(cur, f)
			if !ok {
				return accessCur{}, false
			}
			sel, okSel := e.selectC(cur.ctype, f)
			if !okSel {
				return accessCur{}, false
			}
			if prefix != "" {
				prefix += sel
			} else {
				e.emit(sel)
			}
			cur = next
		case Index:
			low, high, max, isSlice := e.sliceParts(n.ast)
			if isSlice {
				if last && !sliced && prefix != "" {
					// A chain that is only a slice of something the fixed shapes already
					// reach is left to them: they write the header straight into place,
					// where this would bind a temporary nothing goes on to use. Reaching
					// it is what the prefix says -- once an index has consumed that, they
					// cannot express the chain and this is the only way to emit it.
					return accessCur{}, false
				}
				next, prefixNext, ok := e.emitChainSlice(cur, prefix, low, high, max, last)
				if !ok {
					return accessCur{}, false
				}
				prefix, cur, sliced = prefixNext, next, true
				continue
			}
			if low == nil {
				return accessCur{}, false
			}
			next, lenExpr, ok := e.accessIndex(cur, prefix)
			if !ok {
				return accessCur{}, false
			}
			open, pre, closing := "[", "", "]"
			switch {
			case cur.slice:
				open = ".ptr["
			case cur.ctype == cString:
				open, pre, closing = ".str[", e.byteReadOpen(), "])"
			}
			e.emit(pre + e.accessDeref(cur, prefix) + open)
			e.emitIndex(low, lenExpr)
			e.emit(closing)
			prefix = ""
			cur = next
		default:
			return accessCur{}, false
		}
	}
	if prefix != "" {
		e.emit(prefix)
	}
	return cur, true
}

// emitChainSlice emits a slice step of a chain, `a[:][1]` and the like, returning
// the value reached and the name it is now reachable by.
//
// A slice expression is a header value, and C has nowhere to put one mid-expression:
// the steps after it want a base they can write `.ptr` and `.len` off, and streaming
// the header inline would leave them nothing. So the header is bound to a temporary
// declared before the statement, which is exactly the base they need -- and which is
// also what a reader would write by hand, since binding the slice to a variable is
// the workaround this step removes.
//
// A static initializer has no statement to hang the temporary off, so a slice step
// is refused there rather than emitted into nothing.
func (e *emitter) emitChainSlice(cur accessCur, prefix string, low, high, max []int32, last bool) (next accessCur, name string, ok bool) {
	if e.declInit {
		return accessCur{}, "", false
	}
	src, ok := e.accessSliceSource(cur, prefix)
	if !ok {
		return accessCur{}, "", false
	}
	if next, ok = e.accessSlice(cur); !ok {
		return accessCur{}, "", false
	}
	if src.cname == cString {
		e.usesString = true
	} else {
		e.needSlice(sliceElemFromCName(src.cname))
	}
	if last {
		// The end of the chain: nothing after it needs a base, so the header goes
		// straight where the chain's value goes.
		e.emitSliceExpr(src, low, high, max)
		return next, "", true
	}
	return next, e.hoist(src.cname, func() { e.emitSliceExpr(src, low, high, max) }), true
}

// chainHoistPoint finds the first step that needs a base it can name at a point
// where the chain has none left, returning that step's position and the C type of
// the value to bind there. An index consumes the prefix -- what it produces is
// written, not a string that can be appended to -- so anything after it that wants
// a ".len" or a ".ptr" has to be given a name of its own.
//
// A value with no C type to bind stops it: an array is the one such value, and an
// array never asks for a name in the first place, its extents being compile-time
// constants.
func (e *emitter) chainHoistPointAt(cur accessCur, steps []Node) (int, string, bool) {
	ok := true
	named := true
	for i, n := range steps {
		switch n.sym {
		case Selector:
			if cur, ok = e.accessSelect(cur, e.soleIdent(n.ast)); !ok {
				return 0, "", false
			}
		case Index:
			_, _, _, isSlice := e.sliceParts(n.ast)
			if (isSlice || cur.slice || cur.ctype == cString) && !named {
				ctype, ok := e.chainValueCType(cur)
				if !ok {
					return 0, "", false
				}
				return i, ctype, true
			}
			if isSlice {
				if cur, ok = e.accessSlice(cur); !ok {
					return 0, "", false
				}
				// emitChainSlice binds a temporary for every slice step but the last,
				// which it streams because nothing follows to want a name.
				named = i != len(steps)-1
				continue
			}
			// The prefix only has to be non-empty here; its text is not read.
			if cur, _, ok = e.accessIndex(cur, "?"); !ok {
				return 0, "", false
			}
			named = false
		default:
			return 0, "", false
		}
	}
	return 0, "", false
}

// chainValueCType names the C type a chain's value can be bound to. An array has
// none -- C has no array value type -- which is what makes it unbindable.
func (e *emitter) chainValueCType(cur accessCur) (string, bool) {
	switch {
	case cur.slice:
		return sliceCName(cur.elem), true
	case len(cur.dims) != 0, cur.ctype == "":
		return "", false
	}
	return cur.ctype, true
}

// accessChainType walks a chain without emitting, for inference and for validating
// ahead of emission.
func (e *emitter) accessChainType(base string, steps []Node) (accessCur, bool) {
	cur, ok := e.accessBase(base)
	if !ok {
		return accessCur{}, false
	}
	return e.accessChainTypeAt(cur, steps, false)
}

// accessChainTypeAt is accessChainType from a value already reached. It models the
// temporary emitAccessChainAt binds where a step needs a name and the chain has
// none: a value that can be bound gets one back, and one that cannot -- an array --
// is what makes the chain unsupported.
func (e *emitter) accessChainTypeAt(cur accessCur, steps []Node, claimed bool) (accessCur, bool) {
	ok := true
	named := true
	sliced := claimed
	for i := 0; i < len(steps); i++ {
		n := steps[i]
		last := i == len(steps)-1
		switch n.sym {
		case Selector:
			f := e.soleIdent(n.ast)
			// A method of an interface reached through the chain: the slot's result
			// is what the chain has after it, and the CallSuffix that follows is
			// part of the call rather than a step of its own.
			if ms, isIface := e.ifaceMethods[cur.ctype]; isIface && i+1 < len(steps) && steps[i+1].sym == CallSuffix {
				res, has := "", false
				for _, m := range ms {
					if m.name == f {
						res, has = m.res, true
					}
				}
				if !has {
					return accessCur{}, false
				}
				if res == "void" {
					cur = accessCur{}
				} else {
					cur = e.plainOrSlice(res)
				}
				named = false
				i++ // consumed the CallSuffix
				continue
			}
			if cur, ok = e.accessSelect(cur, f); !ok {
				return accessCur{}, false
			}
		case Index:
			if _, _, _, isSlice := e.sliceParts(n.ast); isSlice {
				if last && !sliced && named {
					return accessCur{}, false // left to the fixed shapes, as in emitAccessChain
				}
				if !named {
					if _, ok := e.chainValueCType(cur); !ok {
						return accessCur{}, false
					}
					named = true
				}
				if cur, ok = e.accessSlice(cur); !ok {
					return accessCur{}, false
				}
				// The emitter binds a temporary here, so a name is available again --
				// except for the last step, whose header is streamed into place.
				named, sliced = !last, true
				continue
			}
			if !named && (cur.slice || cur.ctype == cString) {
				if _, ok := e.chainValueCType(cur); !ok {
					return accessCur{}, false
				}
				named = true
			}
			// The prefix only has to be non-empty here; its text is not read.
			if cur, _, ok = e.accessIndex(cur, "?"); !ok {
				return accessCur{}, false
			}
			named = false
		default:
			return accessCur{}, false
		}
	}
	return cur, true
}

// factorAccessChain recognises an identifier followed by a run of selectors and
// indexes that mixes both kinds more than once -- the shapes the fixed helpers
// cannot match. Narrower chains are left to them, so their pinned output is
// unchanged.
func (e *emitter) factorAccessChain(kids []Node) (string, []Node, bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	steps := slices.Collect(it(kids[1].ast))
	if len(steps) == 0 {
		return "", nil, false
	}
	name := e.src(kids[0].tok)
	// `Row(a)[i]` for `type Row [3]int`: a conversion to a defined ARRAY type is a
	// no-op on the representation -- the typedef stands for the same storage -- so
	// the chain continues from the OPERAND and the conversion is dropped. It has to
	// be unwrapped rather than emitted because an array has no C value type: a
	// conversion to one cannot become an expression the steps then apply to, the way
	// a conversion to a scalar can.
	//
	// Every reader of a chain goes through here, so the emitter, the type walk and
	// the channel paths all see through the conversion, not just the one that
	// happened to be reported.
	if operand, rest, ok := e.arrayConvChain(name, steps); ok {
		name, steps = operand, rest
	}
	for _, n := range steps {
		if n.sym != Index && n.sym != Selector {
			return "", nil, false
		}
	}
	return name, steps, true
}

// parenMethodSteps matches a Factor that is a PARENTHESISED expression carrying one
// or more method calls, and answers with those call steps and the expression's C
// type.
//
// The type is the DEFINED name, not the representation: a method lives in its
// type's namespace, and exprReprCType would answer int32_t for a `type Fix int32`
// whose methods are all called Fix_something.
func (e *emitter) parenMethodSteps(kids []Node) ([]Node, string, bool) {
	if len(kids) != 4 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[3].sym != FactorSuffix {
		return nil, "", false
	}
	steps := slices.Collect(it(kids[3].ast))
	if len(steps) == 0 || len(steps)%2 != 0 {
		return nil, "", false
	}
	for i := 0; i < len(steps); i += 2 {
		if steps[i].sym != Selector || steps[i+1].sym != CallSuffix {
			return nil, "", false
		}
	}
	ct, ok := e.inferCType(kids[1].ast)
	if !ok || ct == "" {
		return nil, "", false
	}
	return steps, ct, true
}

// parenMethodResultType types `(expr).M(...)` as its last method's result, which
// the emission alone knew: without it a `(&P{1, 2}).Sum()` took the type of the
// ADDRESS it is called on, and an int result printed as a pointer.
//
// A method with no result, or with several, answers no: the first has no value to
// type and the second is not a single one.
func (e *emitter) parenMethodResultType(kids []Node) (string, bool) {
	steps, ct, ok := e.parenMethodSteps(kids)
	if !ok {
		return "", false
	}
	for i := 0; i < len(steps); i += 2 {
		rets, isMethod := e.funcRet[methodCName(methodBaseType(ct), e.soleIdent(steps[i].ast))]
		if !isMethod || len(rets) != 1 {
			return "", false
		}
		ct = rets[0]
	}
	return ct, true
}

// emitParenMethod emits a method called on a PARENTHESISED expression, `(a -
// b).Scaled()` for a defined type with arithmetic, and reports whether the Factor
// was that shape.
//
// The receiver is an expression with no name, and needs none: a VALUE receiver is
// passed by value, so the parenthesised expression IS the argument. Every other
// receiver shape already worked -- a variable, a parenthesised variable, a field, an
// element, a call's result -- and so did binding the arithmetic to a variable first,
// which is the workaround being accepted while the plain spelling drew "this form is
// not supported yet".
//
// A POINTER receiver is refused in Go's own words: the expression is not
// addressable, and there is nothing to take the address of.
func (e *emitter) emitParenMethod(kids []Node) bool {
	steps, ct, ok := e.parenMethodSteps(kids)
	if !ok {
		return false
	}
	// Built as text so a CHAIN can wrap it, `(a - b).Add(1).Scale(2)` -- each call
	// becomes the receiver of the next, which is what the result type carries.
	text := e.captureC(func() { e.emitExprNode(kids[1]) })
	for i := 0; i < len(steps); i += 2 {
		method := e.soleIdent(steps[i].ast)
		if method == "" {
			return false
		}
		// The expression may itself be a POINTER, `(&P{1, 2}).Sum()`. The method's
		// base is then what it points at, and which of the two the call wants
		// decides the receiver: a pointer method takes the pointer as it stands, a
		// value method takes what it points at. Missing this passed the pointer to
		// a VALUE receiver, which the host compiler rejected -- and which had been
		// a clean refusal before the parenthesised form was supported at all.
		isPtr := e.isPointer(ct)
		cname := methodCName(methodBaseType(ct), method)
		rets, isMethod := e.funcRet[cname]
		if !isMethod {
			return false
		}
		recv := text
		switch {
		case e.methodPtr[cname]:
			if !isPtr {
				// Go's rule, and its words: there is nothing to take the address
				// of. `(&P{...})` is not this case -- it IS the address.
				e.fail("cannot call pointer method %s on %s", method, e.goTypeName(ct))
				return true
			}
		case isPtr:
			recv = "(*" + text + ")"
		}
		args := e.argsCText(cname, steps[i+1].ast)
		if args != "" {
			args = ", " + args
		}
		text = cname + "(" + recv + args + ")"
		if i+2 < len(steps) {
			// Another call follows, so this one's single result is its receiver. A
			// method with none, or with several, ends the chain here and is left to
			// the paths that report it.
			if len(rets) != 1 {
				return false
			}
			ct = rets[0]
		}
	}
	e.emit(text)
	return true
}

// factorDerefChain matches a parenthesised DEREFERENCE carrying a suffix, `(*p).x`
// and `(*p)[i]`, returning the pointer's name and the steps that follow.
//
// Go admits the shorthands `p.x` and, for a pointer to an array, `p[i]`, and those
// are the spellings most code uses -- but the written-out form is ordinary Go, and
// for a pointer to a SLICE or a STRING it is the only form there is, `p[i]` being
// illegal on those.
func (e *emitter) factorDerefChain(kids []Node) (string, []Node, bool) {
	if len(kids) != 4 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[3].sym != FactorSuffix {
		return "", nil, false
	}
	name, ok := e.derefOperand(kids[1].ast)
	if !ok {
		return "", nil, false
	}
	steps := slices.Collect(it(kids[3].ast))
	if len(steps) == 0 {
		return "", nil, false
	}
	return name, steps, true
}

// derefCallSteps reports a chain through a dereference that CALLS something,
// `(*p).m()`. Go defines `p.m()` as the same call -- a selector on a pointer to a
// struct dereferences it, for a method as for a field -- so it is emitted as the
// shorthand rather than through the chain walkers, which model no call step.
func derefCallSteps(steps []Node) bool { return containsSym(steps, CallSuffix) }

// derefBase resolves `(*p)` as the start of a chain: the C text naming what p
// points at, and the value reached there.
//
// plainOrSlice classifies the pointee, so a slice, an array (defined or minted) and
// a plain value each arrive as themselves -- which is what lets the chain walkers
// treat the dereference as any other base. A pointer to an ARRAY reaches this the
// other way too, through arrayBase, since `p[i]` names the same storage; both
// render the same text.
func (e *emitter) derefBase(name string) (string, accessCur, bool) {
	ct, ok := e.varType(name)
	if !ok || !e.isPointer(ct) {
		return "", accessCur{}, false
	}
	return "(*" + e.nilCheckedC(e.varRef(name), ct) + ")", e.plainOrSlice(e.elemType(ct)), true
}

// arrayConvChain matches a leading conversion to a defined array type -- `Row(a)`, or
// the qualified `geo.Row(a)` -- and answers with the operand's name and the steps that
// follow it. The operand
// must be a plain identifier: what makes the unwrap sound is that the conversion
// names the same storage, which is only true of something that HAS storage.
func (e *emitter) arrayConvChain(name string, steps []Node) (string, []Node, bool) {
	ct, used, isConv := e.convChainHead(name, steps)
	if !isConv || used == len(steps) {
		return "", nil, false // nothing follows the conversion: not a chain
	}
	if _, isArray := e.namedArrays[ct]; !isArray {
		return "", nil, false
	}
	args := e.callArgExprs(steps[used-1].ast)
	if len(args) != 1 {
		return "", nil, false
	}
	base, ok := e.exprIdent(args[0].ast)
	if !ok {
		return "", nil, false
	}
	// `Row(a)[0:2]`: slicing an array needs it to be addressable, which a
	// conversion's result is not, so Go refuses this too. Reported from here because
	// unlike the address-of and the assignment it needs no context -- a slice step
	// after this conversion is wrong wherever it stands -- and fail keeps the FIRST
	// error, so this wins over the generic one the typing path would reach.
	for _, st := range steps[used:] {
		if st.sym != Index {
			continue
		}
		if _, _, _, isSlice := e.sliceParts(st.ast); isSlice {
			e.fail("cannot slice a conversion: it is not addressable")
			break
		}
	}
	return base, steps[used:], true
}

// isArrayConv reports whether n is (or wraps) a factor whose chain begins with a
// conversion to a defined array type -- the shape factorAccessChain unwraps. It is
// what lets a context that must NOT see through the conversion say so.
func (e *emitter) isArrayConv(n Node) bool {
	kids := slices.Collect(it(n.ast))
	for len(kids) == 1 && kids[0].sym != 0 {
		if kids[0].sym == Factor {
			break
		}
		kids = slices.Collect(it(kids[0].ast))
	}
	if len(kids) == 1 && kids[0].sym == Factor {
		kids = slices.Collect(it(kids[0].ast))
	}
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return false
	}
	_, _, ok := e.arrayConvChain(e.src(kids[0].tok), slices.Collect(it(kids[1].ast)))
	return ok
}

// isAccessChain reports whether every step is a selector or an index.
func isAccessChain(steps []Node) bool {
	if len(steps) == 0 {
		return false
	}
	for _, n := range steps {
		if n.sym != Index && n.sym != Selector {
			return false
		}
	}
	return true
}

// hasIndexStep reports whether a chain indexes anything, which is what puts it out
// of the reach of the shapes that expect a run of selectors.
func hasIndexStep(steps []Node) bool {
	return slices.ContainsFunc(steps, func(n Node) bool { return n.sym == Index })
}

// sliceParts inspects an Index node. isSlice reports a colon -- a slice expression;
// low, high and max are the bound expressions, nil when omitted, max being the
// third bound of `a[low:high:max]`. For a plain index (no colon), isSlice is false
// and low is the index expression.
//
// Which bound an expression is follows from how many colons precede it, so an
// omitted one is simply a colon the loop passes with nothing in between.
func (e *emitter) sliceParts(indexAST []int32) (low, high, max []int32, isSlice bool) {
	colons := 0
	for n := range it(indexAST) {
		switch n.sym {
		case Expression:
			switch colons {
			case 0:
				low = n.ast
			case 1:
				high = n.ast
			default:
				max = n.ast
			}
		case 0:
			if e.f.ch(n.tok) == COLON {
				isSlice = true
				colons++
			}
		}
	}
	return low, high, max, isSlice
}

// isPackageVar reports whether name reaches a package-level variable of this
// package -- one that outlives every call -- rather than something local.
//
// A local of that name shadows the package one, so the local environments are asked
// first, and both of them: an array lives in its own, not in locals. The same split
// is why the package side asks twice, and asking only the plain one is what let a
// package array take a reference that dies before it does.
func (e *emitter) isPackageVar(name string) bool {
	if _, ok := e.locals[name]; ok {
		return false
	}
	if _, ok := e.arrays[name]; ok {
		return false
	}
	gc := e.globalC(name)
	if _, ok := e.globals[gc]; ok {
		return true
	}
	_, ok := e.globalArrays[gc]
	return ok
}

// varType returns a variable's C type from the local then the package environment.
func (e *emitter) varType(name string) (string, bool) {
	if ct, ok := e.locals[name]; ok {
		return ct, true
	}
	ct, ok := e.globals[e.globalC(name)]
	return ct, ok
}

// globalC is the C name of a package-level variable: its source name mangled into
// the current package's namespace, so two packages' same-named globals do not
// collide as file-scope statics in the one translation unit.
func (e *emitter) globalC(name string) string { return mangle(e.curPkgPrefix, name) }

// varRef is the C name a bare variable reference emits: a package global takes its
// mangled name; a local or parameter (which shadows a global, and lives in locals)
// keeps its source name, as does anything not a known variable (a constant is
// inlined elsewhere). It is the read counterpart to the global declarations.
func (e *emitter) varRef(name string) string {
	if _, ok := e.locals[name]; ok {
		return userIdent(name) // a local or parameter: renamed if C reserved it
	}
	if _, ok := e.globals[e.globalC(name)]; ok {
		return e.globalC(name)
	}
	return userIdent(name)
}

// sliceSource describes what a slice expression slices: the C type of the result
// header, the base pointer, and the base's length and capacity. baseLen is the
// default when high is omitted; baseCap becomes the header's third field and is
// empty for a string, which has no capacity and slices to a 2-field ogo_string.
type sliceSource struct{ cname, ptr, baseLen, baseCap string }

// sliceElemOfArray names the C element type a slice of array a has: a's element
// where a is one-dimensional, and otherwise the TYPEDEF standing for its row --
// `m[:]` over a [2][3]int is a []([3]int), whose element C cannot write inline but
// can name, `typedef int ogo_arr_3_int[3]`.
//
// Minting that typedef here is not a new mechanism: a `[][3]int` written as a type
// has always gone through arrayElemTypedef and produced the same name, so a slice
// made by slicing and one made by a literal are the same C type and interchange.
// This used to refuse instead, on the belief that "a slice of arrays has no element
// type C can name" -- which had stopped being true, and left the language's own
// idiom for a heapless slice, a package-scope backing array sliced at the point of
// use, working for every element type but this one.
func (e *emitter) sliceElemOfArray(a arrDim) string {
	if a.dims() == 1 {
		return a.elem
	}
	return e.arrayTypedef(a.row())
}

// sliceableVar resolves a variable base to slice: a string, a fixed array, a
// pointer to one, or a slice.
func (e *emitter) sliceableVar(base string) (sliceSource, bool) {
	text, a, isArray := e.arrayBase(base)
	switch {
	case e.isStringConstName(base):
		// A string constant has no C variable: every use folds to its literal, so
		// there is nothing named to take ".str" and ".len" off. The pieces stand in
		// directly. (See stringConstParts.)
		ptr, n, _ := e.stringConstParts(base)
		e.usesString = true
		return sliceSource{cString, ptr, n, ""}, true
	case e.isStringVarName(base):
		e.usesString = true
		return sliceSource{cString, base + ".str", base + ".len", ""}, true
	case isArray:
		elem := e.sliceElemOfArray(a)
		e.needSlice(elem)
		if _, isPtr := e.arrayPtrVar(base); isPtr {
			// `p[lo:hi]` slices the array p points at. The dereference is what
			// decays to the first element, where a plain array's name already does.
			return sliceSource{sliceCName(elem), text, a.bound, a.bound}, true
		}
		return sliceSource{sliceCName(elem), base, a.bound, a.bound}, true
	case e.hasSliceVar(base):
		elem, _ := e.sliceElem(base)
		e.needSlice(elem)
		return sliceSource{sliceCName(elem), base + ".ptr", base + ".len", base + ".cap"}, true
	}
	return sliceSource{}, false
}

// sliceableField resolves a struct field base to slice -- `b.data[1:3]` over a
// slice field, `b.arr[1:3]` over an array field. A slice field is re-sliced
// through its own header (its cap still bounds how far the result may grow); an
// array field decays to its inline storage, bounded both ways by the extent.
func (e *emitter) sliceableField(base string, fields []string) (sliceSource, bool) {
	if len(fields) == 0 {
		return sliceSource{}, false
	}
	lv := e.fieldAccessC(base, fields)
	if a, ok := e.fieldArray(base, fields); ok {
		// A field whose element is an ARRAY took a.elem here and named the header
		// after the innermost type, so `b.g[:]` over a [3][2]int built an
		// ogo_slice_int over an int(*)[2]. flexcc only WARNED about the pointer, so
		// the build succeeded and every use of the result was refused afterwards
		// for a reason that named C rather than the program.
		elem := e.sliceElemOfArray(a)
		e.needSlice(elem)
		return sliceSource{sliceCName(elem), lv, a.bound, a.bound}, true
	}
	ct, ok := e.fieldType(base, fields)
	if !ok {
		return sliceSource{}, false
	}
	// A string field slices like a string variable: the result is a string over the
	// same bytes, with no capacity of its own. sliceableVar has always done this for
	// a name; a field reached the slice paths with nothing to answer for it.
	if e.underlyingCType(ct) == cString {
		e.usesString = true
		return sliceSource{cString, lv + ".str", lv + ".len", ""}, true
	}
	if !e.isSliceCType(ct) {
		return sliceSource{}, false
	}
	if elem, ok := e.sliceElemByName[ct]; ok {
		e.needSlice(elem)
	}
	return sliceSource{ct, lv + ".ptr", lv + ".len", lv + ".cap"}, true
}

// sliceableChainRow recognises an access chain whose last step slices what the
// steps before it reach -- `m[0][:]`, a row of a multi-dimensional array.
//
// It is separate from the plain chain walk because emitAccessChain streams its
// output, and once an index has been written the prefix is no longer available as
// text; a slice header needs exactly that text for its pointer. So the prefix is
// typed and then rendered to a string, and the row's own extent bounds the result.
//
// A row of any rank can become a slice. A row of a [2][3][4]int is a [3][4]int and
// slices to a slice of [4]int, whose element C names through the typedef
// accessSliceElem mints -- the same one a `[][4]int` literal is built over, so the
// two are one type.
func (e *emitter) sliceableChainRow(base string, steps []Node) (src sliceSource, low, high, max []int32, ok bool) {
	if len(steps) < 2 || steps[len(steps)-1].sym != Index {
		return sliceSource{}, nil, nil, nil, false
	}
	low, high, max, isSlice := e.sliceParts(steps[len(steps)-1].ast)
	if !isSlice {
		return sliceSource{}, nil, nil, nil, false
	}
	prefix := steps[:len(steps)-1]
	cur, ok := e.accessChainType(base, prefix)
	if !ok || cur.slice || len(cur.dims) == 0 {
		return sliceSource{}, nil, nil, nil, false
	}
	text, ok := e.accessChainCText(base, prefix)
	if !ok {
		return sliceSource{}, nil, nil, nil, false
	}
	elem := e.accessSliceElem(cur)
	e.needSlice(elem)
	// The row decays to a pointer to its first element, and its outermost extent is
	// both the length and the capacity: an array's storage is exactly its extent.
	return sliceSource{sliceCName(elem), text, cur.dims[0], cur.dims[0]}, low, high, max, true
}

// accessChainCText renders an access chain to a string, the way argsCText does
// for a call's arguments, so a caller that needs the chain as an operand rather
// than as streamed output can have it.
func (e *emitter) accessChainCText(base string, steps []Node) (string, bool) {
	cur, ok := e.accessBase(base)
	if !ok {
		return "", false
	}
	return e.accessChainCTextAt(e.accessBaseText(base), cur, steps, false)
}

// accessChainCTextAt is accessChainCText from a value already reached.
func (e *emitter) accessChainCTextAt(prefix string, cur accessCur, steps []Node, claimed bool) (string, bool) {
	saved := e.w
	var buf bytes.Buffer
	e.w = &buf
	_, ok := e.emitAccessChainAt(prefix, cur, steps, claimed)
	e.w = saved
	return buf.String(), ok
}

// emitSliceExpr emits a slice expression `base[low:high]` as a new { pointer,
// length } header aliasing base's storage -- a non-owning view, no copy. An
// omitted low is 0; an omitted high is base's length. Three bases are modelled: a
// string (-> ogo_string over .str/.len), a fixed array (-> ogo_slice_<elem> over
// the decayed array and its compile-time bound), and a slice (-> the same header
// over .ptr/.len). In a static initializer a brace is used, not a compound literal
// (not a constant expression there; see declInit).
//
// A third bound, `base[low:high:max]`, gives the result max less low for its
// capacity instead of the base's own, so appending to it stops there. A string has
// no capacity field and so takes no such bound.
//
// With checks on, bounds that are not provably in range go through the reslice
// helper instead, which panics rather than yielding a header over storage the base
// does not own.
func (e *emitter) emitSliceExpr(src sliceSource, low, high, max []int32) {
	cname, ptr, baseLen, baseCap := src.cname, src.ptr, src.baseLen, src.baseCap
	if max != nil && baseCap == "" {
		e.fail("a string has no capacity to set with a third slice bound")
		return
	}
	// A string has no capacity, so its length is the limit a bound may reach; for
	// an array the two are the same compile-time extent.
	capExpr := baseCap
	if capExpr == "" {
		capExpr = baseLen
	}
	// Bounds that are constants and out of range are wrong however the program runs,
	// so they are reported here rather than left to trap. Go rejects them too.
	if e.reportConstSliceBounds(low, high, max, baseLen, capExpr) {
		return
	}
	if e.sliceNeedsHelper(low, high, max, baseLen, capExpr) {
		e.emitHelperSliceExpr(cname, ptr, baseLen, capExpr, low, high, max)
		return
	}
	if e.declInit {
		e.emit("{")
	} else {
		e.emit("(" + cname + "){")
	}
	// ptr: base's data, offset by low.
	e.emit(ptr)
	if low != nil {
		e.emit(" + ")
		e.emitExpr(low)
	}
	// len: (high, or base's length when omitted) - low.
	e.emit(", ")
	if high != nil {
		e.emitExpr(high)
	} else {
		e.emit(baseLen)
	}
	if low != nil {
		e.emit(" - ")
		e.emitExpr(low)
	}
	// cap (slices only): max when a third bound sets one, else cap(base), less low
	// either way -- so without one the result can still be re-sliced to the end of
	// the backing storage (Go: the slice upper bound reaches cap).
	if baseCap != "" {
		e.emit(", ")
		if max != nil {
			e.emitExpr(max)
		} else {
			e.emit(baseCap)
		}
		if low != nil {
			e.emit(" - ")
			e.emitExpr(low)
		}
	}
	e.emit("}")
}

// reportConstSliceBounds reports a slice expression whose bounds are constants that
// cannot be in range, and says whether it reported. Go rejects such a program at
// compile time rather than letting it trap, and a bound written into the source is
// wrong however the program runs.
//
// It reports only when every written bound folds and the operand's extent is itself
// a compile-time constant -- an array's, since a slice's length and capacity are
// run-time values and a bound against them can only be checked as the program runs.
// Each bound is reported at its own position, and the first wrong one wins, as Go
// does it.
func (e *emitter) reportConstSliceBounds(low, high, max []int32, lenExpr, capExpr string) bool {
	c, err := strconv.ParseInt(capExpr, 10, 64)
	if err != nil {
		return false
	}
	lo, okLo := int64(0), true
	if low != nil {
		lo, okLo = e.foldConstInt(low)
	}
	hi, okHi := e.foldBoundOrLiteral(high, lenExpr)
	mx, okMx := e.foldBoundOrLiteral(max, capExpr)
	if !okLo || !okHi || !okMx {
		return false
	}
	for _, b := range []struct {
		v int64
		n []int32
	}{{lo, low}, {hi, high}, {mx, max}} {
		if b.n == nil {
			continue // an omitted bound is the extent itself, which is in range
		}
		switch {
		case b.v < 0:
			e.failAt(b.n, "invalid argument: index %d must not be negative", b.v)
			return true
		case b.v > c:
			// The bound may reach the extent, so the range Go prints is one past it.
			e.failAt(b.n, "invalid argument: index %d out of bounds [0:%d]", b.v, c+1)
			return true
		}
	}
	// In range individually, but not in order. Go names the later bound and prints
	// the pair the way the source reads, later first.
	switch {
	case hi < lo:
		e.failAt(orNodes(high, low), "invalid slice indices: %d < %d", hi, lo)
	case mx < hi:
		e.failAt(orNodes(max, high), "invalid slice indices: %d < %d", mx, hi)
	default:
		return false
	}
	return true
}

// orNodes is the first of two nodes that was written, for a diagnostic about a pair
// of bounds where either may have been omitted.
func orNodes(a, b []int32) []int32 {
	if a != nil {
		return a
	}
	return b
}

// failAt reports an emitter error at a node's source position.
// failSuffixChain reports why a chain of selectors and indexes could not be
// emitted, naming the operand each step applies to and what that operand is. It
// walks the same steps accessChainType does and stops at the first one the value
// reached cannot take, which is the one the program got wrong.
//
// It is a diagnosis, not a check: everything it reports has already been refused by
// returning false somewhere above. What it adds is a message that names source the
// user wrote, where the fallthrough named an AST node.
func (e *emitter) failSuffixChain(n Node, kids []Node) {
	base, steps, ok := e.factorAccessChain(kids)
	if !ok {
		e.fail("cannot read %s: this form is not supported yet", e.f.exprSource(n))
		return
	}
	if e.failChainSteps(n.Pos(), base, steps) {
		return
	}
	// Every step typed, so what failed was the emission rather than the shape --
	// a chain the walkers can describe but not write, which is worth saying as
	// itself rather than as a made-up type error.
	e.fail("cannot read %s: this combination of indexes and fields is not supported yet", e.f.exprSource(n))
}

// failChainSteps walks base's chain and reports the first step the value reached
// cannot take, naming the operand that step applies to in the source the user
// wrote. start is the token the whole chain begins at, which is what the operand's
// text is measured from. It reports whether it said anything: a chain whose every
// step types is one the caller could not EMIT, which is a different sentence.
//
// Shared by the read and the assignment sides, which fail in the same ways and
// used to describe them differently -- one naming an AST node, the other calling a
// program Go rejects "not supported yet".
func (e *emitter) failChainSteps(start int32, base string, steps []Node) bool {
	cur, ok := e.accessBase(base)
	if !ok {
		e.failAtPos(start, "%s is not a value with fields or elements", base)
		return true
	}
	for _, step := range steps {
		reached := e.f.sourceSpan(start, step.Pos()-1)
		switch step.sym {
		case Selector:
			field := e.soleIdent(step.ast)
			next, ok := e.accessSelect(cur, field)
			if !ok {
				// At the NAME rather than at the dot before it, which is where Go
				// reports it and the only part of the step the user chose.
				e.failAtPos(e.selectorFieldPos(step, step.Pos()), "%s has no field %s", reached, field)
				return true
			}
			cur = next
		case Index:
			if _, _, _, isSlice := e.sliceParts(step.ast); isSlice {
				next, ok := e.accessSlice(cur)
				if !ok {
					e.failAt(step.ast, "cannot slice %s", reached)
					return true
				}
				cur = next
				continue
			}
			// The prefix is only tested for being non-empty here, never read: this
			// walk emits nothing, so any placeholder does.
			next, _, ok := e.accessIndex(cur, "?")
			if !ok {
				e.failAt(step.ast, "cannot index %s", reached)
				return true
			}
			cur = next
		default:
			return false // a call or another suffix: not this walk's to describe
		}
	}
	return false
}

func (e *emitter) failAt(n []int32, format string, args ...any) {
	for c := range it(n) {
		e.failAtPos(c.Pos(), format, args...)
		return
	}
	e.fail(format, args...)
}

// failAtPos is failAt from a token index, for a diagnostic that points at one
// token of a node rather than at its first.
func (e *emitter) failAtPos(pos int32, format string, args ...any) {
	e.fail("%s: "+format, append([]any{e.f.tok(pos).Position().String()}, args...)...)
}

// selectorFieldPos returns the token index of a Selector's field NAME, or def when
// it has none. A Selector begins with its dot, which is not what a message about
// the field should point at.
func (e *emitter) selectorFieldPos(sel Node, def int32) int32 {
	for c := range it(sel.ast) {
		if c.sym == 0 && e.f.ch(c.tok) == IDENT {
			return c.Pos()
		}
	}
	return def
}

// sliceNeedsHelper reports whether a slice expression goes through its reslice
// helper rather than being spelled inline. A static initializer never does, a call
// not being a constant expression there; its bounds are constants anyway.
//
// Two things send an expression to the helper. A bound that can change state has
// to, whatever the checks say, because the inline form names low in all three
// header fields and would evaluate it three times -- Go evaluates each bound once,
// and three different values do not even agree with each other. Otherwise it is the
// bounds check: on, and not already settled at compile time. `x[:]` is always
// settled -- 0 <= 0 <= len <= cap holds for any base -- as are bounds a compile-time
// extent bounds.
func (e *emitter) sliceNeedsHelper(low, high, max []int32, lenExpr, capExpr string) bool {
	if e.declInit {
		return false
	}
	if e.exprHasEffect(low) || e.exprHasEffect(high) || e.exprHasEffect(max) {
		return true
	}
	if !e.checks || (low == nil && high == nil && max == nil) {
		return false
	}
	return !e.constSliceInRange(low, high, max, lenExpr, capExpr)
}

// constSliceInRange reports whether every written bound folds to a constant and
// together they satisfy 0 <= low <= high <= max <= cap, the base's length and
// capacity being known at compile time. An omitted low is 0, an omitted high the
// base's length and an omitted max its capacity; those stand-ins are decimal
// literals only for an array or a string constant -- a slice's ".len" is a run-time
// value and never parses, so such a slice keeps its check.
func (e *emitter) constSliceInRange(low, high, max []int32, lenExpr, capExpr string) bool {
	lo := int64(0)
	if low != nil {
		v, ok := e.foldConstInt(low)
		if !ok {
			return false
		}
		lo = v
	}
	hi, ok := e.foldBoundOrLiteral(high, lenExpr)
	if !ok {
		return false
	}
	c, err := strconv.ParseInt(capExpr, 10, 64)
	if err != nil {
		return false
	}
	mx, ok := e.foldBoundOrLiteral(max, capExpr)
	if !ok {
		return false
	}
	return 0 <= lo && lo <= hi && hi <= mx && mx <= c
}

// foldBoundOrLiteral folds a written slice bound, or parses the expression standing
// in for an omitted one.
func (e *emitter) foldBoundOrLiteral(bound []int32, omitted string) (int64, bool) {
	if bound != nil {
		return e.foldConstInt(bound)
	}
	v, err := strconv.ParseInt(omitted, 10, 64)
	return v, err == nil
}

// emitHelperSliceExpr emits a slice expression as a call to its reslice helper,
// ogo_reslice_<T>(ptr, cap, low, high) -- or ogo_reslice3_<T>(..., max) when a third
// bound sets the capacity -- which builds the header and, in a checked build, panics
// on bounds that are out of range. Being a call, it evaluates each bound exactly
// once, in order, whether or not the check is there.
func (e *emitter) emitHelperSliceExpr(cname, ptr, baseLen, capExpr string, low, high, max []int32) {
	if e.checks {
		e.needPanic()
	}
	e.resliceCalled = true
	switch elem := sliceElemFromCName(cname); {
	case cname == cString:
		e.usesResliceStr = true
		e.emit("ogo_reslice_str(")
	case max != nil:
		e.needSlice(elem)
		e.reslice3Elems[elem] = true
		e.emit(reslice3CName(elem) + "(")
	default:
		e.needSlice(elem)
		e.resliceElems[elem] = true
		e.emit(resliceCName(elem) + "(")
	}
	e.emit(ptr + ", " + capExpr + ", ")
	if low != nil {
		e.emitExpr(low)
	} else {
		e.emit("0")
	}
	e.emit(", ")
	if high != nil {
		e.emitExpr(high)
	} else {
		e.emit(baseLen)
	}
	if max != nil {
		e.emit(", ")
		e.emitExpr(max)
	}
	e.emit(")")
}

// isStringVarName reports whether base names a string-typed variable, a named type
// over string included -- what it is asked for is the representation, and a value of
// `type Name string` is a string.
// isStringConstName reports whether a name is a folded string constant.
func (e *emitter) isStringConstName(base string) bool {
	_, ok := e.foldedStr(base)
	return ok
}

// stringConstParts renders a folded string constant as the two pieces an
// ogo_string is made of: the C string literal and its length.
//
// A string constant is emitted as its literal at every use -- a Go constant has no
// address, so there is nothing to point at -- which means an index or a slice of
// one cannot read ".str" and ".len" off a variable, there being no variable. It
// used to emit them anyway, naming something no C declaration had ever produced.
func (e *emitter) stringConstParts(base string) (ptr, length string, ok bool) {
	v, ok := e.foldedStr(base)
	if !ok {
		return "", "", false
	}
	return cQuote(v), strconv.Itoa(len(v)), true
}

func (e *emitter) isStringVarName(base string) bool {
	ct, ok := e.varType(base)
	return ok && e.underlyingCType(ct) == cString
}

// varReprType is varType with the name resolved away: the C type that decides how a
// variable's value is represented, rather than the one it is declared as. The
// declared name is what a C declaration and a method lookup want; this is what every
// question about the value's shape wants.
func (e *emitter) varReprType(name string) (string, bool) {
	ct, ok := e.varType(name)
	if !ok {
		return "", false
	}
	return e.underlyingCType(ct), true
}

// hasSliceVar reports whether base names a slice variable.
func (e *emitter) hasSliceVar(base string) bool { _, ok := e.sliceElem(base); return ok }

// needPanic records that ogo_panic is reachable and pulls in its includes (printf,
// abort, and _waitms / _reboot from propeller2.h).
func (e *emitter) needPanic() {
	e.usesPanic = true
	e.includes["stdio.h"] = true
	e.includes["stdlib.h"] = true
	e.includes["propeller2.h"] = true
}

// nilCheckedC wraps a POINTER expression in the nil check, so a dereference through
// it panics rather than reading or writing address zero. ctype is the pointer's own
// C type, which the result is cast back to; the caller dereferences what comes back
// exactly as it would have dereferenced ptr.
//
// It is the one place the check is applied, so every dereference site reads the same
// and none can be half-converted. With checks off it is the identity, as the bounds
// check is.
func (e *emitter) nilCheckedC(ptr, ctype string) string {
	if !e.checks {
		return ptr
	}
	// A pointer to an ARRAY is left unchecked, and this is a backend defect rather
	// than a choice. flexcc DROPS an assignment made through a pointer-to-array that
	// came out of a function: given `(*guard(po))[0] = x` it writes nothing at all,
	// silently, where the host compiler writes. Reduced to a dozen lines of C in
	// doc/ptr-to-array-through-call.c. Wrapping the pointer in a comma expression
	// instead of a call fails the same way, so there is no form of the check that
	// leaves the write intact -- and a check that costs the store it guards would be
	// a far worse bargain than the one it buys.
	//
	// Nothing else in the emitter generates that shape: an assignment through a
	// CALL's result is refused ("only simple and field assignment targets are
	// supported yet"), so this defect is reachable only by adding the wrapper, and
	// not adding it is what keeps it unreachable.
	if _, isArrPtr := e.arrayPtrCType(ctype); isArrPtr {
		return ptr
	}
	e.needPanic()
	e.nilHelpers[ctype] = true
	return nilHelperName(ctype) + "(" + ptr + ")"
}

// nilCheckedPtrVar is nilCheckedC for a pointer VARIABLE, looking its own C type up
// so the caller need not. A variable whose type is not known is left alone: there is
// nothing to cast the result back to.
func (e *emitter) nilCheckedPtrVar(name string) string {
	ct, ok := e.varType(name)
	if !ok || !e.isPointer(ct) {
		return e.varRef(name)
	}
	return e.nilCheckedC(e.varRef(name), ct)
}

// emitIndex emits an index expression, wrapping it in a bounds check ogo_bound(i,
// len) unless checks are disabled, the container's length is unknown (lenExpr ""),
// or the index is a constant provably in range. lenExpr is the container's length:
// a slice's ".len", or an array's compile-time bound.
func (e *emitter) emitIndex(idxAST []int32, lenExpr string) {
	if !e.checks || lenExpr == "" || e.constIndexInRange(idxAST, lenExpr) {
		e.emitExpr(idxAST)
		return
	}
	e.needPanic()
	e.usesBound = true
	e.emit("ogo_bound(")
	e.emitExpr(idxAST)
	e.emit(", " + lenExpr + ")")
}

// constIndexInRange reports whether idxAST is an integer literal provably within
// [0, lenExpr) -- both decimal constants and the index in range -- so its bounds
// check can be skipped. A runtime length (a slice's ".len") never parses as an int.
func (e *emitter) constIndexInRange(idxAST []int32, lenExpr string) bool {
	tok, ok := e.soleToken(idxAST)
	if !ok || e.f.ch(tok) != INT {
		return false
	}
	i, err1 := strconv.Atoi(normalizeIntLit(e.src(tok)))
	n, err2 := strconv.Atoi(lenExpr)
	return err1 == nil && err2 == nil && i >= 0 && i < n
}

// isIntLiteral reports whether an operand is a bare integer literal (a non-zero
// constant divisor needs no divide-by-zero check; a constant zero is a compile
// error the C backend rejects).
func (e *emitter) isIntLiteral(n Node) bool {
	tok, ok := e.soleToken(n.ast)
	return ok && e.f.ch(tok) == INT
}

// newBacking returns a fresh, translation-unit-unique name for a make() backing
// array.
func (e *emitter) newBacking() string {
	s := "ogo_backing_" + strconv.Itoa(e.makeN)
	e.makeN++
	return s
}

// peelToFactorAST descends single-child expression wrappers (Expression/SimpleExpr/
// Term/UnaryExpr) and returns the innermost node's child AST -- the Factor level.
func (e *emitter) peelToFactorAST(ast []int32) []int32 {
	cur := ast
	for {
		kids := slices.Collect(it(cur))
		if len(kids) == 1 && kids[0].sym != 0 {
			cur = kids[0].ast
			continue
		}
		return cur
	}
}

// makeSliceInit recognises a `make([]T, len [, cap])` initializer, returning the
// element C type and the length/capacity expression ASTs (capAST nil for the
// two-argument form, where cap == len). ok is false for any other expression.
func (e *emitter) makeSliceInit(initExpr []int32) (elem string, lenAST, capAST []int32, ok bool) {
	recv, suffix, isCall := e.directCall(initExpr)
	if !isCall || recv != "make" || len(suffix) != 1 || suffix[0].sym != CallSuffix {
		return "", nil, nil, false
	}
	args := e.callArgExprs(suffix[0].ast)
	if len(args) < 2 || len(args) > 3 {
		return "", nil, nil, false
	}
	// The first argument is the slice type as a factor; read its element type. A
	// DEFINED slice type names one too -- "make(List, n)" over "type List []int" is
	// Go's. litSliceType is the resolver for a TYPE written out, as against
	// sliceType, which also answers for a VARIABLE and must not resolve a name (see
	// its comment: a variable of a defined slice type keeps its own name, which is
	// what its methods hang off).
	if elem, ok = e.litSliceType(e.peelToFactorAST(args[0].ast)); !ok {
		return "", nil, nil, false
	}
	lenAST = args[1].ast
	if len(args) == 3 {
		capAST = args[2].ast
	}
	return elem, lenAST, capAST, true
}

// emitMakeSliceVar emits a slice variable initialized by make: a fixed backing
// array sized by the capacity (a compile-time constant, since the P2 has no heap)
// plus a { ptr, len, cap } header over it. static drives file-scope emission -- a
// `static` backing that C zero-inits vs a stack backing zeroed explicitly (P2 stack
// locals are not auto-zeroed).
// emitMakeSliceAssign assigns a fresh make([]T, ...) slice to an existing lvalue --
// a slice variable or a struct field -- by hoisting a local backing array and
// assigning a { backing, len, cap } header to lhs (distinct from emitMakeSliceVar,
// which declares a new variable).
func (e *emitter) emitMakeSliceAssign(lhs, cname, elem string, lenAST, capAST []int32) {
	sizeAST := capAST
	if sizeAST == nil {
		sizeAST = lenAST // the two-argument form: cap == len
	}
	size, ok := e.arrayBoundC(sizeAST)
	if !ok {
		e.fail("make needs a constant capacity")
		return
	}
	backing := e.newBacking()
	e.ind()
	e.emit(elem + " " + backing + "[" + size + "] = {0};\n")
	e.ind()
	e.emit(lhs + " = (" + cname + "){" + backing + ", ")
	if capAST != nil {
		e.emitExpr(lenAST)
	} else {
		e.emit(size)
	}
	e.emit(", " + size + "};\n")
}

func (e *emitter) emitMakeSliceVar(name, cname, elem string, lenAST, capAST []int32, static bool) {
	sizeAST := capAST
	if sizeAST == nil {
		sizeAST = lenAST // the two-argument form: cap == len
	}
	size, ok := e.arrayBoundC(sizeAST)
	if !ok {
		e.fail("make needs a constant capacity")
		return
	}
	backing := e.newBacking()
	if !static {
		e.frameBacked[name] = true // the backing array is a local of this frame
	}
	// Backing array.
	if static {
		e.emit("static " + elem + " " + backing + "[" + size + "];\n")
	} else {
		e.ind()
		e.emit(elem + " " + backing + "[" + size + "] = {0};\n")
	}
	// Header { backing, len, cap }. cap == the backing size; len is the initial
	// length (the size for the two-argument form).
	if static {
		e.emit("static ")
	} else {
		e.ind()
	}
	e.emit(cname + " " + name + " = {" + backing + ", ")
	if capAST != nil {
		e.emitExpr(lenAST)
	} else {
		e.emit(size)
	}
	e.emit(", " + size + "};\n")
}

// forHeader decomposes a "for" header. The grammar parses a leading Expression
// before it knows what the header is, so that Expression is the condition when
// nothing follows it, an init statement's left-hand side when a three-clause tail
// follows, or a range key when a range tail does -- the same left-factoring
// SwitchGuard uses.
type forHeader struct {
	// Three-clause / condition form.
	initLHS []int32 // nil when there is no init statement
	initOp  Symbol  // ASSIGN or DEFINE
	initRHS []int32
	cond    []int32 // nil for a conditionless loop
	postLHS []int32 // nil when there is no post statement
	postOp  Symbol  // ASSIGN, DEFINE, INC or DEC
	postRHS []int32
	// The list forms of the init and post, for the multiple-assignment shapes
	// `for i, j := 0, 9; ...; i, j = i+1, j-1`. Each is filled for EVERY clause,
	// one entry included, so a reader has one place to look; the singles above stay
	// for the paths that only ever see one and would otherwise all grow an index.
	initLHSs  [][]int32
	initRHSs  [][]int32
	postLHSs  [][]int32
	postRHSs  [][]int32
	hasClause bool

	// Range form.
	isRange   bool
	rangeExpr []int32 // the ranged operand
	keyVar    []int32 // the index variable, nil for `for range x`
	valVar    []int32 // the value variable, for `for i, v := range x`
	rangeDef  bool    // ":=" rather than "="
	keyStore  string  // emit-time: for an assigning clause, the variable the loop's counter is copied into each iteration
}

// parseForHeader reads a ForHeader node.
func (e *emitter) parseForHeader(n Node) (h forHeader, ok bool) {
	kids := slices.Collect(it(n.ast))
	// A header that opens with "range" is the no-variable form `for range x`.
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == RANGE {
		h.isRange = true
		for _, c := range kids {
			if c.sym == Expression {
				h.rangeExpr = c.ast
			}
		}
		return h, true
	}
	// A header opening with ";" is the three-clause form with an EMPTY init,
	// `for ; cond ; post`. It carries both semicolons and the post clause as its own
	// children rather than in a ForRest, which the walk below does not read: the
	// post was dropped, so the loop never advanced and ran forever.
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == SEMICOLON {
		h.hasClause = true
		for _, c := range kids {
			switch c.sym {
			case Expression:
				h.cond = c.ast // there is no init to mistake it for
			case ForPost:
				if !e.parseForPost(c, &h) {
					return h, false
				}
			}
		}
		return h, true
	}
	for _, c := range kids {
		switch c.sym {
		case Expression:
			// The leading expression: the condition, unless a tail reassigns it.
			h.cond = c.ast
		case ForRest:
			if !e.parseForRest(c, &h) {
				return h, false
			}
		}
	}
	return h, true
}

// parseForRest reads the ForRest following the leading Expression, distinguishing
// the three-clause tail from the range tail.
// containsTok reports whether any terminal among nodes satisfies pred.
func containsTok(nodes []Node, pred func(int32) bool) bool {
	for _, n := range nodes {
		if n.sym == 0 && pred(n.tok) {
			return true
		}
	}
	return false
}

func (e *emitter) parseForRest(n Node, h *forHeader) bool {
	kids := slices.Collect(it(n.ast))
	// `, val := range x`: a comma makes this the two-variable range form, with the
	// leading expression as the key.
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == COMMA {
		if containsTok(kids, func(t int32) bool { return e.f.ch(t) == RANGE }) {
			h.isRange = true
			h.keyVar, h.cond = h.cond, nil
			seenRange := false
			for _, c := range kids {
				switch {
				case c.sym == 0 && e.f.ch(c.tok) == DEFINE:
					h.rangeDef = true
				case c.sym == 0 && e.f.ch(c.tok) == RANGE:
					seenRange = true
				case c.sym == Expression && !seenRange:
					h.valVar = c.ast
				case c.sym == Expression && seenRange:
					h.rangeExpr = c.ast
				}
			}
			return true
		}
		// `for i, j := 0, 9; cond; post`: the leading expression was the first name.
		h.hasClause = true
		h.initLHSs = append(h.initLHSs, h.cond)
		h.cond = nil
		semis, seenOp := 0, false
		for _, c := range kids {
			switch {
			case c.sym == 0 && e.f.ch(c.tok) == SEMICOLON:
				semis++
			case c.sym == 0 && e.f.ch(c.tok) == COMMA:
				// separates one name or value from the next
			case c.sym == 0 && semis == 0:
				h.initOp = e.f.ch(c.tok)
				seenOp = true
			case c.sym == Expression && semis == 0 && !seenOp:
				h.initLHSs = append(h.initLHSs, c.ast)
			case c.sym == Expression && semis == 0:
				h.initRHSs = append(h.initRHSs, c.ast)
			case c.sym == Expression && semis == 1:
				h.cond = c.ast
			case c.sym == ForPost:
				if !e.parseForPost(c, h) {
					return false
				}
			}
		}
		if len(h.initLHSs) != len(h.initRHSs) || len(h.initLHSs) == 0 {
			e.fail("a for-loop init declares %d names from %d values", len(h.initLHSs), len(h.initRHSs))
			return false
		}
		h.initLHS, h.initRHS = h.initLHSs[0], h.initRHSs[0]
		return true
	}
	// Otherwise a leading semicolon or an assignment operator, then ForAssignRest.
	for _, c := range kids {
		switch {
		case c.sym == 0 && e.f.ch(c.tok) == SEMICOLON:
			// `for expr ; cond ; post`: a bare expression as init.
			h.hasClause = true
			h.initLHS, h.cond = h.cond, nil
		case c.sym == 0:
			h.initOp = e.f.ch(c.tok)
		case c.sym == ForAssignRest:
			if !e.parseForAssignRest(c, h) {
				return false
			}
		case c.sym == Expression:
			// A bare-expression init's condition/post follow it in this node.
			h.cond = c.ast
		case c.sym == ForPost:
			if !e.parseForPost(c, h) {
				return false
			}
		}
	}
	return true
}

// parseForAssignRest reads what follows `:=`/`=`: either `range x` (the
// single-variable range form) or the RHS, condition and post of a three-clause.
func (e *emitter) parseForAssignRest(n Node, h *forHeader) bool {
	kids := slices.Collect(it(n.ast))
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == RANGE {
		h.isRange = true
		h.keyVar, h.cond = h.cond, nil
		h.rangeDef = h.initOp == DEFINE
		for _, c := range kids {
			if c.sym == Expression {
				h.rangeExpr = c.ast
			}
		}
		return true
	}
	// The three-clause form: the leading expression was the init LHS.
	h.hasClause = true
	h.initLHS, h.cond = h.cond, nil
	semis := 0
	for _, c := range kids {
		switch {
		case c.sym == 0 && e.f.ch(c.tok) == SEMICOLON:
			semis++
		case c.sym == Expression && semis == 0:
			h.initRHS = c.ast
		case c.sym == Expression && semis == 1:
			h.cond = c.ast
		case c.sym == ForPost:
			if !e.parseForPost(c, h) {
				return false
			}
		}
	}
	return true
}

// parseForPost reads a ForPost node: `i++`, `i--`, or an assignment.
func (e *emitter) parseForPost(n Node, h *forHeader) bool {
	seenOp := false
	for c := range it(n.ast) {
		switch {
		case c.sym == 0 && e.f.ch(c.tok) == COMMA:
			// separates one target or value from the next
		case c.sym == 0:
			h.postOp = e.f.ch(c.tok)
			seenOp = true
		case c.sym == Expression && !seenOp:
			h.postLHSs = append(h.postLHSs, c.ast)
		case c.sym == Expression:
			h.postRHSs = append(h.postRHSs, c.ast)
		}
	}
	if len(h.postLHSs) == 0 {
		return false
	}
	h.postLHS = h.postLHSs[0]
	if len(h.postRHSs) != 0 {
		h.postRHS = h.postRHSs[0]
	}
	// A multiple assignment needs one value per target, as it does anywhere else.
	if len(h.postLHSs) > 1 && len(h.postLHSs) != len(h.postRHSs) {
		e.fail("a for-loop post statement assigns %d values to %d targets", len(h.postRHSs), len(h.postLHSs))
		return false
	}
	return true
}

// emitSimultaneous emits `a, b = x, y` as Go means it: every value is read into a
// temporary before any target is written, so `a, b = b, a` swaps rather than
// duplicating. It is the loop post's form of what emitMultiAssign does for a
// statement.
func (e *emitter) emitSimultaneous(lhss, rhss [][]int32) {
	tmps := make([]string, len(rhss))
	for i, rhs := range rhss {
		ct, ok := e.inferCType(rhs)
		if !ok {
			ct = "int"
		}
		tmps[i] = e.newTmp()
		e.ind()
		e.emit(ct + " " + tmps[i] + " = " + e.exprC(rhs) + ";\n")
	}
	for i, lhs := range lhss {
		e.ind()
		e.emit(e.exprC(lhs) + " = " + tmps[i] + ";\n")
	}
}

func (e *emitter) emitFor(nodes []Node) {
	// A name a for header declares belongs to the statement, not to the block
	// around it (see enterScope).
	defer e.enterScope()()
	// This loop's post placement, replacing whatever an enclosing loop set: a
	// `continue` names the nearest loop, so the nearest loop's answer is the one
	// that must be in force while its body is emitted.
	e.pendingPost, e.postContLabel = nil, ""
	var body []int32
	var h forHeader
	for _, n := range nodes[1:] {
		switch n.sym {
		case ForHeader:
			var ok bool
			if h, ok = e.parseForHeader(n); !ok {
				e.fail("unsupported for-loop header")
				return
			}
		case Block:
			body = n.ast
		default:
			e.fail("for-loop clause %v is not supported yet", n.sym)
			return
		}
	}
	if body == nil {
		e.fail("for-loop without a body")
		return
	}
	if h.isRange {
		e.emitRange(&h, body)
		return
	}
	// A loop's condition is re-evaluated on every iteration, so a temporary hoisted
	// out of it cannot be left standing before the loop, which is where emitStatement
	// would otherwise place it -- there it would be computed once, and the condition
	// would go on testing that one value. The condition is therefore rendered here
	// with its prologue held back: when there is one the loop is written with no
	// condition of its own and the test moves to the top of its body, where the
	// temporary is rebuilt each time round; when there is none, which is every loop
	// until such an expression appears in a condition, nothing about the emitted loop
	// changes.
	// The loop variable's type is recorded before the condition is rendered: the
	// condition names that variable, and how it is compared follows its type. It
	// used to be recorded further down, where the init clause is written, and the
	// condition was rendered against whatever the name meant OUTSIDE the loop --
	// which was the same thing in every loop that shadows nothing, and a string
	// comparison of two ints in one that shadows a string.
	// A multi-name init is declared in a block around the loop; blockInit says one
	// was opened, so it is closed after the body.
	blockInit := false
	initName, initCType := "", ""
	if h.hasClause && h.initLHS != nil && h.initOp == DEFINE {
		initName = e.exprC(h.initLHS)
		var ok bool
		if initCType, ok = e.inferCType(h.initRHS); !ok {
			e.fail("cannot infer the type of a for-loop init variable")
			return
		}
		e.locals[initName] = initCType
	}
	var condText string
	var condPro []string
	if h.cond != nil {
		if h.hasClause {
			condText, condPro = e.capturePrologue(func() { e.emit(e.exprC(h.cond)) })
			if len(condPro) != 0 {
				condText = "(" + condText + ")" // emitCondition's form brings its own
			}
		} else {
			condText, condPro = e.capturePrologue(func() { e.emitCondition(h.cond) })
		}
	}
	var inject func()
	if len(condPro) != 0 {
		inject = func() {
			for _, line := range condPro {
				e.ind()
				e.emit(line)
			}
			e.ind()
			e.emit("if (!" + condText + ") break;\n")
		}
	}
	e.ind()
	if !h.hasClause {
		// The one- and two-part forms keep their existing lowering: a conditionless
		// loop is `for (;;)`, a conditional one a `while`. A condition whose test has
		// moved into the body leaves the loop conditionless here too.
		switch {
		case h.cond == nil, inject != nil:
			e.emit("for (;;) {\n")
		default:
			e.emit("while " + condText + " {\n")
		}
	} else {
		// The three-clause form maps onto C's own, including the init declaration:
		// C scopes a variable declared there to the loop, exactly as Go does.
		if len(h.initLHSs) > 1 {
			// C's init clause declares one type; two names of different types cannot
			// share it. A block around the loop declares them and scopes them to it,
			// which is where Go scopes them too.
			e.emit("{\n")
			e.indent++
			for i, lhs := range h.initLHSs {
				ct, ok := e.inferCType(h.initRHSs[i])
				if !ok {
					ct = "int"
				}
				name := e.exprC(lhs)
				if h.initOp == DEFINE {
					e.locals[name] = ct
					e.ind()
					e.emit(ct + " " + name + " = " + e.exprC(h.initRHSs[i]) + ";\n")
					continue
				}
				e.ind()
				e.emit(name + " = " + e.exprC(h.initRHSs[i]) + ";\n")
			}
			e.ind()
			// The declarations are made; the loop's own init clause is empty.
			h.initLHS = nil
			blockInit = true
		}
		e.emit("for (")
		if h.initLHS != nil {
			lhs := e.exprC(h.initLHS)
			switch h.initOp {
			case DEFINE:
				e.emit(initCType + " " + initName + " = " + e.exprC(h.initRHS))
			case ASSIGN:
				e.emit(lhs + " = " + e.exprC(h.initRHS))
			default:
				e.emit(lhs)
			}
		}
		e.emit("; ")
		if inject == nil {
			e.emit(condText)
		}
		e.emit("; ")
		if len(h.postLHSs) > 1 {
			// A MULTIPLE assignment cannot be C's third clause: Go assigns
			// simultaneously, which needs temporaries, and that clause is an
			// expression with nowhere to declare one. So the loop takes an empty post
			// and the statements go at the END OF THE BODY, behind a label an
			// unlabeled `continue` jumps to -- a plain continue would otherwise skip
			// them. Falling off the bottom reaches them the same way.
			e.labelSeq++
			e.postContLabel = fmt.Sprintf("ogo_post_%d", e.labelSeq)
			lhss, rhss := h.postLHSs, h.postRHSs
			e.pendingPost = func() { e.emitSimultaneous(lhss, rhss) }
			e.emit(") {\n")
			e.emitLoopBody(body, inject)
			if blockInit {
				e.indent--
				e.ind()
				e.emit("}\n")
			}
			return
		}
		if h.postLHS != nil {
			// The post statement runs after every iteration and on every continue, so
			// C's third clause is the only place it fits -- and that clause takes an
			// expression, with nowhere to declare the temporary a value might need.
			post, postPro := e.capturePrologue(func() {
				lhs := e.exprC(h.postLHS)
				switch h.postOp {
				case INC:
					e.emit(lhs + "++")
				case DEC:
					e.emit(lhs + "--")
				case ASSIGN, DEFINE:
					e.emit(lhs + " = " + e.exprC(h.postRHS))
				default:
					e.emit(lhs)
				}
			})
			if len(postPro) != 0 {
				e.fail("a for-loop post statement may not need a temporary; compute the value in the loop body instead")
				return
			}
			e.emit(post)
		}
		e.emit(") {\n")
	}
	e.emitLoopBody(body, inject)
	if blockInit {
		e.indent--
		e.ind()
		e.emit("}\n")
	}
}

// capturePrologue renders through emit and returns the text along with any prologue
// lines the rendering hoisted out of itself, keeping them from reaching the
// enclosing statement. A caller uses it where emitStatement's placement -- before
// the whole statement -- would be the wrong place for a temporary.
func (e *emitter) capturePrologue(emit func()) (text string, pro []string) {
	saved := e.prologue
	e.prologue = nil
	text = e.captureC(emit)
	pro = e.prologue
	e.prologue = saved
	return text, pro
}

// emitLoopBody emits a loop body between the opening `{` and the closing `}`,
// running inject (if any) as the body's first statement -- the range value copy --
// and restoring the switch context, since a break inside the loop names the loop.
func (e *emitter) emitLoopBody(body []int32, inject func()) {
	e.indent++
	e.deferBlockDepth++
	// A labeled continue targets a label at the end of this loop's body, so a jump
	// there falls through to the loop's post step and re-test. Captured and cleared
	// on entry so a nested loop does not inherit this loop's target.
	cont := e.pendingContLabel
	e.pendingContLabel = ""
	// The post statements of a loop whose post cannot fit C's third clause, and the
	// label a `continue` in THIS body jumps to so it runs them. emitFor sets both
	// (to nil and "" for an ordinary loop), so a nested loop replaces rather than
	// inherits them; pendingPost is cleared here because it belongs to this loop
	// alone, while postContLabel stays for the body about to be emitted.
	post, postLabel := e.pendingPost, e.postContLabel
	e.pendingPost = nil
	savedBreak := e.switchBreak
	e.switchBreak = ""
	if inject != nil {
		inject()
	}
	e.emitBlockStmts(body)
	if cont != "" && e.labelUsed[cont] {
		e.ind()
		e.emit(cont + ":;\n")
	}
	if post != nil {
		if e.labelUsed[postLabel] {
			e.ind()
			e.emit(postLabel + ":;\n")
		}
		post()
	}
	e.switchBreak = savedBreak
	e.deferBlockDepth--
	e.indent--
	e.ind()
	e.emit("}\n")
}

// emitRange emits the range forms of "for". Each becomes a counting loop; the
// operand is evaluated once (hoisted to a temporary), matching Go, and the
// two-variable form copies the element into the value variable at the top of each
// iteration.
func (e *emitter) emitRange(h *forHeader, body []int32) {
	// `range Row(a)` for `type Row [3]int`: a conversion to a defined array type
	// changes nothing about the value -- the typedef stands for the same storage --
	// so it is unwrapped and the operand is what is ranged. An array is the one
	// representation C has no value type for, so every path that reads an array
	// operand reads a NAME, and would not otherwise see through the conversion.
	if operand, ok := e.arrayConvOperand(h.rangeExpr); ok {
		h.rangeExpr = operand
	}
	// The representation decides what ranging means -- a value of `type Name string`
	// is a string and yields its runes -- so the name is resolved away here.
	ct, _ := e.exprReprCType(h.rangeExpr)
	// An assigning clause, `for i, v = range xs`, writes variables that already
	// exist rather than declaring new ones. Only a variable can be one: rendering an
	// element or a field as an assignment target is what the assignment paths do,
	// and a range clause does not reach them.
	if !h.rangeDef {
		for _, v := range [][]int32{h.keyVar, h.valVar} {
			if v == nil {
				continue
			}
			if _, ok := e.exprIdent(v); !ok && !e.isFieldTarget(v) {
				e.fail("a range target must be a variable or a struct field")
				return
			}
		}
	}
	key := "_"
	if h.keyVar != nil {
		key = e.exprC(h.keyVar)
		if !h.rangeDef && key != "_" {
			// The counter stays the loop's own, and the clause's variable is written
			// from it at the top of each iteration -- which is where Go assigns it, so
			// after the loop it holds the last index, as Go leaves it.
			h.keyStore = key
			key = e.newTmp()
		}
	}
	if key == "_" {
		key = e.newTmp() // `for range x`, or `for _ := range x`: a hidden counter
	}

	// A literal operand, `range []int{1, 2}`: it has no name for the loop to read its
	// length off, and a slice one's backing storage has to be declared somewhere.
	// Bind it to a temporary exactly as a declaration would -- which is where the
	// backing array comes from -- and range over that. Nothing escapes: the loop
	// reads elements, and the value variable is a copy. Bound before the switch, so
	// the binding happens once however the operand turns out to be shaped.
	name := e.rangeLitVar(h.rangeExpr)
	if name == "" {
		// `range mk()` for a function returning an ARRAY: the result travels through
		// an out parameter, so there is storage to range and no expression naming it.
		// Bound here for the same reason a literal is.
		name = e.rangeArrayResultVar(h.rangeExpr)
	}
	switch {
	case name != "":
		if a, isArray := e.arrays[name]; isArray {
			e.emitRangeArray(h, body, key, a, name)
			break
		}
		e.emitRangeSlice(h, body, key, e.locals[name], name)
	// Asked ahead of the slice case, which reads the operand's C type: for a chain
	// that STARTS at a slice and reaches an array field, `range hs[1].xs`, that type
	// comes back as the slice's, and the loop then ranged the header rather than the
	// field. The operand's own shape is what decides, and a slice has none -- this
	// answers for arrays only, so no slice is claimed here.
	case e.rangeArray(h.rangeExpr) != nil:
		text, a, ok := e.rangeArrayBase(h.rangeExpr, h.valVar != nil)
		if !ok {
			// The predicate reads the operand's SHAPE and this reads its storage, so
			// an operand whose shape is known and whose storage cannot be named lands
			// here. Said rather than ranged over an empty base.
			e.fail("cannot range over this array: it has no storage to name")
			return
		}
		e.emitRangeArray(h, body, key, a, text)
	case e.isSliceCType(ct):
		// Hoist the slice header so .len and .ptr come from one evaluation.
		hdr := e.newTmp()
		e.ind()
		e.emit(ct + " " + hdr + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.emitRangeSlice(h, body, key, ct, hdr)
	case ct == cString:
		// Ranging a string iterates its runes, as Go does (not its bytes): key is the
		// byte index of each rune's start, the two-variable value is the decoded rune,
		// and the index advances by the rune's UTF-8 width. That width lives in a
		// variable declared before the loop so the for-increment (i += w) and the
		// body's decode (which sets it) share it; for ASCII every width is 1, so the
		// index-only form still counts 0,1,2,...
		hdr := e.newTmp()
		e.ind()
		e.emit("ogo_string " + hdr + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.locals[key] = "int"
		e.usesRuneDecode = true
		width := e.newTmp()
		e.ind()
		e.emit("int " + width + " = 0;\n")
		e.ind()
		e.emit("for (int " + key + " = 0; " + key + " < " + hdr + ".len; " + key + " += " + width + ") {\n")
		val := "_"
		if h.valVar != nil {
			val = e.exprC(h.valVar)
		}
		if h.rangeDef && val != "_" {
			e.locals[val] = "int" // a rune is int32, i.e. int on the P2
		}
		inject := func() {
			if h.keyStore != "" {
				e.ind()
				e.emit(h.keyStore + " = " + key + ";\n")
			}
			e.ind()
			if val == "_" {
				// No rune variable (`for range s` or `for i := range s`): decode only
				// to advance the index by the rune width, discarding the rune itself.
				e.emit("ogo_decode_rune(" + hdr + ", " + key + ", &" + width + ");\n")
				return
			}
			decl := ""
			if h.rangeDef {
				decl = "int "
			}
			e.emit(decl + val + " = ogo_decode_rune(" + hdr + ", " + key + ", &" + width + ");\n")
		}
		e.emitLoopBody(body, inject)
	default:
		// A POINTER that is not one to an array, which the checker leaves here when
		// its type model could not resolve the pointee (see indexingPointer). Said
		// before the integer range below, which would otherwise report it as one.
		if base, ok := e.exprIdent(h.rangeExpr); ok {
			if ct, ok := e.varType(base); ok && e.isPointer(ct) {
				e.fail("cannot range over %s: only a pointer to an array is rangeable", base)
				return
			}
		}
		// An integer range. Hoist the bound so a side-effecting or costly operand is
		// evaluated once, as Go does.
		if h.valVar != nil {
			e.fail("ranging an integer yields only the index")
			return
		}
		n := e.newTmp()
		e.ind()
		e.emit("int " + n + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.locals[key] = "int"
		e.ind()
		e.emit("for (int " + key + " = 0; " + key + " < " + n + "; " + key + "++) {\n")
		e.emitLoopBody(body, e.rangeValueInject(h, key, "int", ""))
	}
}

// rangeValueInject returns the closure that opens each iteration of a range loop:
// the value variable of a two-variable form, declared by a ":=" clause and merely
// assigned by an "=" one, and -- for an "=" clause -- the key variable, copied from
// the loop's own counter. It is nil when there is nothing to write. elem is the
// element C type and access the C expression reading the current element.
// isFieldTarget reports whether an expression is a struct field access, `s.i`,
// which a range clause may assign into: the field path renders it as an lvalue,
// which is what writing it each iteration needs. An ELEMENT target is not one --
// indexing renders a bounds check around the read, not a place to write.
func (e *emitter) isFieldTarget(v []int32) bool {
	base, fields, ok := e.factorFieldAccess(e.factorKids(v))
	if !ok || len(fields) == 0 {
		return false
	}
	_, isField := e.fieldType(base, fields)
	return isField
}

func (e *emitter) rangeValueInject(h *forHeader, key, elem, access string) func() {
	var lines []func()
	if h.keyStore != "" {
		store := h.keyStore
		lines = append(lines, func() { e.ind(); e.emit(store + " = " + key + ";\n") })
	}
	if h.valVar != nil {
		if val := e.exprC(h.valVar); val != "_" { // "_" discards the value
			e.noteRangeValueHolder(h.rangeExpr, val, elem)
			// An ARRAY element is COPIED, as Go copies it, and C cannot assign one:
			// `T v = xs.ptr[i]` is not an initializer it accepts. A ":=" clause
			// declares the value here; an "=" clause writes into a variable that
			// already exists, so it emits the copy alone -- and asks first that the
			// two are the same array, since the copy is sized by the destination.
			if a, isArr := e.namedArrays[elem]; isArr {
				lines = append(lines, func() {
					if h.rangeDef {
						e.locals[val] = elem
						e.emitArrayCopy(val, access, a)
						return
					}
					if dst, isArr := e.arrayVar(val); isArr &&
						(dst.elem != a.elem || dst.declSuffix() != a.declSuffix()) {
						e.fail("cannot use %s as %s in range clause",
							e.goArrayTypeName(a), e.goArrayTypeName(dst))
						return
					}
					e.includes["string.h"] = true
					e.ind()
					e.emit("memcpy(" + val + ", " + access + ", sizeof(" + val + "));\n")
				})
			} else {
				decl := ""
				if h.rangeDef {
					e.locals[val] = elem
					decl = elem + " "
				}
				lines = append(lines, func() { e.ind(); e.emit(decl + val + " = " + access + ";\n") })
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return func() {
		for _, line := range lines {
			line()
		}
	}
}

// emitRangeSlice emits the counting loop over a slice named by hdr, whose header
// has already been given a name.
func (e *emitter) emitRangeSlice(h *forHeader, body []int32, key, ct, hdr string) {
	e.locals[key] = "int"
	e.ind()
	e.emit("for (int " + key + " = 0; " + key + " < " + hdr + ".len; " + key + "++) {\n")
	e.emitLoopBody(body, e.rangeValueInject(h, key, e.sliceElemByName[ct], hdr+".ptr["+key+"]"))
}

// emitRangeArray emits the counting loop over an array named by base, bounded by
// its compile-time extent.
func (e *emitter) emitRangeArray(h *forHeader, body []int32, key string, a arrDim, base string) {
	e.locals[key] = "int"
	e.ind()
	e.emit("for (int " + key + " = 0; " + key + " < " + a.bound + "; " + key + "++) {\n")
	// sliceElemOfArray, not a.elem: ranging a [2][3]int yields ROWS, and a.elem is
	// the innermost element, `int`. The row's typedef is what tells the value inject
	// it has an array to COPY -- the same registry it reads for a slice of arrays,
	// which is why `for _, row := range xs` was right where `range m` declared
	// `int row = m[i]` and did not compile.
	e.emitLoopBody(body, e.rangeValueInject(h, key, e.sliceElemOfArray(a), base+"["+key+"]"))
}

// rangeArrayResultVar binds a range operand that is a call returning an ARRAY to a
// fresh local and returns its name, or "" for any other operand.
func (e *emitter) rangeArrayResultVar(rangeExpr []int32) string {
	cname, a, ok := e.arrayResultCall(rangeExpr)
	if !ok {
		return ""
	}
	name := e.newTmp()
	e.ind()
	e.emit(a.elem + " " + name + a.declSuffix() + ";\n")
	e.emitArrayResultCall(name, cname, rangeExpr)
	e.arrays[name] = a
	return name
}

// rangeLitVar binds a range operand that is an array or slice literal to a fresh
// local, the way a declaration of one would, and returns that local's name; it
// returns "" for any other operand. Emitting it here rather than through hoist is
// what gives a slice literal its backing array, which is two declarations, not one.
func (e *emitter) rangeLitVar(rangeExpr []int32) string {
	typeAST, lit, ok := e.soleArrayLit(rangeExpr)
	if !ok {
		return ""
	}
	name := e.newTmp()
	e.emitArrayLitVar(name, typeAST, lit, false)
	return name
}

// rangeArray returns the array dimension of a range operand that is an array, or
// nil.
func (e *emitter) rangeArray(expr []int32) *arrDim {
	// A pointer to an array is ranged AS the array, `range p` being `range *p`, and
	// is the one spelling arrayShapeOf does not answer for -- it reads the value's
	// own shape, and a pointer's is a pointer's.
	if base, ok := e.exprIdent(expr); ok {
		if a, isPtr := e.arrayPtrVar(base); isPtr {
			return &a
		}
	}
	if a, ok := e.arrayShapeOf(expr); ok {
		return &a
	}
	return nil
}

// rangeArrayBase resolves a range operand that is an ARRAY to the C text naming its
// storage and its extents. Four spellings reach the same array: the variable itself,
// a pointer to it -- `range p` being `range *p` -- that dereference written out, and
// a chain of fields and indexes, `h.xs` / `h.in.xs` / `pool[1]`.
//
// The chain is bound to a POINTER temporary and ranged through that. Go evaluates a
// range expression ONCE, and a chain written into the loop's base would be evaluated
// per iteration instead: `range pool[i]` would re-read i -- and re-check its bound --
// every time round, and would follow i if the body changed it. The pointer is what
// the `range p` spelling already produces, so nothing downstream is new.
//
// It RENDERS, so it is asked exactly once; rangeArray is the pure predicate that
// decides whether to ask at all.
func (e *emitter) rangeArrayBase(expr []int32, readsElements bool) (string, arrDim, bool) {
	if base, ok := e.exprIdent(expr); ok {
		if a, isPtr := e.arrayPtrVar(base); isPtr {
			return "(*" + e.varRef(base) + ")", a, true
		}
		if a, isArr := e.arrayVar(base); isArr {
			return base, a, true
		}
		return "", arrDim{}, false
	}
	if text, a, ok := e.arrayDerefOperand(expr); ok {
		return text, a, true
	}
	// The field form is asked first because it keeps the type's NAME, so the pointer
	// is spelled `Row*` rather than by a mint.
	text, a, ok := e.arrayFieldOperand(expr)
	if !ok {
		if text, a, ok = e.arrayChainOperand(expr); !ok {
			return "", arrDim{}, false
		}
	}
	if !readsElements {
		// The index-only form, `for i := range h.xs`, reaches no element, so there
		// is nothing for the base to name and binding one anyway leaves a temporary
		// the C compiler reports as unused. The extents are what the loop needs, and
		// they come off the shape.
		return text, a, true
	}
	tmp := e.newTmp()
	e.ind()
	e.emit(e.arrayTypedef(a) + "* " + tmp + " = &" + text + ";\n")
	return "(*" + tmp + ")", a, true
}

// emitSwitch emits a switch statement as an if / else-if chain. Case bodies are
// independent, a fallthrough repeating the next one, and Go case expressions may
// be non-constant, which a C switch could not express. The shapes handled are:
//
//	switch x { case a, b: ... }        // value switch:      _t = x;  (_t == a || _t == b)
//	switch { case cond: ... }          // expression switch: (cond)
//	switch v := expr { case a: ... }   // guard-var switch:  v = expr; (v == a)
//	switch v := e; t { case a: ... }   // init and tag:      v = e; _t = t; (_t == a)
//	switch v := e; { case cond: ... }  // init only:         v = e; (cond)
//
// The guard is evaluated once (into the guard variable, or a temporary for a
// non-trivial value), so it is not re-run per case. The default clause, wherever
// it appears in source, becomes the trailing else.
func (e *emitter) emitSwitch(ast []int32) {
	// A name a switch header declares belongs to the statement, not to the block
	// around it (see enterScope).
	defer e.enterScope()()
	var guardAST []int32
	var cases []Node
	for n := range it(ast) {
		switch n.sym {
		case SwitchGuard:
			guardAST = n.ast
		case CaseClause:
			cases = append(cases, n)
		}
	}

	// A TYPE switch is a different statement wearing a switch's clothes: its cases
	// name types rather than values, and the name it binds has a different type in
	// each clause. It is lowered on its own, sharing nothing below but the break
	// label, which emitTypeSwitch mints for itself.
	if guardAST != nil {
		if ts, ok := e.typeSwitchGuard(guardAST); ok {
			if ts.bindText != "" {
				// The bound operand is scoped to the statement, like a guard
				// variable, so it goes in a block of its own.
				e.ind()
				e.emit("{\n")
				e.indent++
				e.ind()
				e.emit(ts.iface + " " + ts.operand + " = " + ts.bindText + ";\n")
				e.locals[ts.operand] = ts.iface
				e.emitTypeSwitch(ts, cases)
				e.indent--
				e.ind()
				e.emit("}\n")
				return
			}
			e.emitTypeSwitch(ts, cases)
			return
		}
	}

	// Resolve the guard variable to compare against ("" for an expression switch),
	// emitting an enclosing block + declaration when it needs a scoped name.
	guardVar, block := "", false
	if guardAST != nil {
		var ok bool
		guardVar, block, ok = e.emitSwitchGuard(guardAST)
		if !ok {
			return
		}
	}

	// A break in any case exits the switch. With the if/else lowering that is a
	// forward goto to a label after the chain, minted here and emitted below only
	// if a break referenced it. Saved and restored so a nested switch, or a loop
	// in a case (emitLoopBody), can set its own target.
	label := fmt.Sprintf("ogo_break_%d", e.switchBreakSeq)
	e.switchBreakSeq++
	savedBreak := e.switchBreak
	e.switchBreak = label
	// A labeled switch binds its label to this same end label, so "break L" from a
	// case reaches it exactly as a plain break does. Consumed once, then unbound
	// after the switch is fully emitted.
	srcLabel := e.pendingSwitchLabel
	e.pendingSwitchLabel = ""
	if srcLabel != "" {
		e.labelBreak[srcLabel] = label
	}

	defaultIdx := -1
	wrote := false
	for i, cc := range cases {
		exprs, isDefault := e.caseHead(cc.ast)
		if isDefault {
			defaultIdx = i
			continue
		}
		if !wrote {
			e.ind()
			e.emit("if ")
			wrote = true
		} else {
			e.emit(" else if ")
		}
		e.emitCaseCond(guardVar, exprs)
		e.emit(" {\n")
		e.indent++
		e.emitCaseFrom(cases, i)
		e.indent--
		e.ind()
		e.emit("}")
	}
	switch {
	case defaultIdx >= 0 && wrote:
		e.emit(" else {\n")
		e.indent++
		e.emitCaseFrom(cases, defaultIdx)
		e.indent--
		e.ind()
		e.emit("}\n")
	case defaultIdx >= 0: // a switch of only a default clause
		e.ind()
		e.emit("{\n")
		e.indent++
		e.emitCaseFrom(cases, defaultIdx)
		e.indent--
		e.ind()
		e.emit("}\n")
	case wrote:
		e.emit("\n")
	}

	e.switchBreak = savedBreak
	if e.switchBreakUsed[label] || e.labelUsed[label] {
		e.ind()
		e.emit(label + ":;\n")
	}
	if srcLabel != "" {
		delete(e.labelBreak, srcLabel)
	}

	if block {
		e.indent--
		e.ind()
		e.emit("}\n")
	}
}

// typeSwitch describes a "switch v := x.(type)" -- the name it binds (empty for the
// bare "switch x.(type)"), the operand, and the interface type the operand holds,
// which is what each case's type is looked up against.
type typeSwitch struct {
	name    string
	operand string
	iface   string

	// bindText is the C text of an operand that is not a plain name -- `rs[i]`,
	// `b.r`, `mk()` -- which the statement binds to a temporary and switches on
	// that. Empty when the operand names a variable, which needs no binding.
	bindText string
}

// typeSwitchGuard recognises a type switch's guard. The Selector that spells
// ".(type)" carries the keyword and no Type child, which is exactly what tells it
// from the assertion ".(T)" the same production admits.
func (e *emitter) typeSwitchGuard(guardAST []int32) (ts typeSwitch, ok bool) {
	g, ok := e.f.switchGuardParts(guardAST)
	if !ok {
		return ts, false
	}
	value := g.tag
	if g.hasName {
		value = g.value
	}
	base, prefix, isTypeSwitch := e.typeSwitchOperand(value.ast)
	if !isTypeSwitch {
		return ts, false
	}
	if g.hasName {
		if ts.name, ok = e.exprIdent(g.name.ast); !ok {
			e.fail("a type switch binds a name")
			return ts, false
		}
	}
	if len(prefix) != 0 {
		// The operand is not a name -- `rs[i].(type)`, `b.r.(type)`. Everything
		// below reads the operand by NAME, once per case, so it is bound to a
		// temporary and the cases switch on that. Binding is also what makes the
		// operand evaluated once, which is what Go does and what an index with a
		// side effect would otherwise get wrong.
		text, ctype, _, okChain := e.chainCText(base, prefix)
		if !okChain || !e.isIfaceCType(ctype) {
			e.fail("a type switch needs an operand of interface type; bind %s to a variable and switch on that", base)
			return ts, false
		}
		ts.operand, ts.iface, ts.bindText = e.newTmp(), ctype, text
		return ts, true
	}
	ts.operand = base
	if ts.iface, ok = e.varType(base); !ok || !e.isIfaceCType(ts.iface) {
		e.fail("%s is not an interface, so it has no dynamic type to switch on", base)
		return ts, false
	}
	return ts, true
}

// typeSwitchOperand splits an "x.(type)" expression into the base identifier and
// the suffix steps that reach the operand -- everything before the ".(type)"
// selector, which is empty when the operand is the base itself. A guard that does
// not end in ".(type)" is not a type switch and is reported as such.
func (e *emitter) typeSwitchOperand(ast []int32) (base string, prefix []Node, ok bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term || nodes[0].sym == UnaryExpr) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != Factor {
		return "", nil, false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	steps := slices.Collect(it(kids[1].ast))
	last := len(steps) - 1
	if last < 0 || steps[last].sym != Selector {
		return "", nil, false
	}
	for c := range it(steps[last].ast) {
		if c.sym == 0 && e.f.ch(c.tok) == TYPE {
			return e.src(kids[0].tok), steps[:last], true
		}
	}
	return "", nil, false
}

// caseTypeC resolves a type-switch case expression -- "*T", or "nil" -- to the
// concrete C type it names. A nil case tests the zero interface value, which
// carries no table at all.
//
// A case reads as an EXPRESSION, the grammar having no other place to put it, so
// "*T" arrives as a deref of a name rather than as a Type. Reading it here is what
// keeps the grammar out of it.
// caseIfaceC recognises a type-switch case naming an INTERFACE, `case T:`, and
// answers with its C name.
func (e *emitter) caseIfaceC(ex Node) (string, bool) {
	if name, ok := e.exprIdent(ex.ast); ok {
		mn := mangle(e.curPkgPrefix, name)
		return mn, e.isIfaceCType(mn)
	}
	// `case geo.Sizer:` -- another package's interface, which arrives as a selector
	// rather than a name and so has no sole identifier to read.
	fac, isFac := e.soleFactorNode(ex.ast)
	if !isFac {
		return "", false
	}
	qual, member, isQual := e.qualifiedFactor(fac.ast)
	if !isQual {
		return "", false
	}
	prefix, isImport := e.importQualifiers[qual]
	if !isImport {
		return "", false
	}
	mn := mangle(prefix, member)
	return mn, e.isIfaceCType(mn)
}

// ifaceCaseCond renders the test for a case naming an INTERFACE. A clause like that
// matches on the METHOD SET: any dynamic type implementing it takes the clause, so
// the first such clause wins and the order they are written in is what decides
// between two interfaces a type satisfies.
//
// What makes it decidable here is that the program is closed: the emitter knows
// every type and every method, so the set of dynamic types that could be in the
// operand AND implement the case is a list it can write out. The test is that list
// of table comparisons, OR-ed -- the same comparison a concrete case makes, once per
// type that qualifies.
//
// The EMPTY interface is the one that needs no list: every type implements it, so
// what it asks is only that the value hold something.
func (e *emitter) ifaceCaseCond(operand, operandIface, caseIface string) (string, []string, bool) {
	if len(e.ifaceMethods[caseIface]) == 0 {
		return e.varRef(operand) + ".vt != 0", nil, true
	}
	var conds, types []string
	for _, ct := range e.ifaceImplementors(caseIface) {
		// Only a type that could BE in the operand: the table compared against is
		// the one the operand's interface would have used, and a type that does not
		// implement that interface never had one made.
		if !e.implementsIface(ct, operandIface) {
			continue
		}
		if !e.needVTable(operandIface, ct) {
			return "", nil, false
		}
		conds = append(conds, e.assertOKC(operand, operandIface, ct))
		types = append(types, ct)
	}
	if len(conds) == 0 {
		// No type in this program satisfies both, so the clause is dead. Emitted as
		// an unreachable test rather than refused: a case for an interface nothing
		// implements YET is a reasonable thing to write, and Go accepts it too.
		return "0", nil, true
	}
	return strings.Join(conds, " || "), types, true
}

// ifaceImplementors names every type satisfying iface, sorted for reproducibility.
func (e *emitter) ifaceImplementors(iface string) []string {
	var out []string
	for _, ct := range sortedKeys(e.typeNames) {
		if e.isIfaceCType(ct) {
			continue // an interface is not a dynamic type here; a pointer goes in
		}
		if e.implementsIface(ct, iface) {
			out = append(out, ct)
		}
	}
	return out
}

// implementsIface reports whether concrete has every method iface declares. It is
// needVTable's question without the emission, for a caller asking about a type it
// may then decline.
func (e *emitter) implementsIface(concrete, iface string) bool {
	for _, m := range e.ifaceMethods[iface] {
		// Resolved through the embedding chain, exactly as needVTable resolves it.
		// Reading the method off the concrete type DIRECTLY made a promoted method
		// invisible here while needVTable saw it, so the two disagreed about which
		// types implement an interface -- and this one decides which candidates an
		// interface-to-interface assertion or case tests. With no candidate the test
		// is emitted as a constant 0, so `r.(N)` on a type whose R-method is
		// promoted answered FALSE and said nothing about it.
		if _, _, _, has := e.promotedMethod(concrete, m.name); !has {
			return false
		}
	}
	return true
}

func (e *emitter) caseTypeC(ex Node) (concrete string, isNil, ok bool) {
	if e.isNilExpr(ex.ast) {
		return "", true, true
	}
	// An INTERFACE named bare, `case T:`. Recognised before the pointer shape,
	// which it does not have: `*T` would be a pointer to an interface, and this is
	// the interface itself.
	if name, isIface := e.caseIfaceC(ex); isIface {
		return name, false, true
	}
	nodes := slices.Collect(it(ex.ast))
	for len(nodes) == 1 && (nodes[0].sym == Expression || nodes[0].sym == SimpleExpr || nodes[0].sym == Term) {
		nodes = slices.Collect(it(nodes[0].ast))
	}
	if len(nodes) != 1 || nodes[0].sym != UnaryExpr {
		return "", false, false
	}
	kids := slices.Collect(it(nodes[0].ast))
	if len(kids) != 2 || kids[0].sym != UnaryOp || kids[1].sym != Factor {
		return "", false, false
	}
	if tok, isOp := e.unaryOpTok(kids[0].ast); !isOp || e.f.ch(tok) != MUL {
		return "", false, false
	}
	// `case *geo.Quad:` -- another package's type. A case reads as an expression, so
	// the qualified spelling arrives as a SELECTOR, and it is asked FIRST: soleIdent
	// reads the leading identifier and stops, so `geo.Quad` answered "geo", which is
	// not a type here and refused every qualified case as "a type switch case names a
	// pointer type" about one that does. Mangled into that package's namespace, which
	// is where its typedef was emitted.
	if qual, member, isQual := e.qualifiedFactor(kids[1].ast); isQual {
		if prefix, isImport := e.importQualifiers[qual]; isImport {
			concrete = mangle(prefix, member)
			if !e.isStruct(concrete) && !e.isUserType(concrete) {
				return "", false, false
			}
			return concrete, false, true
		}
	}
	name := e.soleIdent(kids[1].ast)
	if name == "" {
		return "", false, false
	}
	concrete = mangle(e.curPkgPrefix, name)
	if !e.isStruct(concrete) && !e.isUserType(concrete) {
		return "", false, false
	}
	return concrete, false, true
}

// qualifiedFactor reads a Factor spelled "qual.member" -- one identifier and one
// selector -- and returns the two names. It is soleIdent for the qualified spelling,
// and exists for the positions that read a TYPE out of the expression grammar, which
// is the only grammar a type switch case has.
func (e *emitter) qualifiedFactor(ast []int32) (qual, member string, ok bool) {
	kids := slices.Collect(it(ast))
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", "", false
	}
	steps := slices.Collect(it(kids[1].ast))
	if len(steps) != 1 || steps[0].sym != Selector {
		return "", "", false
	}
	for c := range it(steps[0].ast) {
		if c.sym == 0 && e.f.ch(c.tok) == IDENT {
			member = e.src(c.tok)
		}
	}
	if member == "" {
		return "", "", false
	}
	return e.src(kids[0].tok), member, true
}

// emitTypeSwitch lowers a type switch to the chain of table comparisons it is. Each
// clause binds the name at the type that clause proved: the concrete pointer where
// one type was named, and the interface value itself where several were, or none --
// which is Go's rule, and the reason a clause cannot share one declaration with the
// statement.
func (e *emitter) emitTypeSwitch(ts typeSwitch, cases []Node) {
	label := fmt.Sprintf("ogo_break_%d", e.switchBreakSeq)
	e.switchBreakSeq++
	savedBreak := e.switchBreak
	e.switchBreak = label
	srcLabel := e.pendingSwitchLabel
	e.pendingSwitchLabel = ""
	if srcLabel != "" {
		e.labelBreak[srcLabel] = label
	}

	// The bound name is declared inside each clause's block, so its type may differ
	// per clause. The emitter has no scopes of its own, so what it records for the
	// name is restored after the statement rather than after each clause.
	savedType, hadType := e.locals[ts.name], e.locals[ts.name] != ""

	defaultIdx, wrote := -1, false
	for i, cc := range cases {
		exprs, isDefault := e.caseHead(cc.ast)
		if isDefault {
			defaultIdx = i
			continue
		}
		var conds []string
		concrete, single := "", len(exprs) == 1
		caseIface, ifaceTypes := "", []string(nil)
		for _, ex := range exprs {
			ct, isNil, ok := e.caseTypeC(ex)
			if !ok {
				e.fail("a type switch case names a pointer type, an interface type, or nil")
				return
			}
			switch {
			case isNil:
				conds = append(conds, e.varRef(ts.operand)+".vt == 0")
				single = false // nil binds the interface value, not a concrete one
			case e.isIfaceCType(ct):
				cond, types, ok := e.ifaceCaseCond(ts.operand, ts.iface, ct)
				if !ok {
					return
				}
				conds = append(conds, cond)
				caseIface, ifaceTypes = ct, types
			default:
				if !e.needVTable(ts.iface, ct) {
					return
				}
				conds = append(conds, e.assertOKC(ts.operand, ts.iface, ct))
				concrete = ct
			}
		}
		if !wrote {
			e.ind()
			e.emit("if (")
			wrote = true
		} else {
			e.emit(" else if (")
		}
		e.emit(strings.Join(conds, " || ") + ") {\n")
		e.indent++
		if single && caseIface != "" {
			// One INTERFACE named: Go binds the name at that interface, so what the
			// clause gets is a value of it -- the same data word, beside the table
			// for the pair the operand turned out to hold. Which pair that is, is
			// what the condition just decided between, so it is asked again here.
			e.bindTypeSwitchIface(ts, caseIface, ifaceTypes)
		} else {
			e.bindTypeSwitchName(ts, concrete, single)
		}
		e.emitCaseFrom(cases, i)
		e.indent--
		e.ind()
		e.emit("}")
	}
	emitDefault := func() {
		e.bindTypeSwitchName(ts, "", false)
		e.emitCaseFrom(cases, defaultIdx)
	}
	switch {
	case defaultIdx >= 0 && wrote:
		e.emit(" else {\n")
		e.indent++
		emitDefault()
		e.indent--
		e.ind()
		e.emit("}\n")
	case defaultIdx >= 0:
		e.ind()
		e.emit("{\n")
		e.indent++
		emitDefault()
		e.indent--
		e.ind()
		e.emit("}\n")
	case wrote:
		e.emit("\n")
	}

	if hadType {
		e.locals[ts.name] = savedType
	} else {
		delete(e.locals, ts.name)
	}
	e.switchBreak = savedBreak
	if e.switchBreakUsed[label] || e.labelUsed[label] {
		e.ind()
		e.emit(label + ":;\n")
	}
	if srcLabel != "" {
		delete(e.labelBreak, srcLabel)
	}
}

// bindTypeSwitchName declares the name a type switch binds, inside the clause that
// proved its type. A clause naming one type gets that pointer; every other clause
// gets the interface value itself, as in Go.
// bindTypeSwitchIface binds the type switch's name at the INTERFACE a clause named.
// The data word carries over unchanged -- it is the same pointer -- and the table is
// the one for (that interface, whatever concrete type the operand holds), which the
// clause's own condition has narrowed to the listed types but not chosen between.
//
// So it is chosen here, by the same comparison, once per candidate. With one
// candidate that is a straight assignment; with several it is a chain, which is the
// price of a case that matches a set rather than a type.
func (e *emitter) bindTypeSwitchIface(ts typeSwitch, caseIface string, types []string) {
	if ts.name == "" || ts.name == "_" {
		return
	}
	e.locals[ts.name] = caseIface
	e.ind()
	e.emit(caseIface + " " + e.varRef(ts.name) + " = {0};\n")
	text, ok := e.ifaceRebindC(e.varRef(ts.name), caseIface, ts.operand, ts.iface, types, e.indent)
	if !ok {
		return
	}
	e.emit(text)
	e.ind()
	e.emit("(void)" + e.varRef(ts.name) + ";\n")
}

// hoistAssert binds a type assertion's value to a temporary and answers with its
// name and C type, so a SUFFIX after the assertion has a base to apply to. It is
// the one lowering both targets share: an interface one is already statements, and
// a concrete one becomes statements here because the panic check is one.
//
// The temporary is registered as a local, which is what lets the chain and call
// paths type it exactly as they type any other variable.
func (e *emitter) hoistAssert(operand, iface, target string, targetIsIface bool) (string, string, bool) {
	if targetIsIface {
		name, ok := e.hoistIfaceAssert(operand, iface, target)
		if !ok {
			return "", "", false
		}
		e.locals[name] = target
		return name, target, true
	}
	if !e.needVTable(iface, target) {
		return "", "", false
	}
	e.needPanic()
	name := e.newTmp()
	e.prologue = append(e.prologue,
		"if (!("+e.assertOKC(operand, iface, target)+")) ogo_panic(\"interface conversion: "+
			e.goTypeName(iface)+" is not *"+e.goTypeName(target)+"\");\n",
		target+"* "+name+" = "+e.assertValueC(operand, target)+";\n")
	e.locals[name] = target + "*"
	return name, target + "*", true
}

// hoistIfaceAssert emits `v.(T)` for an INTERFACE T standing as one value: the
// check that it holds, a panic when it does not, and the value itself, bound to a
// temporary declared before the statement. The temporary is what the expression
// becomes, since building an interface value is statements and a cast is not.
//
// What it asserts is the METHOD SET -- the same list of table comparisons a type
// switch case for T makes, which is where the two meet.
func (e *emitter) hoistIfaceAssert(operand, iface, target string) (string, bool) {
	cond, types, ok := e.ifaceCaseCond(operand, iface, target)
	if !ok {
		return "", false
	}
	name := e.newTmp()
	e.needPanic()
	rebind, ok := e.ifaceRebindC(name, target, operand, iface, types, 1)
	if !ok {
		return "", false
	}
	e.prologue = append(e.prologue,
		target+" "+name+" = {0};\n",
		"if (!("+cond+")) ogo_panic(\"interface conversion: "+e.goTypeName(iface)+
			" is not "+e.goTypeName(target)+"\");\n",
		rebind)
	return name, true
}

// emitIfaceAssertOk emits `t, ok := v.(T)` for an INTERFACE T. ok is computed
// first and t reads it, as in the concrete form and for the same reason: t is the
// ZERO interface value when the assertion does not hold, which is what Go gives.
func (e *emitter) emitIfaceAssertOk(targets []assignTarget, declare []bool, operand, iface, target string) {
	cond, types, ok := e.ifaceCaseCond(operand, iface, target)
	if !ok {
		return
	}
	okTmp := e.newTmp()
	e.ind()
	e.emit("int " + okTmp + " = " + cond + ";\n")
	val := e.newTmp()
	e.ind()
	e.emit(target + " " + val + " = {0};\n")
	rebind, ok := e.ifaceRebindC(val, target, operand, iface, types, e.indent+1)
	if !ok {
		return
	}
	e.ind()
	e.emit("if (" + okTmp + ") {\n")
	e.emit(rebind)
	e.ind()
	e.emit("}\n")
	e.emitStore(targets[0], declare[0], target, val)
	e.emitStore(targets[1], declare[1], cBool, okTmp)
}

// ifaceRebindC renders the statements that view an interface value AS another
// interface: the data word carries over unchanged -- it is the same pointer -- and
// the table becomes the one for (dst's interface, whatever concrete type the source
// holds). Which that is, is what the caller's own test narrowed to types but did not
// choose between, so it is asked once more here, once per candidate.
//
// Shared by the type switch's binding and the assertion, which build the same value
// from the same two words and differ only in what decided to build it.
func (e *emitter) ifaceRebindC(dst, dstIface, src, srcIface string, types []string, indent int) (string, bool) {
	var b strings.Builder
	ind := func() {
		for range indent {
			b.WriteString("\t")
		}
	}
	ind()
	fmt.Fprintf(&b, "%s.data = %s.data;\n", dst, e.varRef(src))
	for i, ct := range types {
		if !e.needVTable(dstIface, ct) {
			return "", false
		}
		ind()
		if i != 0 {
			b.WriteString("else ")
		}
		fmt.Fprintf(&b, "if (%s) %s.vt = &%s;\n", e.assertOKC(src, srcIface, ct), dst, ifaceVTVar(dstIface, ct))
	}
	return b.String(), true
}

func (e *emitter) bindTypeSwitchName(ts typeSwitch, concrete string, single bool) {
	if ts.name == "" || ts.name == "_" {
		return
	}
	ct, init := ts.iface, e.varRef(ts.operand)
	if single && concrete != "" {
		ct, init = concrete+"*", e.assertValueC(ts.operand, concrete)
	}
	e.locals[ts.name] = ct
	e.ind()
	e.emit(ct + " " + e.varRef(ts.name) + " = " + init + ";\n")
	// Go's rule is that the name is unused only when it is unused in EVERY clause,
	// which the checker asks; the C compiler would warn per clause, so each
	// declaration is used here.
	e.ind()
	e.emit("(void)" + e.varRef(ts.name) + ";\n")
}

// emitSwitchGuard emits the guard of a switch that has one, returning the name to
// compare cases against ("" when it switches on true), whether an enclosing block
// was opened (to close after the chain), and ok. The block is what scopes an init
// statement's name and any temporary to the switch, as Go scopes them, and it is
// opened at most once however many of the two are needed.
//
// Whatever the guard binds is declared as the ordinary local it is, through
// emitInferredLocal, rather than by writing the declaration here. That is what
// records it in e.locals, so emitCaseCond knows its type, and what captures a
// self-referential initializer before the new name shadows the outer one.
//
// A plain variable is compared directly; anything richer is bound to a temporary,
// so the guard is evaluated once rather than per case. The name returned is the
// source one, which the type lookups are keyed by; emitCaseCond spells it in C
// through varRef.
// guardNames reads the names a multi-value switch guard declares, head first.
func (e *emitter) guardNames(g switchGuard) ([]string, bool) {
	head, ok := e.exprIdent(g.name.ast)
	if !ok {
		e.fail("a switch init statement declares names")
		return nil, false
	}
	names := []string{head}
	for _, item := range g.items {
		t, ok := e.lhsItemTarget(item.ast)
		if !ok || !t.plain() {
			e.fail("a switch init statement declares names")
			return nil, false
		}
		names = append(names, t.name)
	}
	return names, true
}

func (e *emitter) emitSwitchGuard(guardAST []int32) (guardVar string, block, ok bool) {
	g, ok := e.f.switchGuardParts(guardAST)
	if !ok || (g.semi && !g.hasName) {
		e.fail("malformed switch guard")
		return "", false, false
	}
	openBlock := func() {
		if !block {
			e.ind()
			e.emit("{\n")
			e.indent++
			block = true
		}
	}
	if g.hasName && len(g.items) != 0 {
		// `switch v, ok := f(); ok`: the same destructuring the statement form uses,
		// inside the block that scopes the names to this statement.
		names, ok := e.guardNames(g)
		if !ok {
			return "", false, false
		}
		openBlock()
		e.emitDestructure(plainTargets(names), allTrue(len(names)), g.value.ast)
	} else if g.hasName {
		vtok, isID := e.soleToken(g.name.ast)
		if !isID || e.f.ch(vtok) != IDENT {
			e.fail("unsupported switch guard variable")
			return "", false, false
		}
		if _, tok := e.inferCType(g.value.ast); !tok {
			e.fail("cannot infer the type of the switch guard variable")
			return "", false, false
		}
		openBlock()
		// Declared before the expression switched on is typed, since that one may
		// name it -- which is the whole point of an init statement.
		e.emitInferredLocal(e.src(vtok), g.value.ast)
	}
	if !g.hasTag { // `switch v := e; {` -- an expression switch with v in scope
		return "", block, true
	}
	if tok, single := e.soleToken(g.tag.ast); single && e.f.ch(tok) == IDENT {
		return e.src(tok), block, true
	}
	if _, tok := e.inferCType(g.tag.ast); !tok {
		e.fail("cannot infer the type of the switch value")
		return "", false, false
	}
	tmp := e.newTmp()
	openBlock()
	e.emitInferredLocal(tmp, g.tag.ast)
	return tmp, block, true
}

// caseHead returns a case clause's case expressions and whether it is the default.
func (e *emitter) caseHead(cc []int32) (exprs []Node, isDefault bool) {
	for n := range it(cc) {
		if n.sym != CaseHead {
			continue
		}
		for h := range it(n.ast) {
			switch h.sym {
			case ExpressionList:
				for ex := range it(h.ast) {
					if ex.sym == Expression {
						exprs = append(exprs, ex)
					}
				}
			case 0:
				if e.f.ch(h.tok) == DEFAULT {
					isDefault = true
				}
			}
		}
	}
	return exprs, isDefault
}

// emitCaseCond emits a case's condition: for a value switch, the guard equals any
// of the case expressions (`guard == a || guard == b`); for an expression switch
// (guardVar ""), the case expressions are themselves the conditions (`a || b`).
func (e *emitter) emitCaseCond(guardVar string, exprs []Node) {
	// A string switch compares the guard against each case by content, not with C's
	// `==` on the { ptr, len } struct (see emitStringCompare). The guard is looked
	// up by its source name and written by its C one; the two differ for a Unicode
	// name and for a package variable, which carries its package's prefix.
	cname, stringGuard := "", false
	if guardVar != "" {
		cname = e.varRef(guardVar)
		ct, _ := e.varReprType(guardVar)
		stringGuard = ct == cString
	}
	e.emit("(")
	for i, ex := range exprs {
		if i != 0 {
			e.emit(" || ")
		}
		if stringGuard {
			e.usesString = true
			e.usesStringEq = true
			e.emit("ogo_string_eq(" + cname + ", ")
			e.emitExpr(ex.ast)
			e.emit(")")
			continue
		}
		if cname != "" {
			e.emit(cname + " == ")
		}
		e.emitExpr(ex.ast)
	}
	e.emit(")")
}

// emitCaseBody emits the statements of a case clause (those following its ":").
// A trailing "fallthrough" emits nothing itself: it is realised by emitCaseFrom,
// which appends the next clause's body.
func (e *emitter) emitCaseBody(cc []int32) {
	// A break here names the switch. emitSwitch sets switchBreak (the break's goto
	// target) around the whole chain, and emitLoopBody clears it inside a loop, so
	// nothing to toggle here.
	e.deferBlockDepth++
	defer func() { e.deferBlockDepth-- }()
	for n := range it(cc) {
		if n.sym == Statement && !e.isFallthroughStmt(n.ast) {
			e.emitStatement(n.ast)
		}
	}
}

// emitCaseFrom emits the body of clause i and, when that body ends in a
// "fallthrough", the body of the clause after it in *source* order -- repeating,
// so a chain of fallthroughs works.
//
// The next clause's body is emitted again here rather than jumped to. A switch
// lowers to an if/else chain, which has no place to jump into: a C label inside
// one arm cannot be reached from another without jumping into a block. Repeating
// the body costs code space (a chain of k clauses can repeat the last body k
// times) but needs no change to the lowering, so a switch without a fallthrough
// emits exactly the C it did before. Source order is what matters here, not
// emission order: a default clause is hoisted to the trailing else wherever it was
// written, yet "fallthrough" continues into whichever clause was written next.
func (e *emitter) emitCaseFrom(clauses []Node, i int) {
	e.emitCaseBody(clauses[i].ast)
	if i+1 >= len(clauses) || !e.clauseFallsThrough(clauses[i].ast) {
		return
	}
	// The appended body gets its own C block. Each clause is a scope of its own in
	// the source, so two clauses may declare the same name; emitted into the block
	// the falling-through clause already occupies, the second declaration would be
	// a C redefinition of the first.
	e.ind()
	e.emit("{\n")
	e.indent++
	e.emitCaseFrom(clauses, i+1)
	e.indent--
	e.ind()
	e.emit("}\n")
}

// clauseFallsThrough reports whether a case clause's last non-empty statement is
// a "fallthrough". The checker has already refused one anywhere else, so this is
// the only position that has to be recognised.
func (e *emitter) clauseFallsThrough(cc []int32) bool {
	r := false
	for n := range it(cc) {
		if n.sym != Statement || isEmptyStatement(n) {
			continue
		}
		r = e.isFallthroughStmt(n.ast)
	}
	return r
}

// isFallthroughStmt reports whether a Statement is exactly "fallthrough".
func (e *emitter) isFallthroughStmt(stmt []int32) bool {
	for n := range it(stmt) {
		if n.sym == 0 && e.f.ch(n.tok) == FALLTHROUGH {
			return true
		}
	}
	return false
}

// emitIf emits an if statement (the IfStmt node):
//
//	IfStmt = "if" HeaderExpression [ IfInit ] Block [ "else" ( IfStmt | Block ) ] .
//
// An init statement -- `if v := f(); v > 0` -- becomes a C block around the whole
// if, holding the variable's declaration. The block is what scopes v to the
// statement, as Go scopes it: visible in the condition and every branch, gone
// afterwards, and free to shadow a name from outside without disturbing it.
//
// It indents the leading `if`, then defers to emitIfBody, which handles the
// condition, the branch, and any `else`/`else if` continuation.
func (e *emitter) emitIf(ast []int32) {
	// A name an "if" header declares belongs to the statement, not to the block
	// around it (see enterScope).
	defer e.enterScope()()
	names, initExpr, cond, ok := e.ifInitParts(ast)
	if !ok {
		e.ind()
		e.emitIfBody(ast)
		return
	}
	e.ind()
	e.emit("{\n")
	e.indent++
	if len(names) > 1 {
		// `if v, ok := f(); ok`: the same destructuring the statement form uses,
		// inside the brace block that scopes the names to this statement.
		e.emitDestructure(plainTargets(names), allTrue(len(names)), initExpr)
	} else {
		e.emitInferredLocal(names[0], initExpr)
	}
	e.ind()
	e.emitIfBodyWithCond(ast, cond) // ends its own line
	e.indent--
	e.ind()
	e.emit("}\n")
}

// ifInitParts decomposes an `if` that carries an init statement, returning the
// declared name, its initializer and the condition. ok is false for a plain `if`,
// whose sole expression is the condition itself.
func (e *emitter) ifInitParts(ast []int32) (names []string, initExpr, cond []int32, ok bool) {
	var lhs, init []int32
	for n := range it(ast) {
		switch n.sym {
		case Expression:
			if lhs == nil {
				lhs = n.ast
			}
		case IfInit:
			init = n.ast
		}
	}
	if init == nil {
		return nil, nil, nil, false
	}
	var exprs [][]int32
	var items []Node
	for n := range it(init) {
		switch n.sym {
		case Expression:
			exprs = append(exprs, n.ast)
		case LhsItem:
			items = append(items, n)
		}
	}
	if len(exprs) != 2 || lhs == nil {
		e.fail("malformed if init statement")
		return nil, nil, nil, false
	}
	head, ok := e.exprIdent(lhs)
	if !ok {
		e.fail("an if init statement declares names")
		return nil, nil, nil, false
	}
	names = []string{head}
	for _, item := range items {
		t, ok := e.lhsItemTarget(item.ast)
		if !ok || !t.plain() {
			e.fail("an if init statement declares names")
			return nil, nil, nil, false
		}
		names = append(names, t.name)
	}
	return names, exprs[0], exprs[1], true
}

// emitIfBody emits `if (cond) { ... }` and its optional else branch, assuming the
// cursor is already positioned — an initial indent for a top-level if, or the
// `} else ` written by an enclosing call for an `else if`. It recurses on an
// `else if` continuation so the C reads `} else if (c) {` on one line.
func (e *emitter) emitIfBody(ast []int32) { e.emitIfBodyWithCond(ast, nil) }

// emitIfBodyWithCond is emitIfBody with the condition supplied, which the init
// form needs: there the statement's own expression is the declared name and the
// condition lives inside the IfInit.
func (e *emitter) emitIfBodyWithCond(ast []int32, condOverride []int32) {
	var cond, thenBody, elseBody, elseIf []int32
	for n := range it(ast) {
		switch n.sym {
		case Expression:
			cond = n.ast
		case IfInit:
			// Its parts were read by ifInitParts; the condition arrives as
			// condOverride.
		case Block:
			if thenBody == nil {
				thenBody = n.ast
			} else {
				elseBody = n.ast
			}
		case IfStmt:
			elseIf = n.ast
		case 0:
			// IF / ELSE terminals.
		default:
			e.fail("if clause %v is not supported yet", n.sym)
			return
		}
	}
	if condOverride != nil {
		cond = condOverride
	}
	if cond == nil || thenBody == nil {
		e.fail("malformed if statement")
		return
	}
	e.emit("if ")
	e.emitCondition(cond)
	e.emit(" {\n")
	e.indent++
	e.deferBlockDepth++
	e.emitBlockStmts(thenBody)
	e.deferBlockDepth--
	e.indent--
	e.ind()
	e.emit("}")
	switch {
	case elseIf != nil:
		e.emit(" else ")
		e.emitIfBody(elseIf)
	case elseBody != nil:
		e.emit(" else {\n")
		e.indent++
		e.deferBlockDepth++
		e.emitBlockStmts(elseBody)
		e.deferBlockDepth--
		e.indent--
		e.ind()
		e.emit("}\n")
	default:
		e.emit("\n")
	}
}

// emitCondition emits an if/for condition wrapped in exactly one set of
// parentheses. It emits the Expression's children directly (rather than the
// Expression node, which would add its own binary-operator parens) so a simple
// `i < 20` becomes `(i < 20)`, not `((i < 20))`.
func (e *emitter) emitCondition(exprChildren []int32) {
	// A condition is the Expression's children directly (not a wrapped Expression
	// node), so route them through emitLogicalKids for the same && / || grouping an
	// Expression operand gets, keeping gcc's -Wparentheses quiet in `if a && b || c`.
	// The C `if`/`while` parentheses stay in place around a lowered string compare.
	e.emit("(")
	kids := slices.Collect(it(exprChildren))
	if !e.emitStringCompare(kids) {
		e.emitLogicalKids(kids)
	}
	e.emit(")")
}

// emitReturn handles `return`, `return expr`, and `return e0, e1, ...`. A bare
// return in main yields `return 0;` to satisfy C's int main; in a void function
// it yields `return;`. A single value emits `return <expr>;`. Multiple values are
// returned as the function's result struct via a compound literal,
// `return (ogo_ret_<fn>){ e0, e1, ... };`.
// emitDefer records a `defer` statement and emits its argument capture here, where
// Go evaluates the arguments. emitDeferred replays the call in LIFO order at each
// return and at a fall-through function end. A defer in a nested block also arms a
// flag, so the replay can tell whether the block ran.
func (e *emitter) emitDefer(nodes []Node) {
	var head, lit Node
	var suffix []Node
	for _, n := range nodes {
		switch n.sym {
		case AssignHead:
			head = n
		case FuncLiteral:
			lit = n
		case Selector, Index, CallSuffix:
			suffix = append(suffix, n)
		}
	}
	// `defer func() { ... }()`: the literal is lifted here, where the defer
	// statement is written, and the replay calls the lifted function by name. It
	// takes no arguments for now -- there is nothing to capture, which is what
	// makes this the whole of it.
	if lit.sym == FuncLiteral {
		if len(suffix) != 1 || suffix[0].sym != CallSuffix || len(e.callArgExprs(suffix[0].ast)) != 0 {
			e.fail("a deferred function literal takes no arguments yet")
			return
		}
		cname, ok := e.liftFuncLit(lit)
		if !ok {
			return
		}
		e.defers = append(e.defers, deferredCall{litName: cname, cond: e.deferBlockDepth > 0, slot: len(e.defers)})
		if e.deferBlockDepth > 0 {
			e.ind()
			e.emit(deferFlagName(len(e.defers)-1) + " = 1;\n")
		}
		return
	}
	if head.sym != AssignHead || len(suffix) == 0 {
		e.fail("a defer statement must be a function call")
		return
	}
	d := deferredCall{head: head, suffix: suffix, cond: e.deferBlockDepth > 0, slot: len(e.defers)}
	// The call suffix is last; its arguments are what get captured.
	call := suffix[len(suffix)-1]
	if call.sym != CallSuffix {
		e.fail("a defer statement must be a function call")
		return
	}
	// A method call's receiver is captured too, ahead of the arguments, because Go
	// evaluates it where the defer stands: `defer w.show()` on a value receiver shows
	// what w held then, not what it holds at the return. It also has to be captured
	// for the replay to resolve at all -- the replay is emitted after the body's
	// block scope has been left, so a LOCAL receiver's name is no longer typed by
	// then, which is why a defer of a method on a local did not compile.
	recvText, ok := e.deferReceiver(&d, head, suffix)
	if !ok {
		return
	}
	if d.recvCType != "" {
		e.ind()
		// A captured ARRAY receiver is COPIED. C assigns no array, and the copy is
		// what makes the capture a capture: a value receiver must see what the array
		// held where the defer stands.
		if _, isArr := e.namedArrays[d.recvCType]; isArr {
			e.includes["string.h"] = true
			e.emit("memcpy(" + deferRecvName(d.slot) + ", " + recvText + ", sizeof(" +
				deferRecvName(d.slot) + "));\n")
		} else {
			e.emit(deferRecvName(d.slot) + " = " + recvText + ";\n")
		}
	}
	for _, a := range e.callArgExprs(call.ast) {
		if e.isIntLiteral(a) {
			d.args = append(d.args, deferArg{expr: a.ast, inline: true})
			continue
		}
		ct, ok := e.inferCType(a.ast)
		if !ok {
			e.fail("cannot infer the type of a deferred call argument")
			return
		}
		d.args = append(d.args, deferArg{ctype: ct, expr: a.ast})
	}
	for i, a := range d.args {
		if a.inline {
			continue
		}
		e.ind()
		e.emit(deferArgName(d.slot, i) + " = ")
		e.emitExpr(a.expr)
		e.emit(";\n")
	}
	if d.cond {
		e.ind()
		e.emit(deferFlagName(d.slot) + " = 1;\n")
	}
	e.defers = append(e.defers, d)
}

// deferReceiver classifies a deferred call whose callee is a method, filling in the
// slot's receiver type and the method's C name and returning the C text the receiver
// is captured from. A plain function call, a call into an imported package and a
// call through a struct field holding a function value are all left alone: they name
// no receiver, so there is nothing to evaluate early.
//
// The pointer adjustment happens at the capture, not at the call: a pointer-receiver
// method captures the address, so it sees later writes exactly as Go says, and a
// value-receiver method captures a copy, so it does not.
func (e *emitter) deferReceiver(d *deferredCall, head Node, suffix []Node) (string, bool) {
	base := e.soleIdent(head.ast)
	if base == "" {
		// `defer (&v).m(args)`. The head is parenthesised, so it carries no sole
		// identifier and the capture below would be skipped silently -- and a skipped
		// capture is not a refusal but a WRONG ANSWER: the receiver would be read at
		// the return instead of here, so a value receiver would show what the variable
		// holds then rather than now. `(&v).m()` is `v.m()`, so the base is v and
		// everything downstream is the shorthand's.
		var isAddr bool
		if base, isAddr = e.addrHead(head); !isAddr {
			return "", true
		}
	}
	steps := suffix[:len(suffix)-1]
	if len(steps) == 0 {
		// `defer f(args)`. If f is a VARIABLE holding a function, what is called is
		// the value it holds where the defer stands, so that value is captured like
		// a receiver; a declared function's own name names one thing forever and
		// needs nothing.
		if ct, ok := e.varType(base); ok && e.isFuncCType(ct) {
			d.recvCType = ct
			d.callsValue = true
			return e.varRef(base), true
		}
		return "", true
	}
	if steps[len(steps)-1].sym != Selector {
		return "", true
	}
	if _, isPkg := e.importQualifiers[base]; isPkg && len(steps) == 1 {
		return "", true // `defer pkg.F(args)`
	}
	method := e.soleIdent(steps[len(steps)-1].ast)
	chain := steps[:len(steps)-1]
	// The receiver's type, and the text reaching it. A chain is rendered through
	// emitAccessChain, which is what admits `ws[i].M()` and `p.ws[i].M()`.
	var ctype, text string
	if len(chain) == 0 {
		ct, ok := e.varType(base)
		if !ok {
			// An ARRAY variable has no C type -- its extents live in e.arrays and
			// nowhere else -- so the DEFINED name is what says which methods it has,
			// exactly as methodRecvCType reads it. Without this the capture was
			// skipped and the receiver read at the RETURN: `defer g.show()` printed
			// what g held then, which is a wrong answer and not a missing feature.
			a, isArr := e.arrayVar(base)
			if !isArr || a.name == "" {
				return "", true // not a variable: leave it to the call path to report
			}
			ct = a.name
		}
		ctype, text = ct, e.varRef(base)
	} else {
		cur, ok := e.accessChainType(base, chain)
		if !ok {
			return "", true
		}
		if ctype, ok = e.chainValueCType(cur); !ok {
			// An ARRAY has no C VALUE type, which is what chainValueCType answers
			// about -- but it does have a name, and a name is what a capture needs to
			// declare a slot of. `defer h.r.show()` arrives here.
			if cur.name == "" {
				return "", true
			}
			ctype = cur.name
		}
		var pro []string
		text, pro = e.capturePrologue(func() { e.emitAccessChain(base, chain) })
		for _, line := range pro {
			e.ind()
			e.emit(line)
		}
	}
	// A field holding a function value is not a method: what is called is the
	// value the field holds, and Go evaluates that where the defer stands too --
	// `defer b.run()` calls what b.run held then, not what it holds at the return.
	// So it is captured the same way, into a temporary of the function type, and
	// the replay calls through that rather than reading the field again.
	if ft, ok := e.fieldType(base, []string{method}); ok && e.isFuncCType(ft) && len(chain) == 0 {
		d.cname = ""
		d.recvCType = ft
		d.callsValue = true
		return e.fieldAccessC(base, []string{method}), true
	}
	cname := methodCName(methodBaseType(ctype), method)
	if _, isMethod := e.funcRet[cname]; !isMethod {
		return "", true
	}
	wantPtr := e.methodPtr[cname]
	recv, ok := e.chainReceiver(text, ctype, true, wantPtr)
	if !ok {
		e.fail("cannot take the address of %s for a pointer-receiver method", text)
		return "", false
	}
	d.cname = cname
	d.recvCType = methodBaseType(ctype)
	if wantPtr {
		d.recvCType += "*"
	}
	return recv, true
}

// emitDeferDecls declares the temporaries backing every defer slot in the function,
// at function scope. They must outlive the block a defer was written in, so they
// cannot be declared at the defer itself. The body is emitted into a buffer first,
// so the full set is known by the time these are written ahead of it.
func (e *emitter) emitDeferDecls() {
	for _, d := range e.defers {
		if d.cond {
			e.ind()
			e.emit("int " + deferFlagName(d.slot) + " = 0;\n")
		}
		if d.recvCType != "" {
			e.ind()
			// An ARRAY slot zeroes with braces: zeroInitC answers "0" for a name it
			// does not know to be one, and `Row r = 0` is an invalid initializer
			// rather than a warning. A zero-length array names no element to brace,
			// so it is declared without one -- as the array declarations elsewhere are.
			zero := " = " + e.zeroInitC(d.recvCType)
			if a, isArr := e.namedArrays[d.recvCType]; isArr {
				zero = " = {0}"
				if a.bound == "0" {
					zero = ""
				}
			}
			e.emit(d.recvCType + " " + deferRecvName(d.slot) + zero + ";\n")
		}
		for i, a := range d.args {
			if a.inline {
				continue
			}
			e.ind()
			// A struct, a string or a slice zeroes with braces, not with 0: C has
			// no scalar zero for an aggregate, and "= 0" is an invalid initializer
			// there rather than a warning.
			e.emit(a.ctype + " " + deferArgName(d.slot, i) + " = " + e.zeroInitC(a.ctype) + ";\n")
		}
	}
}

// emitDeferred replays the recorded defers in LIFO order, each reading the
// arguments captured at its defer statement. One written in a nested block is
// replayed under its flag, so it runs only if that block did. Defers recorded after
// this point are not replayed here, which is right: a return textually before a
// defer cannot have reached it.
func (e *emitter) emitDeferred() {
	for i := len(e.defers) - 1; i >= 0; i-- {
		d := e.defers[i]
		if d.litName != "" {
			// A lifted function literal: no name in the source to resolve again, and
			// no arguments to replay.
			e.ind()
			if d.cond {
				e.emit("if (" + deferFlagName(d.slot) + ") ")
			}
			e.emit(d.litName + "();\n")
			continue
		}
		e.deferReplay, e.deferReplayArgs = d.slot, d.args
		if d.recvCType != "" {
			// The receiver is a temporary of this function, so the call is written
			// here rather than through emitCall, which would resolve the receiver's
			// name again -- in a scope that has already been left.
			e.ind()
			if d.cond {
				e.emit("if (" + deferFlagName(d.slot) + ") ")
			}
			args := e.argsCText(d.cname, d.suffix[len(d.suffix)-1].ast)
			if d.callsValue {
				e.emit(deferRecvName(d.slot) + "(" + args + ");\n")
			} else {
				if args != "" {
					args = ", " + args
				}
				e.emit(d.cname + "(" + deferRecvName(d.slot) + args + ");\n")
			}
			e.deferReplay, e.deferReplayArgs = -1, nil
			continue
		}
		if d.cond {
			// The flag opens a BLOCK, because the call is not always one C
			// statement: println of several arguments is one printf per argument,
			// and a call that hoists a temporary writes that ahead of itself. As a
			// statement PREFIX the flag guarded the first of them and let the rest
			// run, so a deferred `println("big", n)` in a branch that never ran
			// still printed the tail of itself from its zeroed capture temporaries
			// -- " 0" on a line of its own, out of nowhere.
			e.ind()
			e.emit("if (" + deferFlagName(d.slot) + ") {\n")
			e.indent++
			e.emitCall(d.head, d.suffix)
			e.indent--
			e.ind()
			e.emit("}\n")
		} else {
			e.emitCall(d.head, d.suffix)
		}
		e.deferReplay, e.deferReplayArgs = -1, nil
	}
}

// forwardedCallC renders `f()` in `return f()`, where the one call supplies every
// result. It answers only when the callee's results are exactly this function's --
// two functions of one result list share a result struct, and nothing else can be
// returned as one.
func (e *emitter) forwardedCallC(ex Node) (string, bool) {
	callee, suffix, ok := e.directCall(ex.ast)
	if !ok {
		return "", false
	}
	_, resTypes, ok := e.callResultInfo(callee, suffix)
	if !ok || !slices.Equal(resTypes, e.curResultTypes) {
		return "", false
	}
	text := e.captureC(func() { ok = e.emitCallExpr(callee, suffix) })
	if !ok {
		return "", false
	}
	return text, true
}

// soleResultCType is the C type of a function with exactly one result, or "".
func (e *emitter) soleResultCType() string {
	if len(e.curResultTypes) != 1 {
		return ""
	}
	return e.curResultTypes[0]
}

// isDirectCall reports whether an expression is exactly `f(args)`.
func (e *emitter) isDirectCall(ex Node) bool {
	_, _, ok := e.directCall(ex.ast)
	return ok
}

func (e *emitter) emitReturn(nodes []Node) {
	var exprs []Node
	for _, n := range nodes[1:] {
		if n.sym == ExpressionList {
			for c := range it(n.ast) {
				if c.sym == Expression {
					exprs = append(exprs, c)
				}
			}
		}
	}
	e.checkReturnBacking(exprs)
	// An ARRAY result is written through the out parameter the caller supplied, not
	// returned: C cannot return an array. The copy is by size, so a
	// multi-dimensional result travels as one block.
	if a, ok := e.funcArrayRet[e.curFunc]; ok {
		if len(exprs) != 1 {
			e.fail("a function with an array result returns exactly one value")
			return
		}
		// `return mk(k)`: the caller's storage is this function's out parameter, so
		// the inner call writes into it and there is nothing left to copy.
		if cname, ca, isCall := e.arrayResultCall(exprs[0].ast); isCall {
			if ca.elem != a.elem || ca.declSuffix() != a.declSuffix() {
				e.fail("cannot return %s as %s", e.goArrayTypeName(ca), e.goArrayTypeName(a))
				return
			}
			if len(e.defers) != 0 {
				e.emitDeferred()
			}
			e.emitArrayResultCall(arrayResultParam, cname, exprs[0].ast)
			e.ind()
			e.emit("return;\n")
			return
		}
		src, srcDim, okSrc := e.arrayReturnOperand(exprs[0].ast)
		switch {
		case !okSrc:
			// Not an array this can copy from at all. Said as itself rather than
			// through the type mismatch below, whose message named the missing
			// source as the empty type "[]".
			e.fail("an array result must be returned as a variable or an array literal")
			return
		case srcDim.elem != a.elem || srcDim.declSuffix() != a.declSuffix():
			e.fail("cannot return %s as %s", e.goArrayTypeName(srcDim), e.goArrayTypeName(a))
			return
		}
		e.includes["string.h"] = true
		if len(e.defers) != 0 {
			e.emitDeferred()
		}
		e.ind()
		e.emit("memcpy(" + arrayResultParam + ", " + src + ", sizeof(" + a.elem + ")" + arrayCountC(a) + ");\n")
		e.ind()
		e.emit("return;\n")
		return
	}
	// `return f()` -- one call supplying every result, which Go allows when the
	// counts match. Both functions return the SAME C struct, result structs being
	// keyed by the result types, so the call is the return value as it stands.
	if len(exprs) == 1 && len(e.curResultTypes) > 1 {
		text, ok := e.forwardedCallC(exprs[0])
		if !ok {
			e.fail("a return supplying every result needs a call whose results are exactly %s",
				strings.Join(e.curResultTypes, ", "))
			return
		}
		// The call is BOUND to a temporary rather than returned where it stands.
		// `return f();` is valid C and gcc runs it correctly, but the target's
		// compiler miscompiles it when the result struct holds anything narrower
		// than a machine word: a (int, bool) result came back with the bool always
		// false, measured on a P2-EDGE. Binding first is correct and, unlike the
		// direct form, compiles silently. See doc/return-nonword-struct.c.
		//
		// The binding is what a defer needs anyway: Go evaluates the operand and
		// only then runs the defers, so emitting the call after them would let a
		// defer change what it reads.
		tmp := e.newTmp()
		e.ind()
		e.emit(e.retStructNameOf(e.curResultTypes) + " " + tmp + " = " + text + ";\n")
		e.emitDeferred()
		e.ind()
		e.emit("return " + tmp + ";\n")
		return
	}
	// Go evaluates a return's expressions, assigns them to the results, and only
	// then runs the defers. The defers used to run first, so an expression reading
	// what a defer had changed saw the changed value -- "return n + 1" with a defer
	// that multiplies n by 10 gave 11 where Go gives 20. Binding first also gives a
	// named result the one thing it is for: a defer may still change it, and that
	// change is what the caller sees.
	var bound []string
	if len(e.defers) != 0 && len(exprs) != 0 && len(exprs) == len(e.curResultTypes) {
		for i, ex := range exprs {
			// A result nothing can change needs no binding: a literal expression
			// reads no variable, so a defer cannot reach it.
			if name := e.curResultNames[i]; name == "0" && e.exprIsLiteral(ex.ast) {
				bound = append(bound, e.captureC(func() { e.emitReturnValue(i, ex) }))
				continue
			}
			value := e.captureC(func() { e.emitReturnValue(i, ex) })
			if name := e.curResultNames[i]; name != "0" {
				e.ind()
				e.emit(name + " = " + value + ";\n")
				bound = append(bound, name)
				continue
			}
			tmp := e.newTmp()
			e.ind()
			e.emit(e.curResultTypes[i] + " " + tmp + " = " + value + ";\n")
			bound = append(bound, tmp)
		}
	}
	e.emitDeferred()
	e.ind()
	if bound != nil {
		if len(bound) == 1 {
			e.emit("return " + bound[0] + ";\n")
			return
		}
		e.emit("return (" + e.retStructName(e.curFunc) + "){" + strings.Join(bound, ", ") + "};\n")
		return
	}
	switch len(exprs) {
	case 0:
		// A bare "return": main returns 0, a void function returns nothing, and a
		// named-result function returns its result variables (naked return).
		switch {
		case e.mainRet:
			e.emit("return 0;\n")
		case len(e.curResultNames) == 0:
			e.emit("return;\n")
		case len(e.curResultNames) == 1:
			e.emit("return " + e.curResultNames[0] + ";\n")
		default:
			e.emit("return (" + e.retStructName(e.curFunc) + "){")
			for i, nm := range e.curResultNames {
				if i != 0 {
					e.emit(", ")
				}
				e.emit(nm)
			}
			e.emit("};\n")
		}
	case 1:
		// A STRUCT result returned straight from a call is bound to a temporary
		// first, for the same reason a multi-result one is: the target's compiler
		// loses a member narrower than a machine word out of `return f();` and warns
		// while doing it. Binding is correct and silent. See
		// doc/return-nonword-struct.c -- this is the plain-struct half of it, and it
		// predates the multi-result forwarding that found it.
		if ct := e.soleResultCType(); e.isStruct(ct) && e.isDirectCall(exprs[0]) {
			tmp := e.newTmp()
			e.ind()
			e.emit(ct + " " + tmp + " = " + e.captureC(func() { e.emitReturnValue(0, exprs[0]) }) + ";\n")
			e.ind()
			e.emit("return " + tmp + ";\n")
			return
		}
		e.emit("return ")
		e.emitReturnValue(0, exprs[0])
		e.emit(";\n")
	default:
		e.emit("return (" + e.retStructName(e.curFunc) + "){")
		for i, ex := range exprs {
			if i != 0 {
				e.emit(", ")
			}
			e.emitReturnValue(i, ex)
		}
		e.emit("};\n")
	}
}

// exprIsLiteral reports whether an expression is built entirely from literals and
// operators, so that its value depends on nothing a deferred call could change. The
// predeclared constant names count as literals; any other name does not.
func (e *emitter) exprIsLiteral(ast []int32) bool {
	for n := range it(ast) {
		if n.sym != 0 {
			if !e.exprIsLiteral(n.ast) {
				return false
			}
			continue
		}
		switch e.f.ch(n.tok) {
		case INT, FLOAT, STRING, CHAR:
		case IDENT:
			switch e.src(n.tok) {
			case "true", "false", "nil":
			default:
				return false
			}
		}
	}
	return true
}

// emitReturnValue emits one of a return's expressions as the i-th result.
//
// `return nil` in a slice-returning function yields the zero slice header, not the
// integer 0, which is only nil's pointer form.
func (e *emitter) emitReturnValue(i int, ex Node) {
	if i < len(e.curResultTypes) {
		// A nil slice is the all-zero header. The interface case is not here: it
		// belongs to ifaceValueC below, which every position wanting an interface
		// VALUE goes through, so nil is answered once rather than per position.
		if ct := e.curResultTypes[i]; e.isNilExpr(ex.ast) && e.isSliceCType(ct) {
			e.emit("(" + ct + "){0}")
			return
		}
		// A concrete value returned as an interface is wrapped here, the result
		// being the target whose type says so.
		if e.isIfaceCType(e.curResultTypes[i]) {
			if text, ok := e.ifaceValueC(e.curResultTypes[i], ex.ast); ok {
				e.emit(text)
				return
			}
		}
	}
	e.emitExpr(ex.ast)
}

// emitAssignHeadStmt handles the `AssignHead Postfix` statement family: a call
// (Postfix ends in CallSuffix) or an assignment (Postfix carries a PostfixOp).
func (e *emitter) emitAssignHeadStmt(nodes []Node) {
	if len(nodes) != 2 || nodes[1].sym != Postfix {
		e.fail("unsupported statement form")
		return
	}
	head := nodes[0]
	postfix := slices.Collect(it(nodes[1].ast))
	switch {
	case containsSym(postfix, PostfixOp):
		e.emitAssignment(head, postfix)
	case containsSym(postfix, CallSuffix):
		e.emitCall(head, postfix)
	default:
		e.fail("unsupported statement form")
	}
}

// emitCall emits a call statement: builtin print/println (mapped to printf) or,
// via emitCallExpr, a user-function or p2-intrinsic call, indented and closed
// with `;`.
func (e *emitter) emitCall(head Node, postfix []Node) {
	recv := e.soleIdent(head.ast)
	if recv == "" {
		// `(*p).m()` as a statement. Go defines `p.m()` as the same call, so the
		// receiver is the pointer and the shorthand is what is emitted -- the same
		// equivalence the expression form uses (see derefCallSteps).
		if name, ok := e.derefHead(head); ok {
			e.ind()
			e.emitCallExpr(name, postfix)
			e.emit(";\n")
			return
		}
		// `(&v).m()` as a statement, which is what `v.m()` means whichever way the
		// receiver is declared -- the mirror of the dereference above.
		if name, ok := e.addrHead(head); ok {
			e.ind()
			e.emitCallExpr(name, postfix)
			e.emit(";\n")
			return
		}
		e.fail("unsupported call target")
		return
	}
	if len(postfix) == 1 && postfix[0].sym == CallSuffix && (recv == "println" || recv == "print") {
		e.emitPrint(recv == "println", postfix[0].ast)
		return
	}
	if len(postfix) == 1 && postfix[0].sym == CallSuffix && recv == "printf" {
		e.emitPrintf(postfix[0].ast)
		return
	}
	// `e.(*P).m()` as a statement: the assertion binds to a temporary and the call
	// is on that, exactly as in an expression.
	if iface, target, isIface, rest, ok := e.assertionSteps(recv, postfix); ok && len(rest) != 0 {
		name, _, ok := e.hoistAssert(recv, iface, target, isIface)
		if !ok {
			return
		}
		e.ind()
		e.emitCallExpr(name, rest)
		e.emit(";\n")
		return
	}
	e.ind()
	if !e.emitCallExpr(recv, postfix) {
		e.fail("only <pkg>.<Func>(args) or builtin(args) call statements are supported yet")
		return
	}
	e.emit(";\n")
}

// emitCallExpr emits a call in value position (no indent, no trailing `;`): a
// direct user-function call `name(args)` or a p2-qualified call mapped to its
// intrinsic `_intr(args)`. It reports false when the head/suffix is not a
// supported call shape, latching a specific error for a bad p2 call; it is shared
// by the statement call path (emitCall) and the expression Factor path. The
// checker has already verified the callee resolves and the arguments match.
func (e *emitter) emitCallExpr(recv string, suffix []Node) bool {
	// A call whose result is an ARRAY is a statement, not a value: the caller owns
	// the storage and the callee fills it, so there is no expression for the call to
	// become. Bind it and use the variable.
	if len(suffix) != 0 && suffix[len(suffix)-1].sym == CallSuffix {
		cname := e.funcCallC(recv)
		if len(suffix) == 2 && suffix[0].sym == Selector {
			if rct, ok := e.methodRecvCType(recv); ok {
				cname = methodCName(methodBaseType(rct), e.soleIdent(suffix[0].ast))
			}
		}
		if _, isArr := e.funcArrayRet[cname]; isArr {
			e.fail("a call returning an array must be bound to a variable first: `a := %s(...)`, then use a", recv)
			return false
		}
	}
	switch {
	case len(suffix) == 1 && suffix[0].sym == CallSuffix:
		if recv == "len" {
			e.emitLen(suffix[0].ast)
			return true
		}
		if recv == "cap" {
			e.emitCap(suffix[0].ast)
			return true
		}
		if recv == "panic" {
			e.emitPanic(suffix[0].ast)
			return true
		}
		if recv == "append" {
			// Single-result append: s = append(s, x). The two-result form
			// s, ok = append(s, x) is handled in emitMultiAssign.
			e.emitAppend(suffix[0].ast)
			return true
		}
		if recv == "copy" {
			e.emitCopy(suffix[0].ast)
			return true
		}
		if _, isUser := e.userFunc(recv); !isUser && recv == "clear" {
			e.emitClear(suffix[0].ast)
			return true
		}
		if _, isUser := e.userFunc(recv); !isUser && (recv == "min" || recv == "max") {
			// min and max are common names; a user function of that name (in funcRet)
			// shadows the builtin, as Go allows, and is emitted as a real call below.
			e.emitMinMax(recv, suffix[0].ast)
			return true
		}
		if recv == "make" {
			// make needs a hoisted backing array, so it is only handled as a
			// `var s []T = make(...)` initializer (see emitMakeSliceVar), not as a
			// general expression.
			e.fail("make is only supported as a `var s []T = make(...)` initializer yet")
			return true
		}
		if recv == "NewBuilder" {
			// NewBuilder(back []byte) -> a Builder over the caller's backing.
			args := e.callArgExprs(suffix[0].ast)
			if len(args) != 1 {
				e.fail("NewBuilder takes one []byte argument")
				return true
			}
			e.needBuilder()
			e.emit("ogo_builder_new(")
			e.emitExpr(args[0].ast)
			e.emit(")")
			return true
		}
		if ct, ok := e.convType(recv); ok {
			args := e.callArgExprs(suffix[0].ast)
			if len(args) == 1 {
				e.emitConversion(ct, args[0])
				return true
			}
		}
		if _, isUser := e.userFunc(recv); !isUser && isBuiltinFuncName(recv) {
			// A predeclared builtin the emitter does not implement. The checker
			// exempts every builtin name from its undefined check, so one not handled
			// above (len, cap, append, copy, make; print/println via emitCall) would
			// otherwise fall through and emit a call to a C function that does not
			// exist -- as copy silently did. Refuse it by name. A user function of the
			// same name (funcRet holds it) shadows the builtin and is emitted below.
			e.fail("the %s builtin is not supported yet", recv)
			return true
		}
		// A call through a variable holding a function -- a local, a parameter or a
		// package variable. The variable names the callee, so it is emitted as the
		// variable is, NOT mangled into the package's namespace the way a declared
		// function's name is. (Mangling it silently worked in the main package,
		// whose prefix is empty, and emitted a call to `<pkg>_f` in every other.)
		if ct, ok := e.varType(recv); ok && e.isFuncCType(ct) {
			e.emit(e.varRef(recv) + "(")
			e.emitCallArgs(e.funcValueOf[recv], suffix[0].ast)
			e.emit(")")
			return true
		}
		cname := e.funcCallC(recv)
		e.emit(cname + "(")
		e.emitCallArgs(cname, suffix[0].ast)
		e.emit(")")
		return true
	case len(suffix) == 2 && suffix[0].sym == Selector && suffix[1].sym == CallSuffix:
		method := e.soleIdent(suffix[0].ast)
		// A call through an interface value: the table says which function, and the
		// data pointer is the receiver the thunk unpacks.
		if ct, ok := e.varType(recv); ok && e.isIfaceCType(ct) {
			if !e.hasIfaceMethod(ct, method) {
				e.fail("type %s has no method %s", ct, method)
				return false
			}
			// The escape rules a DIRECT method call obeys, asked of the union over
			// the implementations -- the call names no function for them to be
			// looked up by, and asking nothing made this the way around them.
			e.checkIfaceArgs(ct, method, e.callArgExprs(suffix[1].ast))
			e.emit(e.varRef(recv) + ".vt->" + userIdent(method) + "(" + e.varRef(recv) + ".data")
			if args := e.argsCText("", suffix[1].ast); args != "" {
				e.emit(", " + args)
			}
			e.emit(")")
			return true
		}
		// A method call `x.M(args)` on a variable of a user-defined type (struct or
		// named, value or pointer) lowers to `<T>_M(recv, args)`, with the receiver
		// adjusted to match the method's value or pointer receiver. Distinguished from
		// a package call (`p2.F(...)`) by recv naming a typed variable, not an import.
		// A struct field holding a function is called through, not dispatched to: it
		// is a value the caller put there, so the call names the field. Checked ahead
		// of the method lookup, since a field and a method are told apart by which
		// one the type actually has, and only a field can be a function value.
		if ft, ok := e.fieldType(recv, []string{method}); ok && e.isFuncCType(ft) {
			e.emit(e.fieldAccessC(recv, []string{method}) + "(")
			// The BOUND function's own C name, so the call is judged by its
			// summaries -- the callee really is that function. An unbound field
			// yields "", which consults nothing and accepts, as the rest of the
			// analysis does with a callee it cannot name.
			e.emitCallArgs(e.funcValueOf[funcFieldKey(recv, method)], suffix[1].ast)
			e.emit(")")
			return true
		}
		if rct, ok := e.methodRecvCType(recv); ok {
			cname := methodCName(methodBaseType(rct), method)
			// A method PROMOTED from an embedded field is called on that field, which
			// the source did not name and C requires: `d.Get()` is `base_Get(&d.base)`.
			if cn, path, _, okp := e.promotedMethod(rct, method); okp && len(path) != 0 {
				sub := e.varRef(recv) + e.embeddedPathC(rct, path)
				recvArg := sub
				if e.methodPtr[cn] {
					recvArg = "&" + sub
				}
				e.emit(cn + "(" + recvArg)
				if args := e.argsCText(cn, suffix[1].ast); args != "" {
					e.emit(", " + args)
				}
				e.emit(")")
				return true
			}
			// The receiver is in hand here and nowhere further in, so the
			// receiver-lifetime rule is asked here rather than threaded through
			// emitCallArgs, which nine call sites share and only this one is a method.
			e.checkRecvLeak(cname, recv, e.callArgExprs(suffix[1].ast))
			e.emit(cname + "(")
			e.emitMethodReceiver(recv, rct, e.methodPtr[cname])
			// A variadic parameter is passed even when the call wrote no arguments
			// for it: the callee takes a []T either way, and an empty one is the
			// zero header rather than nothing at all.
			if _, at := e.variadicPack(cname); len(e.callArgExprs(suffix[1].ast)) > 0 || at >= 0 {
				e.emit(", ")
				e.emitCallArgs(cname, suffix[1].ast)
			}
			e.emit(")")
			return true
		}
		// p2 is a package of declarations only -- each of its functions is one C
		// intrinsic -- so its calls are substituted rather than mangled. Asked before
		// the qualifier path below, which would name a p2_X that is never defined.
		if recv == "p2" {
			intr, ok := p2Intrinsics[method]
			if !ok {
				e.fail("unsupported p2 function %q", method)
				return false
			}
			e.emit(intr.c + "(")
			e.emitCallArgs("", suffix[1].ast) // a p2 intrinsic takes no slice
			e.emit(")")
			return true
		}
		// A CONVERSION to a type of that package, `geo.Celsius(20)`, which is not a
		// call however much it looks like one. Asked before the qualifier path
		// below, which would emit a geo_Celsius the program never defines.
		if ct, isConv := e.qualConvType(recv, method); isConv {
			if args := e.callArgExprs(suffix[1].ast); len(args) == 1 {
				e.emitConversion(ct, args[0])
				return true
			}
		}
		if prefix, ok := e.importQualifiers[recv]; ok {
			// A call into an imported user package: the exported function is emitted
			// in that package's namespace, so the call resolves to the mangled name.
			cname := mangle(prefix, method)
			e.emit(cname + "(")
			e.emitCallArgs(cname, suffix[1].ast)
			e.emit(")")
			return true
		}
		if recv != "p2" {
			e.fail("unknown package %q (only p2 is supported yet)", recv)
			return false
		}
		intr, ok := p2Intrinsics[method]
		if !ok {
			e.fail("unsupported p2 function %q", method)
			return false
		}
		e.emit(intr.c + "(")
		e.emitCallArgs("", suffix[1].ast) // a p2 intrinsic takes no slice
		e.emit(")")
		return true
	}
	// A longer chain the two fixed shapes cannot match -- a call result selected or
	// called further (`mk().n`, `t.self().n`, `a[i].get()`). chainCText types and
	// lowers it, or reports it cannot (an unsupported step, or a pointer method on a
	// non-addressable value).
	if text, _, _, ok := e.chainCText(recv, suffix); ok {
		e.emit(text)
		return true
	}
	return false
}

// shiftChainC renders a Term containing a shift that needs guarding, folding the
// chain left-associatively into helper calls. It reports false for a Term with no
// such shift, which the ordinary streaming path then emits unchanged.
//
// A shift by an in-range constant needs no guard: its count is known to be one C
// shifts the way Go does. Everything else -- a variable count, or a constant at or
// past the width -- goes through the helper.
func (e *emitter) shiftChainC(kids []Node) (string, bool) {
	needed := false
	for i := 1; i+1 < len(kids); i += 2 {
		if kids[i].sym == MulOp && (e.shiftNeedsGuard(kids[i], kids[i-1], kids[i+1]) || e.divNeedsGuard(kids[i], kids[i-1], kids[i+1])) {
			needed = true
			break
		}
	}
	if !needed {
		return "", false
	}
	text := e.captureC(func() { e.emitExprNode(kids[0]) })
	ctype, haveType := e.inferCType(kids[0].ast)
	for i := 1; i+1 < len(kids); i += 2 {
		op, rhs := kids[i], kids[i+1]
		if op.sym != MulOp {
			return "", false
		}
		rhsText := e.captureC(func() { e.emitExprNode(rhs) })
		switch {
		case haveType && e.shiftNeedsGuard(op, kids[i-1], rhs):
			fn := e.needShift(e.opText(op.ast), e.underlyingCType(ctype))
			text = fn + "(" + text + ", " + e.shiftCountC(rhsText, rhs.ast) + ")"
		case haveType && e.divNeedsGuard(op, kids[i-1], rhs):
			fn := e.needDiv(e.opText(op.ast), e.underlyingCType(ctype))
			text = fn + "(" + text + ", " + rhsText + ")"
		default:
			// A step the chain does not guard leaves the result's type as it was:
			// the value type of a shift, a quotient and a remainder alike is the
			// left operand's.
			text = "(" + text + " " + e.opText(op.ast) + " " + rhsText + ")"
		}
	}
	return text, true
}

// divNeedsGuard reports whether a division step must go through the guarded helper:
// it is "/" or "%" on a signed integer, and the divisor is not a constant already
// known to be neither 0 nor -1.
//
// An unsigned division needs no guard beyond the zero one ogo_nonzero gives it: it
// has no most-negative value to overflow, and its divisor is never -1.
func (e *emitter) divNeedsGuard(op, lhs, rhs Node) bool {
	switch e.opText(op.ast) {
	case "/", "%":
	default:
		return false
	}
	ctype, ok := e.inferCType(lhs.ast)
	if !ok {
		return false
	}
	if _, signed := cUnsignedOf[e.underlyingCType(ctype)]; !signed {
		return false
	}
	if v, ok := e.foldConstInt(rhs.ast); ok && v != 0 && v != -1 {
		return false
	}
	return true
}

// shiftNeedsGuard reports whether a shift step must go through the guarded helper:
// it is a shift at all, the value's C type is a known integer, and the count is not
// a constant already inside that type's width.
func (e *emitter) shiftNeedsGuard(op, lhs, rhs Node) bool {
	switch e.opText(op.ast) {
	case "<<", ">>":
	default:
		return false
	}
	ctype, ok := e.inferCType(lhs.ast)
	if !ok {
		return false
	}
	bits, ok := cIntWidths[e.underlyingCType(ctype)]
	if !ok {
		return false // not an integer this target measures a shift against
	}
	if v, ok := e.foldConstInt(rhs.ast); ok && v >= 0 && v < int64(bits) {
		return false
	}
	return true
}

// emitMethodReceiver emits a method call's receiver argument, bridging the receiver
// the caller holds and the one the method declares: it takes the address of a value
// passed to a pointer method (&x), dereferences a pointer passed to a value method
// (*x), and otherwise passes the receiver unchanged.
// emitRecvCopy copies an ARRAY value receiver out of the pointer it arrived in, the
// receiver counterpart of emitParamCopies. It emits nothing for every other receiver
// shape, which arrive by value or as a pointer the method means to write through.
//
// The copy is what makes it Go's value receiver: the pointer is how the array
// crosses, not what it means, so a method that writes to its receiver writes to the
// copy and the caller's array is untouched.
func (e *emitter) emitRecvCopy(recvName, recvCType string) {
	a, isArr := e.namedArrays[recvCType]
	if !isArr || recvName == "" {
		return
	}
	e.includes["string.h"] = true
	e.ind()
	e.emit(a.elem + " " + userIdent(recvName) + a.declSuffix() + ";\n")
	e.ind()
	e.emit("memcpy(" + userIdent(recvName) + ", " + paramArgName(recvName) + ", sizeof(" + userIdent(recvName) + "));\n")
}

// methodRecvCType is the receiver C type a method call on recv dispatches through,
// and whether recv names a type that can carry methods at all.
//
// An ARRAY variable has no C type to look up: its extents live in e.arrays and
// nowhere else, which is why varType answers nothing for one. The DEFINED name it
// was declared with is the only thing that says which type it is, and therefore
// which methods it has -- without it `g.set(0, 7)` for a `type Row [2]int` was read
// as a package qualification and reported as `unknown package "g"`.
func (e *emitter) methodRecvCType(recv string) (string, bool) {
	if ct, ok := e.varType(recv); ok {
		// isMethodBase rather than isUserType, which a defined ARRAY type fails: it
		// takes a typedef of its own and never reaches the namedTypes registry. A
		// variable whose recorded C type IS such a name -- a range value over a
		// `[2]Row` is one, its element type being Row -- would otherwise answer here
		// and return false, never reaching the array environments below. That is the
		// same split this function already documents twice.
		if e.isMethodBase(methodBaseType(ct)) {
			return ct, true
		}
		// A POINTER to a defined array type. Its base carries a method set like any
		// other named type, but a named array takes a typedef of its own and skips
		// the namedTypes registry isUserType asks, so `p := &v; p.m()` was read as a
		// package qualification where `v.m()` was not.
		if base := methodBaseType(ct); base != ct {
			if _, isArr := e.namedArrays[base]; isArr {
				return ct, true
			}
		}
		return "", false
	}
	// BOTH array environments. A local array is in arrays and a package one in
	// globalArrays, and asking only the first answered "no" for `var g Row` at
	// package scope while the same method on a local worked -- the third time that
	// split has caught a predicate here (see isPackageVar and isFrameVar).
	if a, ok := e.arrays[recv]; ok && a.name != "" {
		return a.name, true
	}
	if a, ok := e.globalArrays[e.globalC(recv)]; ok && a.name != "" {
		return a.name, true
	}
	return "", false
}

func (e *emitter) emitMethodReceiver(recv, recvCType string, wantPtr bool) {
	switch havePtr := e.isPointer(recvCType); {
	case wantPtr && !havePtr:
		e.emit("&" + recv)
	case !wantPtr && havePtr:
		e.emit("*" + recv)
	default:
		e.emit(recv)
	}
}

// isChainVar and isChainFunc classify the leading identifier of a Factor chain: a
// value (a slice, array or plain variable) that a suffix reads from, or a function
// whose first suffix must be the call that produces a value.
func (e *emitter) isChainVar(base string) bool  { _, ok := e.accessBase(base); return ok }
func (e *emitter) isChainFunc(base string) bool { _, ok := e.userFunc(base); return ok }

// userFunc looks up a same-package top-level function's recorded result types by its
// source name, mangling to the current package's namespace (see mangle). funcCallC
// is the C name that same call emits, so a definition and its call always agree.
func (e *emitter) userFunc(name string) ([]string, bool) {
	rts, ok := e.funcRet[mangle(e.curPkgPrefix, name)]
	return rts, ok
}

func (e *emitter) funcCallC(name string) string { return mangle(e.curPkgPrefix, name) }

// argsCText renders a CallSuffix's arguments as C text -- the same output
// emitCallArgs streams, captured to a string so a call reached mid-chain can be
// wrapped in `f(...)`/`T_M(recv, ...)`.
func (e *emitter) argsCText(cname string, callSuffix []int32) string {
	saved := e.w
	var buf bytes.Buffer
	e.w = &buf
	e.emitCallArgs(cname, callSuffix)
	e.w = saved
	return buf.String()
}

// indexCText renders an index expression (with its bound check) to a string.
func (e *emitter) indexCText(idxAST []int32, lenExpr string) string {
	saved := e.w
	var buf bytes.Buffer
	e.w = &buf
	e.emitIndex(idxAST, lenExpr)
	e.w = saved
	return buf.String()
}

// chainReceiver bridges an accumulated receiver expression to the form a method
// declares, like emitMethodReceiver but over text and reporting failure instead of
// emitting: a pointer method wants `&recv`, which is invalid C for a
// non-addressable value (a call result), so that combination is refused.
func (e *emitter) chainReceiver(text, ctype string, addr, wantPtr bool) (string, bool) {
	switch havePtr := e.isPointer(ctype); {
	case wantPtr && havePtr:
		return text, true
	case wantPtr && !havePtr:
		if !addr {
			return "", false // &rvalue is not valid C -- a temporary would be needed
		}
		return "&" + text, true
	case !wantPtr && havePtr:
		return "*" + text, true
	default:
		return text, true
	}
}

// chainCText lowers a Factor's leading identifier and its FactorSuffix run into one
// C expression string, admitting the calls the fixed shapes cannot: a leading
// function call `mk()`, a method call `x.M()` at any point, alternating with field
// selectors and indexes -- `mk().n`, `t.self().n`, `a[i].get()`, `p.inner.get()`.
// It returns text rather than streaming because a method call must wrap the
// receiver text in `T_M(recv, ...)`.
//
// It tracks the type reached (as accessCur) and whether the value is an addressable
// lvalue. A pointer-receiver method needs `&receiver`; on a non-addressable value
// (a call result) that is `&`-of-an-rvalue, which C rejects, so the chain is refused
// (ok=false) rather than miscompiled -- a temporary would be needed and is not yet
// synthesised here.
func (e *emitter) chainCText(base string, steps []Node) (text, ctype string, addr, ok bool) {
	var cur accessCur
	pendingFn := false
	switch {
	case e.isChainVar(base):
		cur, _ = e.accessBase(base)
		text, addr = e.varRef(base), true
	case e.isChainFunc(base):
		pendingFn, text = true, base
	default:
		// A CONVERSION opening the chain, `C(5).twice()`. It looks like a call of
		// the type's name and was neither of the two above, so a method on a
		// converted value had to be written through a variable. The conversion
		// consumes the first step; what it leaves is a value of that type, which the
		// steps after it walk like any other.
		//
		// A conversion with NOTHING after it is a chain of one and is answered the
		// same way. This used to require a further step, which is what a method call
		// needs and not what the contract here is -- so `switch any(&q).(type)` was
		// refused with the wrong reason ("bind any to a variable"), the guard having
		// stripped the `.(type)` before asking.
		ct, used, isConv := e.convChainHead(base, steps)
		if !isConv {
			return "", "", false, false
		}
		args := e.callArgExprs(steps[used-1].ast)
		if len(args) != 1 {
			return "", "", false, false
		}
		text = e.captureC(func() { e.emitConversion(ct, args[0]) })
		cur, steps = e.plainOrSlice(ct), steps[used:]
	}
	for i := 0; i < len(steps); i++ {
		n := steps[i]
		switch n.sym {
		case CallSuffix:
			// A call through a value the chain has already reached -- an array or
			// slice element holding a function, `table[0](5)`, or such a field one
			// step further in. The value names the callee, so the call is just the
			// text so far applied to the arguments.
			if !pendingFn && e.isFuncCType(cur.ctype) {
				// Keyed by the function typedef, which a DEFINED function type is
				// only a name for: `type Fn func(int) int` reaches its results
				// through what it is defined over. Asked before anything is
				// hoisted, so a chain this cannot type leaves no temporary behind.
				rts := e.funcTypeRet[e.underlyingCType(cur.ctype)]
				if len(rts) != 1 {
					return "", "", false, false
				}
				// A call made DIRECTLY through an array element of function-pointer
				// type reaches the wrong function on the P2: every element calls
				// whatever the first one holds. Measured on a P2-EDGE, constant and
				// variable index alike, and with the table filled at package
				// initialization or by assignment -- see doc/call-through-array-element.c.
				// Binding the element to a temporary first is correct, so that is
				// what is emitted. gcc compiles the direct form correctly, which is
				// why this needed the board to find.
				text = e.hoist(cur.ctype, func() { e.emit(text) }) +
					"(" + e.argsCText("", n.ast) + ")"
				cur, addr = e.plainOrSlice(rts[0]), false
				continue
			}
			// Otherwise a call reaches here only on the pending leading function; a
			// method call is recognised at its Selector and consumes the CallSuffix
			// there.
			rts, okr := e.userFunc(base)
			if !pendingFn || !okr || len(rts) != 1 {
				return "", "", false, false
			}
			cname := e.funcCallC(base)
			text = cname + "(" + e.argsCText(cname, n.ast) + ")"
			cur, addr, pendingFn = e.plainOrSlice(rts[0]), false, false
			// A STRUCT result about to become a METHOD's receiver is bound to a
			// temporary: the target drops a member narrower than a machine word when
			// such a value is handed on by value, which `mk(-5).flag()` showed by
			// answering true for a false bool. See doc/return-nonword-struct.c.
			//
			// Only before a method call -- Selector then CallSuffix. A plain field
			// select, `mk().y`, reads the value where it stands and is correct; it
			// also already has a temporary from the paths that hoist a call, and
			// binding again would only emit a copy of a copy.
			if i+2 < len(steps) && steps[i+1].sym == Selector && steps[i+2].sym == CallSuffix && e.isStruct(cur.ctype) {
				bound := text
				text = e.hoist(cur.ctype, func() { e.emit(bound) })
			}
		case Selector:
			field := e.soleIdent(n.ast)
			// A method reached through an interface: the slot's declared result is
			// the call's type, the concrete function behind it being unknown here
			// and its result being the same one anyway.
			if ms, isIface := e.ifaceMethods[cur.ctype]; isIface && i+1 < len(steps) && steps[i+1].sym == CallSuffix {
				slot := -1
				for k, m := range ms {
					if m.name == field {
						slot = k
					}
				}
				if slot < 0 {
					return "", "", false, false
				}
				call := text + ".vt->" + userIdent(field) + "(" + text + ".data"
				if args := e.argsCText("", steps[i+1].ast); args != "" {
					call += ", " + args
				}
				text, addr = call+")", false
				if ms[slot].res == "void" {
					cur = accessCur{}
				} else {
					cur = e.plainOrSlice(ms[slot].res)
				}
				i++ // consumed the CallSuffix
				continue
			}
			bt := methodBaseType(cur.ctype)
			if bt == "" {
				// An ARRAY the chain has reached has no C value type, so the DEFINED
				// name it was declared with is what carries its method set.
				bt = cur.name
			}
			cname := methodCName(bt, field)
			rts, okm := e.funcRet[cname]
			// A name the type has no method for is not a dispatch: it is a FIELD,
			// and a field holding a function value is called through rather than
			// dispatched to -- `table[i].run(arg)`, the dispatch table every command
			// loop is built from. Falling through leaves the field read to
			// accessSelect below, after which the CallSuffix is handled as a call on
			// a function value, the case that already exists for `table[i](arg)`.
			// The two-step shape `x.run(arg)` makes the same distinction the same
			// way round; this chain used to take any Selector-then-CallSuffix for a
			// method and fail when the lookup came up empty.
			if okm && i+1 < len(steps) && steps[i+1].sym == CallSuffix && bt != "" && e.isMethodBase(bt) {
				// A void method (no result) is valid as the final step of a call
				// statement -- `xs[i].update()` mutating an element in place -- so 0
				// results is admitted; a further step then fails on the empty type.
				//
				// A multi-result method is not a single value and cannot CONTINUE a
				// chain -- but as the last step it is exactly what a destructuring
				// assignment wants, `a, b := xs[i].two()`, and refusing it here was
				// what made that "multiple assignment requires a single function call
				// on the right-hand side" for an element of any kind, struct or array.
				// Its value is the result STRUCT, which is what the caller reads the
				// fields off.
				if len(rts) > 1 && i+1 != len(steps)-1 {
					return "", "", false, false
				}
				recv, okr := e.chainReceiver(text, cur.ctype, addr, e.methodPtr[cname])
				if !okr {
					return "", "", false, false
				}
				if args := e.argsCText(cname, steps[i+1].ast); args != "" {
					recv += ", " + args
				}
				text = cname + "(" + recv + ")"
				addr = false
				switch {
				case len(rts) == 1:
					cur = e.plainOrSlice(rts[0])
				case len(rts) > 1:
					cur = accessCur{ctype: e.retStructNameOf(rts)}
				default:
					cur = accessCur{} // void: no value to continue with
				}
				i++ // consumed the CallSuffix
				continue
			}
			next, oks := e.accessSelect(cur, field)
			if !oks {
				return "", "", false, false
			}
			// flexcc miscompiles a field read at a nonzero offset directly off a
			// function's struct return value -- `f(...).y` yields garbage, the return
			// temporary not being materialised before the offset is applied -- while
			// a method call on that same result, which passes the whole struct, is
			// fine. So a non-addressable base is bound to a temporary first, which
			// makes it an ordinary variable that reads correctly and is addressable
			// for whatever the chain does next. The temporary is declared before the
			// statement (see emitStatement); until that was possible this shape had
			// to be refused. Hoisting comes after accessSelect so a chain that is
			// going to fail anyway leaves no declaration behind.
			if !addr {
				if cur.ctype == "" {
					return "", "", false, false
				}
				inner := text
				text, addr = e.hoist(cur.ctype, func() { e.emit(inner) }), true
			}
			sel, okSel := e.selectC(cur.ctype, field)
			if !okSel {
				return "", "", false, false
			}
			text += sel
			cur = next
		case Index:
			if !addr {
				// Indexing a call result, `mk()[1]`. Bound to a temporary for the
				// same reason a field read is (see the Selector case), and with a
				// second benefit for a slice result: the bounds check needs the base
				// as text to form its ".len", and a temporary is exactly that.
				if cur.ctype == "" && !cur.slice && len(cur.dims) == 0 {
					return "", "", false, false
				}
				ct := cur.ctype
				if cur.slice {
					ct = sliceCName(cur.elem)
				}
				if ct == "" {
					return "", "", false, false
				}
				inner := text
				text, addr = e.hoist(ct, func() { e.emit(inner) }), true
			}
			low, _, _, isSlice := e.sliceParts(n.ast)
			if isSlice || low == nil {
				return "", "", false, false
			}
			next, lenExpr, oki := e.accessIndex(cur, text)
			if !oki {
				return "", "", false, false
			}
			open := "["
			if cur.slice {
				open = ".ptr["
			}
			text = e.accessDeref(cur, text) + open + e.indexCText(low, lenExpr) + "]"
			cur, addr = next, true
		default:
			return "", "", false, false
		}
	}
	if pendingFn {
		return "", "", false, false // a bare function name, never called
	}
	return text, cur.ctype, addr, true
}

// chainResultType is chainCText's type half: it walks the same chain without
// emitting -- and without the argument side effects emission has -- to report the
// C type a call chain yields, for inferCType/callResultCType.
func (e *emitter) chainResultType(base string, steps []Node) (string, bool) {
	var cur accessCur
	pendingFn := false
	switch {
	case e.isChainVar(base):
		cur, _ = e.accessBase(base)
	case e.isChainFunc(base):
		pendingFn = true
	default:
		return "", false
	}
	for i := 0; i < len(steps); i++ {
		n := steps[i]
		switch n.sym {
		case CallSuffix:
			rts, okr := e.userFunc(base)
			if !pendingFn || !okr || len(rts) != 1 {
				return "", false
			}
			cur, pendingFn = e.plainOrSlice(rts[0]), false
		case Selector:
			field := e.soleIdent(n.ast)
			bt := methodBaseType(cur.ctype)
			// A call through an interface REACHED BY THE CHAIN -- `sc.first.Name()`,
			// `shapes[1].Area()` -- takes what the slot declares, the same rule
			// callResultCType applies to a plain interface variable. Without this the
			// chain went untyped and a string result printed as two integers.
			if i+1 < len(steps) && steps[i+1].sym == CallSuffix && e.isIfaceCType(cur.ctype) {
				slot := ""
				for _, m := range e.ifaceMethods[cur.ctype] {
					if m.name == field {
						slot = m.res
					}
				}
				if slot == "" || slot == "void" {
					return "", false // no such slot, or one yielding nothing
				}
				cur = e.plainOrSlice(slot)
				i++
				continue
			}
			if bt == "" {
				// An ARRAY the chain has reached has no C value type, so the DEFINED
				// name it was declared with is what carries its method set -- the
				// same fallback the emission half makes. Without it `t := pool[1].sum()`
				// could not be typed, while the emission of the very same call could.
				bt = cur.name
			}
			if i+1 < len(steps) && steps[i+1].sym == CallSuffix && bt != "" && e.isMethodBase(bt) {
				rts, okm := e.funcRet[methodCName(bt, field)]
				if !okm || len(rts) > 1 {
					return "", false
				}
				if len(rts) == 1 {
					cur = e.plainOrSlice(rts[0])
				} else {
					cur = accessCur{} // void method: no result type (a void chain yields none)
				}
				i++
				continue
			}
			// A field of a call result types like any other field; chainCText binds
			// the result to a temporary so it can be read at all.
			var oks bool
			if cur, oks = e.accessSelect(cur, field); !oks {
				return "", false
			}
		case Index:
			if _, _, _, isSlice := e.sliceParts(n.ast); isSlice {
				return "", false
			}
			// A non-empty prefix stands in for the real one: only its emptiness
			// gates the slice-length path in accessIndex, never used for typing.
			var oki bool
			if cur, _, oki = e.accessIndex(cur, base); !oki {
				return "", false
			}
		default:
			return "", false
		}
	}
	if pendingFn || cur.ctype == "" {
		return "", false // never called, or ended on an array/slice, not a single value
	}
	return cur.ctype, true
}

// emitLen emits the builtin `len(x)`: an array's length is its compile-time bound;
// a string's and a slice's is its header's `len` field.
// arrayChainBound returns the extent of an ARRAY reached through a chain of fields
// and indexes: a struct's array field, `len(r.buf)` and `len(r.inner.buf)`; a ROW of
// a multi-dimensional one, `len(m[0])`; and a field reached past an index,
// `len(rs[i].buf)`. It is the length and the capacity alike, and it is a
// compile-time constant, so no storage is read to produce it.
//
// The chain walk is what makes one answer serve all of them. What it reports having
// reached carries the extents still remaining -- one index into a [2][3]int leaves a
// [3]int -- so the answer is simply the outermost of those, exactly as it is for a
// variable. A slice reached the same way carries no extents and falls through to the
// header field, which is where its length lives.
func (e *emitter) arrayChainBound(arg []int32) (string, bool) {
	base, steps, ok := e.factorAccessChain(e.factorKids(arg))
	if !ok {
		return "", false
	}
	cur, ok := e.accessChainType(base, steps)
	if !ok || len(cur.dims) == 0 {
		return "", false
	}
	return cur.dims[0], true
}

func (e *emitter) emitLen(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("len takes exactly one argument")
		return
	}
	arg := args[0].ast
	// `len(Row(a))` / `cap(Row(a))`: a conversion to a defined array type is a no-op
	// on the representation, so the operand is what is measured.
	if operand, ok := e.arrayConvOperand(arg); ok {
		arg = operand
	}
	if tok, ok := e.soleToken(arg); ok && e.f.ch(tok) == IDENT {
		// A pointer to an array measures the array, `len(p)` being `len(*p)`.
		if _, a, ok := e.arrayBase(e.src(tok)); ok {
			e.emit(a.bound)
			return
		}
	}
	// `len(*p)` written out. The dereference of a pointer to a SLICE or a STRING
	// falls through to the header field below, which reads it off `(*p)`; an array
	// has no header, so its extent is answered here.
	if _, a, ok := e.arrayDerefOperand(arg); ok {
		e.emit(a.bound)
		return
	}
	// An array-typed struct field, `len(r.buf)`: its length is the declared extent,
	// exactly as for an array variable. A slice-typed field carries a header and
	// falls through to it below.
	if b, ok := e.arrayChainBound(arg); ok {
		e.emit(b)
		return
	}
	// A string and a slice both carry their length in a `.len` header field.
	if ct, ok := e.exprReprCType(arg); ok && (ct == cString || e.isSliceCType(ct)) {
		e.emitHeaderField(arg, ct, "len")
		return
	}
	e.fail("len is only supported for strings, arrays and slices yet")
}

// emitCap emits the builtin `cap(x)`: an array's capacity is its compile-time
// bound; a slice's is its header's `cap` field. Strings have no capacity.
// emitPanic emits the builtin panic. Only a string argument is supported so far
// -- what smith's oracle assertion and the hardware error paths use -- mapping to
// the runtime ogo_panic(const char* msg) with the ogo_string's char* field. A
// general panic(any) needs value formatting and is left for later.
func (e *emitter) emitPanic(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("panic takes exactly one argument")
		return
	}
	if ct, ok := e.exprReprCType(args[0].ast); !ok || ct != cString {
		e.fail("panic is supported only with a string argument yet")
		return
	}
	e.needPanic()
	e.emit("ogo_panic((")
	e.emitExpr(args[0].ast)
	e.emit(").str)")
}

func (e *emitter) emitCap(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("cap takes exactly one argument")
		return
	}
	arg := args[0].ast
	// `len(Row(a))` / `cap(Row(a))`: a conversion to a defined array type is a no-op
	// on the representation, so the operand is what is measured.
	if operand, ok := e.arrayConvOperand(arg); ok {
		arg = operand
	}
	if tok, ok := e.soleToken(arg); ok && e.f.ch(tok) == IDENT {
		// A pointer to an array measures the array, `len(p)` being `len(*p)`.
		if _, a, ok := e.arrayBase(e.src(tok)); ok {
			e.emit(a.bound)
			return
		}
	}
	// `len(*p)` written out. The dereference of a pointer to a SLICE or a STRING
	// falls through to the header field below, which reads it off `(*p)`; an array
	// has no header, so its extent is answered here.
	if _, a, ok := e.arrayDerefOperand(arg); ok {
		e.emit(a.bound)
		return
	}
	if b, ok := e.arrayChainBound(arg); ok {
		e.emit(b) // an array's capacity is its length: see the len case
		return
	}
	if ct, ok := e.exprReprCType(arg); ok && e.isSliceCType(ct) {
		e.emitHeaderField(arg, ct, "cap")
		return
	}
	e.fail("cap is only supported for arrays and slices yet")
}

// emitHeaderField emits a read of a string's or a slice's header field off arg.
//
// The expression is bound to a temporary first when emitting it produced a call
// returning that header by value: flexcc miscompiles a field read at a nonzero
// offset applied straight to a function's struct return value, yielding garbage
// rather than the field, and both `len` and `cap` read one. It is the defect
// emitAccessChain hoists around for `mk().y`, and a slice expression whose bounds
// are checked is exactly such a call. Everything else keeps the parenthesised read
// it had, so nothing that used to work moved.
func (e *emitter) emitHeaderField(arg []int32, ctype, field string) {
	saved := e.resliceCalled
	e.resliceCalled = false
	text := e.captureC(func() { e.emitExpr(arg) })
	call := e.resliceCalled
	e.resliceCalled = saved
	// Any call, not only a reslice helper: the target's C compiler reads a field at
	// a nonzero offset off a struct RETURN VALUE as garbage, the temporary never
	// having been materialised. The reslice helper was simply the first shape that
	// reached here; `len(b.String())` is another, and it printed -251214335 on the
	// board while the host was right.
	if !call && !e.exprHasCall(arg) {
		e.emit("(" + text + ")." + field)
		return
	}
	e.emit(e.hoist(ctype, func() { e.emit(text) }) + "." + field)
}

// exprHasCall reports whether an expression contains a call anywhere. A field read
// off one has to go through a temporary; see emitHeaderField.
func (e *emitter) exprHasCall(ast []int32) bool {
	for n := range it(ast) {
		if n.sym == CallSuffix {
			return true
		}
		if n.sym != 0 && e.exprHasCall(n.ast) {
			return true
		}
	}
	return false
}

// appendParts validates an append call suffix -- a slice followed by one or more
// values -- returning that slice's element C type and the argument nodes (args[0] is
// the slice, args[1:] the values), and recording needSlice(elem). The first argument
// may be a slice variable or a slice-typed struct field (append(b.data, x)); its type
// is inferred, and emitExpr renders either form. ok is false (with a latched error)
// for any other shape.
func (e *emitter) appendParts(callSuffix []int32) (elem string, args []Node, ok bool) {
	args = e.callArgExprs(callSuffix)
	if len(args) < 2 {
		e.fail("append needs a slice and at least one value -- append(s, x)")
		return "", nil, false
	}
	// Through the underlying type, so a variable of a DEFINED slice type appends:
	// one declared `var d List = make(List, n, c)` keeps "List" as its C type, that
	// being what its methods hang off, and the header's own name is what the element
	// has to be read from.
	ct, ok := e.inferCType(args[0].ast)
	u := e.underlyingCType(ct)
	if !ok || !e.isSliceCType(u) {
		e.fail("append's first argument must be a slice variable or slice field yet")
		return "", nil, false
	}
	elem = sliceElemFromCName(u)
	e.needSlice(elem)
	return elem, args, true
}

// exprIdent returns the sole identifier an expression reduces to (peeling wrapper
// levels), e.g. the "s" in an argument that is just s. ok is false if the
// expression is not exactly one identifier.
func (e *emitter) exprIdent(ast []int32) (string, bool) {
	if tok, ok := e.soleToken(ast); ok && e.f.ch(tok) == IDENT {
		return e.src(tok), true
	}
	return "", false
}

// spreadAppendArg validates `append(s, xs...)` and says what is being spread. Go
// takes exactly one argument after the ellipsis form, and it must be a slice of the
// same element type -- or a STRING when the destination is a []byte, which is the
// one place Go lets two different types meet in an append.
//
// It answers isStr for that string case. A failure is reported here, so a false
// return means the call has already been refused.
func (e *emitter) spreadAppendArg(elem string, args []Node) (isStr, ok bool) {
	if len(args) != 2 {
		e.failAt(args[0].ast, "append takes a single value with \"...\" -- append(s, xs...)")
		return false, false
	}
	ct, known := e.inferCType(args[1].ast)
	if !known {
		e.failAt(args[1].ast, "cannot tell the type of the value spread into append")
		return false, false
	}
	switch {
	case e.isSliceCType(ct) && sliceElemFromCName(ct) == elem:
		return false, true
	case ct == cString && elem == "uint8_t":
		return true, true
	}
	e.failAt(args[1].ast, "cannot append %s... to []%s", e.goTypeName(ct), e.goTypeName(elem))
	return false, false
}

// checkAppendBacking refuses appending a value that reaches this frame's storage into
// a slice whose own backing outlives the frame.
//
// An append writes the value INTO the destination's backing array, so the reference
// lives exactly as long as that array does. Where the destination is a package-level
// or caller-supplied backing, that is longer than the frame -- the same error storing
// into a package variable is, arriving through a door that writes no variable name.
// `gs = append(gs, a[:])` and `boxes = append(boxes, Box{a[:]})` both left a header
// over a dead frame in storage that outlived it, silently.
//
// Appending into a backing that is ITSELF this frame's is fine: the two die together,
// which is what a scratch list built in a function is.
func (e *emitter) checkAppendBacking(elem string, spread bool, args []Node) {
	if len(args) < 2 {
		return
	}
	if _, frame := e.sliceBackingIsFrame(args[0].ast); frame {
		return
	}
	// A SPREAD copies the source's ELEMENTS, not the header naming them, so
	// `append(gs, a[:]...)` over an int element stores no reference to a at all --
	// and refusing it was this check's first answer. What a spread can carry is a
	// reference held INSIDE an element, so it is asked only where the element type
	// can hold one.
	if spread && !e.carriesReference(elem) {
		return
	}
	if n, r, ok := e.frameRefIn(args[1:]); ok {
		e.fail("%v: cannot append %s: the slice it is appended to outlives this function, "+
			"and its storage does not; %s",
			e.f.tok(n.Pos()).Position(), r.what, r.advice())
	}
}

// emitAppend emits the single-result append `append(s, x, ...)` through the trapping
// ogo_append_<T> helper (which panics if the slice is already at cap). Several values
// nest the calls -- append(s, a, b, c) becomes
// ogo_append(ogo_append(ogo_append(s, a), b), c) -- so each is appended in turn and
// any that overflows the capacity traps.
func (e *emitter) emitAppend(callSuffix []int32) {
	elem, args, ok := e.appendParts(callSuffix)
	if !ok {
		return
	}
	e.checkAppendBacking(elem, e.spreadCall(callSuffix), args)
	e.needPanic()
	if e.spreadCall(callSuffix) {
		isStr, ok := e.spreadAppendArg(elem, args)
		if !ok {
			return
		}
		e.includes["string.h"] = true // memmove, in the helper below
		call := appendSliceCName(elem)
		if isStr {
			e.usesAppendStr = true
			call = appendStrCName
		} else {
			e.appendSliceElems[elem] = true
		}
		e.emit(call + "(")
		e.emitExpr(args[0].ast)
		e.emit(", ")
		e.emitExpr(args[1].ast)
		e.emit(")")
		return
	}
	e.appendElems[elem] = true
	values := args[1:]
	for range values {
		e.emit(appendCName(elem) + "(")
	}
	e.emitExpr(args[0].ast)
	for _, v := range values {
		e.emit(", ")
		e.emitExpr(v.ast)
		e.emit(")")
	}
}

// emitCopy emits the builtin copy(dst, src). Both must be slices of the same
// element type; it copies min(len(dst), len(src)) elements and yields that count,
// through the per-element ogo_copy_<T> helper. dst and src may overlap (the helper
// uses memmove), as Go's copy allows.
// registerBuilder pre-populates the type and method maps for the compiler-known
// Builder, so `sb := NewBuilder(back[:])` types as ogo_builder and `sb.WriteString(s)`
// dispatches through the ordinary user-method path -- methodCName + methodPtr -- to
// the ogo_builder_* helpers. Only emitting the helper definitions is gated on actual
// use (usesBuilder), set by needBuilder at the NewBuilder call.
func (e *emitter) registerBuilder() {
	e.namedTypes["ogo_builder"] = true
	e.funcRet["NewBuilder"] = []string{"ogo_builder"}
	e.funcRet["ogo_builder_WriteString"] = nil
	e.funcRet["ogo_builder_WriteByte"] = nil
	e.funcRet["ogo_builder_WriteRune"] = nil
	e.funcRet["ogo_builder_Write"] = nil
	e.funcRet["ogo_builder_Reset"] = nil
	e.funcRet["ogo_builder_String"] = []string{cString}
	e.funcRet["ogo_builder_Len"] = []string{"int"}
	for _, m := range []string{"WriteString", "WriteByte", "WriteRune", "Write", "Reset", "String", "Len"} {
		e.methodPtr["ogo_builder_"+m] = true // all methods take a pointer receiver
	}
}

// needBuilder records that the Builder type is used, so its typedef and helpers are
// emitted, and pulls in what they reference: the byte-slice header, the string type,
// and the stdint/string.h headers.
func (e *emitter) needBuilder() {
	e.usesBuilder = true
	e.needSlice("uint8_t")
	e.usesString = true
	e.includes["stdint.h"] = true
	e.includes["string.h"] = true
}

func (e *emitter) emitCopy(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 2 {
		e.fail("copy takes exactly two arguments -- copy(dst, src)")
		return
	}
	dstCT, dok := e.inferCType(args[0].ast)
	srcCT, sok := e.inferCType(args[1].ast)
	// copy(dst []byte, src string): Go's byte-slice-from-string copy. The string's
	// bytes are copied into the byte slice, min(len(dst), len(src)) of them, with no
	// allocation -- the destination is the caller's storage. This is what lets a
	// user-backed buffer append a string (a WriteString) on this target.
	if dok && sok && srcCT == cString && e.isSliceCType(dstCT) && sliceElemFromCName(dstCT) == "uint8_t" {
		e.needSlice("uint8_t")
		e.usesCopyStr = true
		e.includes["string.h"] = true
		e.emit("ogo_copystr(")
		e.emitExpr(args[0].ast)
		e.emit(", ")
		e.emitExpr(args[1].ast)
		e.emit(")")
		return
	}
	if !dok || !e.isSliceCType(dstCT) || !sok || !e.isSliceCType(srcCT) {
		e.fail("copy's arguments must both be slices")
		return
	}
	if dstCT != srcCT {
		e.fail("copy's arguments must be slices of the same type, not %s and %s", dstCT, srcCT)
		return
	}
	elem := sliceElemFromCName(dstCT)
	e.needSlice(elem)
	e.copyElems[elem] = true
	e.includes["string.h"] = true
	e.emit(copyCName(elem) + "(")
	e.emitExpr(args[0].ast)
	e.emit(", ")
	e.emitExpr(args[1].ast)
	e.emit(")")
}

// emitClear emits the builtin clear(s) over a slice: it zeroes every element, the
// length unchanged. Every OctoGo value's zero is all-zero bytes (a string or slice
// header is {NULL, 0(, 0)}, a bool is false, a pointer is null), so one memset of
// the elements does it for any element type, through the ogo_clear_<T> helper. A
// map argument (there are no maps) or an array (Go's clear takes only a map or
// slice) is refused.
func (e *emitter) emitClear(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("clear takes exactly one argument -- clear(s)")
		return
	}
	ct, ok := e.inferCType(args[0].ast)
	if !ok || !e.isSliceCType(ct) {
		e.fail("clear is only supported on a slice yet")
		return
	}
	elem := sliceElemFromCName(ct)
	e.needSlice(elem)
	e.clearElems[elem] = true
	e.includes["string.h"] = true
	e.emit(clearCName(elem) + "(")
	e.emitExpr(args[0].ast)
	e.emit(")")
}

// isOrderedCType reports whether a C type is one min and max can order: the
// arithmetic types, which C's "<" compares directly, and a string, which needs the
// comparison helper. Go orders exactly these -- its min and max take any ordered
// type -- and a bool, a slice, a struct and a pointer are ordered by neither.
func isOrderedCType(ct string) bool {
	return isIntCType(ct) || ct == "float" || ct == "double" || ct == cString
}

// emitMinMax emits the builtin min or max over one or more ordered arguments. It
// folds a two-argument helper left over the arguments -- min(a, b, c) is
// min(min(a, b), c) -- through a helper (not an inline a<b?a:b) so each argument is
// evaluated exactly once, even one with a side effect. A single argument is itself.
func (e *emitter) emitMinMax(recv string, callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) == 0 {
		e.fail("%s requires at least one argument", recv)
		return
	}
	ct, ok := e.inferCType(args[0].ast)
	if !ok || !isOrderedCType(e.underlyingCType(ct)) {
		e.fail("%s is only supported on ordered arguments -- an integer, a float or a string", recv)
		return
	}
	ct = e.underlyingCType(ct)
	if len(args) == 1 {
		e.emitExpr(args[0].ast) // min(x) / max(x) is x
		return
	}
	if ct == cString {
		e.usesStringCmp = true // the helper orders through ogo_string_cmp
	}
	fn := minCName(ct)
	if recv == "max" {
		fn = maxCName(ct)
		e.maxElems[ct] = true
	} else {
		e.minElems[ct] = true
	}
	for range args[1:] {
		e.emit(fn + "(")
	}
	e.emitExpr(args[0].ast)
	for _, arg := range args[1:] {
		e.emit(", ")
		e.emitExpr(arg.ast)
		e.emit(")")
	}
}

// emitTryAppend emits the two-result append `s, ok = append(s, x)` (or `:=`): it
// binds the ok-form helper's { slice, ok } result to a temporary, then assigns (or,
// for `:=`, declares) the slice and ok targets. A blank target is skipped. This
// form never traps -- an overflow leaves the slice unchanged and reports ok == 0.
func (e *emitter) emitTryAppend(targets []assignTarget, declare []bool, callSuffix []int32) {
	if len(targets) != 2 {
		e.fail("the two-result append form is `s, ok = append(s, x)`")
		return
	}
	elem, args, ok := e.appendParts(callSuffix)
	if !ok {
		return
	}
	if len(args) != 2 {
		e.fail("the two-result append form takes a single value -- s, ok = append(s, x)")
		return
	}
	e.appendokStructs[elem] = true
	call := tryappendCName(elem)
	switch {
	case e.spreadCall(callSuffix):
		isStr, ok := e.spreadAppendArg(elem, args)
		if !ok {
			return
		}
		e.includes["string.h"] = true // memmove, in the helper below
		switch {
		case isStr:
			e.usesTryAppendStr = true
			call = tryappendStrCName
		default:
			e.tryappendSliceEls[elem] = true
			call = tryappendSliceCName(elem)
		}
	default:
		e.tryappendElems[elem] = true
	}
	tmp := e.newTmp()
	e.ind()
	e.emit(appendokCName(elem) + " " + tmp + " = " + call + "(")
	e.emitExpr(args[0].ast)
	e.emit(", ")
	e.emitExpr(args[1].ast)
	e.emit(");\n")
	// The slice target, then the ok target (int).
	if declare[0] && targets[0].plain() {
		e.sliceVars[targets[0].name] = elem
	}
	e.emitStore(targets[0], declare[0], sliceCName(elem), tmp+".slice")
	// A BOOL, as every other ok in the language is and as the checker already types
	// this one -- `var b bool = ok` has always been accepted. Only the emitter said
	// int, so println(ok) printed 1 where the type assertion's ok prints true. The
	// helper's field stays an int and C narrows it, which it does exactly.
	e.emitStore(targets[1], declare[1], cBool, tmp+".ok")
}

// arrayVar looks a name up in the local then the package array environment.
func (e *emitter) arrayVar(name string) (arrDim, bool) {
	if a, ok := e.arrays[name]; ok {
		return a, true
	}
	a, ok := e.globalArrays[e.globalC(name)]
	return a, ok
}

// emitPrint maps print/println to serial output. Each argument prints by type: an
// integer via printf %d, a string via the ogo_string helper (exact byte length),
// and a slice or array as "[e0 e1 ...]".
//
// println separates its arguments with a single space and appends a newline; print
// does NEITHER, writing its arguments adjacently, which is what Go's two do and what
// makes print the one that composes a line -- `print(n, " ")` in a loop puts one
// space between values rather than three.
// emitPrintArg renders a print argument's VALUE. During a defer replay that is the
// temporary captured at the defer statement, not the expression -- which may name a
// variable that has since changed or gone out of scope, and which Go evaluated at
// the defer. The expression is still what the format verb is chosen from; the
// temporary has the same type by construction.
// printArgCType is the C representation type a print argument's value has, which is
// what chooses the format. During a defer replay it comes from the temporary
// captured at the defer statement rather than from the expression: the replay is
// emitted after the body's block scope has been left, so a local's name no longer
// types there, and every argument would fall back to "%d" -- a string printed as
// its header's first word, a bool as 1. That is the whole reason a deferred print
// used to be refused instead of fixed.
func (e *emitter) printArgCType(idx int, arg Node) (string, bool) {
	if e.deferReplay >= 0 && idx < len(e.deferReplayArgs) {
		if ct := e.deferReplayArgs[idx].ctype; ct != "" {
			return e.underlyingCType(ct), true
		}
	}
	return e.exprReprCType(arg.ast)
}

func (e *emitter) emitPrintArg(idx int, arg Node) {
	if e.deferReplay >= 0 && idx < len(e.deferReplayArgs) {
		if a := e.deferReplayArgs[idx]; a.inline {
			e.emitExpr(a.expr)
		} else {
			e.emit(deferArgName(e.deferReplay, idx))
		}
		return
	}
	e.emitExpr(arg.ast)
}

func (e *emitter) emitPrint(newline bool, callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	e.includes["stdio.h"] = true
	switch {
	case len(args) == 0:
		e.ind()
		if newline {
			e.emit("printf(\"\\n\");\n")
		} else {
			e.emit("(void)0;\n")
		}
	case len(args) == 1:
		e.emitPrintOne(newline, 0, args[0])
	default:
		e.emitPrintMulti(newline, args)
	}
}

// printfItem is one piece of a format string: literal text, or a VERB. The
// arguments are matched to the verbs in order by the caller, which is also where a
// count that does not add up is reported.
type printfItem struct {
	lit  string
	spec string // the flags, width and precision between the % and the verb
	verb byte
}

// width reports the field width the spec asks for, and whether it asks at all. It
// is the digits before any '.', a leading '0' being a flag rather than the start of
// a number.
func (it printfItem) width() (int, bool) {
	s := strings.TrimLeft(it.spec, "+- #0")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// precision reports the precision the spec asks for, and whether it asks at all.
// A bare "." means zero, as in Go.
func (it printfItem) precision() (int, bool) {
	i := strings.IndexByte(it.spec, '.')
	if i < 0 {
		return 0, false
	}
	if it.spec[i+1:] == "" {
		return 0, true
	}
	n, err := strconv.Atoi(it.spec[i+1:])
	return n, err == nil
}

// flags is the leading run of flag characters, which is the only place a '0' is one:
// in "%10.3f" the zero belongs to the width.
func (it printfItem) flags() string {
	i := 0
	for i < len(it.spec) && strings.IndexByte("+- #0", it.spec[i]) >= 0 {
		i++
	}
	return it.spec[:i]
}

// hasFlag reports whether the spec carries flag f.
func (it printfItem) hasFlag(f byte) bool { return strings.IndexByte(it.flags(), f) >= 0 }

// leftAlign reports whether the spec pads on the right, which is the '-' flag.
func (it printfItem) leftAlign() bool { return it.hasFlag('-') }

// parsePrintfFormat splits a format string into literal runs and verbs. "%%" is a
// literal percent and consumes no argument; "%T" is a verb like any other, its
// argument being the value whose TYPE is wanted. A verb with no argument left, or
// an argument with no verb, is the caller's to report -- this only reads the shape.
//
// A verb may carry fmt's flags, width and precision -- "%6.2f", "%-8s", "%+05d" --
// which is why the shape read here is more than a single byte. The '*' forms, where
// a width comes from an argument of its own, are not read: they would make the verb
// count stop matching the argument count, and that count is what lets every verb be
// checked against its argument's type at compile time.
func parsePrintfFormat(format string) (items []printfItem, verbs int, badVerb string, ok bool) {
	lit := ""
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			lit += string(format[i])
			continue
		}
		if i+1 == len(format) {
			return nil, 0, "%!(NOVERB)", false // a trailing "%" formats nothing
		}
		i++
		if format[i] == '%' {
			lit += "%"
			continue
		}
		// The flags, then the width, then the precision, in fmt's order.
		start := i
		for i < len(format) && strings.IndexByte("+- #0", format[i]) >= 0 {
			i++
		}
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			i++
		}
		if i < len(format) && format[i] == '.' {
			i++
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		if i == len(format) {
			return nil, 0, "%!(NOVERB)", false
		}
		spec := format[start:i]
		switch c := format[i]; c {
		// Deliberately no 'g': emitPrintfVerb can render it, and the %v path uses it,
		// but C's %g defaults to six significant digits where Go's is the shortest
		// form that reads back exactly, so 1234567.0 prints 1.23457e+06 there and
		// 1.234567e+06 here. Accepting it would buy a rarely-used verb with a silent
		// output difference.
		case 'd', 'x', 'X', 's', 't', 'v', 'f', 'T', 'c':
			if lit != "" {
				items = append(items, printfItem{lit: lit})
				lit = ""
			}
			items = append(items, printfItem{spec: spec, verb: c})
			verbs++
		default:
			return nil, 0, "%" + spec + string(c), false
		}
	}
	if lit != "" {
		items = append(items, printfItem{lit: lit})
	}
	return items, verbs, "", true
}

// emitPrintf emits the builtin `printf(format, args...)`. The format must be a
// CONSTANT -- there is no heap to build one in -- which is what lets every verb be
// checked against its argument's type here rather than going wrong at run time.
//
// One C statement per item, not one packed printf. A string is printed by a helper
// rather than by a conversion (its bytes carry a length and no terminator), and a
// statement each is also what keeps the arguments evaluated in source order: C
// leaves the order in one call unspecified, and the two compilers this targets
// disagree about it (see emitPrintMulti).
func (e *emitter) emitPrintf(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) == 0 {
		e.failAt(callSuffix, "printf takes a format string")
		return
	}
	format, isConst := e.foldConstString(args[0].ast)
	if !isConst {
		e.failAt(args[0].ast, "printf's format must be a constant string")
		return
	}
	items, verbs, badVerb, ok := parsePrintfFormat(format)
	if !ok {
		e.failAt(args[0].ast, "printf: unknown formatting verb %s", badVerb)
		return
	}
	rest := args[1:]
	if verbs != len(rest) {
		e.failAt(args[0].ast, "printf: the format has %s but %s given",
			countUnits(verbs, "verb"), countUnits(len(rest), "argument"))
		return
	}
	e.includes["stdio.h"] = true
	next := 0
	lit := ""
	flush := func() {
		if lit == "" {
			return
		}
		// The literal is itself a C format, so a percent in it -- which is what "%%"
		// became -- has to be doubled again on the way out. It printed correctly under
		// glibc and the target's compiler said "not enough parameters for printf
		// format string", which is what TestTargetBuild is for: the host run builds
		// with -Wno-format and could not have seen it.
		e.ind()
		e.emit("printf(" + cQuote(strings.ReplaceAll(lit, "%", "%%")) + ");\n")
		lit = ""
	}
	for _, item := range items {
		if item.verb == 0 {
			lit += item.lit
			continue
		}
		arg := rest[next]
		next++
		// A type NAME is known here whenever the argument is not an interface, so it
		// joins the literal text and costs no call at all. With a width to fill it
		// cannot: padding is the printf's to do, so that case takes the call.
		if item.verb == 'T' && item.spec == "" {
			if name, static := e.staticTypeName(next-1, arg); static {
				lit += name
				continue
			}
		}
		flush()
		if !e.emitPrintfVerb(item, next-1, arg) {
			return
		}
	}
	flush()
}

// printfArgType resolves the type "%T" reports on. It asks for the DECLARED type
// rather than the representation the other verbs follow: a value of `type Celsius
// int` prints as an int and IS a Celsius, and %T is the one verb that wants the
// name rather than the bytes.
func (e *emitter) printfArgType(idx int, arg Node) (string, bool) {
	if ct, ok := e.inferCType(arg.ast); ok {
		return ct, true
	}
	return e.printArgCType(idx, arg)
}

// staticTypeName answers "%T" for an argument whose type is known where it is
// written, which is every argument that is not an interface value: what an
// interface holds is decided at run time, and its table is what says so.
func (e *emitter) staticTypeName(idx int, arg Node) (string, bool) {
	ct, ok := e.printfArgType(idx, arg)
	if !ok || e.isIfaceCType(ct) {
		return "", false
	}
	return e.goTypeName(ct), true
}

// emitPrintfVerb emits one verb's argument, checking the verb against the type it
// was given. A verb that does not suit its argument is refused here: the format is
// constant and the type is known, so there is nothing left to find out at run time.
func (e *emitter) emitPrintfVerb(item printfItem, idx int, arg Node) bool {
	verb, spec := item.verb, item.spec
	ct, known := e.printArgCType(idx, arg)
	value := func() { e.emitPrintArg(idx, arg) }
	wrong := func(want string) bool {
		e.failAt(arg.ast, "printf: %%%c wants %s, not %s", verb, want, e.goTypeName(ct))
		return false
	}
	// noSpec refuses a width on the verbs that cannot honour one yet. It is a
	// compile-time refusal of a format Go accepts, so it names the verb and what to
	// reach for instead: printing something narrower than asked for, silently, is the
	// outcome worth avoiding.
	noSpec := func(why string) bool {
		e.failAt(arg.ast, "printf: %%%s%c does not take a width or precision yet (%s)",
			spec, verb, why)
		return false
	}
	// Two flags the TARGET's printf ignores, so they are refused here rather than
	// silently dropped there. Both work under the host C compiler, which is exactly
	// what makes them worth refusing: a host-green run would have proved nothing and
	// the board would quietly have printed something narrower. Measured on a P2-EDGE
	// and reduced in doc/printf-flags-ignored.c.
	if item.hasFlag('#') {
		e.failAt(arg.ast, "printf: the '#' flag is not supported by the C backend, "+
			"which prints %%%s%c without the base prefix; write the prefix in the format",
			spec, verb)
		return false
	}
	if item.hasFlag('0') && (verb == 'f' || verb == 'g') {
		e.failAt(arg.ast, "printf: the '0' flag on %%%s%c is not supported by the C "+
			"backend, which pads a float with spaces instead of zeros; it does honour "+
			"'0' on the integer verbs", spec, verb)
		return false
	}
	if verb == 'T' {
		// A width is why a statically known name arrives here rather than being folded
		// into the surrounding literal: the name is known, but the padding is not
		// something a literal can carry.
		if name, static := e.staticTypeName(idx, arg); static {
			e.ind()
			e.emit("printf(\"%" + spec + "s\", " + cQuote(name) + ");\n")
			return true
		}
		// Not static, so an interface: its TABLE names the dynamic type, and a value
		// carrying no table carries no type -- which Go prints as <nil>.
		ict, ok := e.printfArgType(idx, arg)
		if !ok || !e.isIfaceCType(ict) {
			e.failAt(arg.ast, "printf: cannot tell the type of this argument")
			return false
		}
		// Bound to a temporary first: the table is read twice and the argument may be
		// a call. A block keeps that in source order, where the prologue would not.
		tmp := e.newTmp()
		e.ind()
		e.emit("{ " + ict + " " + tmp + " = ")
		value()
		e.emit("; printf(\"%" + spec + "s\", " + tmp + ".vt ? " + tmp + ".vt->" + vtTypeField +
			" : \"<nil>\"); }\n")
		return true
	}
	if verb == 'v' {
		if spec != "" {
			return noSpec("%v prints what println prints, which does its own padding")
		}
		// %v prints what println prints, by calling it -- "[1 2 3]" for a slice, the
		// word for a bool, the shortest form for a float. Restating that would be two
		// spellings free to drift apart.
		//
		// Except for the pointer-shaped types, where the two disagree in Go itself and
		// this follows fmt, whose function printf is: fmt prints "<nil>" for a nil
		// pointer, func or interface where the builtin println prints 0x0, and
		// "&{1 2}" for a pointer to a struct where println prints its address. The
		// second needs a formatter per struct type, which does not exist yet, so
		// rather than print a third thing that is neither, %v declines them and says
		// what does answer.
		if known && !e.printableCType(ct) {
			also := ""
			if e.addressPrintC(ct) != "" {
				also = ", and println prints its address"
			}
			e.failAt(arg.ast, "printf: %%v of %s is not supported yet; %%T prints its type%s",
				e.goTypeName(ct), also)
			return false
		}
		e.emitPrintOne(false, idx, arg)
		return true
	}
	if !known {
		e.failAt(arg.ast, "printf: cannot tell the type of this argument")
		return false
	}
	switch verb {
	case 's':
		if ct != cString {
			return wrong("a string")
		}
		// A string here carries a length and no terminator, so C's own padding cannot
		// be borrowed the way the numeric verbs borrow it: the helper does the width
		// and the precision itself. Precision truncates, as in Go.
		if spec != "" {
			w, _ := item.width()
			p, hasP := item.precision()
			if !hasP {
				p = -1
			}
			e.usesStringPrint = true
			e.usesStringPad = true
			e.ind()
			e.emit("ogo_print_str_pad(")
			value()
			e.emit(fmt.Sprintf(", %d, %d, %d);\n", w, p, boolToInt(item.leftAlign())))
			return true
		}
		e.usesStringPrint = true
		e.ind()
		e.emit("ogo_print_str(")
		value()
		e.emit(");\n")
	case 't':
		if ct != cBool {
			return wrong("a bool")
		}
		e.ind()
		e.emit("printf(\"%" + spec + "s\", (")
		value()
		e.emit(") ? \"true\" : \"false\");\n")
	case 'f', 'g':
		if !isFloatCType(ct) {
			return wrong("a float")
		}
		// %f is fixed-point with six decimals, as C's is and as Go's is; %g is the
		// shortest form, which is what %v of a float asks for. Flags, width and
		// precision mean the same in both, so they pass straight through.
		e.ind()
		e.emit("printf(\"%" + spec + string(verb) + "\", ")
		value()
		e.emit(");\n")
	case 'c':
		if !isIntCType(ct) {
			return wrong("an integer")
		}
		// A rune is one character however many bytes it takes, so the width counts
		// the character and the helper pads around it. A precision is IGNORED rather
		// than refused, which is what fmt does with one: "%5.2c" is a width of five.
		if spec != "" {
			w, _ := item.width()
			e.usesRunePrint = true
			e.usesRunePad = true
			e.ind()
			e.emit("ogo_print_rune_pad((int32_t)(")
			value()
			e.emit(fmt.Sprintf("), %d, %d);\n", w, boolToInt(item.leftAlign())))
			return true
		}
		e.usesRunePrint = true
		e.ind()
		e.emit("ogo_print_rune((int32_t)(")
		value()
		e.emit("));\n")
	case 'd', 'x', 'X':
		if !isIntCType(ct) {
			return wrong("an integer")
		}
		if verb != 'd' && isSignedIntCType(ct) {
			// A negative value prints as a sign and its magnitude, as in Go. C's %x
			// would print the two's complement instead, which is a different number.
			if spec != "" {
				// Padding around a sign the helper prints itself: Go puts %8x of -255
				// as "    -ff" and %08x as "-00000ff", so the fill goes on different
				// sides of the sign depending on the flag. Getting that subtly wrong
				// is worse than saying so.
				return noSpec("a negative %x prints as sign and magnitude here")
			}
			e.usesHexPrint = true
			e.ind()
			e.emit("ogo_print_hex((long long)(")
			value()
			upper := "0"
			if verb == 'X' {
				upper = "1"
			}
			e.emit("), " + upper + ");\n")
			return true
		}
		e.ind()
		e.emit("printf(\"" + intPrintfVerb(verb, ct, spec) + "\", ")
		value()
		e.emit(");\n")
	}
	return true
}

// boolToInt renders a flag as the 0 or 1 a C helper takes.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// intPrintfVerb renders an integer verb at the argument's width: the 64-bit types
// take the "ll" length, which the target's printf needs spelled out (its PRId64 is
// not the standard one -- see the int64 notes in scalarPrintVerb's neighbours).
// The flags, width and precision go between the % and the length, which is where C
// wants them and where fmt writes them, so the spec passes through untouched.
func intPrintfVerb(verb byte, ct, spec string) string {
	long := ""
	switch ct {
	case "int64_t", "uint64_t":
		long = "ll"
	}
	if verb == 'd' {
		switch ct {
		case "unsigned", "uint8_t", "uint16_t", "uint32_t", "uintptr_t":
			return "%" + spec + "u"
		case "uint64_t":
			return "%" + spec + "llu"
		case "int64_t":
			return "%" + spec + "lld"
		}
		return "%" + spec + "d"
	}
	return "%" + spec + long + string(verb)
}

// isFloatCType reports whether ct is one of the floating C types.
func isFloatCType(ct string) bool { return ct == "double" || ct == "float" }

// emitPrintOne emits print/println of a single argument, appending a newline when
// newline is set. Integer and string output are folded into one call (preserving
// the compact printf("%d\n", x) / ogo_println_str(x) forms); slices and arrays go
// through their per-element print helper.
func (e *emitter) emitPrintOne(newline bool, idx int, arg Node) {
	// How a value prints follows its representation, not its name: a value of
	// `type Name string` is a string and prints as one.
	if ct, ok := e.printArgCType(idx, arg); ok {
		if ct == cString {
			e.usesStringPrint = true
			e.ind()
			if newline {
				e.emit("ogo_println_str(")
			} else {
				e.emit("ogo_print_str(")
			}
			e.emitPrintArg(idx, arg)
			e.emit(");\n")
			return
		}
		if e.isSliceCType(ct) {
			e.emitPrintSlice(newline, sliceElemFromCName(ct), func() { e.emitPrintArg(idx, arg) })
			return
		}
	}
	// A bare array variable decays to a pointer, so it is printed by viewing it as a
	// full-length slice header rather than as a (meaningless) %d of its address.
	if base, ok := e.exprIdent(arg.ast); ok {
		if a, ok := e.arrayVar(base); ok {
			e.emitPrintSlice(newline, a.elem, func() {
				e.emit("(" + sliceCName(a.elem) + "){" + base + ", " + a.bound + ", " + a.bound + "}")
			})
			return
		}
	}
	// A bool prints as the word true or false, as in Go.
	if ct, ok := e.printArgCType(idx, arg); ok && ct == cBool {
		e.ind()
		nl := ""
		if newline {
			nl = "\\n"
		}
		e.emit("printf(\"%s" + nl + "\", ")
		e.emitBoolWord(idx, arg)
		e.emit(");\n")
		return
	}
	// A type that does not print as itself does not reach the %d default: that read
	// its first word as an integer and said nothing.
	if ct, ok := e.printArgCType(idx, arg); ok && !e.printableCType(ct) {
		e.emitPrintAddress(newline, ct, idx, arg)
		return
	}
	// Default: an integer, or an integer-typed expression. The conversion is %u for
	// an unsigned type so a large value prints unsigned, as in Go, rather than
	// wrapping negative.
	verb := e.scalarPrintVerbOf(idx, arg)
	e.ind()
	if newline {
		e.emit("printf(\"" + verb + "\\n\", ")
	} else {
		e.emit("printf(\"" + verb + "\", ")
	}
	e.emitPrintArg(idx, arg)
	e.emit(");\n")
}

// emitPrintAddress prints a value Go prints as an address -- a pointer, a func
// value, or an interface as its two words. A struct has no such form in Go, which
// refuses to print one; so does this, and it names the OctoGo type rather than the
// C one.
func (e *emitter) emitPrintAddress(newline bool, ct string, idx int, arg Node) {
	form := e.addressPrintC(ct)
	if form == "" {
		e.failAt(arg.ast, "cannot print a value of type %s", e.goTypeName(ct))
		return
	}
	e.includes["stdint.h"] = true // uintptr_t
	nl := ""
	if newline {
		nl = "\\n"
	}
	e.ind()
	if e.isIfaceCType(ct) {
		// Bound to a temporary first: the two words are read from one value, and the
		// argument may be a call. A block keeps that in source order.
		tmp := e.newTmp()
		e.emit("{ " + ct + " " + tmp + " = ")
		e.emitPrintArg(idx, arg)
		e.emit("; printf(\"" + form + nl + "\", (unsigned)(uintptr_t)" + tmp +
			".data, (unsigned)(uintptr_t)" + tmp + ".vt); }\n")
		return
	}
	e.emit("printf(\"" + form + nl + "\", (unsigned)(uintptr_t)")
	e.emitPrintArg(idx, arg)
	e.emit(");\n")
}

// emitPrintSlice emits a call to the ogo_print_slice_<T> / ogo_println_slice_<T>
// helper for element type elem, with the slice-header argument written by emitArg.
// The element must be scalar-printable (canPrintElem); anything else fails honestly
// rather than emitting a wrong %d. The two forms are tracked apart so only the
// helpers actually called are defined -- a print without a matching println must
// not leave an unused ogo_println_slice_<T> behind.
func (e *emitter) emitPrintSlice(newline bool, elem string, emitArg func()) {
	if !e.canPrintElem(elem) {
		e.fail("printing a slice or array of %q is not supported yet", elem)
		return
	}
	e.needSlice(elem)
	e.ind()
	if newline {
		e.printlnElems[elem] = true
		e.emit(printlnSliceCName(elem) + "(")
	} else {
		e.printSliceElems[elem] = true
		e.emit(printSliceCName(elem) + "(")
	}
	emitArg()
	e.emit(");\n")
}

// emitPrintMulti emits print/println of two or more arguments. When every argument
// prints as a plain integer they fold into a single space-separated printf; a mix
// of types instead prints each value in turn, a space between operands, then the
// trailing newline for println.
func (e *emitter) emitPrintMulti(newline bool, args []Node) {
	allScalar := true
	for i, arg := range args {
		if !e.isScalarPrint(i, arg) {
			allScalar = false
			break
		}
	}
	// An argument that can change state forces the one-per-printf path below, which
	// evaluates the arguments in source order because it emits a separate statement
	// for each. Packing them into a single printf would leave the order to the C
	// compiler, and the two this targets disagree -- `println(f(), x)` where f
	// writes x printed different things depending on which one built it. The bytes
	// written are identical either way; only the number of printf calls differs.
	if allScalar {
		for _, arg := range args {
			if e.exprHasEffect(arg.ast) {
				allScalar = false
				break
			}
		}
	}
	if allScalar {
		e.ind()
		e.emit("printf(\"")
		for i, arg := range args {
			if i > 0 && newline {
				e.emit(" ")
			}
			if e.isBoolPrint(i, arg) {
				e.emit("%s")
			} else {
				e.emit(e.scalarPrintVerbOf(i, arg))
			}
		}
		if newline {
			e.emit("\\n")
		}
		e.emit("\"")
		for i, arg := range args {
			e.emit(", ")
			if e.isBoolPrint(i, arg) {
				e.emitBoolWord(i, arg)
			} else {
				e.emitPrintArg(i, arg)
			}
		}
		e.emit(");\n")
		return
	}
	for i, arg := range args {
		if i > 0 && newline {
			e.ind()
			e.emit("printf(\" \");\n")
		}
		e.emitPrintOne(false, i, arg)
	}
	if newline {
		e.ind()
		e.emit("printf(\"\\n\");\n")
	}
}

// isBoolPrint reports whether an argument prints as a bool word.
func (e *emitter) isBoolPrint(idx int, arg Node) bool {
	ct, ok := e.printArgCType(idx, arg)
	return ok && ct == cBool
}

// emitBoolWord renders a bool argument as the string "true" or "false" via a
// ternary, so println(b) prints the word rather than 1 or 0.
func (e *emitter) emitBoolWord(idx int, arg Node) {
	e.emit("(")
	e.emitPrintArg(idx, arg)
	e.emit(") ? \"true\" : \"false\"")
}

// scalarPrintVerb is the printf conversion for a scalar C type: %u for an unsigned
// one, %d otherwise. Go prints an unsigned value as unsigned, so `%d` on a uint
// wrapped negative once the high bit was set. A narrow unsigned (uint8_t, uint16_t)
// promotes to int in varargs, but its value is non-negative, so %u reads it back
// unchanged; a uint / uint32_t stays unsigned int, which is exactly what %u wants.
func scalarPrintVerb(ct string) string {
	switch ct {
	case "unsigned", "uint8_t", "uint16_t", "uint32_t", "uintptr_t":
		return "%u"
	case "double", "float":
		return "%g" // concise, like Go's fmt for a float (7.0 -> "7", 0.25 -> "0.25")
	case "int64_t":
		return "%lld"
	case "uint64_t":
		return "%llu"
	}
	return "%d"
}

// isSignedIntCType reports whether ct is a SIGNED integer C type. It is what tells
// %x how to print a negative value: Go writes a sign and the magnitude, C the two's
// complement, and only a signed type can be negative to begin with.
func isSignedIntCType(ct string) bool {
	switch ct {
	case "int", "int8_t", "int16_t", "int32_t", "int64_t":
		return true
	}
	return false
}

// isIntCType reports whether ct is one of the integer C types an OctoGo numeric
// maps to. It is the printable-integer set: a named type over int (its own typedef
// name) is not in it, so a slice of one still fails honestly.
// isUnsignedCType reports whether a C type name is an unsigned integer one.
func isUnsignedCType(ct string) bool {
	switch ct {
	case "unsigned", "uint8_t", "uint16_t", "uint32_t", "uint64_t", "uintptr_t":
		return true
	}
	return false
}

// unsignedLevel reports whether an arithmetic level computes in an UNSIGNED type,
// which decides how a constant operand of it must be spelled -- see unsignedLitC.
func (e *emitter) unsignedLevel(ast []int32) bool {
	ct, ok := e.inferCType(ast)
	return ok && isUnsignedCType(ct)
}

// unsignedLitC renders one operand of an unsigned arithmetic level as an UNSIGNED C
// literal, and reports false for an operand that is not a bare integer literal.
//
// It exists for a backend defect, measured on a P2-EDGE. flexcc types `4 * u` --
// a signed constant on the LEFT of an unsigned operand -- as SIGNED, though the
// product's value is right, and every signedness-sensitive operation downstream then
// takes the signed branch:
//
//	4 * u / 3     3937053355, where Go and gcc say 1073741824
//	4 * u >> 1    3758096384, where they say 1610612736
//	v >= 4 * u    true, where they say false
//
// Writing the constant unsigned settles it, and settles the non-commutative shapes
// with it: `100 - u` was wrong the same way and cannot be fixed by reordering.
// `u * 4` -- the unsigned operand first -- was right all along, which is why this
// went unnoticed: the two spellings of one expression disagreed.
//
// A literal that already carries a suffix is left alone; cIntLit gives one to a
// value too wide for a signed long long, which is unsigned already.
func (e *emitter) unsignedLitC(n Node) (string, bool) {
	tok, ok := e.soleToken(n.ast)
	if !ok || e.f.ch(tok) != INT {
		return "", false
	}
	lit := cIntLit(e.src(tok))
	if len(lit) != 0 && !isCDigit(lit[len(lit)-1]) {
		return lit, true // already suffixed, so already unsigned
	}
	return lit + "u", true
}

// isCDigit reports whether c ends a C integer literal's digits rather than its
// suffix. A hex literal's digits include the letters a-f.
func isCDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func isIntCType(ct string) bool {
	switch ct {
	case "int", "unsigned", "int8_t", "int16_t", "int32_t", "int64_t",
		"uint8_t", "uint16_t", "uint32_t", "uint64_t", "uintptr_t":
		return true
	}
	return false
}

// sliceElemPrintf is the printf statement the slice/array printer runs for one
// element `s.ptr[_i]`: a bool as the word true/false and a string as its exact
// bytes, matching the scalar forms (emitBoolWord, ogo_print_str), and an integer
// with the width-appropriate verb from scalarPrintVerb.
func sliceElemPrintf(el string) string {
	switch el {
	case cBool:
		return `printf("%s", s.ptr[_i] ? "true" : "false");`
	case cString:
		// Written out rather than calling ogo_print_str: the slice printers are
		// emitted ahead of the string helpers. Not "%.*s" -- the target's printf
		// truncates that at 62 characters.
		return `for (int _j = 0; _j < s.ptr[_i].len; _j++) { putchar(s.ptr[_i].str[_j]); }`
	}
	return fmt.Sprintf(`printf("%s", s.ptr[_i]);`, scalarPrintVerb(el))
}

// scalarPrintVerbOf returns the print conversion for an argument, defaulting to %d
// when its type cannot be inferred (an integer expression).
func (e *emitter) scalarPrintVerbOf(idx int, arg Node) string {
	if ct, ok := e.printArgCType(idx, arg); ok {
		return scalarPrintVerb(ct)
	}
	return "%d"
}

// isScalarPrint reports whether arg prints via printf %d (an integer or integer-
// typed expression) -- i.e. it is neither a string, a slice nor an array.
func (e *emitter) isScalarPrint(idx int, arg Node) bool {
	if ct, ok := e.printArgCType(idx, arg); ok {
		if ct == cString || e.isSliceCType(ct) {
			return false
		}
	}
	if base, ok := e.exprIdent(arg.ast); ok {
		if _, ok := e.arrayVar(base); ok {
			return false
		}
	}
	return true
}

// printableCType reports whether a value of this C type prints AS ITSELF. What is
// not in the set used to reach the %d default and print its first word as an
// integer -- a struct printed a garbage number that way, which is the silent kind
// of wrong. printArgCType resolves a named type to its representation, so a value
// of `type Celsius int` is an int here and prints as one.
func (e *emitter) printableCType(ct string) bool {
	return ct == cString || ct == cBool || isIntCType(ct) || isFloatCType(ct) ||
		e.isSliceCType(ct)
}

// addressPrintC renders a value that prints as its ADDRESS, which is what Go
// prints for a pointer, a func value and an interface -- the last as its two
// words, "(data,itab)". It returns "" for a type that has no such form, a struct
// above all: Go refuses to print one and so does this.
//
// The 0x is written out rather than taken from %#x, which C suppresses for a zero
// value: a nil pointer printed "0" where Go prints "0x0". The width is the
// target's -- a P2 pointer is 32 bits, so %x is exact there and truncates only
// under the host shim, where an address means nothing to compare anyway.
func (e *emitter) addressPrintC(ct string) string {
	switch {
	case strings.HasSuffix(ct, "*"), strings.HasPrefix(ct, funcTypePrefix):
		return "0x%x"
	case e.isIfaceCType(ct):
		return "(0x%x,0x%x)"
	}
	return ""
}

// canPrintElem reports whether a slice/array of the given C element type can be
// printed: any integer width, a bool, or a string -- the scalar-printable types,
// each rendered by sliceElemPrintf. A slice of structs, pointers, or a named type
// still fails honestly until its own print form is wired up.
func (e *emitter) canPrintElem(elem string) bool {
	return elem == cBool || elem == cString || isIntCType(elem)
}

// derefStars returns the leading pointer-indirection prefix of an AssignHead
// (AssignHead = { "*" } identifier ...), so a dereferenced target `*p = v` writes
// through the pointer rather than to it.
func (e *emitter) derefStars(headAST []int32) string {
	stars := ""
	for n := range it(headAST) {
		if n.sym != 0 || e.f.ch(n.tok) != MUL {
			break
		}
		stars += "*"
	}
	return stars
}

// derefHead matches a parenthesised DEREFERENCE standing as an assignment
// target's base, `(*p).x = v`. AssignHead is `{ "*" } ( identifier | "(" Expression
// ")" )`, so this is the parenthesised alternative holding a `*p` -- distinct from
// the leading-star form `*p = v`, which writes the whole pointee and is read by
// derefStars.
func (e *emitter) derefHead(head Node) (string, bool) {
	kids := slices.Collect(it(head.ast))
	if len(kids) != 3 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[1].sym != Expression {
		return "", false
	}
	return e.derefOperand(kids[1].ast)
}

// addrHead is derefHead for a parenthesised ADDRESS, `(&v).m()` as a statement. The
// caller has already established that a call follows, which is the only suffix this
// form is admitted with -- see factorAddrCall for why the general suffix is not.
func (e *emitter) addrHead(head Node) (string, bool) {
	kids := slices.Collect(it(head.ast))
	if len(kids) != 3 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != LPAREN ||
		kids[2].sym != 0 || e.f.ch(kids[2].tok) != RPAREN || kids[1].sym != Expression {
		return "", false
	}
	return e.addrOperand(kids[1].ast)
}

// emitDerefAssign emits an assignment whose target is reached through a written-out
// dereference. It is the access-chain assignment path with the chain started from
// what the pointer points at rather than from a variable's name -- the same pairing
// of derefBase and the chain walkers' "At" entry points the read side uses.
func (e *emitter) emitDerefAssign(name string, postfix []Node) {
	text, cur, ok := e.derefBase(name)
	if !ok {
		e.fail("cannot assign through *%s", name)
		return
	}
	chain := postfix[:len(postfix)-1]
	if !isAccessChain(chain) {
		e.fail("unsupported assignment target through *%s", name)
		return
	}
	// Typed before anything is emitted, so a target this cannot reach leaves no
	// half-written statement. An ARRAY target is refused with the rest: C has no
	// array assignment, which is what dims being non-empty says.
	at, ok := e.accessChainTypeAt(cur, chain, true)
	if !ok || len(at.dims) != 0 {
		e.fail("cannot assign to this target through *%s", name)
		return
	}
	t, ok := e.assignTailOf(postfix[len(postfix)-1])
	if !ok {
		e.fail("unsupported assignment form through *%s", name)
		return
	}
	t.targetCType = at.ctype
	e.emitAssignTailOrCopy(func() { e.emitAccessChainAt(text, cur, chain, true) }, t)
}

// emitAssignment handles a single-target assignment `lhs = expr`, a field
// assignment `base.f = expr`, a short declaration `lhs := expr`, and increment /
// decrement. The PostfixOp is the postfix's last element; any Selectors before it
// form a field-access target. Indexed targets are not modelled.
func (e *emitter) emitAssignment(head Node, postfix []Node) {
	if len(postfix) == 0 || postfix[len(postfix)-1].sym != PostfixOp {
		e.fail("unsupported assignment target")
		return
	}
	// `(*p).x = v` / `(*p)[i] = v` -- a written-out dereference as the target's
	// base, which the grammar admits as AssignHead's parenthesised alternative.
	// Asked before soleIdent, which finds no name in it: the only identifier is
	// inside the parentheses, and it names the POINTER rather than the target.
	if name, ok := e.derefHead(head); ok {
		e.emitDerefAssign(name, postfix)
		return
	}
	base := e.soleIdent(head.ast)
	if base == "" {
		e.fail("only assignment to a simple variable is supported yet")
		return
	}
	// `Row(a)[0] = v`: like the address-of above, a conversion's result is not
	// addressable, so it cannot be assigned to. Said here rather than left to the
	// shape refusals below, which would call it unsupported -- it is not something
	// this compiler has yet to do, it is something Go rejects.
	if _, _, isConv := e.arrayConvChain(base, postfix); isConv {
		e.fail("cannot assign to a conversion: it is not addressable")
		return
	}
	// A dereferenced target `*p = v` (AssignHead = { "*" } identifier): keep the
	// leading star(s) so the assignment writes through the pointer, not to it. The
	// only reachable case is a pointer receiver mutating its pointee (`*c = v`).
	stars := e.derefStars(head.ast)
	// Both provenance checks come before the shape-specific paths below, each of which
	// returns: what a target is given matters wherever in the target it lands, and
	// both need only the root variable and the operator. A pointer field is what made
	// that necessary -- `n.p = &x` leaves through the access-chain path, so a check
	// placed after it saw slice fields only.
	op := slices.Collect(it(postfix[len(postfix)-1].ast))
	e.checkStoreBacking(base, op)
	e.checkBlockOutlives(base, op)
	e.noteFrameHolder(base, op)
	// `f = keep` rebinds which function f holds; anything else clears the binding.
	if len(postfix) == 1 && stars == "" && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if ct, ok := e.varType(base); ok && e.isFuncCType(ct) {
			if rhs := e.rhsExprs(op[1]); len(rhs) == 1 {
				e.bindFuncValue(base, rhs[0].ast)
			}
		}
	}
	// `b.run = keep` binds a function value held in a FIELD, keyed by the path the
	// call site writes. The binder tracked variables only, so a call through a field
	// consulted no summaries at all and `b.run(&x)` was accepted where the same call
	// through a variable was refused -- a dangling pointer stored in a package
	// variable, silently.
	if stars == "" && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if steps := postfix[:len(postfix)-1]; len(steps) == 1 && steps[0].sym == Selector {
			if fld := e.soleIdent(steps[0].ast); fld != "" {
				if ft, ok := e.fieldType(base, []string{fld}); ok && e.isFuncCType(ft) {
					if rhs := e.rhsExprs(op[1]); len(rhs) == 1 {
						e.bindFuncValue(funcFieldKey(base, fld), rhs[0].ast)
					}
				}
			}
		}
	}
	// A concrete value written into an interface variable: two words rather than one
	// assignment, and the pair decides which table.
	if len(postfix) == 1 && stars == "" && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if ct, ok := e.varType(base); ok && e.isIfaceCType(ct) {
			// nil is not excluded here: ifaceStoreC writes the ZERO interface for
			// it. Leaving it out sent "j = nil" down the ordinary path, which
			// assigns the null POINTER constant -- "j = 0" for a two-word struct,
			// which the host compiler refuses outright and this one miscounts.
			if rhs := e.rhsExprs(op[1]); len(rhs) == 1 {
				if text := e.ifaceStoreC(e.varRef(base), ct, rhs[0].ast); text != "" {
					e.ind()
					e.emit(text)
				}
				return
			}
		}
	}
	// A whole ARRAY written over: `a = b`, or `a = [3]int{1, 2, 3}`. C has no array
	// assignment, so this is a memcpy of the target's own size -- the same lowering
	// `b := a` has always had at a declaration.
	//
	// It used to emit `a = b;` verbatim. flexcc ACCEPTS that as an extension and
	// copies, so the board was right and silent, while the C was not C: gcc rejects
	// it with "assignment to expression with array type", which is why no host test
	// could cover the form. Emitting the copy makes the two agree and the output
	// valid.
	if len(postfix) == 1 && stars == "" && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if dstDim, isArray := e.arrayVar(base); isArray {
			if rhs := e.rhsExprs(op[1]); len(rhs) == 1 {
				if !e.checkArrayShape(dstDim, rhs[0].ast, "assignment") {
					return
				}
				// `w = <-ch` for a channel of arrays: the receive writes through an
				// out parameter, and w IS the storage.
				if elem, ch, ra, isRecv := e.arrayRecvInit(rhs[0].ast); isRecv {
					if dst, ok := e.arrayVar(base); !ok || dst.elem != ra.elem || dst.declSuffix() != ra.declSuffix() {
						e.fail("cannot receive %s into %s", e.goArrayTypeName(ra), e.goArrayTypeName(dst))
						return
					}
					e.chanRecvElems[elem] = true
					e.ind()
					e.emit(chanRecvCName(elem) + "(" + ch + ", " + e.varRef(base) + ");\n")
					return
				}
				// `b = mk()`: the callee fills storage the caller owns, and b IS
				// that storage. So the call writes through b directly -- no copy,
				// which is what the out-parameter ABI is for.
				if cname, a, isCall := e.arrayResultCall(rhs[0].ast); isCall {
					if dst, ok := e.arrayVar(base); !ok || dst.elem != a.elem || dst.declSuffix() != a.declSuffix() {
						e.fail("cannot assign %s to %s", e.goArrayTypeName(a), e.goArrayTypeName(dst))
						return
					}
					e.emitArrayResultCall(e.varRef(base), cname, rhs[0].ast)
					return
				}
				if src, ok := e.arraySourceC(rhs[0].ast); ok {
					e.includes["string.h"] = true
					dst := e.varRef(base)
					e.ind()
					e.emit("memcpy(" + dst + ", " + src + ", sizeof(" + dst + "));\n")
					return
				}
			}
		}
	}
	// Multiple assignment `a, b = f()` / `a, b := f()`: the PostfixOp carries the
	// extra targets as LhsItems ahead of the operator. Recognised here, ahead of the
	// target shapes below, because the head of a multiple assignment may take any of
	// those shapes itself -- `b[i], b[j] = b[j], b[i]` -- and each of them ends in
	// assignTailOf, which a multiple assignment's PostfixOp is not.
	if containsSym(op, LhsItem) {
		e.emitMultiAssign(head, base, stars, postfix[:len(postfix)-1], op)
		return
	}
	// Index target `a[i] = v` (single index; mixing indexes and fields is not
	// modelled). The index is an expression, so it is emitted directly rather than
	// built into the string lhs the field path uses.
	if len(postfix) == 2 && postfix[0].sym == Index {
		e.emitIndexAssign(base, postfix[0], postfix[1])
		return
	}
	// A target whose chain alternates indexes and selectors more than once --
	// `s[i].v[j] = e`. Tried first: the fixed shapes below cannot match it, and the
	// chain is typed before anything is emitted, so a rejected one leaves no
	// half-written statement.
	// A send whose channel is a field, `ports.tx <- v`, is a send and not an
	// assignment to a chain: the tail is "<-", which no assignment tail matches.
	// Asked before the chain path so it is not claimed and then refused there.
	isFieldSend := false
	if tail := postfix[len(postfix)-1]; tail.sym == PostfixOp {
		for c := range it(tail.ast) {
			if c.sym == 0 && e.f.ch(c.tok) == ARROW {
				isFieldSend = true
			}
		}
	}
	if chain := postfix[:len(postfix)-1]; stars == "" && !isFieldSend && isAccessChain(chain) {
		// A slice-valued target is a header assignment, `s[i].v = xs`, which C makes
		// by copying the struct -- the view changes, the storage it names does not.
		// An ARRAY-valued one is a memcpy of the target's own size, C having no array
		// assignment: `sheet[0].body = r`, `h.rows[1] = r`.
		//
		// Both only once an index has put the target out of the fixed shapes' reach,
		// though: a plain field target belongs to them, since they are what knows how
		// to give a `make` its backing array (`b.data = ...`) and what already copies
		// a whole array field (`s.a = b`, through fieldArray below).
		indexed := hasIndexStep(chain)
		if cur, ok := e.accessChainType(base, chain); ok &&
			(len(cur.dims) == 0 && (!cur.slice || indexed) || len(cur.dims) != 0 && indexed) {
			t, ok := e.assignTailOf(postfix[len(postfix)-1])
			if !ok {
				e.fail("unsupported assignment form for an access chain")
				return
			}
			t.targetCType = cur.ctype
			if len(cur.dims) != 0 {
				t.targetArray = arrDim{elem: cur.elem, bound: cur.dims[0], inner: cur.dims[1:], name: cur.name}
			}
			e.emitAssignTailOrCopy(func() { e.emitAccessChain(base, chain) }, t)
			return
		}
	}
	// A full index into a multi-dimensional array, `m[i][j] = v`: an optional field
	// chain then one Index per dimension.
	// An indexed element's field `s[i].x = v` / `p.pts[i].x = v`: an optional field
	// chain, one index, then at least one selector before the assignment. Tried
	// ahead of the index-last shape below, which cannot match a trailing selector.
	// A slice-field indexed target `b.data[i] = v`: a field-access chain, a single
	// trailing index, then the assignment (postfix = { Selector } Index PostfixOp).
	if len(postfix) >= 3 && postfix[len(postfix)-2].sym == Index {
		if flds, ok := e.selectorFields(postfix[:len(postfix)-2]); ok {
			if _, _, _, ok := e.indexedContainer(base, flds); ok {
				e.emitFieldIndexAssign(base, flds, postfix[len(postfix)-2], postfix[len(postfix)-1])
				return
			}
		}
	}
	// A send whose channel is reached through an INDEX, `ws[i].cmd <- v`. The chain
	// walker renders it and says what it reaches, which the field list below cannot
	// do; a channel is a pointer, so what it renders is the channel.
	if isFieldSend {
		if steps := postfix[:len(postfix)-1]; isAccessChain(steps) && hasIndexStep(steps) {
			text, ct, _, okc := e.chainCText(base, steps)
			if !okc || !e.isChanCType(ct) {
				e.fail("a send statement needs a channel on the left")
				return
			}
			e.emitChanSend(text, e.chanElemOfCType(ct), op)
			return
		}
	}
	// `e.(*P).n = v` -- an assertion as the target's base. The assertion binds to a
	// temporary and the rest of the target applies to that, which is the same
	// rebasing the read side does.
	if tail := postfix[len(postfix)-1]; tail.sym == PostfixOp {
		if iface, target, isIface, rest, ok := e.assertionSteps(base, postfix[:len(postfix)-1]); ok && len(rest) != 0 {
			name, _, ok := e.hoistAssert(base, iface, target, isIface)
			if !ok {
				return
			}
			cur, ok := e.accessChainType(name, rest)
			if !ok || len(cur.dims) != 0 {
				e.fail("cannot assign to this target through an assertion")
				return
			}
			t, ok := e.assignTailOf(tail)
			if !ok {
				e.fail("unsupported assignment form through an assertion")
				return
			}
			t.targetCType = cur.ctype
			e.emitAssignTailOrCopy(func() { e.emitAccessChain(name, rest) }, t)
			return
		}
	}
	// A target the shapes above did not claim. Diagnosed against the chain before
	// the field list is built, so a step the operand's type cannot take is named as
	// itself -- an index on an int field used to be "only simple and field
	// assignment targets are supported yet", which describes a missing feature
	// where Go rejects the program.
	//
	// Only when the base is a VALUE this walker knows. A write through an import
	// qualifier, `pkg.V = x`, has a package name there, which is not a value with
	// fields at all and belongs to qualifiedGlobalRead below.
	if steps := postfix[:len(postfix)-1]; isAccessChain(steps) {
		if _, isValue := e.accessBase(base); isValue && e.failChainSteps(head.Pos(), base, steps) {
			return
		}
	}
	var fields []string
	for _, n := range postfix[:len(postfix)-1] {
		fld := ""
		if n.sym == Selector {
			fld = e.soleIdent(n.ast)
		}
		if fld == "" {
			e.fail("only simple and field assignment targets are supported yet")
			return
		}
		fields = append(fields, fld)
	}
	lhs := e.varRef(base) // a package global target is mangled; a local keeps its name
	if len(fields) != 0 {
		// A write to an exported variable of an imported package, `pkg.V = x` (or a
		// field of it): base is the import qualifier, so the target is that package's
		// mangled global, symmetric with the read. Otherwise a struct field target.
		if text, _, ok := e.qualifiedGlobalRead(base, fields); ok {
			lhs = text
		} else {
			lhs = e.fieldAccessC(base, fields) // a field target, "->" through pointers
		}
	}
	if stars != "" {
		// Parenthesised, because C's "++" binds tighter than its unary "*": `*p++`
		// there is `*(p++)`, which increments the POINTER and throws the load away,
		// where Go's is `(*p)++`. The other tails do not need it -- "=" and the
		// compound operators bind looser -- but writing one form keeps the target
		// from depending on which tail follows it.
		lhs = "(" + stars + lhs + ")"
	}

	// A whole ARRAY FIELD written over, `s.a = b` / `s.a = [3]int{1, 2, 3}`: the
	// same copy the plain-variable target takes, and needed for the same reason --
	// `s.a = b;` is not C, however willingly flexcc takes it.
	if len(fields) != 0 && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if fa, isArray := e.fieldArray(base, fields); isArray {
			if rhs := e.rhsExprs(op[1]); len(rhs) == 1 {
				if !e.checkArrayShape(fa, rhs[0].ast, "assignment") {
					return
				}
				// `s.v = <-ch`: the field is the storage the receive writes into.
				if elem, ch, ra, isRecv := e.arrayRecvInit(rhs[0].ast); isRecv {
					if fa.elem != ra.elem || fa.declSuffix() != ra.declSuffix() {
						e.fail("cannot receive %s into %s", e.goArrayTypeName(ra), e.goArrayTypeName(fa))
						return
					}
					e.chanRecvElems[elem] = true
					e.ind()
					e.emit(chanRecvCName(elem) + "(" + ch + ", " + lhs + ");\n")
					return
				}
				// `s.v = mk()`: the field is the caller's storage, so the out
				// parameter writes into it, as it does for a plain variable.
				if cname, a, isCall := e.arrayResultCall(rhs[0].ast); isCall {
					if fa.elem != a.elem || fa.declSuffix() != a.declSuffix() {
						e.fail("cannot assign %s to %s", e.goArrayTypeName(a), e.goArrayTypeName(fa))
						return
					}
					e.emitArrayResultCall(lhs, cname, rhs[0].ast)
					return
				}
				if src, ok := e.arraySourceC(rhs[0].ast); ok {
					e.includes["string.h"] = true
					e.ind()
					e.emit("memcpy(" + lhs + ", " + src + ", sizeof(" + lhs + "));\n")
					return
				}
			}
		}
	}

	// Increment/decrement: PostfixOp = "++" | "--" (no operand of its own).
	if len(op) == 1 && op[0].sym == 0 {
		switch e.f.ch(op[0].tok) {
		case INC:
			e.ind()
			e.emit(lhs + "++;\n")
			return
		case DEC:
			e.ind()
			e.emit(lhs + "--;\n")
			return
		}
	}
	// PostfixOp = AssignOp Expression -- a compound assignment `x += e`. The target
	// is emitted once, which is the language semantics: it is evaluated once, not
	// twice as the `x = x + e` expansion would suggest.
	if len(op) == 2 && op[0].sym == AssignOp {
		t, ok := e.assignTailOf(postfix[len(postfix)-1])
		if !ok {
			e.fail("unsupported compound assignment operator")
			return
		}
		// Routed through emitAssignTailOrCopy so a string "+=" is refused there,
		// centrally, the same as the field and access-chain compound targets.
		e.emitAssignTailOrCopy(func() { e.emit(lhs) }, t)
		return
	}
	// PostfixOp = "<-" Expression (send), or ( "=" | ":=" ) ExpressionList, for the
	// single-target forms. The RHS of "="/":=" is a list of one here (a longer list
	// is a multiple assignment, handled above); a send keeps its bare Expression.
	if len(op) != 2 || op[0].sym != 0 {
		e.fail("only `name = expr`, `name := expr`, `name++` and `name--` are supported yet")
		return
	}
	opTok := e.f.ch(op[0].tok)
	var rhsAst []int32
	if opTok == ARROW {
		if op[1].sym != Expression {
			e.fail("unsupported send statement")
			return
		}
		rhsAst = op[1].ast
	} else {
		rhs := e.rhsExprs(op[1])
		if len(rhs) != 1 {
			e.fail("only `name = expr`, `name := expr`, `name++` and `name--` are supported yet")
			return
		}
		rhsAst = rhs[0].ast
	}
	switch opTok {
	case ARROW:
		// A send `ch <- v`. The receive form `x = <-ch` is an ordinary assignment
		// whose right-hand side is a receive expression, so it does not come here.
		// The channel may be a FIELD, `ports.tx <- v`. A channel is a pointer to its
		// cell, so a field holding one is an ordinary pointer field and lhs already
		// names it; only the type has to be looked up through the field rather than
		// off the root variable.
		ct, ok := e.varType(base)
		if len(fields) != 0 {
			ct, ok = e.fieldType(base, fields)
		}
		if !ok || !e.isChanCType(ct) {
			e.fail("a send statement needs a channel on the left")
			return
		}
		e.emitChanSend(lhs, e.chanElemOfCType(ct), op)
		return
	case ASSIGN:
		if lhs == "_" {
			// A blank-identifier assignment discards the value: evaluate the
			// right-hand side for its side effects and drop the result. The
			// `(void)` cast makes the discard explicit and valid C even when the
			// expression is a plain value. (`_ := expr` is rejected by the checker.)
			e.emitDiscard(rhsAst)
			return
		}
		// `s = nil` resets a slice or an interface to its ZERO VALUE, not to the
		// integer 0 -- neither is one word. It holds for a field as well as a
		// variable: asking only about a bare name left "h.s = nil" assigning 0 to a
		// three-word header, which the host compiler refuses and this one miscounts.
		if e.isNilExpr(rhsAst) {
			ct, ok := e.varType(base)
			if len(fields) != 0 {
				ct, ok = e.fieldType(base, fields)
			}
			switch {
			case ok && e.isSliceCType(ct):
				e.ind()
				e.emit(lhs + " = (" + ct + "){0};\n")
				return
			case ok && e.isIfaceCType(ct):
				e.ind()
				e.emit(e.ifaceStoreC(lhs, ct, rhsAst))
				return
			}
		}
		// A make initializer assigned to an existing lvalue -- a slice variable
		// (`s = make(...)`) or a slice struct field (`b.data = make(...)`) -- hoists a
		// backing array and assigns a fresh { backing, len, cap } header.
		if elem, lenAST, capAST, ok := e.makeSliceInit(rhsAst); ok {
			e.needSlice(elem)
			e.emitMakeSliceAssign(lhs, sliceCName(elem), elem, lenAST, capAST)
			return
		}
		// Shared with the indexed and access-chain targets, so a struct holding an
		// array becomes a memcpy here too (see emitAssignTailOrCopy).
		e.emitAssignTailOrCopy(func() { e.emit(lhs) }, assignTail{op: "=", rhs: rhsAst})
	case DEFINE:
		if len(fields) != 0 {
			e.fail("a short declaration cannot have a field target")
			return
		}
		e.emitInferredLocal(base, rhsAst)
	default:
		e.fail("only `name = expr` and `name := expr` are supported yet")
	}
}

// emitInferredLocal emits a type-inferred local declaration -- the short form
// `name := expr` or the var form `var name = expr` -- inferring name's C type from
// the initializer. A make initializer synthesises a slice backing array + header; a
// slice-typed result records its element type so later indexing / len / cap /
// append on name resolve.
func (e *emitter) emitInferredLocal(name string, initExpr []int32) {
	e.bindFuncValue(name, initExpr)
	// `r := row(a)` for `type row [3]int`: the conversion changes nothing about the
	// value -- a defined type is a typedef of what it stands for -- so what is
	// declared is a copy of the operand, which is the branch below. Unwrapped here
	// because an array is the one representation C has no value type for, so the
	// paths that read an array operand all read a NAME and would not see through it.
	if operand, ok := e.arrayConvOperand(initExpr); ok {
		initExpr = operand
	}
	if typeAST, lit, ok := e.soleArrayLit(initExpr); ok {
		e.emitArrayLitVar(name, typeAST, lit, false)
		return
	}
	// `v := <-ch` for a channel of arrays: the receive writes through an out
	// parameter, and the declaration IS the storage it writes into. Asked before the
	// array-result call below, which it mirrors.
	if elem, base, a, ok := e.arrayRecvInit(initExpr); ok {
		e.arrays[name] = a
		e.locals[name] = elem
		e.chanRecvElems[elem] = true
		e.ind()
		e.emit(a.elem + " " + userIdent(name) + a.declSuffix() + ";\n")
		e.ind()
		e.emit(chanRecvCName(elem) + "(" + base + ", " + userIdent(name) + ");\n")
		return
	}
	// `a := mk()` where mk returns an array: the caller owns the storage and the
	// callee fills it, so the declaration IS the storage and the call is a statement.
	if cname, a, ok := e.arrayResultCall(initExpr); ok {
		e.arrays[name] = a
		e.ind()
		e.emit(a.elem + " " + userIdent(name) + a.declSuffix() + ";\n")
		e.emitArrayResultCall(userIdent(name), cname, initExpr)
		return
	}
	// `b := a` where a is an array variable: Go copies the array by value. C cannot
	// assign an array, so declare b with a's dimensions and memcpy from a. (inferCType
	// cannot type an array operand -- an array has no assignable C value type -- so
	// this must be handled before the general path below.)
	if rhs, ok := e.exprIdent(initExpr); ok {
		if a, isLocal := e.arrays[rhs]; isLocal {
			e.emitArrayCopy(name, rhs, a)
			return
		}
		if a, isGlobal := e.globalArrays[e.globalC(rhs)]; isGlobal {
			e.emitArrayCopy(name, e.globalC(rhs), a)
			return
		}
	}
	// `b := *p` where p points at an array: the same copy, reading through the
	// dereference. inferCType does type `*p` -- it is a pointer's element -- but the
	// type it names is the array's typedef, which C cannot initialize from another
	// array any more than it can assign one, so this has to come first.
	if src, a, ok := e.arrayDerefOperand(initExpr); ok {
		e.emitArrayCopy(name, src, a)
		return
	}
	// `x := h.f` where the field is an ARRAY: the same copy again, reached through a
	// field rather than named directly. Without it the declaration fell through to
	// inferCType, which types no array operand and reported "cannot infer a type" of
	// a field whose type the program had written down.
	if src, a, ok := e.arrayFieldOperand(initExpr); ok {
		e.emitArrayCopy(name, src, a)
		return
	}
	// `x := pool[1]` where the element is an ARRAY: the same copy through a chain
	// that includes an index. The field form above is tried first because it keeps
	// the type's NAME, which a walk through an index cannot -- an array of a defined
	// array type is flattened to its extents, so `[2]Row` is a [2][2]int by then.
	// The copy itself is by shape, which is what Go's copy is.
	if src, a, ok := e.arrayChainOperand(initExpr); ok {
		e.emitArrayCopy(name, src, a)
		return
	}
	if elem, lenAST, capAST, ok := e.makeSliceInit(initExpr); ok {
		cname := sliceCName(elem)
		e.needSlice(elem)
		e.sliceVars[name] = elem
		e.locals[name] = cname
		e.emitMakeSliceVar(name, cname, elem, lenAST, capAST, false)
		return
	}
	ct, ok := e.inferCType(initExpr)
	if !ok {
		e.fail("cannot infer a type for the declaration of %q", name)
		return
	}
	// A literal of a DEFINED type gives the variable THAT type rather than the
	// representation underneath it. inferCType answers with the representation --
	// `List{1, 2, 3}` is an ogo_slice_int -- and taking that verbatim lost the name
	// a method hangs off: `d := List{1, 2, 3}` then `d.sum()` was reported as
	// "unknown package \"d\"", the emitter reading the call as a package
	// qualification because nothing said d had a type with methods. The declared
	// forms, `var d List = make(...)` and `var l List = back[:]`, always kept it.
	if nm, _, isLit := e.soleCompositeLit(initExpr); isLit && nm != ct && e.underlyingCType(nm) == ct {
		ct = nm
	}
	e.locals[name] = ct
	// Through the underlying type, so a defined slice type is still a slice here:
	// what makes `d[i]` and len(d) work is this entry, and the name above is what
	// makes `d.sum()` work. Both are wanted.
	if u := e.underlyingCType(ct); e.isSliceCType(u) {
		e.sliceVars[name] = sliceElemFromCName(u)
		// A slice copied from another views the same storage, so it inherits where
		// that storage lives; otherwise returning the copy would dodge the check.
		if e.initViewsFrame(initExpr) {
			e.frameBacked[name] = true
		}
	}
	e.noteDeclFrameHolder(ct, name, initExpr)
	e.bindLitFuncFields(name, initExpr)
	e.emitVarDeclInit(ct, name, initExpr)
}

// noteDeclFrameHolder marks a declared variable that its initializer gives a
// reference to this frame's storage: a struct literal with a local's address or a
// slice of a local array in a field, `b := buf{data: a[:]}`, or a copy of a variable
// already marked, `c := b`.
//
// Only a struct or pointer target is considered. An initializer is a whole
// expression and a frame reference may appear anywhere inside one without the value
// carrying it out -- `n := len(a[:])` is an int -- so the type is what says whether
// there is anything to carry.
func (e *emitter) noteDeclFrameHolder(ctype, name string, initExpr []int32) {
	// A BUILDER is considered too. It is a pointer into a backing array the caller
	// owns -- that is the whole point of it -- so one built over a LOCAL array holds
	// a reference to this frame as surely as a struct with a slice field does, and
	// it is not in e.structs, being a type the compiler supplies rather than one the
	// program declares.
	if _, isStruct := e.structs[methodBaseType(ctype)]; !isStruct && !e.isPointer(ctype) && ctype != "ogo_builder" {
		return
	}
	if r, ok := e.frameRefOf(initExpr); ok {
		e.frameHolder[name] = r.origin
		return
	}
	// A composite literal: each element is its own expression, and any one of them
	// carrying a reference makes the whole value carry it.
	for n := range it(initExpr) {
		if n.sym == 0 {
			continue
		}
		if r, ok := e.frameRefOf(n.ast); ok {
			e.frameHolder[name] = r.origin
			return
		}
		e.noteDeclFrameHolder(ctype, name, n.ast)
		if e.frameHolder[name] != "" {
			return
		}
	}
}

// emitVarDeclInit emits a local declaration of ctype with an initializer. A struct
// holding an array is declared and then filled with memcpy: initializing one from
// another value is a copy, which flexcc gets wrong (see hasArrayField). A composite
// literal is not a copy -- it is the aggregate initialization that does work -- so
// it stays on the ordinary path.
func (e *emitter) emitVarDeclInit(ctype, name string, initExpr []int32) {
	e.bindFuncValue(name, initExpr)
	// `var v [3]int = <-ch` / `v := <-ch` for a channel of arrays: the receive writes
	// through an out parameter, and this declaration IS the storage it writes into.
	if elem, base, a, ok := e.arrayRecvInit(initExpr); ok {
		e.arrays[name] = a
		e.locals[name] = elem
		e.chanRecvElems[elem] = true
		e.ind()
		e.emit(a.elem + " " + userIdent(name) + a.declSuffix() + ";\n")
		e.ind()
		e.emit(chanRecvCName(elem) + "(" + base + ", " + userIdent(name) + ");\n")
		return
	}
	if e.isIfaceCType(ctype) {
		e.locals[name] = ctype
		e.ind()
		e.emit(ctype + " " + name + " = {0};\n")
		if text := e.ifaceStoreC(name, ctype, initExpr); text != "" {
			e.ind()
			e.emit(text)
		}
		return
	}
	// A self-referential initializer -- `var x = x + 5` where an outer x is in scope
	// and this declaration shadows it -- reads the *outer* binding in Go, because a
	// var initializer is evaluated before the new name comes into scope. C, however,
	// brings the new name into scope for its own initializer, so a verbatim emission
	// would read the new, uninitialized variable instead. Evaluate the initializer
	// into a fresh temporary first, while `name` still resolves to the outer binding,
	// then define name from the temporary. The temporary name cannot itself appear in
	// the initializer, so the recursion bottoms out on the ordinary path below.
	// cn is the emitted C name (Unicode-escaped); `name` stays the source name, used
	// for initRefsName's comparison against the initializer's own identifiers.
	cn := userIdent(name)
	if e.initRefsName(initExpr, name) {
		tmp := e.newTmp()
		e.emitVarDeclInit(ctype, tmp, initExpr)
		if e.hasArrayField(ctype) {
			e.includes["string.h"] = true
			e.ind()
			e.emit(ctype + " " + cn + ";\n")
			e.ind()
			e.emit("memcpy(&" + cn + ", &" + tmp + ", sizeof(" + ctype + "));\n")
			return
		}
		e.ind()
		e.emit(ctype + " " + cn + " = " + tmp + ";\n")
		return
	}
	if _, _, isLit := e.soleCompositeLit(initExpr); !isLit && e.hasArrayField(ctype) {
		if !e.checkStructCopySrc(ctype, initExpr) {
			return
		}
		e.ind()
		e.emit(ctype + " " + cn + ";\n")
		e.emitStructCopy(cn, ctype, initExpr)
		return
	}
	// An element of a struct literal that C cannot put in an initializer -- an
	// ARRAY field filled from a value -- is zeroed there and copied in here, the
	// declaration being what gives the literal a name to copy into.
	fixups := e.captureLitFixups(func() {
		e.ind()
		e.emit(ctype + " " + cn + " = ")
		e.emitVarInit(initExpr)
		e.emit(";\n")
	})
	e.flushLitFixups(cn, fixups)
}

// initRefsName reports whether initExpr reads the variable `name` as a value: a
// bare identifier occurrence, but not a struct field (a Selector's field name) or
// the field name in a keyed composite literal. Such a read means the outer,
// shadowed binding, so emitVarDeclInit must capture the initializer before the new
// C variable of the same name shadows it. A Selector node's subtree is only the
// field identifier -- the selected operand is a separate sibling -- so skipping it
// drops `obj.name` (a field) while still catching `name.field` (the operand).
func (e *emitter) initRefsName(initExpr []int32, name string) bool {
	var walk func(nodes []int32) bool
	walk = func(nodes []int32) bool {
		for n := range it(nodes) {
			switch {
			case n.sym == Selector:
				// The field name, not a value reference -- skip its subtree.
			case n.sym != 0:
				if walk(n.ast) {
					return true
				}
			case e.f.ch(n.tok) == IDENT && e.src(n.tok) == name:
				return true
			}
		}
		return false
	}
	return walk(initExpr)
}

// selectorFields collects the field names from a run of Selector nodes, or ok=false
// if any node is not a plain field selector (or the run is empty).
func (e *emitter) selectorFields(nodes []Node) (fields []string, ok bool) {
	fields, ok = e.selectorFieldsAll(nodes)
	return fields, ok && len(fields) != 0
}

// selectorFieldsAll is selectorFields without the non-empty requirement: an empty
// node list yields no fields and ok, so a chain may start directly with an index
// (`s[i].x`, where nothing is selected before the index).
func (e *emitter) selectorFieldsAll(nodes []Node) (fields []string, ok bool) {
	for _, n := range nodes {
		if n.sym != Selector {
			return nil, false
		}
		f := e.soleIdent(n.ast)
		if f == "" {
			return nil, false
		}
		fields = append(fields, f)
	}
	return fields, true
}

// indexedContainer resolves what an index applies to: the C expression naming the
// element storage, the element C type, and the length to bounds-check against.
// With no leading field chain the base is a slice or array variable; with one it
// is a slice-typed struct field (`p.pts[i]`).
func (e *emitter) indexedContainer(base string, pre []string) (expr, elem, lenExpr string, ok bool) {
	if len(pre) == 0 {
		if el, ok := e.sliceElem(base); ok {
			return base + ".ptr", el, base + ".len", true
		}
		if a, ok := e.arrayVar(base); ok {
			if a.dims() > 1 {
				// One index into a [N][M]T yields a [M]T row, not an element. C
				// cannot assign an array by value, and typing the result as elem
				// would emit `int r = m[1];`, so leave it to the full-index path.
				return "", "", "", false
			}
			return base, a.elem, a.bound, true
		}
		return "", "", "", false
	}
	// An array-typed field indexes its inline storage directly, bounded by the
	// declared extent; a slice-typed one goes through its header's backing pointer,
	// bounded by the runtime length.
	if a, ok := e.fieldArray(base, pre); ok {
		if a.dims() > 1 {
			return "", "", "", false // see the variable case above
		}
		return e.fieldAccessC(base, pre), a.elem, a.bound, true
	}
	ct, ok := e.fieldType(base, pre)
	if !ok || !e.isSliceCType(ct) {
		return "", "", "", false
	}
	el, ok := e.sliceElemByName[ct]
	if !ok {
		return "", "", "", false
	}
	lv := e.fieldAccessC(base, pre)
	return lv + ".ptr", el, lv + ".len", true
}

// emitIndexSelect emits `<container>[i].f...` -- an indexed element followed by a
// field chain. The trailing selectors are emitted after the index rather than
// concatenated into the prefix, because once the index expression has been written
// the accumulated C text is no longer available as a string.
func (e *emitter) emitIndexSelect(expr, lenExpr string, low []int32, elem string, post []string) {
	e.emit(expr + "[")
	e.emitIndex(low, lenExpr)
	e.emit("]")
	ct := elem
	for _, f := range post {
		sel, ok := e.selectC(ct, f)
		if !ok {
			e.fail("no field %q on %s", f, ct)
			return
		}
		e.emit(sel)
		ct, _ = e.structFieldType(ct, f) // validated by the caller via chainFieldType
	}
}

// assignTail classifies the PostfixOp closing an assignment statement: `++`, `--`,
// or `= expr`. For the first two suffix is the C operator and rhs is nil; for an
// assignment suffix is empty and rhs is the right-hand Expression. ok is false for
// any other PostfixOp -- a channel send, or a multi-target assignment -- which has
// its own path.
//
// It classifies without emitting, so a caller can reject an unsupported form
// before writing a partial statement.
// rhsExprs returns the right-hand-side expressions of an assignment: the children
// of an ExpressionList, or a single bare Expression. The assignment RHS is an
// ExpressionList, while a compound assignment's operand is a bare Expression.
func (e *emitter) rhsExprs(n Node) []Node {
	if n.sym == Expression {
		return []Node{n}
	}
	if n.sym != ExpressionList {
		return nil
	}
	var out []Node
	for c := range it(n.ast) {
		if c.sym == Expression {
			out = append(out, c)
		}
	}
	return out
}

func (e *emitter) assignTailOf(opNode Node) (assignTail, bool) {
	if opNode.sym != PostfixOp {
		return assignTail{}, false
	}
	op := slices.Collect(it(opNode.ast))
	if len(op) == 1 && op[0].sym == 0 {
		switch e.f.ch(op[0].tok) {
		case INC:
			return assignTail{op: "++"}, true
		case DEC:
			return assignTail{op: "--"}, true
		}
	}
	if len(op) != 2 {
		return assignTail{}, false
	}
	rhs := e.rhsExprs(op[1])
	if len(rhs) != 1 {
		return assignTail{}, false // a multi-value list is not a single-target tail
	}
	if op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		return assignTail{op: "=", rhs: rhs[0].ast}, true
	}
	if op[0].sym == AssignOp {
		if tok, ok := e.soleToken(op[0].ast); ok {
			sym := e.f.ch(tok)
			if c, ok := cAssignOps[sym]; ok {
				return assignTail{op: c, rhs: rhs[0].ast, complement: sym == ANDNOT_ASSIGN}, true
			}
		}
	}
	return assignTail{}, false
}

// assignTail describes what follows an assignment target: the C operator, the
// right operand (nil for ++/--), and whether that operand must be complemented.
type assignTail struct {
	op         string
	rhs        []int32
	complement bool
	// targetCType is the C type written through, when the caller knows it. Only a
	// shift assignment reads it, to decide the width its count is measured against;
	// a target that is a plain name needs no help, its type being on the variable.
	targetCType string
	// targetRepeatable says the target's C text may be written a second time --
	// evaluating it twice repeats no side effect. A shift assignment needs that,
	// since it becomes "t = f(t, n)".
	targetRepeatable bool
	// targetArray is the shape of the ARRAY the target names, when it is one -- an
	// element of an array of arrays, an array-typed field of an element, anything a
	// chain reaches through an index. C has no array assignment, so writing one is a
	// memcpy of the target's own size; the shape says how big, and what a receive or
	// an out-parameter call may be checked against. Zero for every other target,
	// which is what the paths that set no array target leave it.
	targetArray arrDim
}

// isArray reports whether the tail's target is an ARRAY. A zero arrDim has no
// outermost extent, and every real one does, however small -- `[0]int`'s is "0".
func (t assignTail) isArray() bool { return t.targetArray.bound != "" }

// cAssignOps maps each compound assignment token to the C operator that applies
// it. C has no "&^=": Go's `x &^= y` clears in x every bit set in y, which is
// `x &= ~(y)`, so ANDNOT_ASSIGN maps to "&=" and complements its operand.
var cAssignOps = map[Symbol]string{
	ADD_ASSIGN:    "+=",
	SUB_ASSIGN:    "-=",
	MUL_ASSIGN:    "*=",
	QUO_ASSIGN:    "/=",
	REM_ASSIGN:    "%=",
	AND_ASSIGN:    "&=",
	OR_ASSIGN:     "|=",
	XOR_ASSIGN:    "^=",
	SHL_ASSIGN:    "<<=",
	SHR_ASSIGN:    ">>=",
	ANDNOT_ASSIGN: "&=",
}

// emitArrayTargetAssign writes a whole ARRAY through a target C cannot assign to,
// dst being the target's already-rendered C text. It is the copy `a = b` and
// `s.a = b` already take, reached through a target those two shapes cannot name --
// `m[1] = row`, `sheet[0].body = r`, `h.rows[1] = r`.
//
// dst is rendered once and used twice, as the destination and as the operand of the
// sizeof. That repeats no evaluation: sizeof does not evaluate its operand, so an
// index inside dst -- and the bounds check around it -- still runs exactly once.
func (e *emitter) emitArrayTargetAssign(dst string, a arrDim, rhs []int32) {
	if !e.checkArrayShape(a, rhs, "assignment") {
		return
	}
	// `m[1] = <-ch` for a channel of arrays: the receive writes through an out
	// parameter, and the target IS the storage it writes into.
	if elem, ch, ra, isRecv := e.arrayRecvInit(rhs); isRecv {
		if a.elem != ra.elem || a.declSuffix() != ra.declSuffix() {
			e.fail("cannot receive %s into %s", e.goArrayTypeName(ra), e.goArrayTypeName(a))
			return
		}
		e.chanRecvElems[elem] = true
		e.ind()
		e.emit(chanRecvCName(elem) + "(" + ch + ", " + dst + ");\n")
		return
	}
	// `m[1] = mk()`: the callee fills storage the caller owns, and the target IS that
	// storage, so the call writes through it directly -- no copy, which is what the
	// out-parameter ABI is for.
	if cname, ra, isCall := e.arrayResultCall(rhs); isCall {
		if a.elem != ra.elem || a.declSuffix() != ra.declSuffix() {
			e.fail("cannot assign %s to %s", e.goArrayTypeName(ra), e.goArrayTypeName(a))
			return
		}
		e.emitArrayResultCall(dst, cname, rhs)
		return
	}
	src, ok := e.arraySourceC(rhs)
	if !ok {
		e.fail("cannot copy this into %s: it is not an array this can read from",
			e.goArrayTypeName(a))
		return
	}
	e.includes["string.h"] = true
	e.ind()
	e.emit("memcpy(" + dst + ", " + src + ", sizeof(" + dst + "));\n")
}

// emitAssignTailOrCopy emits an indented assignment statement whose target is
// written by target and whose tail is t, lowering the one case C's own assignment
// cannot express here: a struct that holds an array is copied with memcpy (see
// hasArrayField). target is rendered rather than streamed for that case, since
// memcpy needs the destination as an argument; it is called exactly once either
// way, and not at all if the copy is refused, so a refusal leaves no half-written
// statement.
func (e *emitter) emitAssignTailOrCopy(target func(), t assignTail) {
	// A whole ARRAY written over. Asked FIRST, ahead of every branch that reads
	// targetCType: for an array of interfaces the callers set that to the ELEMENT's
	// type, which would send `grid[1] = row` down the interface path to be written as
	// two words into storage that holds a row of them.
	if t.isArray() {
		if t.op != "=" {
			// `m[1]++`, `m[1] += r`: Go defines no operator on an array, so this is
			// a program it rejects rather than a lowering that is missing.
			e.fail("cannot update %s in place: no operator applies to an array",
				e.goArrayTypeName(t.targetArray))
			return
		}
		e.emitArrayTargetAssign(e.captureC(target), t.targetArray, t.rhs)
		return
	}
	// A concrete value written to a target of interface type -- a field, an element,
	// anything a chain reaches -- is wrapped where it stands, the same two words the
	// plain-variable assignment writes.
	if t.op == "=" && e.isIfaceCType(t.targetCType) {
		if text, ok := e.ifaceValueC(t.targetCType, t.rhs); ok {
			e.ind()
			target()
			e.emit(" = " + text + ";\n")
			return
		}
	}
	if t.op == "=" {
		if ct, ok := e.inferCType(t.rhs); ok && e.hasArrayField(ct) {
			if !e.checkStructCopySrc(ct, t.rhs) {
				return
			}
			e.emitStructCopy(e.captureC(target), ct, t.rhs)
			return
		}
	}
	// A compound assignment whose operand is a string ("s += t") is runtime string
	// concatenation -- the only such op Go allows on a string -- which this target
	// cannot do, exactly as `s = s + t` is refused. Caught here, across the bare,
	// field and access-chain target paths, rather than emitted as "+=" on a struct,
	// which flexcc rejects as "Expected integer type".
	if t.rhs != nil && t.op != "=" {
		if ct, ok := e.exprReprCType(t.rhs); ok && ct == cString {
			e.fail("string concatenation with a non-constant operand needs allocation, which the target does not have")
			return
		}
	}
	e.ind()
	if text, ok := e.guardedAssignC(target, t); ok {
		e.emit(text + ";\n")
		return
	}
	target()
	e.emitAssignTail(t)
}

// guardedAssignC rewrites a compound assignment whose operator needs a guard --
// "x <<= n", "x /= n" -- into the guarded form "x = ogo_shl_<T>(x, n)". It reports
// false for any other assignment, which the ordinary path then emits unchanged.
//
// The target is written twice, so only one that is a plain name or a field path
// through one qualifies, or one the caller says repeats no evaluation. A target that
// is itself evaluated is refused rather than emitted wrong: repeating it would
// repeat its index expression, and leaving the operator unguarded would give C's
// answer instead of Go's, which is what this whole path exists to stop.
func (e *emitter) guardedAssignC(target func(), t assignTail) (string, bool) {
	op := strings.TrimSuffix(t.op, "=")
	isShift := op == "<<" || op == ">>"
	isDiv := op == "/" || op == "%"
	if t.rhs == nil || t.complement || !(isShift || isDiv) {
		return "", false
	}
	text := e.captureC(target)
	ctype := t.targetCType
	if ctype == "" {
		var ok bool
		if ctype, ok = e.varType(plainTargetRoot(text)); !ok {
			return "", false
		}
	}
	var fn string
	switch {
	case isShift && e.shiftNeedsGuard1(ctype, t.rhs):
		fn = shiftHelperName(op, e.underlyingCType(ctype))
	case isDiv && e.divNeedsGuard1(ctype, t.rhs):
		fn = divHelperName(op, e.underlyingCType(ctype))
	default:
		return "", false
	}
	if !t.targetRepeatable && !plainTargetText(text) {
		e.fail("a %s= assignment whose operands C and Go disagree on needs a target that can be named twice; this one is evaluated", op)
		return "", false
	}
	if isShift {
		e.needShift(op, e.underlyingCType(ctype))
	} else {
		e.needDiv(op, e.underlyingCType(ctype))
	}
	rhsText := e.captureC(func() { e.emitExpr(t.rhs) })
	if isShift {
		rhsText = e.shiftCountC(rhsText, t.rhs)
	}
	return text + " = " + fn + "(" + text + ", " + rhsText + ")", true
}

// shiftCountC renders a guarded shift's COUNT argument, widening it to the
// int64_t the helper declares when its own type is narrower.
//
// flexcc does not widen it itself: given a 64-bit EXPRESSION for the value and a
// narrower count, it passes one slot where two are wanted -- it says so, "Bad
// number of parameters in call to ogo_shr_int64_t: expected 4 found 3" -- and the
// callee reads the count's high word from whatever was next in the frame. What ran
// was a shift by a garbage count, or a panic for a count that came out negative.
// A plain variable for the value escapes it, which is why every shift written the
// ordinary way was right and only "(s << 62) >> m" was wrong.
//
// The cast is written only where the count is KNOWN to be narrower. A count that
// is already 64 bits needs none, and casting one would step into the other fault
// this backend has -- a cast to a 64-bit type applied to a 64-bit expression, the
// very thing shiftHelperDef binds a temporary to avoid.
func (e *emitter) shiftCountC(text string, rhs []int32) string {
	if ct, ok := e.inferCType(rhs); ok && cIntWidths[e.underlyingCType(ct)] == 64 {
		return text
	}
	e.includes["stdint.h"] = true
	return "(int64_t)(" + text + ")"
}

// divNeedsGuard1 is divNeedsGuard for a value whose C type is already known.
func (e *emitter) divNeedsGuard1(ctype string, rhs []int32) bool {
	if _, signed := cUnsignedOf[e.underlyingCType(ctype)]; !signed {
		return false
	}
	if v, ok := e.foldConstInt(rhs); ok && v != 0 && v != -1 {
		return false
	}
	return true
}

// plainTargetText reports whether a target's C text is a name or a field path
// through one, so that writing it a second time repeats no evaluation.
func plainTargetText(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.':
		case c == '-' && i+1 < len(text) && text[i+1] == '>':
			i++
		default:
			return false
		}
	}
	return text != ""
}

// plainTargetRoot is the leading name of such a target, whose type is the shift's.
func plainTargetRoot(text string) string {
	for i := 0; i < len(text); i++ {
		if c := text[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return text[:i]
		}
	}
	return text
}

// shiftNeedsGuard1 is shiftNeedsGuard for a value whose C type is already known.
func (e *emitter) shiftNeedsGuard1(ctype string, rhs []int32) bool {
	bits, ok := cIntWidths[e.underlyingCType(ctype)]
	if !ok {
		return false
	}
	if v, ok := e.foldConstInt(rhs); ok && v >= 0 && v < int64(bits) {
		return false
	}
	return true
}

// emitAssignTail writes the classified tail after a target has been emitted. The
// complemented operand is parenthesised, so `x &^= a | b` clears both bits rather
// than complementing only a.
func (e *emitter) emitAssignTail(t assignTail) {
	if t.rhs == nil {
		e.emit(t.op + ";\n") // "++" or "--"
		return
	}
	e.emit(" " + t.op + " ")
	if t.complement {
		ct, _ := e.inferCType(t.rhs)
		e.emitComplement(t.rhs, ct, func() { e.emitExpr(t.rhs) })
	} else {
		e.emitExpr(t.rhs)
	}
	e.emit(";\n")
}

// emitFieldIndexAssign emits a write to a slice-field element `b.data[i] = v`,
// through the field header's backing pointer and bounds-checked against its length.
// Only plain assignment is modelled (no slice colon, no ++/-- on the element).
func (e *emitter) emitFieldIndexAssign(base string, fields []string, index, opNode Node) {
	low, _, _, isSlice := e.sliceParts(index.ast)
	if isSlice {
		e.fail("slicing a slice-field target is not supported yet")
		return
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed field element")
		return
	}
	expr, elem, lenExpr, ok := e.indexedContainer(base, fields)
	if !ok {
		e.fail("unsupported indexed field assignment target")
		return
	}
	t.targetCType = elem
	e.emitAssignTailOrCopy(func() { e.emitIndexSelect(expr, lenExpr, low, elem, nil) }, t)
}

// emitIndexAssign emits an indexed assignment `a[i] = v` or an indexed increment/
// decrement `a[i]++` / `a[i]--`. index is the Index node, opNode the PostfixOp.
func (e *emitter) emitIndexAssign(base string, index, opNode Node) {
	if opNode.sym != PostfixOp {
		e.fail("unsupported assignment target")
		return
	}
	var idx []int32
	for n := range it(index.ast) {
		if n.sym == Expression {
			idx = n.ast
		}
	}
	if idx == nil {
		e.fail("unsupported index target")
		return
	}
	// A slice element is addressed through its backing pointer; an array directly,
	// and a pointer to an array through the dereference `p[i]` abbreviates. The
	// index is bounds-checked against the container length.
	lhs := base
	lenExpr, elem := "", ""
	var row arrDim
	if el, ok := e.sliceElem(base); ok {
		lhs = base + ".ptr"
		lenExpr = base + ".len"
		elem = el
		// A slice whose ELEMENT is an array, `xs[1] = r` over a `[][2]int`: the
		// element is a whole array and is written by copying it, exactly as the
		// array container's row is. The registry that answers this is the one
		// plainOrSlice reads, which is why the same target reached through a chain
		// -- `h.xs[1] = r` -- was already right while this emitted
		// `xs.ptr[i] = (ogo_arr_2_int){7, 8}`, not C.
		if a, isArr := e.namedArrays[el]; isArr {
			row = a
		}
	} else if text, a, ok := e.arrayBase(base); ok {
		if _, isPtr := e.arrayPtrVar(base); isPtr {
			lhs = text
		}
		lenExpr, elem = a.bound, a.elem
		if a.dims() > 1 {
			// One index into a multi-dimensional array reaches a ROW, `m[1] = r`,
			// which is itself an array and so is written by copying it. This used
			// to be refused outright -- there was no lowering for it, and typing
			// the target as the ELEMENT would have written one int over a row.
			// Writing a row is how a table of rows is filled.
			row = a.row()
		}
	} else if !e.indexableBase(base) {
		return
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed target")
		return
	}
	t.targetCType, t.targetArray = elem, row
	// The index is evaluated twice by a guarded shift assignment (see shiftAssignC),
	// which is only sound when evaluating it has no effect. It usually has none: an
	// index is a name or a literal far more often than it is a call.
	t.targetRepeatable = !e.exprHasEffect(idx)
	e.emitAssignTailOrCopy(func() {
		e.emit(lhs + "[")
		e.emitIndex(idx, lenExpr)
		e.emit("]")
	}, t)
}

// assignTarget is one target of a multiple assignment: the root variable, the
// pointer indirections written ahead of it, and the chain of selectors and indexes
// that reaches the storage written. A plain variable has neither of the latter two,
// which was the only shape these paths modelled at first.
type assignTarget struct {
	name  string
	stars string
	chain []Node
	tok   int32
}

// plain reports a target that is a bare variable name -- the only shape a `:=` can
// declare, and the only one a declaration path ever passes.
func (t assignTarget) plain() bool { return t.stars == "" && len(t.chain) == 0 }

// plainTargets wraps bare names as targets, for the `var a, b = f()` paths, whose
// targets are declarations and so are always names.
func plainTargets(names []string) []assignTarget {
	targets := make([]assignTarget, len(names))
	for i, name := range names {
		targets[i] = assignTarget{name: name, tok: -1}
	}
	return targets
}

// emitStore writes val -- a C expression, in practice a temporary already holding
// the value -- to one target of a multiple assignment. A plain name is declared or
// assigned; anything else is emitted as the lvalue it names, which is what lets a
// field, an element or a pointee be a target. ctype is the value's C type, read
// only where the target is being declared. A blank target drops the value.
func (e *emitter) emitStore(t assignTarget, declare bool, ctype, val string) {
	if t.name == "_" {
		return
	}
	if t.plain() {
		e.ind()
		if declare {
			e.locals[t.name] = ctype
			e.emit(ctype + " " + userIdent(t.name) + " = " + val + ";\n")
		} else {
			e.emit(e.varRef(t.name) + " = " + val + ";\n")
		}
		return
	}
	if declare {
		e.fail("non-name %s on the left side of :=", t.name)
		return
	}
	if t.stars != "" && len(t.chain) != 0 {
		e.fail("a dereferenced target with a field or index is not supported yet")
		return
	}
	// Typed before anything is emitted, so an unsupported chain fails without
	// leaving a half-written statement behind.
	if len(t.chain) != 0 {
		if _, ok := e.accessChainType(t.name, t.chain); !ok {
			e.fail("unsupported target in a multiple assignment")
			return
		}
	}
	e.ind()
	if len(t.chain) == 0 {
		e.emit(t.stars + e.varRef(t.name))
	} else {
		e.emitAccessChain(t.name, t.chain)
	}
	e.emit(" = " + val + ";\n")
}

// lhsItemTarget reads one LhsItem (LhsItem = AssignHead { Selector | Index }) as a
// target, the counterpart of the head target emitMultiAssign is handed.
func (e *emitter) lhsItemTarget(ast []int32) (assignTarget, bool) {
	nodes := slices.Collect(it(ast))
	if len(nodes) == 0 || nodes[0].sym != AssignHead {
		return assignTarget{}, false
	}
	name := e.soleIdent(nodes[0].ast)
	if name == "" || (len(nodes) > 1 && !isAccessChain(nodes[1:])) {
		return assignTarget{}, false
	}
	t := assignTarget{name: name, stars: e.derefStars(nodes[0].ast), chain: nodes[1:], tok: -1}
	if tok, ok := e.soleToken(nodes[0].ast); ok {
		t.tok = tok
	}
	return t, true
}

// emitMultiAssign emits a destructuring assignment `a, b = f()` or `a, b := f()`
// (any target may be the blank identifier). C has no multiple assignment, so the
// multi-result call's struct is bound to a temporary and each target reads its
// field: a `:=` target is declared with its result type, a `=` target is assigned,
// and a blank target is skipped. first is the head identifier, stars and headChain
// what the head writes around it; op holds the PostfixOp children (the remaining
// LhsItem targets, the operator, and the call).
func (e *emitter) emitMultiAssign(head Node, first, stars string, headChain []Node, op []Node) {
	if len(headChain) != 0 && !isAccessChain(headChain) {
		e.fail("unsupported target in a multiple assignment")
		return
	}
	targets := []assignTarget{{name: first, stars: stars, chain: headChain, tok: -1}}
	if tok, ok := e.soleToken(head.ast); ok {
		targets[0].tok = tok
	}
	define := false
	var rhs []Node
	for _, n := range op {
		switch n.sym {
		case LhsItem:
			t, ok := e.lhsItemTarget(n.ast)
			if !ok {
				e.fail("unsupported target in a multiple assignment")
				return
			}
			targets = append(targets, t)
		case ExpressionList:
			rhs = e.rhsExprs(n)
		case 0:
			if ch := e.f.ch(n.tok); ch == ASSIGN || ch == DEFINE {
				define = ch == DEFINE
			}
		}
	}
	declare := e.declareTargets(define, targets)
	// One expression for several targets distributes a multi-result call; a matching
	// count is a value list assigned pairwise.
	if len(rhs) == 1 {
		e.emitDestructure(targets, declare, rhs[0].ast)
		return
	}
	if len(rhs) != len(targets) {
		e.fail("assignment mismatch: %d targets, %d values", len(targets), len(rhs))
		return
	}
	e.emitValueList(targets, declare, rhs)
}

// declareTargets decides, per target of a multiple assignment, whether to emit a C
// declaration or an assignment.
//
// A plain "=" never declares. A ":=" declares each target *except* those Go says it
// only assigns to: a name already declared in the same scope, as in "a, b := f()"
// where a is already here. The emitter has no scopes -- e.locals is flat over the
// whole function -- so it cannot tell that name from one shadowing an outer block,
// which ":=" genuinely does redeclare. The checker can, and recorded the answer per
// target position while it walked the scopes; this reads it back. Without that, both
// cases emitted a second C declaration of the same name in the same block, which the
// target's C compiler accepts with a warning and then ignores, leaving the variable
// holding its old value.
func (e *emitter) declareTargets(define bool, targets []assignTarget) []bool {
	declare := make([]bool, len(targets))
	for i, t := range targets {
		declare[i] = define
		if define && t.tok >= 0 && e.f.defineRedeclares[e.f.tok(t.tok).Position().String()] {
			declare[i] = false
		}
	}
	return declare
}

// emitValueList lowers `a, b = c, d` (or `:=`). Every value is evaluated into a
// temporary first, then each target takes its temporary, so all right-hand sides
// see the pre-assignment values -- which is what makes `a, b = b, a` a swap.
func (e *emitter) emitValueList(targets []assignTarget, declare []bool, rhs []Node) {
	tmps := make([]string, len(rhs))
	types := make([]string, len(rhs))
	dims := make([]arrDim, len(rhs))
	for i, r := range rhs {
		// An ARRAY value is bound by COPY. It has no C value type to declare a
		// temporary of, so inferCType answered no and the whole statement was
		// "cannot infer the type of a value in a multiple assignment" -- which is
		// what stopped `m[i], m[j] = m[j], m[i]`, the swap every sort of a table of
		// rows is written with. The temporary is what makes a swap a swap, and an
		// array needs one as much as anything else does.
		if a, isArr := e.arrayShapeOf(r.ast); isArr {
			src, ok := e.arraySourceC(r.ast)
			if !ok {
				e.fail("cannot read %s in a multiple assignment: it is not an array this can copy from",
					e.goArrayTypeName(a))
				return
			}
			tmps[i], dims[i] = e.newTmp(), a
			e.emitArrayCopy(tmps[i], src, a)
			continue
		}
		ct, ok := e.inferCType(r.ast)
		if !ok {
			e.fail("cannot infer the type of a value in a multiple assignment")
			return
		}
		types[i] = ct
		tmps[i] = e.newTmp()
		e.ind()
		e.emit(ct + " " + tmps[i] + " = ")
		e.emitExpr(r.ast)
		e.emit(";\n")
	}
	for i, tgt := range targets {
		if dims[i].bound != "" {
			e.emitStoreArray(tgt, declare[i], dims[i], tmps[i])
			continue
		}
		e.emitStore(tgt, declare[i], types[i], tmps[i])
	}
}

// emitStoreArray writes an ARRAY -- already bound to the temporary val -- to one
// target of a multiple assignment. C cannot assign an array, so a declared target is
// declared and copied into and an assigned one is memcpy'd, which is the lowering
// every other array write takes.
//
// The target's own shape is checked against the value's first: the copy is sized by
// the destination, so a mismatch would read past the end of a shorter source or drop
// what did not fit.
func (e *emitter) emitStoreArray(t assignTarget, declare bool, a arrDim, val string) {
	if t.name == "_" {
		return
	}
	if declare {
		if !t.plain() {
			e.fail("non-name %s on the left side of :=", t.name)
			return
		}
		e.emitArrayCopy(t.name, val, a)
		return
	}
	if t.stars != "" {
		e.fail("a dereferenced array target is not supported yet")
		return
	}
	var dst arrDim
	text := ""
	if len(t.chain) == 0 {
		d, isArr := e.arrayVar(t.name)
		if !isArr {
			e.fail("cannot assign %s to %s, which is not an array", e.goArrayTypeName(a), t.name)
			return
		}
		text, dst = e.varRef(t.name), d
	} else {
		// Typed before anything is emitted, so an unsupported chain fails without
		// leaving a half-written statement behind, as the scalar store does.
		cur, ok := e.accessChainType(t.name, t.chain)
		if !ok || len(cur.dims) == 0 {
			e.fail("unsupported array target in a multiple assignment")
			return
		}
		text = e.captureC(func() { e.emitAccessChain(t.name, t.chain) })
		dst = arrDim{elem: cur.elem, bound: cur.dims[0], inner: cur.dims[1:], name: cur.name}
	}
	if dst.elem != a.elem || dst.declSuffix() != a.declSuffix() {
		e.fail("cannot use %s as %s in assignment", e.goArrayTypeName(a), e.goArrayTypeName(dst))
		return
	}
	e.includes["string.h"] = true
	e.ind()
	e.emit("memcpy(" + text + ", " + val + ", sizeof(" + text + "));\n")
}

// emitDestructure lowers a multi-result call bound to several targets, shared by
// `a, b = f()` / `a, b := f()` and the var form `var a, b T = f()`. C has no
// multiple assignment, so the call's result struct is bound to a temporary and each
// target reads its field: a defined target is declared with its result type, an
// assigned target is assigned, and a blank target is skipped. rhs is the call
// expression; define selects declaration (`:=` / `var`) over plain assignment.
func (e *emitter) emitDestructure(targets []assignTarget, declare []bool, rhs []int32) {
	// "v, ok := x.(T)": no call, and the two values are a cast and a comparison.
	// The order matters -- v is the zero value when the assertion does not hold, as
	// in Go -- so ok is computed first and v reads it.
	operand, iface, target, isIface, isAssert := e.typeAssertion(rhs)
	if !isAssert {
		// The operand may be an expression rather than a name, in which case it is
		// bound first and the assertion reads the binding. bindAssertionOperand
		// decides for itself and emits nothing unless it answers yes.
		operand, iface, target, isIface, isAssert = e.bindAssertionOperand(rhs)
	}
	if isAssert {
		if len(targets) != 2 {
			e.fail("a type assertion yields one value, or two in the comma-ok form")
			return
		}
		if isIface {
			e.emitIfaceAssertOk(targets, declare, operand, iface, target)
			return
		}
		if !e.needVTable(iface, target) {
			return
		}
		okTmp := e.newTmp()
		e.ind()
		e.emit("int " + okTmp + " = " + e.assertOKC(operand, iface, target) + ";\n")
		e.emitStore(targets[0], declare[0], target+"*", okTmp+" ? "+e.assertValueC(operand, target)+" : 0")
		e.emitStore(targets[1], declare[1], cBool, okTmp)
		return
	}
	callee, suffix, ok := e.directCall(rhs)
	if !ok {
		// A method reached through a chain that includes an INDEX,
		// `a, b := xs[i].two()`. Refusing it here said "requires a single function
		// call" of a call, for an element of any kind -- a plain struct one included.
		callee, suffix, ok = e.chainCallOf(rhs)
	}
	if !ok {
		e.fail("multiple assignment requires a single function call on the right-hand side")
		return
	}
	if callee == "append" && len(suffix) == 1 && suffix[0].sym == CallSuffix {
		// Two-result append: s, ok = append(s, x) -- the ok form, no trap.
		e.emitTryAppend(targets, declare, suffix[0].ast)
		return
	}
	_, resTypes, ok := e.callResultInfo(callee, suffix)
	if !ok || len(resTypes) != len(targets) {
		e.fail("multiple-assignment target/result count mismatch")
		return
	}
	tmp := e.newTmp()
	e.ind()
	// Keyed by the result TYPES, not by the callee: a call through a function value
	// has no callee name to key on, and a named one gives the same struct either way.
	e.emit(e.retStructNameOf(resTypes) + " " + tmp + " = ")
	// A method reached through fields is emitted here rather than by emitCallExpr,
	// which knows the two fixed call shapes and not this one: the receiver is the
	// field chain, taken by address when the method wants a pointer.
	if fields, cn, wantPtr, isField := e.methodOnField(callee, suffix); isField {
		lv := e.fieldAccessC(callee, fields)
		ct, _ := e.fieldType(callee, fields)
		recv, ok := e.chainReceiver(lv, ct, true, wantPtr)
		if !ok {
			e.fail("cannot take the address of %s for a pointer-receiver method", lv)
			return
		}
		if args := e.argsCText(cn, suffix[len(suffix)-1].ast); args != "" {
			recv += ", " + args
		}
		e.emit(cn + "(" + recv + ")")
	} else if len(suffix) > 2 {
		// A chain receiver: emitCallExpr knows the one- and two-step shapes and not
		// this one, so the chain walk renders the whole call, receiver included.
		text, _, _, okc := e.chainCText(callee, suffix)
		if !okc {
			e.fail("unsupported call on the right-hand side of a multiple assignment")
			return
		}
		e.emit(text)
	} else if !e.emitCallExpr(callee, suffix) {
		e.fail("unsupported call on the right-hand side of a multiple assignment")
		return
	}
	e.emit(";\n")
	for i, tgt := range targets {
		e.emitStore(tgt, declare[i], resTypes[i], fmt.Sprintf("%s._%d", tmp, i))
	}
}

// variadicPack names the element type of a callee's variadic parameter and the
// position it sits at, or -1. It reads the recorded slice-parameter types rather
// than the signature, so it answers for a method and an imported function too.
func (e *emitter) variadicPack(cname string) (elem string, at int) {
	i, ok := e.funcVariadic[cname]
	if !ok {
		return "", -1
	}
	sliceParams := e.funcSliceParams[cname]
	if i >= len(sliceParams) || sliceParams[i] == "" {
		return "", -1
	}
	return sliceElemFromCName(sliceParams[i]), i
}

// spreadCall reports whether a call wrote "f(xs...)", handing an existing slice to
// a variadic parameter rather than values to pack.
func (e *emitter) spreadCall(callSuffix []int32) bool {
	for n := range it(callSuffix) {
		if n.sym != ArgumentList {
			continue
		}
		for c := range it(n.ast) {
			if c.sym == 0 && e.f.ch(c.tok) == ELLIPSIS {
				return true
			}
		}
	}
	return false
}

// packVariadic renders the []T a variadic call passes: an array of this frame
// holding the trailing arguments, and a header over it. No arguments is the zero
// header, which is Go's nil slice -- len 0, and nothing allocated for it.
func (e *emitter) packVariadic(elem string, args []Node) string {
	e.needSlice(elem)
	if len(args) == 0 {
		return "(" + sliceCName(elem) + "){0}"
	}
	// The values are ASSIGNED into the array one at a time rather than written as its
	// initializer. The target's compiler refuses an aggregate inside an array
	// initializer unless it is itself braced: a struct VARIABLE and a struct-returning
	// CALL each draw "incompatible types in assignment: expected int but got
	// _struct__P", which names a member of the element rather than the element, while
	// the same value is accepted as the right-hand side of an assignment. Braces cover
	// a composite literal and nothing else, so a variadic of structs compiled when
	// every argument happened to be written as a literal and not when one was a
	// variable -- the earlier fix here reached the literal spelling only.
	//
	// An assignment also wants the ordinary spelling of a value rather than the
	// static-initializer one, so declInit stays off: a string argument is the
	// compound literal `(ogo_string){"a", 1}`, which is what an assignment takes.
	tmp := e.newTmp()
	n := strconv.Itoa(len(args))
	e.prologue = append(e.prologue, elem+" "+tmp+"["+n+"];\n")
	for i, a := range args {
		val, wrapped := "", false
		// A concrete value handed to an INTERFACE element is wrapped where it
		// stands, exactly as one handed to an interface parameter is: the two words
		// the element is, made of the value's address and the table for that pair.
		// Without it the pack stored the raw pointer where the two words go, so a
		// variadic of interfaces did not compile at all. ifaceValueC may itself
		// declare a temporary -- widening one interface to another is statements
		// rather than an expression -- which lands after this array's declaration
		// and before the assignment reading it, which is the order it needs.
		if e.isIfaceCType(elem) && e.deferReplay < 0 {
			val, wrapped = e.ifaceValueC(elem, a.ast)
		}
		if !wrapped {
			val = e.captureC(func() { e.emitExpr(a.ast) })
		}
		e.prologue = append(e.prologue, tmp+"["+strconv.Itoa(i)+"] = "+val+";\n")
	}
	return "(" + sliceCName(elem) + "){" + tmp + ", " + n + ", " + n + "}"
}

// callResultInfo resolves a call's C name and result types, for the three callees a
// multi-result call can have: a function of this package, a method, and a function
// of an imported one. The C name is what names the result struct, so it has to be
// the same one emitCallExpr will call -- a method's is namespaced by its receiver
// type, an imported function's by its package.
// allSelectors reports whether every step is a Selector.
func allSelectors(steps []Node) bool {
	for _, n := range steps {
		if n.sym != Selector {
			return false
		}
	}
	return true
}

// methodOnField resolves a call whose receiver is reached through struct fields,
// `m.st.pop()`: the fields leading to the receiver, the method's C name, and
// whether that method takes a pointer receiver.
func (e *emitter) methodOnField(recv string, suffix []Node) (fields []string, cname string, wantPtr, ok bool) {
	if len(suffix) < 3 || suffix[len(suffix)-1].sym != CallSuffix || !allSelectors(suffix[:len(suffix)-1]) {
		return nil, "", false, false
	}
	sels := suffix[:len(suffix)-1]
	for _, n := range sels[:len(sels)-1] {
		fields = append(fields, e.soleIdent(n.ast))
	}
	ct, ok := e.fieldType(recv, fields)
	if !ok || !e.isMethodBase(methodBaseType(ct)) {
		// An ARRAY field: fieldType answers with its ELEMENT, the extents living
		// beside it, so the DEFINED name -- which is what carries the method set --
		// is only in fieldArray's answer. Without this a MULTI-RESULT method on such
		// a field reported a target/result count mismatch, the shape having been read
		// as no method at all.
		a, isArr := e.fieldArray(recv, fields)
		if !isArr || a.name == "" {
			return nil, "", false, false
		}
		ct = a.name
	}
	cname = methodCName(methodBaseType(ct), e.soleIdent(sels[len(sels)-1].ast))
	return fields, cname, e.methodPtr[cname], true
}

// chainCallOf recognises a call whose callee is a method reached through a chain
// that includes an INDEX, `xs[i].two()`. directCall takes a run of SELECTORS only,
// and deliberately: widening it would change every caller it has. This is the shape
// the one position that wants it asks for separately.
func (e *emitter) chainCallOf(ast []int32) (string, []Node, bool) {
	fac, okf := e.soleFactorNode(ast)
	if !okf {
		return "", nil, false
	}
	recv, sfx, okc := e.factorCall(slices.Collect(it(fac.ast)))
	if !okc || len(sfx) < 3 || sfx[len(sfx)-1].sym != CallSuffix || sfx[len(sfx)-2].sym != Selector {
		return "", nil, false
	}
	if !isAccessChain(sfx[:len(sfx)-1]) {
		return "", nil, false
	}
	return recv, sfx, true
}

// chainMethodResult resolves a method call whose receiver is reached through a chain
// that includes an INDEX -- `xs[i].two()`, `b.rows[i].two()` -- to the method's C
// name and its result types. It is methodOnField's counterpart for a receiver the
// all-selectors walk cannot describe.
func (e *emitter) chainMethodResult(base string, steps []Node) (string, []string, bool) {
	if len(steps) < 3 || steps[len(steps)-1].sym != CallSuffix || steps[len(steps)-2].sym != Selector {
		return "", nil, false
	}
	cur, ok := e.accessChainType(base, steps[:len(steps)-2])
	if !ok {
		return "", nil, false
	}
	bt := methodBaseType(cur.ctype)
	if bt == "" {
		bt = cur.name // an ARRAY the chain reached carries its type in the name
	}
	if bt == "" || !e.isMethodBase(bt) {
		return "", nil, false
	}
	cname := methodCName(bt, e.soleIdent(steps[len(steps)-2].ast))
	rts, isMethod := e.funcRet[cname]
	return cname, rts, isMethod
}

func (e *emitter) callResultInfo(recv string, suffix []Node) (cname string, resTypes []string, ok bool) {
	if _, cn, _, isField := e.methodOnField(recv, suffix); isField {
		resTypes, ok = e.funcRet[cn]
		return cn, resTypes, ok
	}
	// A method reached through a CHAIN, `xs[i].two()`. methodOnField above answers
	// for a run of SELECTORS only, so an index anywhere in the receiver left the
	// call untyped -- which is what refused `a, b := ps[1].two()` for a plain struct
	// element, arrays having nothing to do with it.
	if cn, rts, isChain := e.chainMethodResult(recv, suffix); isChain {
		return cn, rts, true
	}
	if len(suffix) == 2 && suffix[0].sym == Selector && suffix[1].sym == CallSuffix {
		member := e.soleIdent(suffix[0].ast)
		// A call through an interface takes its result from the method's slot: the
		// concrete function behind it is not known here, and its result type is the
		// one the interface declared anyway.
		if rct, isVar := e.varType(recv); isVar && e.isIfaceCType(rct) {
			for _, m := range e.ifaceMethods[rct] {
				if m.name == member {
					if m.res == "void" {
						return "", nil, true
					}
					return "", []string{m.res}, true
				}
			}
			return "", nil, false
		}
		// A struct FIELD holding a function value, `o.dm(17, 5)`: the field's own
		// typedef says what a call through it yields. Asked before the method path,
		// which would read the same `o.dm` as a method of o's type and find none.
		if ct, okf := e.fieldType(recv, []string{member}); okf {
			if res, isFunc := e.funcTypeRet[ct]; isFunc {
				return "", res, true
			}
		}
		// methodRecvCType rather than varType-plus-isUserType: an ARRAY variable has
		// no C type at all, so the pair answered no for one and `t := g.sum()` could
		// not be typed -- while `var t int; t = g.sum()`, which asks nothing, was
		// fine. It is the same lookup the call itself dispatches through.
		if rct, isRecv := e.methodRecvCType(recv); isRecv {
			cname = methodCName(methodBaseType(rct), member)
		} else if prefix, isPkg := e.importQualifiers[recv]; isPkg {
			cname = mangle(prefix, member)
		} else {
			return "", nil, false
		}
		resTypes, ok = e.funcRet[cname]
		return cname, resTypes, ok
	}
	// A call THROUGH A VALUE, `f(17, 5)` where f holds a function: the results are
	// the ones its typedef yields, the concrete function behind it being whatever it
	// was last assigned. There is no cname -- nothing to name a result struct after
	// -- which is why the callers key that off the result types instead.
	if ct, isVar := e.varType(recv); isVar {
		if res, isFunc := e.funcTypeRet[ct]; isFunc {
			return "", res, true
		}
	}
	cname = e.funcCallC(recv)
	resTypes, ok = e.funcRet[cname]
	return cname, resTypes, ok
}

// directCall reports the callee name and call suffix of an expression that is
// exactly a direct call `f(args)`, descending the single-child Expression/
// SimpleExpr/Term/UnaryExpr wrappers to a Factor whose only suffix is a CallSuffix.
// The suffix lets the caller re-emit the call through emitCallExpr, which — unlike
// emitExpr — does not reject a multi-result callee.
func (e *emitter) directCall(ast []int32) (recv string, suffix []Node, ok bool) {
	nodes := slices.Collect(it(ast))
	for len(nodes) == 1 {
		switch nodes[0].sym {
		case Expression, SimpleExpr, Term, UnaryExpr:
			nodes = slices.Collect(it(nodes[0].ast))
		case Factor:
			kids := slices.Collect(it(nodes[0].ast))
			// A plain call "f(...)", and the two-step forms a multi-result call also
			// takes: a method call "x.m(...)" and a call into an imported package
			// "pkg.F(...)", both of which are a Selector followed by the CallSuffix.
			if r, sfx, ok := e.factorCall(kids); ok {
				switch {
				case len(sfx) == 1 && sfx[0].sym == CallSuffix:
					return r, sfx, true
				case len(sfx) >= 2 && sfx[len(sfx)-1].sym == CallSuffix && allSelectors(sfx[:len(sfx)-1]):
					// `x.m(...)`, and a method on a FIELD: `m.st.pop()`. Only the
					// two-step form was taken, so a multi-result method reached
					// through a field was "multiple assignment requires a single
					// function call on the right-hand side" -- of a call.
					return r, sfx, true
				}
			}
			return "", nil, false
		default:
			return "", nil, false
		}
	}
	return "", nil, false
}

// newTmp returns a fresh generated temporary name, unique within the function.
func (e *emitter) newTmp() string {
	s := "_ogo_t" + strconv.Itoa(e.tmp)
	e.tmp++
	return s
}

// emitDiscard emits `(void)<expr>;` — an expression evaluated for its side effects
// with its value dropped, the C rendering of a blank-identifier discard. emitExpr
// already parenthesizes binary operands, so no extra parentheses are needed for
// the cast to bind correctly.
func (e *emitter) emitDiscard(expr []int32) {
	e.ind()
	e.emit("(void)")
	e.emitExpr(expr)
	e.emit(";\n")
}

// emitCallArgs emits a call's argument list. cname is the callee's C name, used
// only to recover its parameter types; "" (a p2 intrinsic, or a callee whose
// signature is not recorded) simply emits every argument as written.
// hoistArrayCallArg binds an argument that is a call returning an ARRAY to a
// temporary of this frame and answers with its name.
func (e *emitter) hoistArrayCallArg(arg Node) (string, bool) {
	if e.declInit || e.deferReplay >= 0 {
		return "", false
	}
	cname, a, ok := e.arrayResultCall(arg.ast)
	if !ok {
		return "", false
	}
	name := e.newTmp()
	saved := e.indent
	e.indent = 0
	text := e.captureC(func() {
		e.emit(a.elem + " " + name + a.declSuffix() + ";\n")
		e.emitArrayResultCall(name, cname, arg.ast)
	})
	e.indent = saved
	if text == "" {
		return "", false
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		e.prologue = append(e.prologue, line+"\n")
	}
	e.arrays[name] = a
	return name, true
}

// hoistStructCallArg binds an argument that is a call returning a STRUCT to a
// temporary of this frame and answers with its name. See the call site for why.
func (e *emitter) hoistStructCallArg(arg Node) (string, bool) {
	if e.declInit || e.deferReplay >= 0 {
		return "", false
	}
	callee, suffix, ok := e.directCall(arg.ast)
	if !ok {
		return "", false
	}
	_, resTypes, ok := e.callResultInfo(callee, suffix)
	if !ok || len(resTypes) != 1 || !e.isStruct(resTypes[0]) {
		return "", false
	}
	text := e.captureC(func() { ok = e.emitCallExpr(callee, suffix) })
	if !ok {
		return "", false
	}
	name := e.newTmp()
	e.prologue = append(e.prologue, resTypes[0]+" "+name+" = "+text+";\n")
	return name, true
}

// checkArrayArgs refuses an argument whose array shape is not the parameter's. Go
// rejects such a call, and an array parameter is a POINTER the callee memcpys the
// parameter's own size out of -- an array parameter is a copy, as Go says -- so a
// shorter argument was read past the end of, with the program running and printing a
// wrong answer.
//
// It is the last position an array flows into that carried no shape check. The others
// read the destination's extents off the destination; here they cannot be read off the
// C type at all, arrayParamCType being a pointer to the element, so they are kept
// beside it (funcArrayParams).
//
// An argument whose shape arrayShapeOf cannot read is passed, not refused, for the
// reason checkArrayShape passes one: this is a check on what the program wrote down.
func (e *emitter) checkArrayArgs(cname string, args []Node) {
	dims := e.funcArrayParams[cname]
	for i, arg := range args {
		if i >= len(dims) || dims[i].bound == "" {
			continue // not a parameter, or not an array one
		}
		a, ok := e.arrayShapeOf(arg.ast)
		if !ok {
			continue
		}
		if a.elem != dims[i].elem || a.declSuffix() != dims[i].declSuffix() {
			e.fail("cannot use %s as %s in argument to %s",
				e.goArrayTypeName(a), e.goArrayTypeName(dims[i]), cname)
			return
		}
	}
}

func (e *emitter) emitCallArgs(cname string, callSuffix []int32) {
	sliceParams := e.funcSliceParams[cname]
	args := e.callArgExprs(callSuffix)
	e.checkCrossArgs(cname, args, e.spreadCall(callSuffix))
	e.checkIntoArgs(cname, args)
	e.checkArrayArgs(cname, args)
	// A variadic callee takes one []T where the call wrote however many values.
	// They are packed into an array of this frame, which is what Go allocates for
	// and this target cannot -- so the lifetime rules see it as a slice literal's
	// backing, and a callee that keeps it is refused by them.
	if elem, at := e.variadicPack(cname); at >= 0 && !e.spreadCall(callSuffix) {
		if len(args) < at {
			e.fail("not enough arguments in call to %s", cname)
			return
		}
		pack := e.packVariadic(elem, args[at:])
		for i, arg := range args[:at] {
			if i != 0 {
				e.emit(", ")
			}
			e.emitExpr(arg.ast)
		}
		if at != 0 {
			e.emit(", ")
		}
		e.emit(pack)
		return
	}
	// Go evaluates a call's arguments left to right. C leaves the order
	// unspecified, and the two compilers this targets disagree: given
	// `f(t(1), t(2), t(3))` the P2 backend evaluates left to right while the host's
	// gcc goes right to left, so the same program answered differently depending on
	// which one built it. When an argument can change state and there is more than
	// one, each is evaluated into a temporary in source order and the call takes the
	// temporaries, which pins the order rather than relying on either compiler's
	// choice. A single argument, or arguments that cannot change state, are emitted
	// in place as before.
	if len(args) > 1 && e.deferReplay < 0 {
		effect := false
		for _, a := range args {
			if e.exprHasEffect(a.ast) {
				effect = true
				break
			}
		}
		if effect {
			if names, ok := e.hoistArgs(cname, args); ok {
				e.emit(strings.Join(names, ", "))
				return
			}
		}
	}
	first := true
	for i, arg := range args {
		if !first {
			e.emit(", ")
		}
		first = false
		// An ARRAY-returning call as an argument, `take(mk())`. The parameter is a
		// pointer the callee memcpys from, so what the call site needs is storage:
		// the result is bound to a temporary and that is passed.
		if name, ok := e.hoistArrayCallArg(arg); ok {
			e.emit(name)
			continue
		}
		// A struct-returning CALL as an argument, `take(mk(3))`. Bound first: the
		// target drops a member narrower than a machine word when such a call is
		// passed where it stands -- `take(mk(-5))` answered true for a false bool on
		// a P2-EDGE. Same defect as the direct return; see
		// doc/return-nonword-struct.c.
		if name, ok := e.hoistStructCallArg(arg); ok {
			e.emit(name)
			continue
		}
		// An ARRAY literal written as an argument, `take([3]int{1, 2, 3})`. The
		// parameter is a pointer the callee memcpys from -- an array parameter is a
		// copy, as Go says -- so what the call needs is an lvalue, and a temporary
		// of this frame is one. The callee copies before the frame can go anywhere.
		if name, ok := e.hoistArrayLitExpr(arg.ast); ok {
			e.emit(name)
			continue
		}
		// The predeclared nil has no type of its own: alone it emits the null
		// pointer 0, which is not a slice header, so passing it to a slice
		// parameter built a call whose argument count did not even match. The
		// parameter is what says which nil this is, so here it becomes that slice
		// type's zero value. This precedes the defer replay because nil is a
		// constant -- there is nothing to capture and re-read at the return.
		if i < len(sliceParams) && sliceParams[i] != "" && e.isNilExpr(arg.ast) {
			e.emit("(" + sliceParams[i] + "){0}")
			continue
		}
		// A concrete value handed to an interface parameter is wrapped where it
		// stands: the two words the parameter is, made of the value's address and
		// the table for that pair. The same thing the assignment writes in two
		// statements, as one value, because a parameter has no name here to write
		// them into.
		if params := e.funcParams[cname]; i < len(params) && e.isIfaceCType(params[i]) && e.deferReplay < 0 {
			if text, ok := e.ifaceValueC(params[i], arg.ast); ok {
				e.emit(text)
				continue
			}
		}
		// Replaying a deferred call reads the temporaries captured at the defer
		// statement rather than re-evaluating the expressions, which may name a
		// variable that has since changed or gone out of scope.
		if e.deferReplay >= 0 {
			if a := e.deferReplayArgs[i]; a.inline {
				e.emitExpr(a.expr)
			} else {
				e.emit(deferArgName(e.deferReplay, i))
			}
			continue
		}
		e.emitExpr(arg.ast)
	}
}

// callArgExprs returns the argument Expression nodes of a CallSuffix.
func (e *emitter) callArgExprs(callSuffix []int32) []Node {
	var args []Node
	for n := range it(callSuffix) {
		if n.sym != ArgumentList {
			continue
		}
		for a := range it(n.ast) {
			if a.sym == Expression {
				args = append(args, a)
			}
		}
	}
	return args
}

// soleIdent returns the single identifier of a subtree (an AssignHead bare name
// or a Selector's field), or "" if the shape is richer.
func (e *emitter) soleIdent(ast []int32) string {
	name := ""
	for n := range it(ast) {
		if n.sym != 0 || e.f.ch(n.tok) != IDENT {
			continue
		}
		if name != "" {
			return ""
		}
		name = e.src(n.tok)
	}
	return name
}

// factorCall recognises a Factor of the form `identifier FactorSuffix` whose
// suffix is a call (a direct call `f(args)` or a qualified call `pkg.F(args)`),
// returning the head identifier and the suffix's child nodes. ok is false for a
// bare identifier, a literal, or a non-call suffix (field selection or index),
// which are handled elsewhere or not yet supported.
func (e *emitter) factorCall(kids []Node) (recv string, suffix []Node, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	suffix = slices.Collect(it(kids[1].ast))
	if !containsSym(suffix, CallSuffix) {
		return "", nil, false
	}
	// `Row(a)[i]` is spelled like a call but is a conversion the chain paths unwrap
	// (arrayConvChain). Declining it here is what lets them see it: this runs first,
	// and claiming it would only reach the refusal for a call whose result is an
	// array. A conversion with nothing after it is left alone -- that is
	// emitConversion's, and it is already handled.
	if _, _, isConv := e.arrayConvChain(e.src(kids[0].tok), suffix); isConv {
		return "", nil, false
	}
	return e.src(kids[0].tok), suffix, true
}

// isStruct reports whether a C type name denotes a modelled struct type.
func (e *emitter) isStruct(ctype string) bool { _, ok := e.structs[ctype]; return ok }

// isSliceCType reports whether a C type name is a slice header type (ogo_slice_<T>).
//
// A POINTER to one is not: `ogo_slice_int*` shares the prefix and is a different
// type, and answering yes for it made `p := &xs` a slice variable, so `p[0]` and
// `len(p)` emitted `p.ptr` and `p.len` off a pointer -- C that does not compile,
// from a program the checker leaves to this stage (see indexingPointer).
func (e *emitter) isSliceCType(ctype string) bool {
	return strings.HasPrefix(ctype, sliceTypePrefix) && !strings.HasSuffix(ctype, "*")
}

// needSlice records that a slice `[]elem` is used, so its header typedef is emitted.
func (e *emitter) needSlice(elem string) {
	e.sliceElems[elem] = true
	e.sliceElemByName[sliceCName(elem)] = elem
}

// sliceElem returns a slice variable's element C type, from the local then the
// package slice environment.
func (e *emitter) sliceElem(name string) (string, bool) {
	if el, ok := e.sliceVars[name]; ok {
		return el, true
	}
	if el, ok := e.globalSliceVars[e.globalC(name)]; ok {
		return el, true
	}
	// A named type over a slice is a slice, `type List []int`: it is not in the two
	// registries above, which are filled where a slice type is written out, but its
	// representation says what it is.
	if ct, ok := e.varReprType(name); ok && e.isSliceCType(ct) {
		return sliceElemFromCName(ct), true
	}
	return "", false
}

// factorFieldAccess recognises a Factor that is a field access `base.f` (or a
// chain `base.f.g`) -- an identifier followed by a FactorSuffix of selectors only,
// no index or call -- returning the base name and the selected field names.
func (e *emitter) factorFieldAccess(kids []Node) (base string, fields []string, ok bool) {
	if len(kids) != 2 || kids[0].sym != 0 || e.f.ch(kids[0].tok) != IDENT || kids[1].sym != FactorSuffix {
		return "", nil, false
	}
	for _, n := range slices.Collect(it(kids[1].ast)) {
		if n.sym != Selector {
			return "", nil, false
		}
		fld := e.soleIdent(n.ast)
		if fld == "" {
			return "", nil, false
		}
		fields = append(fields, fld)
	}
	if len(fields) == 0 {
		return "", nil, false
	}
	return e.src(kids[0].tok), fields, true
}

// qualifiedStrConstVal gives the VALUE of an imported package's string constant,
// `geo.Name`. A string constant has no C symbol -- it is inlined at each use, a Go
// constant having no address -- so unlike that package's integer constants, which
// emit a `static const` the ordinary global path finds, there is nothing to name and
// the read has to produce the literal itself.
//
// Without it a qualified string constant was refused wherever it stood: the read fell
// through to the chain path, whose base is a variable, and reported "geo is not a
// value with fields or elements" -- of a package, about a constant that is there.
// Every other constant type crossed the boundary; only a string did not.
func (e *emitter) qualifiedStrConstVal(base string, fields []string) (string, bool) {
	prefix, isQual := e.importQualifiers[base]
	if !isQual || len(fields) != 1 {
		return "", false
	}
	v, ok := e.constStr[mangle(prefix, fields[0])]
	return v, ok
}

// qualifiedGlobalRead resolves a read of an exported variable from an imported
// user package -- `pkg.V`, or a field chain `pkg.V.f` selecting into it -- to the C
// text and type of the mangled package global. The imported package's variables
// were recorded in e.globals under their mangled names when that package's files
// were emitted (every reachable package is collected before any body is emitted),
// so the read resolves just as a same-package `x := g` does. ok is false when base
// is not an import qualifier or the member is not one of that package's globals (a
// function or a type member, left to the caller's other shapes).
func (e *emitter) qualifiedGlobalRead(base string, fields []string) (text, ctype string, ok bool) {
	// A p2 constant is a literal, not a symbol: the p2 package has no source and
	// nothing to define one in.
	if base == "p2" && len(fields) == 1 {
		if v, isConst := p2Constants[fields[0]]; isConst {
			return v, "unsigned", true
		}
	}
	prefix, isQual := e.importQualifiers[base]
	if !isQual || len(fields) == 0 {
		return "", "", false
	}
	gn := mangle(prefix, fields[0])
	// A folded string constant has no addressable C symbol -- it is inlined at each
	// use (see emitConstDecl) -- so a cross-package read of one is left to the
	// caller's other shapes (reported there) rather than naming a symbol that does
	// not exist. An integer constant does emit a `static const` definition, so it
	// resolves through the ordinary global path below (naming the symbol, matching a
	// same-package read, so the definition is not left unreferenced).
	if _, isStr := e.constStr[gn]; isStr {
		return "", "", false
	}
	ct, ok := e.globals[gn]
	if !ok {
		// An exported function of that package named as a value, `mathy.Double`.
		// The C name is the same one a call of it resolves to; only its type is new.
		if len(fields) == 1 {
			if ft, isFn := e.funcValueCType(gn); isFn {
				return gn, ft, true
			}
		}
		return "", "", false
	}
	text, ctype = gn, ct
	// A further field chain selects into the global's struct value (or pointer).
	for _, f := range fields[1:] {
		sel, okSel := e.selectC(ctype, f)
		if !okSel {
			return "", "", false
		}
		text += sel
		if ctype, ok = e.structFieldType(ctype, f); !ok {
			return "", "", false
		}
	}
	return text, ctype, true
}

// isPointer reports whether a C type is a pointer (spelled "T*").
// isPointer reports whether a C type is a pointer, following a chain of definitions
// to reach one: `type PP *P` emits `typedef P* PP;`, and a variable of PP takes an
// address and reaches fields through "->" exactly as a *P does.
func (e *emitter) isPointer(ctype string) bool {
	return strings.HasSuffix(e.underlyingCType(ctype), "*")
}

// elemType strips one pointer level from a C type ("T*" -> "T"), resolving a
// defined pointer type first so its element is reached as well.
func (e *emitter) elemType(ctype string) string {
	if u := e.underlyingCType(ctype); strings.HasSuffix(u, "*") {
		return strings.TrimSuffix(u, "*")
	}
	return strings.TrimSuffix(ctype, "*")
}

// structFieldType returns the C type of a struct's field. ctype may be a struct
// value or a pointer to one (a field access auto-dereferences, like Go's).
// fieldPath resolves a selected name to the chain of C members that reaches it: the
// name itself for a field declared on the type, and the embedded fields in front of
// it for a promoted one. Go's rule is breadth-first -- the shallowest wins, and two
// at the same depth are ambiguous rather than one of them -- so this searches by
// depth and reports nothing when a depth holds two.
func (e *emitter) fieldPath(ctype, field string) ([]string, bool) {
	type step struct {
		ctype string
		path  []string
	}
	level := []step{{ctype: e.elemType(ctype)}}
	for depth := 0; depth < 16 && len(level) != 0; depth++ {
		var found []string
		var next []step
		for _, st := range level {
			for _, fld := range e.structs[st.ctype] {
				switch {
				case fld.name == field:
					if found != nil {
						return nil, false // two at this depth: ambiguous, as in Go
					}
					found = append(append([]string{}, st.path...), field)
				case fld.embedded:
					next = append(next, step{ctype: e.elemType(fld.ctype), path: append(append([]string{}, st.path...), fld.name)})
				}
			}
		}
		if found != nil {
			return found, true
		}
		level = next
	}
	return nil, false
}

func (e *emitter) structFieldType(ctype, field string) (string, bool) {
	// A promoted field is reached through the embedded members in front of it, and
	// its type is what stands at the end of that path. selectC renders the same
	// path, so the two stay in step wherever a chain advances a step at a time.
	if path, ok := e.fieldPath(ctype, field); ok && len(path) > 1 {
		ct := ctype
		for _, step := range path {
			if ct, ok = e.structFieldDirect(ct, step); !ok {
				return "", false
			}
		}
		return ct, true
	}
	return e.structFieldDirect(ctype, field)
}

// promotedMethod resolves a method reachable on a C type through embedding: the C
// name it is declared under, the member path from this type to the receiver it
// wants, and the receiver's own C type. Breadth-first, as Go promotes, and nothing
// is reported when a depth holds two of the same name.
//
// The type's OWN method comes back with an empty path, so one lookup answers both.
func (e *emitter) promotedMethod(ctype, method string) (cname string, path []string, recvType string, ok bool) {
	type step struct {
		ctype string
		path  []string
	}
	level := []step{{ctype: methodBaseType(ctype)}}
	for depth := 0; depth < 16 && len(level) != 0; depth++ {
		found := false
		var next []step
		for _, st := range level {
			if cn := methodCName(st.ctype, method); e.funcRet[cn] != nil || e.funcHasName(cn) {
				if found {
					return "", nil, "", false // two at this depth: ambiguous, as in Go
				}
				cname, path, recvType, found = cn, st.path, st.ctype, true
			}
			for _, fld := range e.structs[st.ctype] {
				if fld.embedded {
					next = append(next, step{ctype: methodBaseType(fld.ctype), path: append(append([]string{}, st.path...), fld.name)})
				}
			}
		}
		if found {
			return cname, path, recvType, true
		}
		level = next
	}
	return "", nil, "", false
}

// funcHasName reports whether a function or method of that C name was declared, for
// the void ones funcRet records as an empty slice.
func (e *emitter) funcHasName(cname string) bool {
	_, ok := e.funcRet[cname]
	return ok
}

// embeddedPathC renders a member path as C text, for reaching an embedded receiver.
func (e *emitter) embeddedPathC(ctype string, path []string) string {
	text := ""
	for _, step := range path {
		sep := "."
		if e.isPointer(ctype) {
			sep = "->"
		}
		text += sep + e.fieldIdent(step)
		ctype, _ = e.structFieldDirect(ctype, step)
	}
	return text
}

// selectC renders the C member access for a selected field: one member for a field
// the type declares, and the embedded members in front of it for a promoted one.
func (e *emitter) selectC(ctype, field string) (string, bool) {
	path, ok := e.fieldPath(ctype, field)
	if !ok {
		return "", false
	}
	text := ""
	for _, step := range path {
		sep := "."
		if e.isPointer(ctype) {
			sep = "->"
		}
		text += sep + e.fieldIdent(step)
		if ctype, ok = e.structFieldDirect(ctype, step); !ok && step != path[len(path)-1] {
			return "", false
		}
	}
	return text, true
}

// structFieldDirect is structFieldType for a field the type declares itself, with
// no promotion. It is what the two above walk a path with.
func (e *emitter) structFieldDirect(ctype, field string) (string, bool) {
	for _, fld := range e.structs[e.elemType(ctype)] {
		if fld.name == field {
			if fld.dim.bound != "" {
				// An array field has no single C value type -- C cannot assign or
				// pass one by value -- so it is not reportable here. Indexing reaches
				// it through structFieldArray instead, and any path that wanted a
				// plain value fails honestly rather than emitting an array where a
				// scalar was expected.
				return "", false
			}
			return fld.ctype, true
		}
	}
	return "", false
}

// structFieldArray returns a struct field's array dimension, for a field declared
// with a fixed extent (`data [3]int`). It is the array counterpart of
// structFieldType, which deliberately refuses such a field.
func (e *emitter) structFieldArray(ctype, field string) (arrDim, bool) {
	for _, fld := range e.structs[e.elemType(ctype)] {
		if fld.name == field && fld.dim.bound != "" {
			return fld.dim, true
		}
	}
	return arrDim{}, false
}

// fieldArray resolves an array-typed field at the end of an access chain
// `base.f.g...`, returning its element type and bound.
func (e *emitter) fieldArray(base string, fields []string) (arrDim, bool) {
	if len(fields) == 0 {
		return arrDim{}, false
	}
	ctype, ok := e.varType(base)
	if !ok {
		return arrDim{}, false
	}
	for _, f := range fields[:len(fields)-1] {
		if ctype, ok = e.structFieldType(ctype, f); !ok {
			return arrDim{}, false
		}
	}
	return e.structFieldArray(ctype, fields[len(fields)-1])
}

// fieldType resolves the C type of a field access chain `base.f.g...` from the
// type environment: base's (possibly pointer) struct type, then each field's type
// in turn.
func (e *emitter) fieldType(base string, fields []string) (string, bool) {
	ctype, ok := e.varType(base)
	if !ok {
		return "", false
	}
	for _, f := range fields {
		if ctype, ok = e.structFieldType(ctype, f); !ok {
			return "", false
		}
	}
	return ctype, true
}

// fieldIdent names a struct member in C. It is cIdent plus one thing C does not
// need and the backend does: flexcc refuses a member whose name is also a type
// name -- "Unable to combine types", or "Internal error, confusing type
// declaration", pointed at the line before -- where C keeps member names in a
// namespace of their own and gcc accepts it.
//
// `type logger struct{ ... }` beside `type app struct{ logger logger }` is
// ordinary Go, so the member is renamed rather than the program refused. The
// suffix goes on wherever a member is written, which is every caller of this
// function, and nowhere else: a program with no such collision emits exactly the C
// it emitted before.
func (e *emitter) fieldIdent(name string) string {
	id := userIdent(name)
	for e.typeNames[id] {
		id += "_"
	}
	return id
}

// fieldAccessC renders a field access chain `base.f.g...` in C, choosing "->" for
// each pointer step (an auto-dereferenced Go field access) and "." otherwise.
func (e *emitter) fieldAccessC(base string, fields []string) string {
	ctype, _ := e.varType(base)
	// A field reached THROUGH a pointer is a dereference, so the base takes the nil
	// check: "p.f" is "p->f", which on this target reads or writes address zero
	// happily when p is nil.
	s := e.varRef(base) // a global base is mangled, a Unicode local base escaped
	if len(fields) != 0 && e.isPointer(ctype) {
		s = e.nilCheckedC(s, ctype)
	}
	for _, f := range fields {
		sel, ok := e.selectC(ctype, f)
		if !ok {
			sel = "." + e.fieldIdent(f) // let the C compiler name what is missing
		}
		s += sel
		ctype, _ = e.structFieldType(ctype, f)
	}
	return s
}

// inferCType determines the C type of an expression for a `x := expr` short
// declaration, from the current type environment (locals and funcRet). ok is
// false when the type is outside the modelled subset, so the caller fails
// honestly rather than emitting a wrongly-typed variable.
func (e *emitter) inferCType(ast []int32) (string, bool) {
	return e.inferNodes(slices.Collect(it(ast)))
}

// inferNodes types one expression level (the children of an Expression/SimpleExpr/
// Term/UnaryExpr). A relational operator makes the value a bool (C int); an
// arithmetic operator makes the type that of the first operand; a unary operator
// is transparent. Otherwise the level is a single operand, typed by inferNode.
func (e *emitter) inferNodes(nodes []Node) (string, bool) {
	for _, n := range nodes {
		if n.sym == RelOp {
			return cBool, true // a comparison yields bool
		}
	}
	// The operands of an arithmetic operator are of one type, so the type of the
	// whole is the type of any operand that HAS one: an untyped constant beside a
	// typed operand takes the typed operand's type. Taking the first operand
	// outright declared "b := 1 + v" an int for an int64 v and truncated it to 1,
	// and did the same to a float ("2 * f") and to a uint32 ("1 + u"); the leading
	// literal named a type it does not have.
	//
	// A shift needs no exception here: its count is not an operand of the value's
	// type, but an untyped count contributes nothing and a typed one only ever
	// appears where the checker already required the types to agree.
	//
	// When every operand is untyped the first still answers, which is what it
	// always did -- an all-untyped expression takes its default type.
	var first Node
	firstSet := false
	for _, n := range nodes {
		switch n.sym {
		case AddOp, MulOp, UnaryOp:
			continue // an operator; the type comes from the operand(s)
		case 0:
			switch e.f.ch(n.tok) {
			case SUB, ADD, NOT, XOR, MUL, AND:
				continue // a prefix operator token; skip to its operand
			}
		}
		if !firstSet {
			first, firstSet = n, true
		}
		if e.operandUntyped(n) {
			continue
		}
		return e.inferNode(n)
	}
	if firstSet {
		return e.inferNode(first)
	}
	return "", false
}

// operandUntyped reports whether an operand is an untyped constant, contributing
// no type of its own to the expression it sits in.
func (e *emitter) operandUntyped(n Node) bool {
	if n.sym == 0 {
		return e.tokenUntyped(n.tok)
	}
	return e.exprUntyped(n.ast)
}

// exprUntyped reports whether every leaf of an expression is an untyped constant
// -- a literal, iota, or a constant recorded untyped by emitConstSpecName. A name
// that is anything else (a variable, a typed constant, a conversion's type name)
// makes the expression typed, since it brings a type with it.
//
// It answers "untyped" for a shape it does not recognise, which is what the
// inference did for every expression before this existed: the answer only ever
// makes inferNodes look FURTHER for a type, so an unrecognised leaf leaves the
// old behaviour rather than inventing a new one.
func (e *emitter) exprUntyped(ast []int32) bool {
	for n := range it(ast) {
		if n.sym != 0 {
			if !e.exprUntyped(n.ast) {
				return false
			}
			continue
		}
		if !e.tokenUntyped(n.tok) {
			return false
		}
	}
	return true
}

// tokenUntyped is exprUntyped for a single terminal.
func (e *emitter) tokenUntyped(tok int32) bool {
	switch e.f.ch(tok) {
	case INT, CHAR, FLOAT, STRING:
		return true // an untyped literal
	case IDENT:
		nm := e.src(tok)
		return nm == "iota" || e.constUntyped[nm] || e.constUntyped[e.globalC(nm)]
	}
	return true // an operator or punctuation carries no type
}

// inferNode types a single expression node: a wrapper level recurses, a
// parenthesised expression unwraps, a call takes its result type, an identifier
// its declared type, and an integer literal is int.
func (e *emitter) inferNode(n Node) (string, bool) {
	switch n.sym {
	case Expression, SimpleExpr, Term:
		return e.inferNodes(slices.Collect(it(n.ast)))
	case UnaryExpr, Factor:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 3 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == LPAREN {
			return e.inferNode(kids[1])
		}
		// `(a - b).Scaled()` is its method's result, not the parenthesised
		// expression's type. Asked before unparenKids, which splices the
		// parentheses away and leaves the shape unrecognisable.
		if ct, ok := e.parenMethodResultType(kids); ok {
			return ct, true
		}
		kids = e.unparenKids(kids)
		if elem, _, ok := e.recvOperand(n, kids); ok {
			return elem, true
		}
		// A slice literal standing as a value is its header type. An array literal
		// has no C value type, as an array never does, so it is left untyped here
		// and reaches the places that know what to do with the extents themselves.
		if litType, _, isLit := e.factorArrayLit(n); isLit {
			if elem, ok := e.sliceType(litType); ok {
				e.needSlice(elem)
				return sliceCName(elem), true
			}
			return "", false
		}
		// Address-of `&x` adds a pointer level; deref `*p` removes one.
		if n.sym == UnaryExpr && len(kids) >= 2 && kids[0].sym == UnaryOp {
			if tok, ok := e.unaryOpTok(kids[0].ast); ok {
				switch e.f.ch(tok) {
				case AND:
					// `&a` for an ARRAY variable is a pointer to that array. An
					// array has no C value type, so inferNode cannot answer for it
					// and the pointee's typedef is minted from the extents instead
					// -- the same name a written `*[3]int` resolves to, so the two
					// spellings meet.
					if nm, isName := e.exprIdent(kids[len(kids)-1].ast); isName {
						if a, isArr := e.arrayVar(nm); isArr {
							return e.arrayTypedef(a) + "*", true
						}
					}
					// `&h.f` and `&pool[1]`: the same pointer, to an array reached
					// through a chain rather than named directly. Handing one to a
					// PARAMETER already worked -- the parameter's type says what it
					// is -- but a DECLARATION has only this to go on, and reported
					// "cannot infer a type" of an address the program had written.
					if a, isArr := e.arrayOperandOf(kids[len(kids)-1]); isArr {
						return e.arrayTypedef(a) + "*", true
					}
					if t, ok := e.inferNode(kids[len(kids)-1]); ok {
						return t + "*", true
					}
					return "", false
				case MUL:
					if t, ok := e.inferNode(kids[len(kids)-1]); ok && e.isPointer(t) {
						return e.elemType(t), true
					}
					return "", false
				}
			}
		}
		if n.sym == Factor {
			// A function literal has the type its signature mints, the same typedef
			// a named function used as a value gets.
			// "gq.Bump" has the method's type without its receiver.
			if base, method, ok := e.factorMethodValue(kids); ok {
				rct, _ := e.varType(base)
				fv := e.methodValueTypes[methodCName(methodBaseType(rct), method)]
				if len(fv.res) > 1 {
					return "", false
				}
				return e.funcTypeFor(fv), true
			}
			if lit, suffix, ok := e.factorFuncLit(kids); ok {
				var sig []int32
				for c := range it(lit.ast) {
					if c.sym == Signature {
						sig = c.ast
					}
				}
				if sig == nil {
					return "", false
				}
				// Called where it stands, the value is what the call yields.
				if len(suffix) != 0 {
					if _, res := e.cSig(sig); len(res) == 1 {
						return res[0], true
					}
					return "", false
				}
				return e.funcTypeOfSig(sig)
			}
			// "x.(T)" is the pointer that was put in, read back out.
			if _, _, target, isIface, ok := e.typeAssertionKids(kids); ok {
				if isIface {
					return target, true // the asserted interface, not a pointer
				}
				return target + "*", true
			}
			// The same for an assertion on an EXPRESSION. Its type is the asserted
			// type whatever the operand is, so this needs only the selector and
			// never lowers the operand -- typing must not emit.
			if _, _, sel, ok := e.assertionSplit(kids); ok {
				if target, isIface, ok := e.assertedTargetC(sel); ok {
					if isIface {
						return target, true
					}
					return target + "*", true
				}
			}
			// `q := e.(*P).xs` -- a SUFFIX after the assertion. The chain is walked
			// from what the assertion yields, which is a pure question and needs no
			// temporary; the emitter binds one, this only has to agree about types.
			if _, _, target, isIface, rest, ok := e.typeAssertionPrefix(kids); ok && len(rest) != 0 {
				cur := accessCur{ctype: target + "*"}
				if isIface {
					cur = accessCur{ctype: target}
				}
				if at, ok := e.accessChainTypeAt(cur, rest, true); ok && len(at.dims) == 0 {
					if at.slice {
						return sliceCName(at.elem), true
					}
					return at.ctype, true
				}
				return "", false
			}
			// `x := mk()[1]` types as the chain reached from the bound result.
			if name, steps, ok := e.hoistArrayResultCallKids(kids); ok {
				cur, okc := e.accessChainType(name, steps)
				if !okc || len(cur.dims) != 0 {
					return "", false
				}
				if cur.slice {
					return sliceCName(cur.elem), true
				}
				return cur.ctype, true
			}
			// A bracketed conversion types as its TARGET, the operand having the
			// same representation.
			if typeAST, arg, steps, ok := e.factorBracketConv(n); ok {
				if _, isID := e.bracketConvOperand(typeAST, arg); !isID || len(steps) != 0 {
					return "", false
				}
				if elem, isSlice := e.litSliceType(typeAST); isSlice {
					e.needSlice(elem)
					return sliceCName(elem), true
				}
				return "", false // an array has no C value type to name here
			}
			// `[]int{1, 2, 3}[0]` types as the literal's ELEMENT. Typed from the
			// literal rather than by walking a hoisted temporary, since inferring a
			// type must emit nothing; a longer chain than one index falls through to
			// the paths below rather than being guessed at.
			if typeAST, _, steps, ok := e.factorLitIndexed(n); ok {
				if len(steps) == 0 || steps[0].sym != Index {
					return "", false
				}
				if _, _, _, isSlice := e.sliceParts(steps[0].ast); isSlice {
					return "", false
				}
				// The first index reaches the literal's element; anything after it
				// is walked from there, so `[2]P{{1, 2}, {3, 4}}[1].y` is typed by
				// the same walker a variable's chain uses. Rank > 1 is left to the
				// paths below, an inner row not being a value type here.
				elem := ""
				if a, isArray := e.arrayDim(typeAST); isArray && a.dims() == 1 {
					elem = a.elem
				} else if el, isSlice := e.litSliceType(typeAST); isSlice {
					elem = el
				}
				if elem == "" {
					return "", false
				}
				if len(steps) == 1 {
					return elem, true
				}
				cur, okc := e.accessChainTypeAt(e.plainOrSlice(elem), steps[1:], false)
				if !okc || len(cur.dims) != 0 {
					return "", false
				}
				if cur.slice {
					return sliceCName(cur.elem), true
				}
				return cur.ctype, true
			}
			// "T{...}" is a value of T, and a struct's C type is the typedef named
			// after it, so the literal types itself.
			if name, _, ok := e.factorCompositeLit(kids); ok {
				return name, true
			}
			if recv, suffix, ok := e.factorCall(kids); ok {
				return e.callResultCType(recv, suffix)
			}
			if base, fields, ok := e.factorFieldAccess(kids); ok {
				// An exported variable read from an imported package, `pkg.V`, types
				// from that package's mangled global; a plain `base.f` from the struct.
				if _, ctype, ok := e.qualifiedGlobalRead(base, fields); ok {
					return ctype, true
				}
				if _, ok := e.qualifiedStrConstVal(base, fields); ok {
					return cString, true
				}
				return e.fieldType(base, fields)
			}
			// `geo.Name[1:3]` -- SLICING another package's string constant, which the
			// emitter binds to a temporary of this frame. A slice of a string is a
			// string, and saying so here is what makes println print one: without it
			// the value was typed as whatever the chain walk made of a base that is a
			// package name, and `println(geo.Tag[1:3])` printed "9737, 2" -- the
			// header's two words -- where Go prints "eo".
			if base, steps, ok := e.factorAccessChain(kids); ok && len(steps) == 2 && steps[0].sym == Selector && steps[1].sym == Index {
				if field := e.soleIdent(steps[0].ast); field != "" {
					if _, isConst := e.qualifiedStrConstVal(base, []string{field}); isConst {
						if _, _, _, isSlice := e.sliceParts(steps[1].ast); isSlice {
							return cString, true
						}
					}
				}
			}
			// `s[i].v[j]` -- the general chain's result type.
			if base, steps, ok := e.factorAccessChain(kids); ok {
				// Fall through to the fixed shapes when the walker cannot type the
				// chain, rather than short-circuiting: a slice expression is theirs.
				if src, _, _, _, ok := e.sliceableChainRow(base, steps); ok {
					return src.cname, true // `m[0][:]` -- a slice over a row
				}
				if cur, ok := e.accessChainType(base, steps); ok && len(cur.dims) == 0 {
					// A chain reaching a slice -- `a[1:6][1:4]`, or a slice field past
					// an index -- is its header type; an array is still nameless, C
					// having no array value type.
					if cur.slice {
						return sliceCName(cur.elem), true
					}
					return cur.ctype, true
				}
			}
			// `q := (&v).m()` types as `v.m()` does, being the same call.
			if name, steps, ok := e.factorAddrCall(kids); ok {
				return e.callResultCType(name, steps)
			}
			// `q := (*p).x` -- the same chain, started from a dereference. It is
			// CLAIMED: a trailing slice step has no fixed shape to be handed back to
			// here, the fixed shapes all starting from a variable's name.
			if name, steps, ok := e.factorDerefChain(kids); ok {
				if derefCallSteps(steps) {
					return e.callResultCType(name, steps)
				}
				_, cur, ok := e.derefBase(name)
				if !ok {
					return "", false
				}
				if cur, ok := e.accessChainTypeAt(cur, steps, true); ok && len(cur.dims) == 0 {
					if cur.slice {
						return sliceCName(cur.elem), true
					}
					return cur.ctype, true
				}
				return "", false
			}
			// `m[i][j]` -- a fully indexed multi-dimensional array yields its element.
			// `s[i].x` / `p.pts[i].x` -- the element's selected field type.
			// `base.f[i]` -- indexing a slice struct field. Checked before the
			// plain-index and fallback paths: factorFieldAccess rejects the trailing
			// Index and factorIndex rejects the leading Selector, so without this the
			// level fell through to inferNodes(kids), which types a Factor by its
			// first identifier and so yielded the *base struct's* type (`Buf` for
			// `b.data[0]`, not `int`) -- invalid C at the declaration it feeds.
			if base, fields, indexAST, ok := e.factorFieldIndex(kids); ok {
				if _, _, _, isSlice := e.sliceParts(indexAST); isSlice {
					// Re-slicing a field yields a slice header: the field's own type
					// for a slice field, one over the element type for an array field.
					if src, ok := e.sliceableField(base, fields); ok {
						return src.cname, true
					}
					return "", false
				}
				if _, elem, _, ok := e.indexedContainer(base, fields); ok {
					return elem, true
				}
				return "", false
			}
			if base, indexAST, ok := e.factorIndex(kids); ok {
				if _, _, _, isSlice := e.sliceParts(indexAST); isSlice {
					// Slicing a string yields a string; slicing an array or a slice
					// yields the corresponding slice header type.
					if e.isStringVarName(base) {
						return cString, true
					}
					if _, a, ok := e.arrayBase(base); ok {
						// sliceElemOfArray, not a.elem: slicing a [2][3]int yields a
						// slice of ROWS, and naming it after the innermost type
						// declared the variable an ogo_slice_int while the header
						// built beside it was an ogo_slice_ogo_arr_3_int.
						elem := e.sliceElemOfArray(a)
						e.needSlice(elem)
						return sliceCName(elem), true
					}
					if elem, ok := e.sliceElem(base); ok {
						return sliceCName(elem), true
					}
					return "", false
				}
				// A plain index yields the element type of an array or a slice. One
				// index into a multi-dimensional array yields a row instead, which
				// has no C value type, so it is refused rather than typed as elem.
				if a, ok := e.arrayVar(base); ok {
					if a.dims() > 1 {
						return "", false
					}
					return a.elem, true
				}
				if elem, ok := e.sliceElem(base); ok {
					return elem, true
				}
				if e.isStringVarName(base) {
					return "uint8_t", true // s[i] is a byte, as in Go
				}
				return "", false
			}
		}
		return e.inferNodes(kids)
	case 0:
		switch e.f.ch(n.tok) {
		case INT:
			return "int", true
		case CHAR:
			// A rune literal defaults to rune, which IS int32, so "x := 'a'" gives a
			// variable of that type -- not the int an integer literal gives. The two
			// are the same width here, so nothing computes differently; what changes
			// is the type the program has, which "%T" prints and the checker now
			// tells apart. Unlike the names that reach a C type through cTypeName,
			// an inferred one has to ask for its header itself.
			e.includes["stdint.h"] = true
			return "int32_t", true
		case FLOAT:
			return "double", true // an untyped float literal defaults to float64 (C double)
		case STRING:
			return cString, true
		case IDENT:
			nm := e.src(n.tok)
			if nm == "true" || nm == "false" {
				return cBool, true // the predeclared bool constants
			}
			if ct, ok := e.locals[nm]; ok {
				return ct, true
			}
			if ct, ok := e.globals[e.globalC(nm)]; ok {
				return ct, true
			}
			// A top-level function's name standing as a value has that function's
			// own type, which is what lets `g := dbl` infer one.
			return e.funcValueCType(mangle(e.curPkgPrefix, nm))
		}
	}
	return "", false
}

// callResultCType returns the C result type of a call in expression position: a
// user function's recorded result type, or int for a p2 intrinsic (propeller2.h
// intrinsics all return int).
func (e *emitter) callResultCType(recv string, suffix []Node) (string, bool) {
	switch {
	case len(suffix) == 1 && suffix[0].sym == CallSuffix:
		if recv == "len" || recv == "cap" || recv == "copy" {
			return "int", true // the builtins len, cap and copy return int
		}
		if recv == "min" || recv == "max" {
			// min/max return the type of their arguments; take the first.
			if args := e.callArgExprs(suffix[0].ast); len(args) >= 1 {
				return e.inferCType(args[0].ast)
			}
			return "", false
		}
		if recv == "append" {
			// append returns a slice of its first argument's element type.
			args := e.callArgExprs(suffix[0].ast)
			if len(args) >= 1 {
				if base, ok := e.exprIdent(args[0].ast); ok {
					if elem, ok := e.sliceElem(base); ok {
						return sliceCName(elem), true
					}
				}
			}
			return "", false
		}
		if ct, ok := e.convType(recv); ok {
			return ct, true // a conversion T(x) has type T
		}
		// Only a single-result call is a usable single value; a multi-result call
		// belongs in a destructuring assignment (emitMultiAssign), not here.
		if rts, ok := e.userFunc(recv); ok && len(rts) == 1 {
			return rts[0], true
		}
		// A call through a VARIABLE holding a function: its result type is the one
		// recorded for that function typedef. Only a named function was typed here,
		// so `b := a(0)` had no type to give b.
		if ct, ok := e.varType(recv); ok {
			if rts := e.funcTypeRet[e.underlyingCType(ct)]; len(rts) == 1 {
				return rts[0], true
			}
		}
		return "", false
	case len(suffix) == 2 && suffix[0].sym == Selector && suffix[1].sym == CallSuffix:
		// A call through an interface takes the result its slot declares. Which
		// concrete function answers is not known here and does not matter: every
		// type filling the slot returns what the interface said it would.
		if rct, ok := e.varType(recv); ok && e.isIfaceCType(rct) {
			method := e.soleIdent(suffix[0].ast)
			for _, m := range e.ifaceMethods[rct] {
				if m.name == method {
					return m.res, m.res != "void"
				}
			}
			return "", false
		}
		// A single-result method call `x.M()` carries its recorded result type,
		// keyed by the receiver type's mangled method name.
		// methodRecvCType rather than varType-plus-isUserType: an ARRAY variable has
		// no C type at all -- its extents live in e.arrays and nowhere else -- so the
		// pair answered no for one and `t := g.sum()` was "cannot infer a type for the
		// declaration", while `var t int; t = g.sum()`, which asks nothing, worked. It
		// is the same lookup the call itself dispatches through.
		if rct, ok := e.methodRecvCType(recv); ok {
			method := e.soleIdent(suffix[0].ast)
			// The type's own method, or one promoted from an embedded field: both
			// resolve to the C name it was declared under.
			if cn, _, _, okp := e.promotedMethod(rct, method); okp {
				if rts := e.funcRet[cn]; len(rts) == 1 {
					return rts[0], true
				}
			}
			return "", false
		}
		if recv == "p2" {
			// A p2 intrinsic's result carries its declared C type (unsigned for a
			// uint32 one), so a high-bit value prints and compares unsigned. A void
			// intrinsic has no result type. Asked before the qualifier path below,
			// which would look for a p2_X this package never defines.
			if intr, ok := p2Intrinsics[e.soleIdent(suffix[0].ast)]; ok {
				return intr.ret, intr.ret != ""
			}
			return "", false
		}
		// A conversion to a type of that package is its own type, not a function's
		// result. Asked first, as the emission path asks it first.
		if ct, isConv := e.qualConvType(recv, e.soleIdent(suffix[0].ast)); isConv {
			return ct, true
		}
		if prefix, ok := e.importQualifiers[recv]; ok {
			// A call into an imported user package: its function's recorded result type
			// is keyed by its mangled name in that package's namespace.
			if rts, ok := e.funcRet[mangle(prefix, e.soleIdent(suffix[0].ast))]; ok && len(rts) == 1 {
				return rts[0], true
			}
			return "", false
		}
	}
	// A longer call chain (`mk().n`, `t.self().n`): its value type is what the chain
	// reaches, mirroring chainCText's lowering.
	return e.chainResultType(recv, suffix)
}

// emitExpr emits a value expression. Binary operators (Expression/SimpleExpr/
// Term) are parenthesized so the OctoGo parse grouping is preserved even where C
// operator precedence differs (notably Go binds << tighter than C does).
// Integer-literal text is normalized for C by normalizeIntLit.
func (e *emitter) emitExpr(ast []int32) {
	// A condition or assignment RHS reaches here as the Expression's unwrapped
	// children, so a string comparison must be recognized on this flat list too --
	// emitExprNode's Expression case only fires for a wrapped Expression node.
	// emitKidsStringCompare rewrites both a standalone and an embedded string compare and
	// is otherwise identical to emitting the kids in order.
	e.emitKidsStringCompare(slices.Collect(it(ast)))
}

// stringCompareAt reports whether kids[i:i+3] is a string comparison --
// "operand <relop> operand" with a string operand -- and returns the operator. C
// cannot compare the { ptr, len } structs; Go compares by content (==/!=) or
// lexicographic order (< <= > >=).
func (e *emitter) stringCompareAt(kids []Node, i int) (op string, ok bool) {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp {
		return "", false
	}
	switch op = e.opText(kids[i+1].ast); op {
	case "==", "!=", "<", "<=", ">", ">=":
		// a comparison operator
	default:
		return "", false
	}
	if ct, ok := e.exprReprCType(kids[i].ast); !ok || ct != cString {
		return "", false
	}
	return op, true
}

// structEqName is the C name of a struct type's generated equality helper.
func structEqName(ctype string) string { return "ogo_eq_" + ctype }

// goArrayTypeName renders an array's type in OctoGo's spelling, `[2][]int` rather
// than the `[2]ogo_slice_int` the emitted C calls it.
func (e *emitter) goArrayTypeName(a arrDim) string {
	s := ""
	for _, b := range a.bounds() {
		s += "[" + b + "]"
	}
	return s + e.goTypeName(a.elem)
}

// goTypeName renders a C type back in OctoGo's spelling, for a diagnostic that has
// to name a type the reader wrote rather than the one emitted for it.
func (e *emitter) goTypeName(ct string) string {
	if e.isSliceCType(ct) {
		return "[]" + e.goTypeName(sliceElemFromCName(ct))
	}
	if strings.HasSuffix(ct, "*") {
		return "*" + e.goTypeName(strings.TrimSuffix(ct, "*"))
	}
	if name, ok := goCTypeNames[ct]; ok {
		return name
	}
	if a, ok := e.namedArrays[ct]; ok {
		return e.goArrayTypeName(a)
	}
	// A MINTED interface name has no source spelling to return -- the program wrote
	// the shape, not a name -- so the shape is what a message about it says. Left to
	// a set rather than to the name's prefix: a C type classified by how its name
	// begins has gone wrong here before (see isSliceCType).
	if e.anonIfaceMinted[ct] {
		ms := e.ifaceMethods[ct]
		if len(ms) == 0 {
			return "interface{}"
		}
		var b strings.Builder
		b.WriteString("interface{ ")
		for i, m := range ms {
			if i != 0 {
				b.WriteString("; ")
			}
			b.WriteString(m.name + "()")
		}
		b.WriteString(" }")
		return b.String()
	}
	return ct
}

// comparableCType reports whether values of a C type may be compared with == and
// !=, and names what makes one that cannot. It is Go's rule: a slice compares only
// with nil, a function not at all, an array is comparable when its element is, and
// a struct when every field is.
func (e *emitter) comparableCType(ct string) (string, bool) {
	switch {
	case e.isSliceCType(ct), e.isFuncCType(ct):
		return e.goTypeName(ct), false
	}
	if a, ok := e.namedArrays[ct]; ok {
		if what, ok := e.comparableCType(a.elem); !ok {
			return what, false
		}
	}
	for _, fld := range e.structs[ct] {
		if what, ok := e.comparableCType(fld.ctype); !ok {
			return what, false
		}
	}
	return "", true
}

// checkCompareAt refuses a comparison the language does not define, ahead of the
// lowerings below -- each of which would otherwise emit C that means something else
// or nothing at all, and leave the reader a complaint about generated C.
//
// Ordering is defined on the numeric types and on strings; a struct or a slice has
// none. Equality reaches further: a struct compares field by field and an array
// element by element, but only when everything inside is itself comparable, and a
// slice compares with nil alone. The wording is Go's, so what a reader knows from
// Go carries over and a search for the text finds something.
func (e *emitter) checkCompareAt(kids []Node, i int) bool {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp {
		return true
	}
	op := e.opText(kids[i+1].ast)
	ordering := false
	switch op {
	case "==", "!=":
	case "<", "<=", ">", ">=":
		ordering = true
	default:
		return true // "&&" and "||", which group the chain rather than compare
	}
	// A comparison with nil is legal wherever it is modelled and is lowered below.
	if e.isNilExpr(kids[i].ast) || e.isNilExpr(kids[i+2].ast) {
		return true
	}
	pos := e.f.tok(kids[i+1].Pos()).Position()
	for _, n := range []Node{kids[i], kids[i+2]} {
		ct, ok := e.inferCType(n.ast)
		if !ok {
			continue
		}
		what := ""
		switch {
		case e.isStruct(ct):
			what = "struct"
		case e.isSliceCType(ct):
			what = "slice"
		}
		if ordering && what != "" {
			e.fail("%v: invalid operation: operator %s not defined on %s", pos, op, what)
			return false
		}
		if ordering {
			continue
		}
		if what == "slice" {
			e.fail("%v: invalid operation: slice can only be compared to nil", pos)
			return false
		}
		if inner, ok := e.comparableCType(ct); !ok {
			if what == "struct" {
				e.fail("%v: invalid operation: struct containing %s cannot be compared", pos, inner)
			} else {
				e.fail("%v: invalid operation: %s cannot be compared", pos, e.goTypeName(ct))
			}
			return false
		}
	}
	return true
}

// structCompareAt reports whether kids[i..i+2] is a struct equality "a == b" or
// inequality "a != b" -- the operands being of the same struct C type -- and, if so,
// that type. Structs are comparable only for equality in Go, not ordering, so only
// == and != qualify.
func (e *emitter) structCompareAt(kids []Node, i int) (op, ctype string, ok bool) {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp {
		return "", "", false
	}
	switch op = e.opText(kids[i+1].ast); op {
	case "==", "!=":
		// an equality operator
	default:
		return "", "", false
	}
	ct, ok := e.inferCType(kids[i].ast)
	if !ok {
		return "", "", false
	}
	if _, isStruct := e.structs[ct]; !isStruct {
		return "", "", false
	}
	if e.isIfaceCType(ct) {
		// An interface is a struct here, and it IS registered as one -- with no
		// fields, since its members are the data word and the table rather than
		// anything the source declared. The struct helper would therefore compare
		// NOTHING and return whatever was in the return register, which is how two
		// interfaces holding different pointers compared equal. ifaceCompareAt owns
		// this case.
		return "", "", false
	}
	return op, ct, true
}

// ifaceCompareAt reports whether kids[i..i+2] compares INTERFACE values for
// equality -- two of them, or one against nil -- and, if so, the operator and the
// two operands.
//
// Go compares interface values by dynamic type AND value, which here is the table
// pointer and the data pointer; against nil it is the zero interface, which carries
// no table. Neither is what comparing the two structs member by member would give,
// even if the struct helper knew the members.
func (e *emitter) ifaceCompareAt(kids []Node, i int) (op string, l, r Node, ok bool) {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp {
		return "", Node{}, Node{}, false
	}
	switch op = e.opText(kids[i+1].ast); op {
	case "==", "!=":
	default:
		return "", Node{}, Node{}, false
	}
	l, r = kids[i], kids[i+2]
	lct, lok := e.inferCType(l.ast)
	rct, rok := e.inferCType(r.ast)
	switch {
	case lok && e.isIfaceCType(lct) && (rok && e.isIfaceCType(rct) || e.isNilExpr(r.ast)):
		return op, l, r, true
	case rok && e.isIfaceCType(rct) && e.isNilExpr(l.ast):
		return op, r, l, true // the interface first, so the nil case reads one way
	}
	return "", Node{}, Node{}, false
}

// emitIfaceCompareTriple emits an interface equality. Each operand is rendered
// ONCE -- both words of it are read, and an operand that is a call must not be
// evaluated twice -- which emitStructOperand's hoist already arranges.
func (e *emitter) emitIfaceCompareTriple(op string, l, r Node) {
	lt := e.captureC(func() { e.emitStructOperand(l) })
	if e.isNilExpr(r.ast) {
		// The zero interface carries no table, so that word alone answers it.
		e.emit("(" + lt + ".vt " + op + " 0)")
		return
	}
	rt := e.captureC(func() { e.emitStructOperand(r) })
	join, eq := " && ", "=="
	if op == "!=" {
		join, eq = " || ", "!="
	}
	e.emit("(" + lt + ".vt " + eq + " " + rt + ".vt" + join + lt + ".data " + eq + " " + rt + ".data)")
}

// isNilExpr reports whether an expression is exactly the predeclared nil.
func (e *emitter) isNilExpr(ast []int32) bool {
	tok, ok := e.soleToken(ast)
	return ok && e.f.ch(tok) == IDENT && e.src(tok) == "nil"
}

// sliceNilCompareAt reports whether kids[i..i+2] compares a slice with nil for
// equality (`s == nil` / `s != nil`, either operand order) and, if so, the op and
// the slice operand. A slice is the only aggregate whose nil is modelled here;
// scalars/pointers compare with nil via the ordinary `== 0` path.
func (e *emitter) sliceNilCompareAt(kids []Node, i int) (op string, sliceNode Node, ok bool) {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp {
		return "", Node{}, false
	}
	if op = e.opText(kids[i+1].ast); op != "==" && op != "!=" {
		return "", Node{}, false
	}
	l, r := kids[i], kids[i+2]
	lNil, rNil := e.isNilExpr(l.ast), e.isNilExpr(r.ast)
	if lNil == rNil {
		return "", Node{}, false // both nil, or neither: not a slice-vs-nil compare
	}
	operand := l
	if lNil {
		operand = r
	}
	// Through the underlying type: `type List []int` is a slice however its name
	// reads, and asking only the written name let it fall through to the scalar
	// path, emitting `b == 0` against a three-word header -- which the host compiler
	// refuses outright and the target's miscounts.
	if ct, ok := e.inferCType(operand.ast); ok && e.isSliceCType(e.underlyingCType(ct)) {
		return op, operand, true
	}
	return "", Node{}, false
}

// emitSliceNilTriple lowers a slice-vs-nil comparison. A slice is nil exactly when
// its backing pointer is null, so `s == nil` becomes `(s.ptr == 0)`.
func (e *emitter) emitSliceNilTriple(sliceNode Node, op string) {
	e.emit("(")
	e.emitExprNode(sliceNode)
	e.emit(".ptr " + op + " 0)")
}

// emitStructCompareTriple emits a struct equality/inequality as a call to the
// per-type helper ogo_eq_<T>, negated for "!=". Go compares structs field by field.
func (e *emitter) emitStructCompareTriple(l, r Node, op, ctype string) {
	e.needStructEq(ctype)
	if op == "!=" {
		e.emit("!")
	}
	e.emit(structEqName(ctype) + "(")
	e.emitStructOperand(l)
	e.emit(", ")
	e.emitStructOperand(r)
	e.emit(")")
}

// emitStructOperand emits a struct-valued operand for a helper that takes it BY
// VALUE, binding a call to a temporary first. The target loses a member narrower
// than a machine word across such a handoff, which made `mk(3) == mk(3)` false.
func (e *emitter) emitStructOperand(n Node) {
	if name, ok := e.hoistStructCallArg(n); ok {
		e.emit(name)
		return
	}
	e.emitExprNode(n)
}

// isCompositeLitExpr reports whether an expression is written as a composite
// literal, `T{...}` or the type-elided `{...}` -- storage the literal declares
// rather than a value copied into it.
func (e *emitter) isCompositeLitExpr(v Node) bool {
	if v.sym == CompositeLit {
		return true
	}
	kids, ok := e.soleFactor(v.ast)
	if !ok {
		return false
	}
	for _, k := range kids {
		if k.sym == CompositeLit {
			return true
		}
	}
	return false
}

// needStructEq records that struct type ctype needs an equality helper and, by
// walking its fields, that every struct type reachable through a struct field does
// too (so nested comparisons resolve). A field type that is not comparable -- a
// slice, or a fixed array (whose element comparison is not lowered yet) -- is
// refused here, during body emission, so the diagnostic has a source position.
// Marking ctype before recursing bottoms out a type reached through a pointer field.
func (e *emitter) needStructEq(ctype string) {
	if e.eqStructs[ctype] {
		return
	}
	e.eqStructs[ctype] = true
	for _, fld := range e.structs[ctype] {
		switch {
		case fld.dim.bound != "":
			// The eq helper takes the struct by value, and flexcc cannot pass a struct
			// with an array field by value (see refuseArrayStructABI / the memcpy
			// workaround). So a struct with an array field -- directly, or through a
			// nested struct, since this recurses -- cannot be compared yet.
			e.fail("struct comparison with an array field (%s) is not supported: the backend cannot pass a struct with an array field by value", fld.name)
		case fld.ctype == cString:
			e.usesStringEq = true
		case e.isSliceCType(fld.ctype):
			e.fail("struct comparison with a slice field (%s) is not supported: slices are not comparable", fld.name)
		case e.structs[fld.ctype] != nil:
			e.needStructEq(fld.ctype) // a nested struct field compares through its own helper
		}
	}
}

// fieldEqCmp returns a boolean C expression comparing operands l and r of C type
// ct: ogo_string_eq for a string, the struct's helper for a struct, C == otherwise
// (a scalar or pointer).
func (e *emitter) fieldEqCmp(ct, l, r string) string {
	switch {
	case ct == cString:
		return "ogo_string_eq(" + l + ", " + r + ")"
	case e.structs[ct] != nil:
		return structEqName(ct) + "(" + l + ", " + r + ")"
	default:
		return l + " == " + r
	}
}

// structEqDef renders struct type ctype's equality helper: a function returning the
// conjunction of its fields' comparisons -- ogo_string_eq for a string field, the
// nested helper for a struct field, C == for a scalar or pointer field. An empty
// struct (its C form carries one hidden byte, not compared) is always equal.
func (e *emitter) structEqDef(ctype string) string {
	var b strings.Builder
	// The parameters use the emitter's reserved _ogo_ prefix so they cannot collide
	// with any struct field name -- `a`/`b` would clash with a field named a or b
	// (and flexcc then mishandles the resulting `b.b`).
	fmt.Fprintf(&b, "static int %s(%s _ogo_l, %s _ogo_r) {\n\treturn ", structEqName(ctype), ctype, ctype)
	fields := e.structs[ctype]
	if len(fields) == 0 {
		b.WriteString("1") // an empty struct (its C form's hidden byte is not compared) is always equal
	}
	for i, fld := range fields {
		if i > 0 {
			b.WriteString(" && ")
		}
		nm := e.fieldIdent(fld.name)
		b.WriteString(e.fieldEqCmp(fld.ctype, "_ogo_l."+nm, "_ogo_r."+nm))
	}
	b.WriteString(";\n}\n")
	return b.String()
}

// emitStringCompareTriple emits a string comparison: equality (== / !=) via the
// content helper ogo_string_eq, ordering (< <= > >=) via the lexicographic
// ogo_string_cmp compared against 0. l is the left operand, r the right.
func (e *emitter) emitStringCompareTriple(l, r Node, op string) {
	e.usesString = true
	switch op {
	case "==", "!=":
		e.usesStringEq = true
		if op == "!=" {
			e.emit("!")
		}
		e.emit("ogo_string_eq(")
		e.emitExprNode(l)
		e.emit(", ")
		e.emitExprNode(r)
		e.emit(")")
	default: // < <= > >=
		e.usesStringCmp = true
		e.emit("(ogo_string_cmp(")
		e.emitExprNode(l)
		e.emit(", ")
		e.emitExprNode(r)
		e.emit(") " + op + " 0)")
	}
}

// emitStringCompare lowers a standalone string comparison -- kids being exactly
// "operand <relop> operand" with string operands -- returning true when it did. It
// is the no-extra-parens fast path; emitKidsStringCompare handles a comparison
// embedded in a larger chain.
func (e *emitter) emitStringCompare(kids []Node) bool {
	if len(kids) != 3 {
		return false
	}
	op, ok := e.stringCompareAt(kids, 0)
	if !ok {
		return false
	}
	e.emitStringCompareTriple(kids[0], kids[2], op)
	return true
}

// emitKidsStringCompare emits a flat operand/operator kid list, rewriting each
// string sub-comparison (equality or ordering) so a string compare embedded in a
// larger || / && chain (`s == "a" && cond`, `s < t || s == "z"`) lowers correctly.
// Non-string operands and every other operator emit unchanged, so a chain with no
// string comparison is identical to emitting the kids in order.
func (e *emitter) emitKidsStringCompare(kids []Node) {
	// Whether THIS level computes unsigned, which decides how a constant operand of
	// it is spelled (see unsignedLitC). Read from the kid list rather than passed
	// in: a logical or relational chain infers bool and so answers no, and every
	// arithmetic level answers for itself, which is what keeps a nested one from
	// inheriting a verdict that is not about it.
	ct, ctOK := e.inferNodes(kids)
	unsignedLevel := ctOK && isUnsignedCType(ct)
	for i := 0; i < len(kids); {
		if !e.checkCompareAt(kids, i) {
			return
		}
		if op, sn, ok := e.sliceNilCompareAt(kids, i); ok {
			e.emitSliceNilTriple(sn, op)
			i += 3
			continue
		}
		if op, a, ok := e.arrayCompareAt(kids, i); ok {
			e.emitArrayCompareTriple(kids[i], kids[i+2], op, a)
			i += 3
			continue
		}
		if op, l, r, ok := e.ifaceCompareAt(kids, i); ok {
			e.emitIfaceCompareTriple(op, l, r)
			i += 3
			continue
		}
		if op, ct, ok := e.structCompareAt(kids, i); ok {
			e.emitStructCompareTriple(kids[i], kids[i+2], op, ct)
			i += 3
			continue
		}
		if op, ok := e.stringCompareAt(kids, i); ok {
			e.emitStringCompareTriple(kids[i], kids[i+2], op)
			i += 3
			continue
		}
		if lit, ok := e.unsignedLitC(kids[i]); unsignedLevel && ok {
			e.emit(lit)
		} else {
			e.emitExprNode(kids[i])
		}
		i++
	}
}

// emitLogicalKids emits the operand/operator children of an Expression or
// SimpleExpr in source order. When a chain mixes || with && it wraps each
// ||-operand that holds a && in parentheses: C already groups it that way (&& binds
// tighter than ||), so the parentheses change nothing, but they keep gcc's
// -Wparentheses -- which the run tests treat as an error -- quiet. `a && b || c`
// becomes `(a && b) || c`. Every other chain, including one with only comparisons
// or only && or only ||, is emitted verbatim.
func (e *emitter) emitLogicalKids(kids []Node) {
	hasOr, hasAnd := false, false
	for _, c := range kids {
		if c.sym == RelOp {
			switch e.opText(c.ast) {
			case "||":
				hasOr = true
			case "&&":
				hasAnd = true
			}
		}
	}
	if !hasOr || !hasAnd {
		e.emitKidsStringCompare(kids)
		return
	}
	emitGroup := func(group []Node) {
		wrap := slices.ContainsFunc(group, func(c Node) bool {
			return c.sym == RelOp && e.opText(c.ast) == "&&"
		})
		if wrap {
			e.emit("(")
		}
		e.emitKidsStringCompare(group)
		if wrap {
			e.emit(")")
		}
	}
	start := 0
	for i, c := range kids {
		if c.sym == RelOp && e.opText(c.ast) == "||" {
			emitGroup(kids[start:i])
			e.emitExprNode(c) // the " || " operator itself
			start = i + 1
		}
	}
	emitGroup(kids[start:])
}

func (e *emitter) emitExprNode(n Node) {
	switch n.sym {
	case Expression, SimpleExpr:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 1 {
			e.emitExprNode(kids[0])
			return
		}
		// A constant expression whose value does not fit a C int is emitted as that
		// value: C would compute it in int and get a different answer (see intCLit).
		if lit, ok := e.wideConstLit(n.ast); ok {
			e.emit(lit)
			return
		}
		// A string-typed additive expression is concatenation. C cannot add two
		// ogo_string structs, and the target has no heap to build a new one at
		// runtime, so a concatenation of constants is folded to a single literal and
		// anything with a runtime operand is rejected.
		if ct, ok := e.exprReprCType(n.ast); ok && ct == cString {
			if v, ok := e.foldConstString(n.ast); ok {
				e.emitFoldedString(v)
				return
			}
			e.fail("string concatenation with a non-constant operand needs allocation, which the target does not have")
			return
		}
		// A standalone string equality is a content compare (see emitStringCompare); a C
		// `==` on the { ptr, len } struct is not valid.
		if e.emitStringCompare(kids) {
			return
		}
		// C computed this in int if the operands are narrower than one; Go computes
		// in their own type. See narrowCType.
		if nc := e.narrowCType(n.ast); nc != "" {
			e.emit("(" + nc + ")")
		}
		e.emit("(")
		e.emitLogicalKids(kids)
		e.emit(")")
	case Term:
		// Like SimpleExpr, but a "/" or "%" divisor is guarded against zero:
		// `a / b` -> `(a / ogo_nonzero(b))`. A constant divisor needs no guard.
		kids := slices.Collect(it(n.ast))
		if len(kids) == 1 {
			e.emitExprNode(kids[0])
			return
		}
		if lit, ok := e.wideConstLit(n.ast); ok {
			e.emit(lit)
			return
		}
		narrow := e.narrowCType(n.ast) // see narrowCType
		if text, ok := e.shiftChainC(kids); ok {
			if narrow != "" {
				e.emit("(" + narrow + ")")
			}
			e.emit(text)
			return
		}
		if narrow != "" {
			e.emit("(" + narrow + ")")
		}
		e.emit("(")
		unsignedTerm := e.unsignedLevel(n.ast)
		guardNext, complementNext := false, false
		for _, c := range kids {
			switch {
			case c.sym == MulOp:
				op := e.opText(c.ast)
				guardNext, complementNext = false, false
				if op == "&^" {
					// C has no "&^". Go defines "a &^ b" as "a AND NOT b", so it
					// lowers to an AND with the complement of b -- the same rewrite
					// "&^=" uses. The operand is parenthesised because it may be an
					// expression and the complement binds tighter than anything
					// inside one. See complementC for why the complement is not "~".
					e.emit(" & ")
					complementNext = true
					continue
				}
				e.emit(" " + op + " ")
				guardNext = e.checks && (op == "/" || op == "%")
			case complementNext:
				complementNext = false
				ct, _ := e.inferNode(c)
				e.emitComplement(c.ast, ct, func() { e.emitExprNode(c) })
			case guardNext && !e.isIntLiteral(c):
				guardNext = false
				ct, _ := e.inferCType(c.ast)
				// A float divisor is never guarded: Go's float division by zero is
				// ±Inf/NaN, not a panic, and ogo_nonzero(int) would truncate the
				// divisor (2.5 -> 2), miscompiling the division.
				if ct == "double" || ct == "float" {
					e.emitExprNode(c)
					continue
				}
				e.needPanic()
				// A 64-bit divisor needs the 64-bit guard, or ogo_nonzero(int) would
				// truncate it (mis-detecting a large nonzero divisor as zero and
				// dividing by a wrong value).
				fn := "ogo_nonzero"
				if ct == "int64_t" || ct == "uint64_t" {
					fn, e.usesNonzero64 = "ogo_nonzero64", true
				} else {
					e.usesNonzero = true
				}
				e.emit(fn + "(")
				e.emitExprNode(c)
				e.emit(")")
			default:
				if lit, ok := e.unsignedLitC(c); unsignedTerm && ok {
					e.emit(lit) // see unsignedLitC
				} else {
					e.emitExprNode(c)
				}
				guardNext = false
			}
		}
		e.emit(")")
	case UnaryExpr, Factor:
		kids := slices.Collect(it(n.ast))
		if len(kids) == 3 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == LPAREN {
			e.emit("(")
			e.emitExprNode(kids[1])
			e.emit(")")
			return
		}
		kids = e.unparenKids(kids)
		// A negated wide literal, "-987654321098". It reaches no binary level, so it
		// is folded here as well: written out as C source it is a unary minus, which
		// the target's compiler folds in no aggregate initializer at all. The fold
		// is over the NODE, not its children: a prefix operator belongs to the node
		// and foldIntSeq would read it as the first operand of a sequence.
		if v, ok := e.foldIntNode(n); ok && !fitsCInt(v) {
			e.emit(intCLit(v))
			return
		}
		// A receive `<-ch` wraps its operand in the channel's recv helper, so it
		// cannot be emitted as the operator token followed by the operand.
		if elem, base, ok := e.recvOperand(n, kids); ok {
			e.chanRecvElems[elem] = true
			// An ARRAY element comes back through an out parameter -- C cannot return
			// one -- so the receive binds to a temporary of this frame and the
			// temporary is what stands here, as a call returning an array does.
			if a, isArr := e.namedArrays[elem]; isArr {
				name, ok := e.hoistChanRecv(base, elem, a)
				if !ok {
					e.fail("a receive of an array element is only supported as a statement or an initializer")
					return
				}
				e.emit(name)
				return
			}
			e.emit(chanRecvCName(elem) + "(" + base + ")")
			return
		}
		if n.sym == Factor {
			// A function literal becomes the file-scope function it is lifted to,
			// named here as any function used as a value is.
			// "gq.Bump" standing as a value: the function lifted for it, named here
			// as any function used as a value is.
			if base, method, ok := e.factorMethodValue(kids); ok {
				if cname, ok := e.liftMethodValue(base, method); ok {
					e.emit(cname)
				}
				return
			}
			if lit, suffix, ok := e.factorFuncLit(kids); ok {
				cname, ok := e.liftFuncLit(lit)
				if !ok {
					return
				}
				// A literal called where it stands: the lifted name, then the call.
				if len(suffix) == 1 && suffix[0].sym == CallSuffix {
					e.emit(cname + "(")
					e.emitCallArgs(cname, suffix[0].ast)
					e.emit(")")
					return
				}
				if len(suffix) != 0 {
					e.fail("a function literal may only be called where it stands")
					return
				}
				e.emit(cname)
				return
			}
			// "x.(T)" standing as one value panics when it does not hold, as Go's
			// does. The check is a statement, so it goes in the prologue and the
			// expression is left as the cast -- which puts the panic ahead of every
			// position an assertion can stand in, not just a declaration.
			operand, iface, target, isIface, ok := e.typeAssertionKids(kids)
			if !ok {
				// The operand is an expression rather than a name, "rs[i].(*A)":
				// bind it first and assert on the binding.
				operand, iface, target, isIface, ok = e.boundAssertionKids(kids)
			}
			if ok {
				if isIface {
					// An INTERFACE target: the result is another interface value, and
					// building one is statements rather than a cast, so it goes to a
					// temporary in the prologue and the temporary stands here.
					if name, ok := e.hoistIfaceAssert(operand, iface, target); ok {
						e.emit(name)
					}
					return
				}
				if !e.needVTable(iface, target) {
					return
				}
				e.needPanic()
				e.prologue = append(e.prologue, "if (!("+e.assertOKC(operand, iface, target)+")) "+
					"ogo_panic(\"interface conversion: "+e.goTypeName(iface)+" is not *"+e.goTypeName(target)+"\");\n")
				e.emit(e.assertValueC(operand, target))
				return
			}
			// `e.(*P).foo()` / `e.(T).n` -- an assertion carrying a SUFFIX. The
			// assertion binds to a temporary and the rest applies to that, which is
			// what gives the steps a base of the ASSERTED type to be read against.
			if operand, iface, target, isIface, rest, ok := e.typeAssertionPrefix(kids); ok && len(rest) != 0 {
				name, _, ok := e.hoistAssert(operand, iface, target, isIface)
				if !ok {
					return
				}
				if containsSym(rest, CallSuffix) {
					e.emitCallExpr(name, rest)
					return
				}
				if _, ok := e.emitAccessChain(name, rest); ok {
					return
				}
				e.fail("cannot read the result of an assertion through this suffix")
				return
			}
			// `mk()[1]` -- a call returning an ARRAY, read through a suffix. The
			// result is bound to a temporary, since C cannot return an array and the
			// call is therefore a statement with no expression to index.
			if name, steps, ok := e.hoistArrayResultCallKids(kids); ok {
				if _, ok := e.emitAccessChain(name, steps); ok {
					return
				}
				e.fail("an array result cannot be read through this suffix")
				return
			}
			// `([]int)(xs)` / `([3]int)(q)` -- a conversion to an unnamed composite
			// type, written parenthesised because the grammar cannot spell it bare.
			if typeAST, arg, steps, ok := e.factorBracketConv(n); ok {
				text, isID := e.bracketConvOperand(typeAST, arg)
				if !isID {
					e.fail("a conversion to %s is only supported where it changes nothing about the value, "+
						"from a defined type over it", e.litTypeName(typeAST))
					return
				}
				if len(steps) == 0 {
					e.emit(text)
					return
				}
				if _, ok := e.emitAccessChain(text, steps); ok {
					return
				}
				e.fail("a conversion to %s cannot be read through this suffix", e.litTypeName(typeAST))
				return
			}
			// `[]int{1, 2, 3}[0]` -- a bracketed literal read through a suffix. The
			// literal becomes a temporary and the steps apply to that, which is what
			// gives an array literal something indexable to be.
			if typeAST, lit, steps, ok := e.factorLitIndexed(n); ok {
				if name, ok := e.hoistLitVar(typeAST, lit); ok {
					if _, ok := e.emitAccessChain(name, steps); ok {
						return
					}
				}
				e.fail("a %s literal cannot be read through this suffix", e.litTypeName(typeAST))
				return
			}
			// A literal of a DEFINED array or slice type is matched first: its
			// factor looks exactly like a struct literal's -- a name and a
			// CompositeLit -- and the struct path would brace-initialize it, which
			// for a slice means writing its first element where its backing pointer
			// goes. factorArrayLit only claims a name it resolves to one of those
			// two, so a struct's name still falls through.
			if litType, lit, ok := e.factorArrayLit(n); ok && e.isNamedLitType(kids) {
				if name, ok := e.hoistLit(litType, lit); ok {
					e.emit(name)
					return
				}
			}
			if name, lit, ok := e.factorCompositeLit(kids); ok {
				e.emitCompositeLit(name, lit, e.declInit)
				return
			}
			// A slice literal standing as a value: bound to a local declared before
			// the statement -- the same two declarations a variable's initializer
			// emits -- with that local's name standing here. An array literal keeps
			// the refusal below, C having no array value for it to become.
			//
			// The lifetime rules see the literal itself as reaching this frame's
			// storage (frameRefOf), so returning one, storing one in a package
			// variable and handing one to another cog are refused before this runs.
			if litType, lit, ok := e.factorArrayLit(n); ok {
				name, ok := e.hoistLit(litType, lit)
				if !ok {
					// An ARRAY literal, in the positions that hoist nothing to point
					// at: an append and a channel send. C has a value form for one --
					// the compound literal `(Row){1, 2}` -- and a literal of a DEFINED
					// array type has always emitted exactly that, falling through to
					// emitCompositeLit above with the name the program wrote. The
					// unnamed spelling of the same value had no name to write and was
					// refused for want of one rather than for want of a form, so
					// `ch <- Row{1, 2}` compiled where `ch <- [2]int{1, 2}` did not.
					//
					// arrayElemTypedef mints that name, the one a `[][2]int` element
					// is already given, and the two spellings meet.
					if tn, isArray := e.arrayElemTypedef(litType); isArray {
						e.emitCompositeLit(tn, lit, e.declInit)
						return
					}
					e.fail("a %s literal cannot stand here; bind it to a variable and use that",
						e.litTypeName(litType))
					return
				}
				e.emit(name)
				return
			}
			if recv, suffix, ok := e.factorCall(kids); ok {
				// A multi-result call yields no single value; it is only valid in a
				// destructuring assignment (emitMultiAssign), not as an operand.
				if rts, _ := e.userFunc(recv); len(suffix) == 1 && suffix[0].sym == CallSuffix && len(rts) > 1 {
					e.fail("a multi-value call cannot be used as a single value")
					return
				}
				if !e.emitCallExpr(recv, suffix) {
					e.fail("unsupported call in expression")
				}
				return
			}
			// A read of an exported variable from an imported user package, `pkg.V`
			// (or a field chain into it). Checked before the struct-field shapes,
			// which take base to be a local/global variable; here base is an import
			// qualifier, so the read resolves to that package's mangled global.
			if base, fields, ok := e.factorFieldAccess(kids); ok {
				if text, _, ok := e.qualifiedGlobalRead(base, fields); ok {
					e.emit(text)
					return
				}
				// That package's STRING constant, which has no symbol to name and is
				// inlined here exactly as one of this package's is.
				if v, ok := e.qualifiedStrConstVal(base, fields); ok {
					e.emitFoldedString(v)
					return
				}
			}
			// `geo.Name[0]` and `geo.Name[1:]` -- indexing or slicing another
			// package's STRING constant. A constant has no variable, and every chain
			// walker reads its base by NAME, so there was nothing to start from and
			// the diagnosis said "geo is not a value with fields or elements" -- of a
			// package, about a constant that is there. Binding the value to a
			// temporary hands the rest of the chain the string variable it expects.
			// An INTEGER constant of another package needs none of this: it emits a
			// `static const`, which is a name.
			if base, steps, ok := e.factorAccessChain(kids); ok && len(steps) > 1 && steps[0].sym == Selector {
				if field := e.soleIdent(steps[0].ast); field != "" {
					if v, isConst := e.qualifiedStrConstVal(base, []string{field}); isConst {
						tmp := e.hoist(cString, func() { e.emitFoldedString(v) })
						e.locals[tmp] = cString
						rest := steps[1:]
						// A SLICE step is not one the chain walker emits, so the two
						// shapes that take one are tried first, exactly as the general
						// chain below tries them.
						if len(rest) == 1 && rest[0].sym == Index {
							if low, high, max, isSlice := e.sliceParts(rest[0].ast); isSlice {
								if src, okSrc := e.sliceableVar(tmp); okSrc {
									e.emitSliceExpr(src, low, high, max)
									return
								}
							}
						}
						if src, low, high, max, okRow := e.sliceableChainRow(tmp, rest); okRow {
							e.emitSliceExpr(src, low, high, max)
							return
						}
						if _, ok := e.emitAccessChain(tmp, rest); ok {
							return
						}
					}
				}
			}
			// A chain that alternates indexes and selectors more than once --
			// `s[i].v[j]` -- which no fixed shape below can match.
			if base, steps, ok := e.factorAccessChain(kids); ok {
				// `m[0][:]` -- slicing a row. Tried first: the chain walk cannot
				// emit a slice step, and would consume the prefix the header needs.
				if src, low, high, max, ok := e.sliceableChainRow(base, steps); ok {
					e.emitSliceExpr(src, low, high, max)
					return
				}
				if _, ok := e.emitAccessChain(base, steps); ok {
					return
				}
			}
			// `(&v).m()` -- the written-out address form of a method call, which is
			// what `v.m()` means either way its receiver is declared.
			if name, steps, ok := e.factorAddrCall(kids); ok {
				e.emitCallExpr(name, steps)
				return
			}
			// `(*p).x` / `(*p)[i]` -- a written-out dereference carrying a suffix.
			// The chain starts from what p points at, named by the dereference.
			if name, steps, ok := e.factorDerefChain(kids); ok {
				if derefCallSteps(steps) {
					e.emitCallExpr(name, steps)
					return
				}
				if text, cur, ok := e.derefBase(name); ok {
					if _, ok := e.emitAccessChainAt(text, cur, steps, true); ok {
						return
					}
				}
				e.fail("cannot read *%s through this suffix", name)
				return
			}
			// `m[i][j]` -- a full index into a multi-dimensional array.
			// `s[i].x` / `p.pts[i].x` -- index a container, then select from the
			// element. Checked ahead of the index-only shapes, which cannot match a
			// trailing selector anyway.
			if base, fields, indexAST, ok := e.factorFieldIndex(kids); ok {
				// A struct field indexed directly, `b.data[i]`: a slice field reads
				// through its header's backing pointer bounded by len, an array field
				// its inline storage bounded by the declared extent. indexedContainer
				// resolves which, so both read the same way here.
				low, high, max, isSlice := e.sliceParts(indexAST)
				if isSlice {
					// Re-slicing a struct field, `b.data[1:3]`.
					if src, ok := e.sliceableField(base, fields); ok {
						e.emitSliceExpr(src, low, high, max)
						return
					}
				} else {
					if expr, elem, lenExpr, ok := e.indexedContainer(base, fields); ok {
						e.emitIndexSelect(expr, lenExpr, low, elem, nil)
						return
					}
				}
			}
			if base, indexAST, ok := e.factorIndex(kids); ok {
				low, high, max, isSlice := e.sliceParts(indexAST)
				if isSlice {
					src, ok := e.sliceableVar(base)
					if !ok {
						e.fail("only string, array and slice slicing is supported yet")
						return
					}
					e.emitSliceExpr(src, low, high, max)
					return
				}
				// A slice is indexed through its backing pointer, a string through its
				// byte pointer, an array directly. The index is bounds-checked against
				// the container length.
				lenExpr, closing := "", "]"
				switch {
				case e.hasSliceVar(base):
					e.emit(base + ".ptr[")
					lenExpr = base + ".len"
				case e.isStringConstName(base):
					// See stringConstParts: the literal stands where the variable
					// would, since a string constant never becomes one.
					ptr, n, _ := e.stringConstParts(base)
					e.emit(e.byteReadOpen() + ptr + "[")
					lenExpr, closing = n, "])"
				case e.isStringVarName(base):
					e.emit(e.byteReadOpen() + base + ".str[")
					lenExpr, closing = base+".len", "])"
				default:
					_, a, isArray := e.arrayBase(base)
					if isArray && a.dims() > 1 {
						e.fail("a multi-dimensional array must be indexed in every dimension")
						return
					}
					if !isArray && !e.indexableBase(base) {
						return
					}
					if _, isPtr := e.arrayPtrVar(base); isPtr {
						e.emit("(*" + e.varRef(base) + ")[") // `p[i]` is `(*p)[i]`
					} else {
						e.emit(base + "[")
					}
					if isArray {
						lenExpr = a.bound
					}
				}
				e.emitIndex(low, lenExpr)
				e.emit(closing)
				return
			}
		}
		if len(kids) == 2 && kids[0].sym == UnaryOp {
			tok, haveOp := e.unaryOpTok(kids[0].ast)
			switch {
			// Go's unary ^ is a bitwise complement, emitted the same way "&^" emits
			// its right operand rather than as C's "~" -- see emitComplement.
			case haveOp && e.f.ch(tok) == XOR:
				ct, _ := e.inferNode(kids[1])
				e.emitComplement(kids[1].ast, ct, func() { e.emitExprNode(kids[1]) })
				return
			// `&Row(a)[1]`: a conversion's result is not addressable, so Go refuses
			// this and so does OctoGo. It has to be said here because the chain paths
			// UNWRAP the conversion (factorAccessChain), which would otherwise give
			// the address of the operand's element -- a meaning for a program Go does
			// not accept.
			case haveOp && e.f.ch(tok) == AND && e.isArrayConv(kids[1]):
				e.fail("cannot take the address of a conversion: it is not addressable")
				return
			// A dereference of something that is NOT a pointer. The checker refuses
			// the spellings its type model resolves (see exprPointerness); this backs
			// it for the rest -- an element, a call's result -- because otherwise the
			// star is written in front of the operand and the C compiler answers
			// "invalid type argument of unary *", a diagnostic about the emitted C
			// rather than about the program. A type it cannot infer is left alone,
			// so this only ever refuses what it has resolved.
			case haveOp && e.f.ch(tok) == MUL:
				if ct, ok := e.inferNode(kids[1]); ok && !e.isPointer(ct) {
					e.fail("cannot indirect %s: it is not a pointer", e.f.exprSource(kids[1]))
					return
				}
			// Unary minus on a narrow type: C negates the promoted int, Go negates
			// in the type. See narrowCType.
			case haveOp && e.f.ch(tok) == SUB:
				if nc := e.narrowCTypeNode(kids[1]); nc != "" {
					e.emit("(" + nc + ")(-(")
					e.emitExprNode(kids[1])
					e.emit("))")
					return
				}
			}
		}
		// A Factor carrying a SUFFIX that nothing above claimed. Emitting its
		// children reaches the FactorSuffix node itself, which no case handles, so
		// what came out was "unsupported expression node FactorSuffix" -- a message
		// naming an internal node in a program whose source contains no such thing.
		// Say what could not be done to which operand instead.
		if n.sym == Factor && containsSym(kids, FactorSuffix) {
			// A METHOD called on a parenthesised expression, `(a - b).Scaled()`.
			// Last, so it sees only what nothing above claimed: `(&v).m()` and
			// `(*p).m()` are parenthesised too, and their own paths adjust the
			// receiver in ways this one must not.
			if e.emitParenMethod(slices.Collect(it(n.ast))) {
				return
			}
			e.failSuffixChain(n, kids)
			return
		}
		// A written-out DEREFERENCE, `*p` of a pointer variable, takes the nil check.
		// The generic walk below emits the star and the name separately -- the star
		// through emitOperandToken, which sees a token and not a shape -- so the one
		// place that knows this is a dereference is here, before the walk.
		// The shape is read off the CHILDREN here, not through derefOperand: that one
		// takes the ast of an expression CONTAINING a UnaryExpr, and this node is the
		// UnaryExpr, so handing it n.ast matches nothing.
		if n.sym == UnaryExpr && len(kids) == 2 && kids[0].sym == UnaryOp {
			if tok, isOp := e.unaryOpTok(kids[0].ast); isOp && e.f.ch(tok) == MUL {
				if name, isName := e.exprIdent(e.unparenExpr(kids[1].ast)); isName {
					if ct, okT := e.varType(name); okT && e.isPointer(ct) {
						e.emit("(*" + e.nilCheckedC(e.varRef(name), ct) + ")")
						return
					}
				}
			}
		}
		for _, c := range kids {
			e.emitExprNode(c)
		}
	case AddOp, MulOp, RelOp:
		e.emit(" " + e.opText(n.ast) + " ")
	case UnaryOp:
		// A prefix operator: `-`, `!`, `&` (address-of), `*` (deref), `^` -> `~`.
		if tok, ok := e.unaryOpTok(n.ast); ok {
			e.emitOperandToken(tok)
		}
	case 0:
		e.emitOperandToken(n.tok)
	default:
		e.fail("unsupported expression node %v", n.sym)
	}
}

// unaryOpTok returns the operator token of a UnaryOp node.
func (e *emitter) unaryOpTok(ast []int32) (int32, bool) {
	for n := range it(ast) {
		if n.sym == 0 {
			return n.tok, true
		}
	}
	return 0, false
}

// opText returns the operator terminal's text from an AddOp/MulOp/RelOp node.
// The OctoGo operators here all coincide with their C spellings.
func (e *emitter) opText(ast []int32) string {
	for n := range it(ast) {
		if n.sym == 0 {
			return e.src(n.tok)
		}
	}
	return ""
}

func (e *emitter) emitOperandToken(tok int32) {
	switch ch := e.f.ch(tok); ch {
	case INT:
		e.emit(cIntLit(e.src(tok)))
	case CHAR:
		// A rune literal is its Unicode code point, an int32. Emitted as the numeric
		// value rather than a C character constant, so a non-ASCII rune ('é' = 233)
		// is its code point and not an implementation-defined narrow-char value.
		r, ok := runeLitValue(e.src(tok))
		if !ok {
			e.fail("malformed rune literal %s", e.src(tok))
			return
		}
		e.emit(strconv.Itoa(int(r)))
	case FLOAT:
		e.emit(cFloatLit(e.src(tok))) // a float literal is valid C as written (see cFloatLit)
	case IDENT:
		// The predeclared bool constants have no C keyword here (bool is int); emit
		// their integer values. Any other identifier is a name reference.
		switch s := e.src(tok); s {
		case "true":
			e.emit("1")
		case "false":
			e.emit("0")
		case "nil":
			// The nil pointer, emitted as the null pointer constant 0 -- valid as a
			// pointer value and in a pointer comparison (`p != nil` -> `p != 0`). A nil
			// slice or other aggregate zero value is not modelled here.
			e.emit("0")
		case "iota":
			// Inside a const spec's expression, iota is its value; elsewhere it is an
			// ordinary name (the checker rejects a bare iota outside a const).
			if e.iota >= 0 {
				e.emit(strconv.Itoa(e.iota))
				return
			}
			e.emit(s)
		default:
			// A string constant is inlined as its folded literal -- it has no C
			// variable (see emitConstDecl).
			if v, ok := e.foldedStr(s); ok {
				e.emitFoldedString(v)
				return
			}
			e.emit(e.varRef(s)) // a package global is mangled; a local keeps its name
		}
	case STRING:
		e.emitStringLit(tok)
	case SUB, ADD, NOT, AND, MUL:
		e.emit(e.src(tok)) // unary -, +, !, & (address-of), * (deref) prefix
	case XOR:
		e.emit("~") // Go unary ^ is bitwise complement
	default:
		e.fail("unsupported operand %v", ch)
	}
}

// emitStringLit emits a string literal as an ogo_string { pointer, length }
// header. In a static initializer a brace `{"s", n}` is required (a compound
// literal is not a constant expression there); elsewhere the compound literal
// `(ogo_string){"s", n}` is used. n is the decoded byte length (escapes counted as
// one byte).
// constRuneStringFactor folds a Factor that is a `string(x)` conversion with a
// CONSTANT operand, which is a constant string in Go and so may stand in a constant
// concatenation. The operand may be an integer -- the rune conversion this exists
// for -- or itself a constant string, `string("a")`, which converts to its own
// bytes.
func (e *emitter) constRuneStringFactor(kids []Node) (string, bool) {
	name, suffix, ok := e.factorCall(kids)
	if !ok || name != "string" {
		return "", false
	}
	var args []Node
	for _, n := range suffix {
		if n.sym == CallSuffix {
			args = e.callArgExprs(n.ast)
			break
		}
	}
	if len(args) != 1 {
		return "", false
	}
	if v, isInt := e.constIntValue(args[0].ast); isInt {
		return runeString(v), true
	}
	return e.foldConstString(args[0].ast)
}

// foldConstString folds a compile-time-constant string expression to its decoded
// value: a string literal, a string constant, or a concatenation (with "+") of
// those. It reports false for anything with a non-constant operand -- a variable --
// or a non-additive operator, which is what distinguishes a foldable concatenation
// from a runtime one that a zero-allocation target cannot perform.
func (e *emitter) foldConstString(ast []int32) (string, bool) {
	var b strings.Builder
	ok := true
	var walk func(ast []int32)
	walk = func(ast []int32) {
		for n := range it(ast) {
			if !ok {
				return
			}
			switch n.sym {
			case AddOp:
				if e.opText(n.ast) != "+" {
					ok = false // only "+" concatenates; a string has no other operator
				}
			case 0:
				switch e.f.ch(n.tok) {
				case STRING:
					v, err := strconv.Unquote(e.src(n.tok))
					if err != nil {
						ok = false
						return
					}
					b.WriteString(v)
				case IDENT:
					if cv, is := e.foldedStr(e.src(n.tok)); is {
						b.WriteString(cv)
					} else {
						ok = false // a non-constant operand: a runtime concatenation
					}
				case LPAREN, RPAREN:
					// grouping, no value
				default:
					ok = false
				}
			default:
				// A QUALIFIED string constant, `geo.Name`, whose halves the walk
				// below would meet one token at a time: "geo" folds to nothing, so
				// the whole concatenation was reported as having a non-constant
				// operand and needing an allocation -- of an expression Go folds at
				// compile time, and that this compiler folds when both halves are
				// this package's.
				if n.sym == Factor {
					kids := slices.Collect(it(n.ast))
					if b2, f2, isField := e.factorFieldAccess(kids); isField {
						if cv, is := e.qualifiedStrConstVal(b2, f2); is {
							b.WriteString(cv)
							continue
						}
					}
					// `"hi" + string('!')`. A constant rune converted to a string
					// is a constant string, so it concatenates at compile time like
					// any other -- and without this the walk met the conversion one
					// token at a time, failed to fold the bare name `string`, and
					// called the whole expression a runtime concatenation.
					if cv, is := e.constRuneStringFactor(kids); is {
						b.WriteString(cv)
						continue
					}
				}
				walk(n.ast)
			}
		}
	}
	walk(ast)
	if !ok {
		return "", false
	}
	return b.String(), true
}

// emitFoldedString emits a decoded string value as an ogo_string, re-quoting it as
// a C string literal. Under declInit it is a brace, not a compound literal, since a
// file-scope initializer is not a constant expression otherwise.
func (e *emitter) emitFoldedString(v string) {
	e.usesString = true
	body := cQuote(v) + ", " + strconv.Itoa(len(v))
	if e.declInit {
		e.emit("{" + body + "}")
	} else {
		e.emit("(" + cString + "){" + body + "}")
	}
}

func (e *emitter) emitStringLit(tok int32) {
	src := e.src(tok)
	if len(src) != 0 && src[0] == '`' {
		// A raw string is verbatim between the back quotes, with carriage returns
		// discarded (Go spec) and no escape processing. Its text is not a valid C
		// literal as-is (it may hold real newlines and unescaped backslashes), so
		// decode it and re-quote through the same path a folded string uses.
		e.emitFoldedString(strings.ReplaceAll(src[1:len(src)-1], "\r", ""))
		return
	}
	decoded, err := strconv.Unquote(src)
	if err != nil {
		e.fail("invalid string literal %s", src)
		return
	}
	// Decoded and RE-QUOTED, not passed through. Go and C share the common escapes
	// and part company on the rest: C's "\x" has no length limit, so Go's "a\xffb"
	// reads there as one escape of value 0xffb -- a warning and the wrong bytes --
	// and Go's "\u2028" is a UTF-8 sequence here and a universal character name
	// there. Re-quoting settles all of it by writing the BYTES.
	e.emitFoldedString(decoded)
}

// normalizeIntLit rewrites an OctoGo integer literal to a form the C backend
// accepts. Go's explicit-octal prefix "0o"/"0O" is not valid C — C spells octal
// with a bare leading "0" (0o17 -> 017) — so it is converted. Underscores (digit
// separators) are stripped as well: the flexcc backend happens to accept them,
// but removing them keeps the emitted C independent of that leniency. Decimal,
// hex ("0x"/"0X"), binary ("0b"/"0B", accepted by flexcc) and Go's legacy
// leading-zero octal are already valid C and pass through, so a hex or binary
// literal keeps its base (readability matters for pin masks).
func normalizeIntLit(src string) string {
	s := strings.ReplaceAll(src, "_", "")
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'o' || s[1] == 'O') {
		s = "0" + s[2:]
	}
	return s
}

func containsSym(nodes []Node, sym Symbol) bool {
	for _, n := range nodes {
		if n.sym == sym {
			return true
		}
	}
	return false
}

// emitRecvStmt emits a bare receive statement `<-ch`. The value is discarded but
// the receive still happens: on a rendezvous channel that is how a program waits
// for a goroutine, which is the whole point of the form.
//
// The grammar attaches the "<-" to the statement rather than to an expression, so
// the operand here is the channel alone and recvOperand -- which matches a "<-"
// inside a unary expression -- does not apply; both resolve the channel through
// chanOperand. The (void) cast is what `_ = <-ch` already emitted, and keeps the
// discarded result from drawing a warning.
func (e *emitter) emitRecvStmt(nodes []Node) {
	for _, n := range nodes {
		if n.sym != Expression {
			continue
		}
		elem, base, ok := e.chanOperand(n.ast)
		if !ok {
			e.fail("a receive statement needs a plain channel operand")
			return
		}
		e.chanRecvElems[elem] = true
		e.ind()
		if a, isArr := e.namedArrays[elem]; isArr {
			// The value is discarded, but the rendezvous still has to happen, so it
			// is received into a temporary nothing reads.
			tmp := e.newTmp()
			e.emit(a.elem + " " + tmp + a.declSuffix() + ";\n")
			e.ind()
			e.emit(chanRecvCName(elem) + "(" + base + ", " + tmp + ");\n")
			return
		}
		e.emit("(void)" + chanRecvCName(elem) + "(" + base + ");\n")
		return
	}
	e.fail("a receive statement needs a channel operand")
}

// allTrue is the per-target declare vector of a `var` declaration, which declares
// every one of its names -- unlike ":=", which may only assign to some.
func allTrue(n int) []bool {
	r := make([]bool, n)
	for i := range r {
		r[i] = true
	}
	return r
}

// sortedArrayKeys is sortedKeys for the array-equality helper map.
func sortedArrayKeys(m map[string]arrDim) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	slices.Sort(r)
	return r
}

// arrayEqName is the name of the equality helper for an array type, `[3]int` ->
// `ogo_eq_arr_3_int`. Every extent is in the name, so a [2][3]int and a [3][2]int
// get distinct helpers.
func arrayEqName(a arrDim) string {
	s := "ogo_eq_arr"
	for _, b := range a.bounds() {
		s += "_" + b
	}
	return s + "_" + sanitizeElem(a.elem)
}

// needArrayEq records that an array type needs an equality helper, and that a
// multi-dimensional one needs a helper for its rows too, since it compares them
// through it. A struct element compares through its own helper, which is required
// here for the same reason.
func (e *emitter) needArrayEq(a arrDim) {
	name := arrayEqName(a)
	if _, seen := e.eqArrays[name]; seen {
		return
	}
	e.eqArrays[name] = a
	switch {
	case a.dims() > 1:
		e.needArrayEq(a.row())
	case a.elem == cString:
		e.usesStringEq = true
	case e.structs[a.elem] != nil:
		e.needStructEq(a.elem)
	}
}

// arrayEqDef defines an array's equality helper. The operands are pointers, not
// values: an array decays to one at the call, which is also what keeps this clear
// of the by-value struct-with-array-field limit that stops struct comparison
// (needStructEq) -- nothing is passed by value here.
func (e *emitter) arrayEqDef(name string, a arrDim) string {
	cmp := ""
	if a.dims() > 1 {
		// A row is itself an array and decays in turn, so it compares through the
		// helper for one dimension less.
		cmp = arrayEqName(a.row()) + "(_ogo_l[i], _ogo_r[i])"
	} else {
		cmp = e.fieldEqCmp(a.elem, "_ogo_l[i]", "_ogo_r[i]")
	}
	return fmt.Sprintf(`%s {
	for (int i = 0; i < %s; i++) {
		if (!(%s)) { return 0; }
	}
	return 1;
}
`, e.arrayEqSig(name, a), a.bound, cmp)
}

// arrayEqSig renders an array equality helper's signature, shared by the forward
// declaration and the definition so the two cannot drift apart.
func (e *emitter) arrayEqSig(name string, a arrDim) string {
	// The outermost extent is the one that decays to a pointer, so it is the empty
	// bracket and every inner extent must stay complete: a [2][3]int parameter is
	// `int p[][3]`, not `int p[2][]`, which is an array of incomplete type.
	inner := ""
	for _, b := range a.bounds()[1:] {
		inner += "[" + b + "]"
	}
	// No const on the parameters. It would be accurate -- nothing here writes --
	// but for a multi-dimensional array the parameter is a pointer to an array, and
	// the target's C compiler does not apply the qualifier conversion there: passing
	// an `int (*)[2]` where an `int (*)[2]` const-qualified is expected draws
	// "incompatible pointer types in parameter passing" on every call.
	return fmt.Sprintf("static int %s(%s _ogo_l[]%s, %s _ogo_r[]%s)", name, a.elem, inner, a.elem, inner)
}

// arrayCompareAt reports whether kids[i..i+2] compares two arrays for equality.
// Arrays are comparable in Go when their element type is, and only for equality,
// never ordering.
//
// It exists because C would happily accept the comparison and mean something else:
// both operands decay to pointers, so `a == b` asks whether they are the same
// array, which for two distinct ones is always false. That compiled without a
// murmur from either compiler and quietly answered false.
func (e *emitter) arrayCompareAt(kids []Node, i int) (op string, a arrDim, ok bool) {
	if i+2 >= len(kids) || kids[i+1].sym != RelOp {
		return "", arrDim{}, false
	}
	// The operands are classified before the operator, so that an ordering operator
	// on arrays is refused here rather than falling through to C's -- where it would
	// compare the decayed pointers and mean nothing, exactly as == did.
	l, lok := e.arrayOperand(kids[i])
	r, rok := e.arrayOperand(kids[i+2])
	if !lok && !rok {
		return "", arrDim{}, false
	}
	if !lok || !rok {
		known := l
		if !lok {
			known = r
		}
		e.fail("cannot compare %s with a value of another type", e.goArrayTypeName(known))
		return "", arrDim{}, false
	}
	pos := e.f.tok(kids[i+1].Pos()).Position()
	switch op = e.opText(kids[i+1].ast); op {
	case "==", "!=":
		// Go compares arrays for equality only.
	default:
		e.fail("%v: invalid operation: operator %s not defined on %s", pos, op, e.goArrayTypeName(l))
		return "", arrDim{}, false
	}
	if l.elem != r.elem || !slices.Equal(l.bounds(), r.bounds()) {
		e.fail("%v: cannot compare %s with %s", pos, e.goArrayTypeName(l), e.goArrayTypeName(r))
		return "", arrDim{}, false
	}
	// An array is comparable when its element is. A slice element is what the
	// per-element helper would have compared with C's "==", which asks whether two
	// headers are the same bytes rather than refusing.
	if _, ok := e.comparableCType(l.elem); !ok {
		e.fail("%v: invalid operation: %s cannot be compared", pos, e.goArrayTypeName(l))
		return "", arrDim{}, false
	}
	return op, l, true
}

// arrayOperand reports the array type of a comparison operand: an array variable,
// a dereferenced pointer to one, an array reached through a chain of fields and
// indexes, or a literal, which has no C value and is bound to a temporary by
// emitArrayOperand -- what is needed here is only its extents, to tell it from a
// value of another type.
//
// It reads exactly the shapes arrayShapeOf does, and recognising anything less is
// not a refusal but a WRONG ANSWER: an operand this declines sends the comparison
// down C's own "==", which asks whether the two decayed pointers are equal. So
// `pool[0] == pool[1]` was false for two identical rows, in C the host compiler
// warns about no more than it warns about comparing any two pointers.
func (e *emitter) arrayOperand(n Node) (arrDim, bool) {
	return e.arrayShapeOf(n.ast)
}

// emitArrayOperand emits one side of an array comparison. The helper takes the
// arrays by pointer, so what each side needs is something whose address the call can
// pass -- which is what arraySourceC names, binding a literal to a temporary since a
// literal has no C value to take the address of.
func (e *emitter) emitArrayOperand(n Node) {
	if text, ok := e.arraySourceC(n.ast); ok {
		e.emit(text)
		return
	}
	e.emitExprNode(n)
}

// emitArrayCompareTriple emits an array equality as a call to the per-type helper,
// negated for "!=".
func (e *emitter) emitArrayCompareTriple(l, r Node, op string, a arrDim) {
	e.needArrayEq(a)
	if op == "!=" {
		e.emit("!")
	}
	e.emit(arrayEqName(a) + "(")
	e.emitArrayOperand(l)
	e.emit(", ")
	e.emitArrayOperand(r)
	e.emit(")")
}

// hoistArgs evaluates each argument of a call into a temporary, in source order,
// and returns the temporaries' names. It reports false when any argument's type
// cannot be named in C -- an array, say -- in which case the caller emits the
// arguments in place and the order stays whatever the C compiler chooses.
func (e *emitter) hoistArgs(cname string, args []Node) ([]string, bool) {
	sliceParams := e.funcSliceParams[cname]
	names := make([]string, 0, len(args))
	// Every hoist appends a declaration to the statement's prologue, so giving up
	// part way has to take those back: the caller then emits the arguments in
	// place, and a temporary left standing is at best a variable nothing reads and
	// at worst a second evaluation of an argument that changes something.
	mark := len(e.prologue)
	fail := func() ([]string, bool) {
		e.prologue = e.prologue[:mark]
		return nil, false
	}
	for i, a := range args {
		// A bare nil at a slice parameter has no type of its own; it takes the
		// parameter's, exactly as it does when emitted in place.
		if i < len(sliceParams) && sliceParams[i] != "" && e.isNilExpr(a.ast) {
			ct := sliceParams[i]
			names = append(names, e.hoist(ct, func() { e.emit("(" + ct + "){0}") }))
			continue
		}
		ct, ok := e.inferCType(a.ast)
		if !ok {
			return fail()
		}
		names = append(names, e.hoist(ct, func() { e.emitExpr(a.ast) }))
	}
	return names, true
}

// sliceBackingIsFrame reports whether a slice-valued expression's backing array is
// storage of the current frame, and names what it came from.
//
// It answers the question a return has to ask. A slice is a { pointer, len, cap }
// view, and on a target with no heap the storage it views is either a package-level
// array, the caller's (reached through a parameter), or a local of this frame --
// and the last dangles the moment the frame goes. Three shapes are recognised: a
// local slice variable whose backing this frame created (make, or a slice
// literal), a slice of a local array, and a re-slice of either. Anything else --
// a package variable, a parameter, a call's result -- is left alone, so a shape
// this does not model is accepted rather than wrongly refused.
func (e *emitter) sliceBackingIsFrame(ast []int32) (string, bool) {
	if name, ok := e.exprIdent(ast); ok {
		return name, e.frameBacked[name]
	}
	// A CONVERSION to a slice type views the same storage: `L(a[:])` is backed by
	// whatever `a[:]` is. Followed here as well as in frameRefOf, because a
	// DECLARATION asks this question to decide whether the new variable inherits the
	// backing, and `s := L(a[:])` inherited nothing.
	if recv, suffix, ok := e.directCall(ast); ok && len(suffix) != 0 &&
		suffix[len(suffix)-1].sym == CallSuffix && e.convToSliceType(recv, suffix) {
		if args := e.callArgExprs(suffix[len(suffix)-1].ast); len(args) == 1 {
			return e.sliceBackingIsFrame(args[0].ast)
		}
	}
	// A slice step at the end of a CHAIN: `a[:]` and `s[1:2]`, but also `b.arr[:]`,
	// `m[0][:]`, `bs[1].arr[:]` and `p[:]`. Only the bare one-step shape used to be
	// read, so every longer route to the same storage produced a dangling slice at
	// every sink -- an array field of a local struct being the ordinary way a program
	// carries a buffer around.
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return "", false
	}
	kids := slices.Collect(it(fac.ast))
	// `(*p)[:]`, the written-out form: what it reaches is what p points at, which is
	// the holder mark's business rather than the pointer's own storage.
	if name, steps, isDeref := e.factorDerefChain(kids); isDeref && e.endsInSliceStep(steps) {
		return name, e.frameHolder[name] != ""
	}
	base, steps, isChain := e.factorAccessChain(kids)
	if !isChain || !e.endsInSliceStep(steps) {
		return "", false
	}
	// Through a POINTER the chain reaches what the pointer points at, and only the
	// holder mark knows where that is: `p := &pkgArray` is a local pointing at
	// storage that outlives the call, and reading the pointer's own would refuse it.
	if ct, isVar := e.varType(base); isVar && e.isPointer(ct) {
		return base, e.frameHolder[base] != ""
	}
	cur, walked := e.accessChainType(base, steps[:len(steps)-1])
	if !walked {
		return "", false
	}
	switch {
	case len(cur.dims) != 0:
		// An ARRAY's storage is wherever the array itself lives, so slicing one
		// reached from a local root views this frame -- and one reached from a
		// package root does not.
		return base, e.isFrameVar(base)
	case cur.slice:
		// A SLICE has a backing of its own, and its mark is what says where.
		return base, e.frameBacked[base] || e.frameHolder[base] != ""
	}
	return "", false
}

// endsInSliceStep reports whether the last step of a chain is a SLICE rather than an
// index -- `xs[1:2]` and not `xs[1]`. Only a slice keeps a view of the storage; an
// index reads a value out of it.
func (e *emitter) endsInSliceStep(steps []Node) bool {
	if len(steps) == 0 || steps[len(steps)-1].sym != Index {
		return false
	}
	_, _, _, isSlice := e.sliceParts(steps[len(steps)-1].ast)
	return isSlice
}

// frameRef describes a value that reaches storage of the current frame: which local
// that storage belongs to, and how a diagnostic should name the value.
//
// Three kinds of value reach it and each needs its own words -- a slice viewing a
// local array, the address of a local, and a variable that was handed one of those.
// Everything a diagnostic has to say beyond the naming is the same for all three,
// which is why the five sinks share one phrasing and substitute what.
type frameRef struct {
	origin string // how to name the storage the value reaches, after "a pointer into"
	what   string // how to name the value
	name   string // the referent VARIABLE, bare, for a block-lifetime comparison. Empty where the storage is a temporary the emitter minted, which belongs to the block being emitted (see blockDepthOf).
	view   bool   // the value is itself a slice over that storage
}

func sliceRef(name string) frameRef {
	return frameRef{origin: "local " + name, what: "a slice backed by local " + name, name: name, view: true}
}

func addrRef(name string) frameRef {
	return frameRef{origin: "local " + name, what: "the address of local variable " + name, name: name}
}

// litRef names a slice literal, whose backing array the emitter mints as a local of
// this frame -- so the header views this frame's storage exactly as a slice of a
// local array does, with no variable of its own to name.
func litRef() frameRef {
	return frameRef{
		origin: "a slice literal's backing array",
		what:   "a slice literal, whose backing array is this function's",
		view:   true,
	}
}

// tempOrigin names the storage the emitter mints for a value that has none of its
// own -- a literal or a call's result put into an interface. There is no variable a
// reader could be told to move to package scope, so advice() answers differently.
const tempOrigin = "a temporary of this function"

// addrLitRef names `&T{...}`. The literal has no variable, so the emitter gives it a
// temporary of this frame and the address is that temporary's -- which makes it reach
// this frame exactly as the address of a local does.
//
// Binding it to a variable first was refused (ifaceStoreC marks the holder), and
// every DIRECT form was accepted: stored in a package variable, returned, sent,
// launched on a cog and passed to a function that keeps it, all five a dangling
// pointer into a frame that had returned. specs.go had said it was refused since
// interfaces shipped.
func addrLitRef() frameRef {
	return frameRef{
		origin: tempOrigin,
		what:   "the address of a composite literal, which has no variable of its own",
	}
}

func holderRef(name, origin string) frameRef {
	r := frameRef{origin: origin, what: "local " + name + ", which holds a pointer into " + origin}
	// The referent is the variable the origin NAMES, not the holder: how long the
	// holder itself lives says nothing about the storage it points at. A minted
	// temporary (tempOrigin) names no variable and leaves this empty.
	if v, isVar := strings.CutPrefix(origin, "local "); isVar {
		r.name = v
	}
	return r
}

// noteRangeValueHolder passes a container's frame mark on to the value variable a
// range binds. `for _, s := range xs` over an xs that holds a reference to this
// frame binds an s that holds it too, and without this the loop handed the element
// out under a new name that every sink accepted.
//
// The element's own type decides, as it does for a field read: a slice element
// inherits the backing mark, a pointer or struct element inherits the holder mark,
// and a scalar element carries nothing.
func (e *emitter) noteRangeValueHolder(rangeExpr []int32, val, elem string) {
	r, reaches := e.frameRefOf(rangeExpr)
	if !reaches {
		return
	}
	if e.isSliceCType(e.underlyingCType(elem)) {
		e.frameBacked[val] = true
		return
	}
	if e.carriesReference(elem) {
		e.frameHolder[val] = r.origin
	}
}

// initViewsFrame reports whether a slice variable's initializer views storage of
// this frame, by either route: a backing this frame owns, which sliceBackingIsFrame
// resolves, or a value read out of something already MARKED as holding one, which
// only frameRefOf knows about.
//
// The declaration has to inherit the mark or a copy launders it: `s := b.d` and
// `s := xs[0]` would carry out what `g = b.d` and `g = xs[0]` are refused for. It
// also catches a slice LITERAL, whose backing is a minted local with no name for
// sliceBackingIsFrame to have asked about.
func (e *emitter) initViewsFrame(initExpr []int32) bool {
	if _, frame := e.sliceBackingIsFrame(initExpr); frame {
		return true
	}
	_, ref := e.frameRefOf(initExpr)
	return ref
}

// readHolderRef names a value read out of a marked holder -- `b.d`, `xs[0]`,
// `bs[1].d`. It says what the program WROTE rather than naming the variable, which
// would send a reader looking at a line they did not write.
func readHolderRef(read, origin string) frameRef {
	return frameRef{origin: origin, what: read + ", which holds a pointer into " + origin}
}

// carriesReference reports whether a value of this C type can hold a reference to
// storage elsewhere: a slice header, a pointer, or a struct that may contain either.
// A scalar cannot, which is what keeps reading an int field out of a marked holder
// from being refused for no reason.
//
// A STRING is not counted. One can view storage -- a Builder's backing -- but that
// is its own open question, and counting it here would refuse the ordinary string
// field a great many structs have.
func (e *emitter) carriesReference(ctype string) bool {
	u := e.underlyingCType(ctype)
	return e.isSliceCType(u) || e.isPointer(u) || e.isStruct(u)
}

// advice names the fix. A view has a backing array to move; anything else is the
// variable itself, and telling a reader to move a backing array that is not there
// sends them looking for one.
func (r frameRef) advice() string {
	switch {
	case r.view:
		return "declare the backing array at package scope"
	case r.origin == tempOrigin:
		return "assign the value to a package variable and use that"
	}
	return "declare " + strings.TrimPrefix(r.origin, "local ") + " at package scope"
}

// returnAdvice is advice() for a RETURN, where a view has one option it has nowhere
// else: the CALLER can own the backing array and pass it in, which keeps the function
// usable without moving storage to package scope.
//
// The three sinks that phrase this used to hardcode the backing-array wording, which
// is right for a slice and wrong for everything else -- a struct address refused at a
// send was told to move a backing array it does not have. advice() knew the
// difference and only one sink of four asked it.
func (r frameRef) returnAdvice() string {
	if r.view {
		return "take the backing array from the caller or declare it at package scope"
	}
	return r.advice()
}

// frameRefOf reports whether a single expression reaches this frame's storage.
//
// Three shapes do. A slice viewing a local array, which sliceBackingIsFrame resolves.
// The address of a local. And a variable that was given one of those -- a struct
// whose field was assigned a local's address or a slice of a local array, or a copy
// of such a struct. The third is why frameHolder exists: a struct hands the reference
// on without being one, and following it per field would mean tracking provenance
// per field, so the variable carries the mark instead.
func (e *emitter) frameRefOf(ast []int32) (frameRef, bool) {
	if name, frame := e.sliceBackingIsFrame(ast); frame {
		return sliceRef(name), true
	}
	// A slice literal is backed by an array this frame owns, so it reaches this
	// frame's storage by construction -- there is no variable to have marked.
	if elem, _, ok := e.soleSliceLit(ast); ok && elem != "" {
		return litRef(), true
	}
	// `&T{...}`, the same by another spelling: a struct literal given a frame
	// temporary to be the address of.
	if _, _, isAddrLit := e.addrOfCompositeLit(ast); isAddrLit {
		return addrLitRef(), true
	}
	if name, ok := e.addrOfRoot(ast); ok && e.isFrameVar(name) {
		return addrRef(name), true
	}
	if name, ok := e.exprIdent(ast); ok {
		if origin := e.frameHolder[name]; origin != "" {
			return holderRef(name, origin), true
		}
	}
	// A COMPOSITE LITERAL carries its elements into the value it makes: `Box{a[:]}`
	// is a struct holding a slice of this frame, so handing the struct on hands the
	// slice on. Every door had this hole and only the DECLARATION compensated for it,
	// by descending into the literal itself (noteDeclFrameHolder) -- so binding the
	// value to a variable first was refused while storing, returning, sending,
	// launching or passing the literal DIRECTLY was accepted. The workaround was
	// checked and the plain spelling was not.
	//
	// Only a literal is descended into. An arbitrary expression may MENTION a frame
	// reference without the value carrying it out -- `len(a[:])` is an int -- which is
	// the same distinction noteDeclFrameHolder makes by looking at the target's type.
	if r, ok := e.frameRefInLit(ast); ok {
		return r, true
	}
	// Reading a field OUT of a marked holder hands on the reference the holder
	// carries. `b.d = a[:]` marks b, and handing b on is refused -- but `g = b.d` is
	// the same header by another spelling, and it was accepted at every sink: a
	// dangling slice stored, returned, sent and launched, silently.
	//
	// The mark is per VARIABLE, deliberately (see noteFrameHolder), so there is no
	// per-field provenance to consult and the field's own TYPE is what separates a
	// reference-carrying read from a harmless one. That costs the same over-refusal
	// the variable mark already costs -- a second slice field holding package storage
	// reads as suspect too -- and it costs nothing on a scalar field, which cannot
	// carry a reference at all.
	// A method CALL on a marked holder that hands the reference out: `sb.String()`
	// is a VIEW of the Builder's backing, so it reaches whatever that backing does.
	// Stored in a package variable it printed the frame's leftovers -- silently, the
	// one thing these rules exist to stop.
	//
	// carriesReference deliberately does not count a STRING, because an ordinary
	// string field carries no reference and counting it would refuse a great many
	// structs. What is counted here is not the type but the PROVENANCE: a string that
	// came out of a marked holder, which an ordinary one never does.
	if fac, isFac := e.soleFactorNode(ast); isFac {
		kids := slices.Collect(it(fac.ast))
		if base, steps, isCall := e.factorCall(kids); isCall && len(steps) == 2 &&
			steps[0].sym == Selector && steps[1].sym == CallSuffix {
			if origin := e.frameHolder[base]; origin != "" {
				if ct, okc := e.callResultCType(base, steps); okc &&
					(ct == cString || e.carriesReference(ct)) {
					return readHolderRef(e.f.exprSource(fac), origin), true
				}
			}
		}
	}
	if fac, isFac := e.soleFactorNode(ast); isFac {
		if base, steps, isChain := e.factorAccessChain(slices.Collect(it(fac.ast))); isChain {
			if origin := e.frameHolder[base]; origin != "" {
				// The whole chain, not a field path: an ARRAY of slices or of structs
				// is marked on the array, and `xs[0]` and `bs[1].d` reach out of it
				// exactly as `b.d` does.
				if cur, walked := e.accessChainType(base, steps); walked {
					if ct, valued := e.chainValueCType(cur); valued && e.carriesReference(ct) {
						return readHolderRef(e.f.exprSource(fac), origin), true
					}
				}
			}
		}
	}
	// A call whose result derives from one of its arguments hands the argument's
	// provenance back out: `id(&x)` reaches x's storage exactly as `&x` does. Without
	// this a single call launders a reference past every sink -- `return id(&x)`
	// compiled, and so did storing or sending one.
	if recv, suffix, ok := e.directCall(ast); ok && len(suffix) != 0 && suffix[len(suffix)-1].sym == CallSuffix {
		// A CONVERSION to a slice type is not a call: it renames the same header
		// over the same storage, so whatever the operand referred to, the result
		// refers to. `g = L(a[:])` for a local a laundered the reference past every
		// sink -- the plain `g = a[:]` was refused and the conversion of it was not.
		// A conversion to an INTERFACE type is the same laundering by the other
		// spelling: the value holds a POINTER to its operand and nothing else, so
		// `g = Shape(&q)` and `g = any(&q)` reach q exactly as `g = &q` does.
		if e.convToSliceType(recv, suffix) || e.convToIfaceType(recv, suffix) {
			if args := e.callArgExprs(suffix[len(suffix)-1].ast); len(args) == 1 {
				return e.frameRefOf(args[0].ast)
			}
		}
		cname := e.calleeSummaryName(recv)
		if len(suffix) != 1 {
			// A method or an imported package's function: the same resolution the
			// call itself uses, so `return r.id(&x)` follows the receiver's method.
			if n, _, ok := e.callResultInfo(recv, suffix); ok {
				cname = n
			} else {
				cname = ""
			}
		}
		derives := e.retParams[cname]
		args := e.callArgExprs(suffix[len(suffix)-1].ast)
		for i, a := range args {
			if i < len(derives) && derives[i] {
				if r, ok := e.frameRefOf(a.ast); ok {
					return r, true
				}
			}
		}
	}
	return frameRef{}, false
}

// convToSliceType reports whether recv names a defined type whose underlying type is
// a SLICE, so that `recv(x)` is a conversion rather than a call and the result views
// whatever x views. steps is the number of suffix steps, which must be the one call:
// a qualified name is another package's and is not resolved here.
//
// Only a slice result matters to the lifetime rules. A conversion to a scalar or to
// an array yields a VALUE, which refers to nothing; a conversion to a slice yields
// the same three words over the same backing.
func (e *emitter) convToSliceType(recv string, suffix []Node) bool {
	ct, used, ok := e.convChainHead(recv, suffix)
	return ok && used == len(suffix) && e.isSliceCType(e.underlyingCType(ct))
}

// convToIfaceType is convToSliceType for an INTERFACE target. An interface value holds
// a POINTER to its operand -- that is the whole representation on this target -- so a
// conversion of a local's address is that address by another name, and the reference
// it carries is the operand's. Without this, enabling the conversion at all would have
// opened a laundering route past every sink: `g = &q` is refused for a local q and
// `g = Shape(&q)` was accepted, which is the shape the slice conversion had.
//
// `any` is included, being the empty interface under a name the universe holds rather
// than any declaration; convType answers it the same way.
func (e *emitter) convToIfaceType(recv string, suffix []Node) bool {
	ct, used, ok := e.convChainHead(recv, suffix)
	return ok && used == len(suffix) && e.isIfaceCType(ct)
}

// receiverFrameRef reports a method receiver that reaches this frame's storage when
// the method is launched on another cog. A pointer receiver hands out the address of
// the receiver itself; a value receiver is copied, and carries a reference only if
// the value holds one.
func (e *emitter) receiverFrameRef(recv string, wantPtr bool) (frameRef, bool) {
	if wantPtr && e.isFrameVar(recv) {
		return addrRef(recv), true
	}
	if origin := e.frameHolder[recv]; origin != "" {
		return holderRef(recv, origin), true
	}
	return frameRef{}, false
}

// soleSliceLit matches an expression that is nothing but a slice literal, returning
// its element C type. An array literal is not one: it is a value, copied where it is
// used, and carries no reference to anything.
//
// litSliceType, not sliceType: a literal of a DEFINED slice type is a slice literal
// too, backed by an array of this frame exactly as the "[]T{...}" spelling is. It
// used to be read with sliceType, which matches only the written "[]T" shape, so
// "xs = L{1, 2}" for a "type L []int" passed the lifetime check that refused
// "xs = []int{1, 2}" -- and stored a header over a dead frame into a package
// variable. That is the one thing the lifetime rules exist to prevent, and it was
// SILENT: the program built and read back whatever had since been written over the
// frame.
func (e *emitter) soleSliceLit(ast []int32) (string, Node, bool) {
	typeAST, lit, ok := e.soleArrayLit(ast)
	if !ok {
		return "", Node{}, false
	}
	if elem, ok := e.litSliceType(typeAST); ok {
		return elem, lit, true
	}
	return "", Node{}, false
}

// frameRefInLit finds a frame reference among the ELEMENTS of a composite literal,
// in both spellings: named or type-elided (`Box{a[:]}`, `{a[:]}`) and bracketed
// (`[]Box{{a[:]}}`). Nesting is covered because each element is asked of frameRefOf,
// which comes back here for a literal of its own.
func (e *emitter) frameRefInLit(ast []int32) (frameRef, bool) {
	lit, ok := Node{}, false
	if _, l, isNamed := e.soleCompositeLit(ast); isNamed {
		lit, ok = l, true
	} else if _, l, isBracket := e.soleArrayLit(ast); isBracket {
		lit, ok = l, true
	}
	if !ok {
		return frameRef{}, false
	}
	for _, el := range compositeLitElements(lit) {
		if r, isRef := e.frameRefOf(el.value.ast); isRef {
			return r, true
		}
	}
	return frameRef{}, false
}

// frameRefIn finds the first of several expressions that reaches this frame's storage.
func (e *emitter) frameRefIn(exprs []Node) (Node, frameRef, bool) {
	for _, x := range exprs {
		if r, ok := e.frameRefOf(x.ast); ok {
			return x, r, true
		}
	}
	return Node{}, frameRef{}, false
}

// noteFrameHolder records that a local was given a reference to this frame's storage,
// so that handing the local on is refused as handing the reference would be. It fires
// on a write into the variable or into a field of it -- `b.data = a[:]`, `n.p = &x` --
// and on a copy of a variable already marked.
//
// The mark is per variable and is never cleared, which is what keeps it sound with no
// per-field tracking: a struct with two fields, one holding a frame reference and one
// not, must stay marked. The cost is a refusal of the program that overwrites the
// only such field with package-level storage and then crosses -- rare, and the fix is
// to use the package-level storage from the start.
func (e *emitter) noteFrameHolder(base string, op []Node) {
	if !e.isFrameVar(base) {
		return
	}
	if len(op) != 2 || op[0].sym != 0 || e.f.ch(op[0].tok) != ASSIGN {
		return
	}
	for n := range it(op[1].ast) {
		if n.sym != Expression {
			continue
		}
		r, ok := e.frameRefOf(n.ast)
		if !ok {
			continue
		}
		if _, isSlice := e.sliceVars[base]; isSlice && r.view {
			// A slice variable assigned a view of this frame is the shape frameBacked
			// already models, and the one its wording fits.
			e.frameBacked[base] = true
			return
		}
		e.frameHolder[base] = r.origin
		return
	}
}

// checkReturnBacking refuses returning a value that reaches storage of this frame.
// The reference would outlive the storage, and with no heap there is nowhere to
// promote that storage to, so it is a static error -- the counterpart of the
// checker's refusal to return a local's address.
func (e *emitter) checkReturnBacking(exprs []Node) {
	if x, r, ok := e.frameRefIn(exprs); ok {
		// Positioned, unlike most emitter diagnostics: this one refuses a program a
		// user wrote rather than reporting a shape the emitter cannot lower, so it
		// needs to say where.
		e.fail("%v: cannot return %s: its storage does not outlive the function; %s",
			e.f.tok(x.Pos()).Position(), r.what, r.returnAdvice())
	}
}

// checkStoreBacking refuses storing a slice whose backing array is a local of this
// frame into a package-level variable, or a field of one.
//
// A package variable outlives every call, so the header would still point at the
// frame's storage long after it is gone -- the same error checkReturnBacking
// catches at a return, through the other door. crossBackedByFrame is the third
// door: a reference that does not provably outlive the frame, but leaves its
// control.
func (e *emitter) checkStoreBacking(base string, op []Node) {
	if !e.isPackageVar(base) {
		return // a local target dies with the frame, like the backing
	}
	if len(op) != 2 || op[0].sym != 0 || e.f.ch(op[0].tok) != ASSIGN {
		return // only a plain "=" carries such a value here
	}
	var vals []Node
	for n := range it(op[1].ast) {
		if n.sym == Expression {
			vals = append(vals, n)
		}
	}
	if n, r, ok := e.frameRefIn(vals); ok {
		e.fail("%v: cannot store %s in package variable %s: its storage does not outlive the function",
			e.f.tok(n.Pos()).Position(), r.what, base)
	}
}

// checkBlockOutlives refuses storing a reference to an inner-block variable where
// the target outlives that block. It is checkStoreBacking's rule at BLOCK
// granularity rather than frame: the storage a reference points at must not die
// before the reference does, and a block is where that can happen without the
// function returning.
//
// This is what makes the loop-variable semantics Go adopted in 1.22 honest here. A
// per-iteration variable and a reused one differ only where a reference outlives the
// iteration, which is why Go's own compiler keeps one cell until it escapes -- and
// where it does escape, matching Go needs one cell per iteration, of a count not
// known until it runs. That is a heap, so it is refused, exactly as `new` and a map
// are refused. Every program this accepts means what Go means:
//
//	for i := 0; i < 3; i++ {
//		ps[i] = &i        // refused: ps outlives the iteration i belongs to
//		bump(&i)          // fine: the address does not outlive the call
//	}
//
// It is NOT only about loop variables, and describing it that way would have been
// wrong. `x := i * 10` in a loop BODY is a fresh variable per iteration in every
// version of Go, back to 1.0, and taking its address had the same defect. The rule
// is written about blocks so both fall out of it.
func (e *emitter) checkBlockOutlives(base string, op []Node) {
	if !e.isFrameVar(base) {
		return // a package target is checkStoreBacking's, and answers differently
	}
	if len(op) != 2 || op[0].sym != 0 || e.f.ch(op[0].tok) != ASSIGN {
		return // only a plain "=" stores such a value
	}
	target := e.blockDepthOf(base)
	for n := range it(op[1].ast) {
		if n.sym != Expression {
			continue
		}
		r, ok := e.frameRefOf(n.ast)
		if !ok || e.blockDepthOf(r.name) <= target {
			continue
		}
		e.fail("%v: cannot store %s in %s: %s does not outlive the block it is declared in, "+
			"and %s does; declare it where %s is",
			e.f.tok(n.Pos()).Position(), r.what, base, r.origin, base, base)
		return
	}
}

// crossBackedByFrame finds, among values about to cross to another cog, one that is
// a slice viewing this frame's storage, and names the local it came from.
//
// Where a return or a store into a package variable hands out a reference that
// provably outlives its referent, a crossing hands out one that leaves the frame's
// control: a goroutine runs until it returns, and a receiver keeps the header it
// took long after the rendezvous that delivered it. Either may read the backing
// array once this function has returned and its storage been reused. Callers report
// it themselves, each in its own words -- the two crossings dangle for different
// reasons and a programmer fixing one is served by hearing which.
//
// It sees the three shapes frameRefOf does: a slice viewing a local array, the
// address of a local, and a variable that was handed either -- a struct with such a
// field, or a copy of one.

// checkCrossArgs refuses an argument backed by this frame where the callee lets that
// parameter reach another cog. It is increment 3's rule applied one call further out:
// the crossing itself is in the callee, but the storage is chosen here, and here is
// the only place that knows where it came from.
//
// The diagnostic has to carry that, because the line it points at contains no `go`
// and no send -- so it names the callee and the parameter, which is what the reader
// needs to find the crossing for themselves.
// checkRecvLeak refuses an argument that reaches this frame's storage where the
// callee stores that parameter into its RECEIVER and the receiver outlives this
// frame. `h.set(a[:])` for a package-level h leaves a header over a dead frame in
// storage that survives the call; the same call on a LOCAL h is fine, the two dying
// together.
//
// Only the call site can tell those apart, which is why the summary carries leakRecv
// rather than folding it into leakGlobal: the callee stores into storage it did not
// choose and cannot see the lifetime of.
//
// A receiver that is a PARAMETER counts as outliving. isFrameVar says a parameter's
// own storage is this frame's -- true, and not the question here: what it POINTS AT
// belongs to the caller, so storing a reference to this frame through it is the same
// leak one level up.
func (e *emitter) checkRecvLeak(cname, recv string, args []Node) {
	if recv == "" {
		return
	}
	local := e.isFrameVar(recv) && !e.curParams[recv]
	crosses := e.crossParams[cname]
	for i, a := range args {
		if i >= len(crosses) || crosses[i]&leakRecv == 0 {
			continue
		}
		r, ok := e.frameRefOf(a.ast)
		if !ok {
			continue
		}
		// A local receiver dies with this frame, so the frame question is answered
		// -- but not the BLOCK question: a receiver declared outside the block the
		// reference points into still outlives it.
		outlives := "this function"
		if local {
			if e.blockDepthOf(r.name) <= e.blockDepthOf(recv) {
				continue
			}
			outlives = "the block " + r.origin + " is declared in"
		}
		e.fail("%v: cannot pass %s to %s: it is stored in the receiver %s, which outlives %s; %s",
			e.f.tok(a.Pos()).Position(), r.what, e.funcSourceName(cname), recv, outlives, r.advice())
		return
	}
}

// checkIntoArgs refuses an argument backed by this frame where the callee stores
// that parameter THROUGH another of its parameters, and the argument at that
// position outlives this frame. It is checkRecvLeak's rule for a plain function:
// `fill(&g, a[:])` leaves a header over a dead frame in a package variable, and
// the same call with a local in place of g is fine, the two dying together.
//
// Only a target this can positively identify as outliving answers -- a package
// variable, or a parameter, whose pointee belongs to the caller. An expression it
// cannot name is left alone rather than refused on suspicion: over-refusal here
// would fall on ordinary code that keeps nothing, and the shapes worth catching
// are the ones a reader would write.
func (e *emitter) checkIntoArgs(cname string, args []Node) {
	e.checkIntoArgsIn(e.crossInto[cname], e.funcSourceName(cname), args)
}

// checkIfaceArgs refuses an argument backed by this frame where the method might
// keep it. Which function a call through an interface reaches is the TABLE's answer
// at run time, so there is no callee to look a summary up by -- and nothing was
// asked at all, which made an interface the way to launder a reference past every
// rule that a direct call obeys.
//
// The answer is the UNION over every concrete type that implements the interface.
// It has to be: any of them may be the one behind the pointer, and the compiler that
// could say which is the devirtualizing pass that does not exist yet. So a leak in
// one implementation constrains the calls through the interface, which is the
// conservative direction and the same one the whole analysis takes.
//
// leakRecv becomes leakGlobal here. A store into the receiver is a leak to whoever
// owns it, and an interface value is a pointer to storage this cannot name -- the
// question the call site answers for `h.set(a[:])` has no answer when the receiver
// arrived inside an interface.
func (e *emitter) checkIfaceArgs(iface, method string, args []Node) {
	crosses, intos, any := e.ifaceCallSummary(iface, method)
	if !any {
		return
	}
	who := method + " (through " + e.goTypeName(iface) + ")"
	e.checkCrossArgsIn(crosses, who, args)
	e.checkIntoArgsIn(intos, who, args)
}

// ifaceCallSummary unions the crossing summaries of the method named, over every
// concrete type implementing the interface. any is false when no implementation is
// known, which consults nothing rather than guessing.
func (e *emitter) ifaceCallSummary(iface, method string) (crosses []leak, intos []uint32, any bool) {
	if s, ok := e.ifaceSummaries[iface+"."+method]; ok {
		return s.crosses, s.intos, s.any
	}
	for concrete := range e.typeNames {
		if e.isIfaceCType(concrete) || !e.implementsIface(concrete, iface) {
			continue
		}
		cname, _, _, ok := e.promotedMethod(concrete, method)
		if !ok {
			continue
		}
		for i, f := range e.crossParams[cname] {
			for len(crosses) <= i {
				crosses = append(crosses, 0)
			}
			if f&leakRecv != 0 {
				f = f&^leakRecv | leakGlobal
			}
			crosses[i] |= f
			any = true
		}
		for i, m := range e.crossInto[cname] {
			for len(intos) <= i {
				intos = append(intos, 0)
			}
			intos[i] |= m
			any = true
		}
	}
	// Memoised: the set of implementations cannot change once emission has begun,
	// and a hot interface is called from many sites.
	e.ifaceSummaries[iface+"."+method] = ifaceSummary{crosses: crosses, intos: intos, any: any}
	return crosses, intos, any
}

// ifaceSummary is one interface method's unioned escape summary (see
// ifaceCallSummary).
type ifaceSummary struct {
	crosses []leak
	intos   []uint32
	any     bool
}

// checkIntoArgsIn is checkIntoArgs against a summary handed in rather than looked
// up by name, for a call whose callee HAS no name here -- an interface method,
// which the table picks at run time.
func (e *emitter) checkIntoArgsIn(intos []uint32, who string, args []Node) {
	for i, a := range args {
		if i >= len(intos) || intos[i] == 0 {
			continue
		}
		r, ok := e.frameRefOf(a.ast)
		if !ok {
			continue
		}
		for j := 0; j < len(args) && j < intoBits; j++ {
			if intos[i]&(1<<j) == 0 {
				continue
			}
			tgt := e.crossRoot(args[j].ast)
			if tgt == "" {
				continue
			}
			// Outliving the FRAME is the first question, and outliving the block
			// the reference points into is the second: a callee that stores through
			// a pointer to an OUTER-block local keeps the reference past the block
			// it belongs to without the function ever returning.
			outlives := "this function"
			switch {
			case e.isPackageVar(tgt) || e.curParams[tgt]:
			case e.blockDepthOf(r.name) > e.blockDepthOf(tgt):
				outlives = "the block " + r.origin + " is declared in"
			default:
				continue
			}
			e.fail("%v: cannot pass %s to %s: it is stored through %s, which outlives %s; %s",
				e.f.tok(a.Pos()).Position(), r.what, who, tgt, outlives, r.advice())
			return
		}
	}
}

func (e *emitter) checkCrossArgs(cname string, args []Node, spread bool) {
	crosses := e.crossParams[cname]
	// The pack a variadic call builds is an array of THIS frame, so a callee that
	// lets its variadic parameter outlive the call is handed a reference that does
	// not. Go allocates the pack and has no such problem; here it is the rule a
	// slice literal already obeys, asked of an argument list the source did not
	// write as a slice at all. A spread passes an existing slice instead, and is
	// judged by where THAT came from, in the loop below.
	// leakRecv is masked out of both tests below: whether a store into the RECEIVER
	// outlives this frame is checkRecvLeak's question, and it has the receiver.
	// Letting it through here refused `h.set(a[:])` for a LOCAL h -- safe, the two
	// dying together -- and said "stored where it outlives every frame" of a store
	// into a struct that does not.
	if _, at := e.variadicPack(cname); !spread && at >= 0 && at < len(crosses) &&
		crosses[at]&(leakCog|leakGlobal) != 0 {
		why := "is stored where it outlives every frame"
		if crosses[at]&leakCog != 0 {
			why = "reaches another cog, which may outlive this function"
		}
		pos := e.f.tok(args[0].Pos()).Position()
		if at < len(args) {
			pos = e.f.tok(args[at].Pos()).Position()
		}
		e.fail("%v: cannot pass these values to %s: they are packed into an array of this function, and its parameter %d %s; pack them into a package array and pass a slice of it",
			pos, e.funcSourceName(cname), at+1, why)
		return
	}
	e.checkCrossArgsIn(crosses, e.funcSourceName(cname), args)
}

// checkCrossArgsIn is checkCrossArgs' per-argument half against a summary handed in
// rather than looked up by name. An interface method call has no name to look up:
// which function runs is the table's answer, at run time.
func (e *emitter) checkCrossArgsIn(crosses []leak, who string, args []Node) {
	for i, a := range args {
		if i >= len(crosses) || crosses[i]&(leakCog|leakGlobal) == 0 {
			continue
		}
		r, ok := e.frameRefOf(a.ast)
		if !ok {
			continue
		}
		why := "is stored where it outlives every frame"
		if crosses[i]&leakCog != 0 {
			why = "reaches another cog, which may outlive this function"
		}
		e.fail("%v: cannot pass %s to %s: its parameter %d %s; %s",
			e.f.tok(a.Pos()).Position(), r.what, who, i+1, why, r.advice())
		return
	}
}

// bindFuncValue records which function a variable holds, when the initializer or
// right-hand side names one outright -- `f := keep`, `var f Fn = keep`, `f = other`.
// A call through the variable is then judged by that function's escape summaries,
// which is the only way the requirement reaches a call site naming a variable.
//
// Anything else CLEARS the binding rather than leaving a stale one: a variable
// reassigned from a parameter, a field or another variable holds a function this
// cannot name, and answering with the previous one would be worse than not knowing.
// funcFieldKey names a function value held in a struct FIELD for funcValueOf, whose
// other keys are plain variable names. A field path cannot collide with one: an
// identifier has no dot in it.
func funcFieldKey(base, field string) string { return base + "." + field }

// bindLitFuncFields binds the function values a struct LITERAL puts into func-typed
// fields, `b := B{run: keep}`. It is the literal counterpart of the assignment form,
// and without it the same callback bound the other way consulted no summaries at
// all -- so handing it a local's address was accepted where `b.run = keep` was not.
func (e *emitter) bindLitFuncFields(varName string, initExpr []int32) {
	nm, lit, isLit := e.soleCompositeLit(initExpr)
	if !isLit {
		return
	}
	values, fields, ok := e.litFieldValues(nm, lit)
	if !ok {
		return
	}
	for i, f := range fields {
		if i >= len(values) || values[i] == nil || !e.isFuncCType(f.ctype) {
			continue
		}
		e.bindFuncValue(funcFieldKey(varName, f.name), values[i].ast)
	}
}

func (e *emitter) bindFuncValue(name string, initExpr []int32) {
	if fn, ok := e.exprIdent(initExpr); ok {
		if _, isFunc := e.userFunc(fn); isFunc {
			e.funcValueOf[name] = e.funcCallC(fn)
			return
		}
	}
	delete(e.funcValueOf, name)
}

// calleeSummaryName is the C name whose summaries a call through recv should be
// judged by. It is recv's own mangled name for a declared function, and the bound
// function for a variable holding one; a variable holding a function this cannot
// name yields "", which consults nothing and accepts, as the rest of the analysis
// does with what it cannot see.
func (e *emitter) calleeSummaryName(recv string) string {
	if ct, ok := e.varType(recv); ok && e.isFuncCType(ct) {
		return e.funcValueOf[recv]
	}
	return e.funcCallC(recv)
}

// isFrameVar reports whether a name is a variable of the frame being emitted, as
// opposed to a package-level one. A parameter counts: its own storage is this
// frame's, whatever it may point at.
//
// It asks BOTH local environments. A local array is in arrays and nowhere else, so a
// one-map question answered "no" for it -- and "return &a[i]" over a local array went
// unrefused, which is the shape the whole rule exists to stop. The same split has
// caught isPackageVar before.
// visibleLocals is the set of local names in scope right now, whatever environment
// the emitter keeps each kind in.
func (e *emitter) visibleLocals() map[string]bool {
	out := make(map[string]bool, len(e.locals)+len(e.arrays))
	for n := range e.locals {
		out[n] = true
	}
	for n := range e.arrays {
		out[n] = true
	}
	return out
}

// blockDepthOf says how deeply nested the block declaring name is: 0 for a
// parameter, 1 for a local of the function body, one more for each block inside it.
// A name that names nothing local -- a package variable -- is 0, outliving them all.
//
// It is counted rather than recorded, from the snapshots enterScope keeps: a
// variable declared in block k is absent from the snapshots of every block from the
// outermost through k, and present in the ones taken after. That needs no
// cooperation from the many places a local is registered, which is what makes it
// reliable -- a declaration form nobody thought of here still gets a depth.
//
// An empty name is the storage the emitter MINTS for a value with no variable of its
// own -- a slice literal's backing, the temporary behind `&T{...}` -- which belongs
// to the block being emitted, so it answers with the current depth.
func (e *emitter) blockDepthOf(name string) int {
	if name == "" {
		return len(e.scopeNames)
	}
	if !e.isFrameVar(name) {
		return 0
	}
	d := 0
	for _, seen := range e.scopeNames {
		if !seen[name] {
			d++
		}
	}
	return d
}

func (e *emitter) isFrameVar(name string) bool {
	if _, local := e.locals[name]; local {
		return true
	}
	_, arr := e.arrays[name]
	return arr
}

// funcSourceName gives the name a function was declared with, for a diagnostic that
// should read the way the program was written rather than the way it was mangled. It
// is unqualified even for a function from another package: the position the
// diagnostic carries points at the call, where the qualifier is already in view.
func (e *emitter) funcSourceName(cname string) string {
	if name := e.crossNames[cname]; name != "" {
		return name
	}
	return cname
}

// localChanCell gives a locally declared channel its cell, and returns the cell's
// C name. The cell is a file-scope static, one per declaration site, initialised
// once at package init.
//
// It used to be an ordinary local, which put a channel's rendezvous state on the
// declaring function's stack. Two things followed. Passing such a channel to a
// goroutine -- `var ch chan int; go worker(ch)`, the ordinary way to write this --
// handed another cog a pointer into a frame that the spawner was free to leave,
// after which the goroutine's sends wrote over whatever reused that stack. And the
// lock was acquired on every call and never released, so a function declaring a
// channel could be called about fifteen times before the P2 ran out of locks.
//
// A static cell fixes both: the storage outlives every frame, and the lock is taken
// once. The cost is that the cell belongs to the *site*, not to the call, so two
// concurrent calls of the same function share one channel rather than getting one
// each -- which is the trade the no-heap model asks for, and is why the cell can be
// bounded at all: the P2 has 16 hardware locks, so a program cannot have more live
// channels than sites anyway.
func (e *emitter) localChanCell(elem string) string {
	cell := fmt.Sprintf("ogo_chan_cell_%d", e.chanCellN)
	e.chanCellN++
	e.chanCells = append(e.chanCells, "static "+chanCellCName(elem)+" "+cell+";")
	e.chanInitElems[elem] = true
	e.deferPkgInit(chanInitCName(elem) + "(&" + cell + ");")
	return cell
}
