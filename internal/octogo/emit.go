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

// cReserved is the set of names the emitted C has already spoken for: C's own
// keywords, and the library functions and macros declared by the headers the output
// includes -- <stdio.h>, <stdlib.h>, <string.h> and propeller2.h. A user symbol of
// one of these names is emitted with the ogo_ prefix instead.
//
// It does not need to be exhaustive to be worth having: it covers what a program is
// plausibly going to name, and a name missing from it fails the way it does today,
// as a C compile error naming the collision.
var cReserved = map[string]bool{}

func init() {
	for _, name := range []string{
		// C keywords that are valid OctoGo identifiers. The ones that are also C
		// TYPE names -- int, char, float, long and the rest -- are left out on
		// purpose: the emitter writes them as types itself, and every identifier it
		// emits comes through here.
		"auto", "enum", "extern", "register", "sizeof", "static", "typedef",
		"union", "volatile", "inline", "restrict",
		// <stdio.h>
		"printf", "fprintf", "sprintf", "snprintf", "puts", "putchar", "getchar",
		"fopen", "fclose", "fread", "fwrite", "fseek", "ftell", "rewind", "remove",
		"rename", "perror", "stdin", "stdout", "stderr", "EOF",
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
			return "ogo_" + cIdent(name)
		}
		return cIdent(name)
	}
	return prefix + "_" + cIdent(name)
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
	"AckPin":       {"_akpin", ""},
	"ReadPin":      {"_rdpin", "unsigned"},
	"WritePinMode": {"_wrpin", ""},
	"WritePinX":    {"_wxpin", ""},
	"WritePinY":    {"_wypin", ""},
	"GetCt":        {"_cnt", "unsigned"},
	"GetMs":        {"_getms", "unsigned"},
	"GetSec":       {"_getsec", "unsigned"},
	"Rnd":          {"_rnd", "unsigned"},
	"Rev":          {"_rev", "unsigned"},
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

// cTypes maps predeclared OctoGo type names to C types. int is int32 in OctoGo
// and the P2's C int is 32-bit, so int maps to plain int. Fixed-width names use
// <stdint.h> (see stdintType).
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

// stringHelpers print a string header's exact bytes. A string is not
// null-terminated, so %s is wrong; and the target's printf TRUNCATES "%.*s" at 62
// characters -- silently, so a 63-character line printed 62 of it and nothing said
// so. The bytes go out one at a time instead, which is exact at any length and
// costs nothing next to a serial line.
const stringHelpers = "static inline void ogo_print_str(ogo_string s) { for (int _i = 0; _i < s.len; _i++) { putchar(s.str[_i]); } }\n" +
	"static inline void ogo_println_str(ogo_string s) { ogo_print_str(s); putchar('\\n'); }\n"

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
	base, ok = e.exprIdent(ast)
	if !ok {
		return "", "", false
	}
	ct, ok := e.varType(base)
	if !ok || !e.isChanCType(ct) {
		return "", "", false
	}
	return e.chanElemByName[ct], base, true
}

// goSite is one `go` statement: the callee's C name and the C types of its
// arguments. Each gets a struct to marshal the arguments through and a trampoline
// matching _cogstart's `void (*)(void *)` signature.
type goSite struct {
	callee string
	args   []string
	id     int
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
	var head Node
	var suffix []Node
	for _, n := range nodes {
		switch n.sym {
		case AssignHead:
			head = n
		case Selector, Index, CallSuffix:
			suffix = append(suffix, n)
		}
	}
	base := e.soleIdent(head.ast)
	crossed := func(what string, at Node) {
		e.fail("%v: cannot pass %s to a goroutine: its storage does not outlive the function, and the "+
			"goroutine may; declare the backing array at package scope",
			e.f.tok(at.Pos()).Position(), what)
	}
	// `go x.M(args)` is `go f(args)` with the receiver in front: the trampoline's
	// struct carries it like any other argument, so the cog calls <T>_M(recv, ...)
	// with a receiver evaluated here, at the `go` statement, as Go evaluates it.
	var site goSite
	var callSuffix Node
	var recvText, recvCType string
	switch {
	case base != "" && len(suffix) == 1 && suffix[0].sym == CallSuffix:
		if _, ok := e.userFunc(base); !ok {
			e.fail("only `go f(args)` on a package function is supported yet")
			return
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
			if !ok || !e.isUserType(methodBaseType(rct)) {
				e.fail("unsupported receiver in a go statement")
				return
			}
			cname := methodCName(methodBaseType(rct), name)
			wantPtr := e.methodPtr[cname]
			if r, bad := e.receiverFrameRef(base, wantPtr); bad {
				crossed(r.what, head)
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
		rct, isVar := e.varType(base)
		// A variable of a user type is a method call; an import qualifier is a
		// function of that package. The variable is asked about first, since a local
		// of the qualifier's name shadows the import, as it does at an ordinary call.
		if !isVar || !e.isUserType(methodBaseType(rct)) {
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
			crossed(r.what, head)
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
		crossed(r.what, x)
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
	first := 0
	if recvText != "" {
		e.ind()
		e.emit(ap + "->a0 = " + recvText + ";\n")
		first = 1
	}
	for i, a := range args {
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
		fmt.Fprintf(&tramps, "static void %s(void* p) {\n\t%s* a = p;\n\t%s(",
			goTrampolineCName(s.id), goArgsCName(s.id), s.callee)
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
		e.ind()
		e.emit(send.elem + " " + valTmp + " = ")
		e.emitExpr(send.val.ast)
		e.emit(";\n")
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
		e.emit(c.elem + " " + tmps[i] + ";\n")
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
		guard := ""
		if tryRecv != "" {
			guard = tryRecv + " && "
		}
		e.emit("if (" + guard + chanTryRecvCName(c.elem) + "(" + c.ch + ", &" + tmp + ")) {\n")
		e.indent++
		if !hasDefault {
			e.ind()
			e.emit(done + " = 1;\n") // set before the body, so a break in it is the user's
		}
		if c.target.name != "" {
			// The same store a multiple assignment writes, so a clause may receive
			// into a field or an element -- `case b.v = <-ch:` -- as the plain
			// assignment `b.v = <-ch` always could.
			e.emitStore(c.target, c.declare, c.elem, tmp)
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
	if len(chain) != 0 {
		e.fail("a select send clause needs a plain channel operand")
		return false
	}
	c.send, c.val = true, value
	return e.selectChan(head, c)
}

// selectChan resolves the channel a clause polls.
func (e *emitter) selectChan(n Node, c *selectCase) bool {
	base, ok := e.exprIdent(n.ast)
	if !ok {
		e.fail("a select clause needs a plain channel operand")
		return false
	}
	ct, ok := e.varType(base)
	if !ok || !e.isChanCType(ct) {
		e.fail("a select clause needs a channel operand")
		return false
	}
	c.ch, c.elem = base, e.chanElemByName[ct]
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
	if e.isStruct(ctype) || ctype == cString || e.isSliceCType(ctype) || ctype == "ogo_builder" {
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
		e.ind()
		e.emit(ctype + " " + tmp + " = ")
		e.emitCompositeLit(name, lit, true)
		e.emit(";\n")
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
	if _, _, isCall := e.factorCall(kids); !isCall {
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
func (e *emitter) pkgInitAssign(target string, initExpr []int32) {
	saved := e.prologue
	e.prologue = nil
	text := e.exprC(initExpr)
	pro := e.prologue
	e.prologue = saved
	step := pkgInitStep{target: target, deps: e.globalRefs(initExpr)}
	for _, line := range pro {
		step.stmts = append(step.stmts, strings.TrimSuffix(line, "\n"))
	}
	step.stmts = append(step.stmts, target+" = "+text+";")
	e.pkgInit = append(e.pkgInit, step)
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
			if n.sym != 0 {
				walk(n.ast)
				continue
			}
			if e.f.ch(n.tok) != IDENT {
				continue
			}
			if gn := e.globalC(e.src(n.tok)); !slices.Contains(out, gn) {
				out = append(out, gn)
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
	for _, i := range stableTopoOrder(names, deps) {
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
	if len(fv.res) > 1 {
		e.fail("a function with more than one result cannot be used as a value yet")
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
	paramTypes := e.cParamTypes(sig)
	params := strings.Join(paramTypes, ", ")
	if params == "" {
		params = "void"
	}
	ret := "void"
	if len(resTypes) == 1 {
		ret = resTypes[0]
	}
	return funcValueType{key: ret + " (*)(" + params + ")", res: resTypes, params: paramTypes}
}

// funcTypeOfSig mints (or reuses) the typedef standing for a Signature, shared by
// the written type `func(...)...` and by a function name used as a value, so the
// two agree by construction.
func (e *emitter) funcTypeOfSig(sig []int32) (string, bool) {
	fv := e.funcSigCParts(sig)
	if len(fv.res) > 1 {
		// A multi-result function returns a struct named after the function, not
		// after its signature, so two functions of one type would return two
		// different C types. Reconciling that needs a per-signature result struct;
		// until then, say so rather than emit a mismatch.
		e.fail("a function with more than one result cannot be used as a value yet")
		return "", false
	}
	return e.funcTypeFor(fv), true
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
	// "ret (*)(params)" -> "typedef ret (*name)(params);"
	e.addTypedef(name, "typedef "+strings.Replace(fv.key, "(*)", "(*"+name+")", 1)+";\n",
		append(slices.Clone(fv.res), fv.params...)...)
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
			return append(out, deferred...) // a cycle
		}
		rest = deferred
	}
	return out
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
	e.chanElems[elem] = true
	e.chanElemByName[chanCName(elem)] = elem
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
func (e *emitter) chanRuntimeDefs(elem string) string {
	c, snd, rcv, ini := chanCName(elem), chanSendCName(elem), chanRecvCName(elem), chanInitCName(elem)
	var b strings.Builder
	fmt.Fprintf(&b, `typedef struct { int lock; volatile int full; volatile int taken; volatile %[2]s val; } %[6]s;
typedef %[6]s* %[1]s;
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
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
	if e.chanSendElems[elem] {
		fmt.Fprintf(&b, `static void %[3]s(%[1]s ch, %[2]s v) {
	int mine = 0; // always set below before the rendezvous loop reads it; the
	// initializer only quiets flexcc, whose flow analysis cannot prove the first
	// loop exits solely through the break that follows the assignment.
	while (1) { // wait for the cell to be free, then deposit
		if (!ch->full && _locktry(ch->lock)) {
			if (!ch->full) {
				mine = ch->taken;
				ch->val = v;
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
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
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
		fmt.Fprintf(&b, `static int ogo_chan_offer_%[7]s(%[1]s ch, %[2]s v, int* mine) {
	if (!ch->full && _locktry(ch->lock)) {
		if (!ch->full) {
			*mine = ch->taken;
			ch->val = v;
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
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	if e.chanTryRecvElems[elem] {
		fmt.Fprintf(&b, `static int ogo_chan_tryrecv_%[7]s(%[1]s ch, %[2]s* out) {
	if (ch->full && _locktry(ch->lock)) {
		if (ch->full) {
			*out = ch->val;
			ch->full = 0;
			ch->taken++;
			_lockrel(ch->lock);
			return 1;
		}
		_lockrel(ch->lock);
	}
	return 0;
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	if e.chanRecvElems[elem] {
		fmt.Fprintf(&b, `static %[2]s %[4]s(%[1]s ch) {
	while (1) {
		if (ch->full && _locktry(ch->lock)) {
			if (ch->full) {
				%[2]s v = ch->val;
				ch->full = 0;
				ch->taken++;
				_lockrel(ch->lock);
				return v;
			}
			_lockrel(ch->lock);
		}
		_waitx(1);
	}
}
`, c, elem, snd, rcv, ini, chanCellCName(elem), sanitizeElem(elem))
	}
	return b.String()
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
		body += fmt.Sprintf("\tif (%s) return 0;\n\treturn (%s)((%s)v << n);\n", cmp, ctype, u)
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

// reachableFiles returns the files of the main package and every package reachable
// through its imports, in dependency order: a package's imports precede it, and the
// main package's files come last. Each package appears once. This flattens the whole
// program into the single C translation unit the emitter produces (all reachable
// packages are compiled together, matching the no-separate-compilation model). A p2
// import resolves to noPkg -- it has no .ogo source, only intrinsics -- and is
// skipped here, staying on the p2Intrinsics path.
func reachablePackages(main *Package) []*Package {
	var order []*Package
	seen := map[string]bool{}
	var visit func(p *Package)
	visit = func(p *Package) {
		if p == nil || p == noPkg || seen[p.ImportPath] {
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
	e := &emitter{includes: map[string]bool{}, funcRet: map[string][]string{}, funcSliceParams: map[string][]string{}, funcParams: map[string][]string{}, methodPtr: map[string]bool{}, globals: map[string]string{}, structs: map[string][]structField{}, namedTypes: map[string]bool{}, typeNames: map[string]bool{}, interfaceTypes: map[string]bool{}, ifaceMethods: map[string][]ifaceMethod{}, ifaceVTables: map[string]bool{}, namedUnderlying: map[string]string{}, namedArrays: map[string]arrDim{}, constInt: map[string]string{}, constStr: map[string]string{}, arrays: map[string]arrDim{}, globalArrays: map[string]arrDim{}, sliceVars: map[string]string{}, globalSliceVars: map[string]string{}, chanElems: map[string]bool{}, chanInitElems: map[string]bool{}, chanSendElems: map[string]bool{}, chanRecvElems: map[string]bool{}, chanTryRecvElems: map[string]bool{}, chanTrySendElems: map[string]bool{}, chanElemByName: map[string]string{}, sliceElems: map[string]bool{}, sliceElemByName: map[string]string{}, appendElems: map[string]bool{}, tryappendElems: map[string]bool{}, copyElems: map[string]bool{}, resliceElems: map[string]bool{}, reslice3Elems: map[string]bool{}, clearElems: map[string]bool{}, minElems: map[string]bool{}, maxElems: map[string]bool{}, printSliceElems: map[string]bool{}, printlnElems: map[string]bool{}, switchBreakUsed: map[string]bool{}, labelBreak: map[string]string{}, labelContinue: map[string]string{}, labelUsed: map[string]bool{}, eqStructs: map[string]bool{}, eqArrays: map[string]arrDim{}, frameBacked: map[string]bool{}, frameHolder: map[string]string{}, crossParams: map[string][]leak{}, retParams: map[string][]bool{}, funcValueOf: map[string]string{}, crossNames: map[string]string{}, initNames: map[string]string{}, funcValueTypes: map[string]funcValueType{}, funcTypeNames: map[string]string{}, funcTypeRet: map[string][]string{}, shiftHelpers: map[string][2]string{}, divHelpers: map[string][2]string{}, deferReplay: -1, iota: -1}
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
	// environment.
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
	for _, el := range sortedKeys(e.tryappendElems) {
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
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n"+
			"\tif (s.len >= s.cap) {\n\t\togo_panic(\"append: out of capacity\");\n\t} else {\n"+
			"\t\ts.ptr[s.len] = v;\n\t\ts.len++;\n\t}\n\treturn s;\n}\n",
			sliceCName(el), appendCName(el), sliceCName(el), el)
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
		fmt.Fprintf(&helperDefs, "static %s %s(%s s, %s v) {\n\t%s r;\n"+
			"\tif (s.len >= s.cap) {\n\t\tr.slice = s;\n\t\tr.ok = 0;\n\t} else {\n"+
			"\t\ts.ptr[s.len] = v;\n\t\ts.len++;\n\t\tr.slice = s;\n\t\tr.ok = 1;\n\t}\n\treturn r;\n}\n",
			appendokCName(el), tryappendCName(el), sliceCName(el), el, appendokCName(el))
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
	out.Write(body.Bytes())
	_, err := w.Write(out.Bytes())
	return err
}

type emitter struct {
	w io.Writer // body buffer during the walk
	f *File     // file currently being emitted, for token access
	// constPreScan runs emitConstDecl for its folded VALUES only -- no C emitted,
	// no types resolved. See collectConstValues.
	constPreScan bool
	// foldConv lets the integer fold see through a conversion, `int32(4)`. Off for
	// the fold that RENDERS an expression, where a conversion's cast is part of the
	// emitted type. See constIntValue.
	foldConv           bool
	indent             int
	includes           map[string]bool
	funcRet            map[string][]string      // user function / mangled method name -> C result types (empty=void), for typing calls
	funcSliceParams    map[string][]string      // same key -> per parameter, its C slice type or "", so a bare nil argument knows it is a slice header
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
	typedefUnits       []typedefUnit            // the typedef section, in the order collected; emitted in dependency order
	sliceElems         map[string]bool          // element C types that need an ogo_slice_<T> typedef
	sliceElemByName    map[string]string        // ogo_slice_<T> C type name -> its element C type; the forward direction mangles pointers, so the reverse is recorded, not derived
	appendElems        map[string]bool          // element C types needing the trapping ogo_append_<T> helper
	tryappendElems     map[string]bool          // element C types needing the ok-form ogo_tryappend_<T> helper + ogo_appendok_<T>
	copyElems          map[string]bool          // element C types needing the ogo_copy_<T> helper for the copy builtin
	resliceElems       map[string]bool          // element C types needing the ogo_reslice_<T> helper, a bounds-checked slice expression
	reslice3Elems      map[string]bool          // element C types needing its three-bound twin, ogo_reslice3_<T>
	usesResliceStr     bool                     // a string is sliced through the helper: emit ogo_reslice_str
	resliceCalled      bool                     // a reslice helper call was just emitted, so a field read off it needs a temporary (see emitHeaderField)
	usesCopyStr        bool                     // copy(dst []byte, src string) is used: emit the ogo_copystr helper
	usesBuilder        bool                     // the Builder type is used: emit its typedef and method helpers
	importQualifiers   map[string]string        // import qualifier -> the imported package's C symbol prefix (resolved user packages, not p2)
	curPkgPrefix       string                   // the C symbol prefix of the package whose file is currently being emitted ("" for main)
	clearElems         map[string]bool          // element C types needing the ogo_clear_<T> helper for the clear builtin
	minElems           map[string]bool          // C types needing the ogo_min_<T> helper for the min builtin
	maxElems           map[string]bool          // C types needing the ogo_max_<T> helper for the max builtin
	printSliceElems    map[string]bool          // element C types printed without a newline, needing the ogo_print_slice_<T> helper
	printlnElems       map[string]bool          // element C types printed with a newline, needing ogo_println_slice_<T> (which calls ogo_print_slice_<T>)
	defers             []deferredCall           // the current function's top-level defers, in source order, replayed LIFO before each return
	switchBreak        string                   // goto target for a break in the current switch case (the if/else lowering has no C switch to break); "" means a plain C break -- a loop, or outside any switch
	switchBreakSeq     int                      // counter minting unique switch-end labels
	switchBreakUsed    map[string]bool          // switch-end labels a break actually jumped to, so an unreferenced label is not emitted
	labelBreak         map[string]string        // source label -> C break-target label, for "break L" (a labeled for or switch)
	labelContinue      map[string]string        // source label -> C continue-target label, for "continue L" (a labeled for)
	labelUsed          map[string]bool          // C labels a labeled break/continue jumped to, so an unreferenced one is not emitted
	labelSeq           int                      // counter minting unique labeled-loop break/continue labels
	pendingContLabel   string                   // the current labeled for's C continue target, for emitLoopBody to place at the body's end
	pendingSwitchLabel string                   // the source label of a labeled switch, for emitSwitch to bind to its end label
	deferBlockDepth    int                      // nesting inside if/for/switch bodies; a defer at depth > 0 needs a runtime flag
	deferReplay        int                      // slot being replayed, or -1: makes emitCallArgs read the captured temporaries
	iota               int                      // the current iota value while emitting a const spec's expression, or -1 outside one
	deferReplayArgs    []deferArg               // that slot's arguments, so emitCallArgs knows which were captured
	usesPanic          bool                     // ogo_panic is called: emit its definition and pull in its includes
	usesBound          bool                     // ogo_bound is called: emit the index bounds-check helper
	usesNonzero        bool                     // ogo_nonzero is called: emit the divide-by-zero-check helper
	usesNonzero64      bool                     // ogo_nonzero64 (64-bit divisor guard) is called
	shiftHelpers       map[string][2]string     // guarded shift helper name -> {operator, value C type}
	divHelpers         map[string][2]string     // guarded signed division helper name -> {operator, value C type}
	release            bool                     // release build: a panic reboots (_reboot) instead of halting the cog
	checks             bool                     // emit runtime bounds / divide-by-zero checks (set by Checked; ogo build enables it by default)
	locals             map[string]string        // current function's parameter/local name -> C type, for typing `x := y`
	curFunc            string                   // name of the function whose body is being emitted (for its result-struct type)
	pkgScope           bool                     // a package variable's initializer is being emitted, where this frame's storage does not exist
	curResultNames     []string                 // current function's result C-variable names, for a bare "return" (naked return)
	curResultTypes     []string                 // current function's result C types, for typing a `return nil` in a slice-returning function
	tmp                int                      // per-function counter for generated temporaries (destructuring)
	makeN              int                      // translation-unit counter for make() backing arrays
	wroteDecl          bool                     // a top-level definition has been emitted (drives blank-line separators)
	mainRet            bool                     // currently emitting main's body: a bare `return` yields `return 0;`
	declInit           bool                     // emitting a static initializer: a string literal must use a brace, not a compound literal
	usesString         bool                     // an ogo_string type/literal appears: emit stringTypedef
	usesStringPrint    bool                     // a string is printed: emit stringHelpers
	usesStringEq       bool                     // a string == / != appears: emit ogo_string_eq
	eqStructs          map[string]bool          // struct C types compared with == / !=: emit an ogo_eq_<T> helper
	eqArrays           map[string]arrDim        // array types compared with == / !=, keyed by helper name: emit an ogo_eq_arr_<...> helper
	prologue           []string                 // lines to emit before the statement being emitted, for a temporary an expression needs hoisted out of itself (see emitStatement)
	frameBacked        map[string]bool          // local slice variables whose backing array is storage of this frame, so returning one would dangle (see checkReturnBacking)
	crossParams        map[string][]leak        // per function, how each parameter lets a value escape the caller's frame -- a cog crossing or a store that outlives it, directly or through a call (see collectCrossParams)
	retParams          map[string][]bool        // per function, which parameters a RESULT derives from, so a reference handed back out is followed to the storage it came from (see frameRefOf)
	funcValueOf        map[string]string        // variable holding a function -> that function's C name, when it is known, so a call through the variable is judged by the callee's summaries (see bindFuncValue)
	crossEdges         []crossEdge              // call sites passing a parameter straight on, the graph closeCrossParams walks
	retEdges           []crossEdge              // returns of a call taking a parameter, the graph the result summary is closed over
	crossNames         map[string]string        // C function name -> the name it was declared with, for crossParams diagnostics
	frameHolder        map[string]string        // local -> the local whose storage it holds a reference to, a struct field having been given one (see noteFrameHolder)
	chanCells          []string                 // file-scope static cell declarations for locally declared channels, discovered while emitting bodies (see emitLocalChanCell)
	chanCellN          int                      // counter minting unique cell names, program-wide like makeN
	usesStringCmp      bool                     // a string < <= > >= appears: emit ogo_string_cmp
	usesRuneDecode     bool                     // `for i, c := range s` appears: emit ogo_decode_rune
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
}

// dims reports the number of dimensions.
func (a arrDim) dims() int { return 1 + len(a.inner) }

// bounds returns every extent, outermost first.
func (a arrDim) bounds() []string { return append([]string{a.bound}, a.inner...) }

// row is the array one index in: the element type of a [2][3]int is a [3]int.
// Only meaningful when dims() > 1.
func (a arrDim) row() arrDim { return arrDim{elem: a.elem, bound: a.inner[0], inner: a.inner[1:]} }

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
		if strings.HasPrefix(underlying, chanTypePrefix) {
			// A defined type over a channel takes no typedef of its own and is
			// answered for by the cell's name (see cType). The cell's typedef is
			// emitted with the channel helpers, after this section, so a typedef
			// naming it here would name a type C has not seen -- and the emitter
			// reaches a channel through its C type name everywhere (the cell, the
			// helpers, the element), so a second name for it would have to be
			// resolved at each of those. The type keeps its identity to the checker;
			// what it gives up is a method of its own, which is refused by name.
			continue
		}
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

// ifaceVTName names the vtable STRUCT of an interface -- the table's shape, one
// function pointer per method.
func ifaceVTName(iface string) string { return iface + "_vt" }

// ifaceVTVar names the static vtable of a concrete type viewed as an interface, and
// ifaceThunkName the function that adapts one of its methods to the slot's
// signature. Both are keyed by the pair, since the same method may fill a slot in
// more than one interface and each slot has its own position.
func ifaceVTVar(iface, concrete string) string { return iface + "_vt_" + concrete }

func ifaceThunkName(iface, concrete, method string) string {
	return iface + "_" + concrete + "_" + cIdent(method)
}

// collectInterfaceType records an interface's methods in declaration order and
// emits the two typedefs it becomes: the value -- a data pointer beside a pointer
// to a table -- and the table's shape. The value is what a variable of the type is,
// and the table is what tells a call which function to make.
func (e *emitter) collectInterfaceType(mn string, structAST []int32) {
	var methods []ifaceMethod
	for n := range it(structAST) {
		if n.sym != MethodSpec {
			continue
		}
		name := e.soleIdent(n.ast)
		if name == "" {
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
			return
		}
		methods = append(methods, ifaceMethod{name: name, res: res, params: e.cParamTypes(n.ast)})
	}
	e.ifaceMethods[mn] = methods

	vt := ifaceVTName(mn)
	var b strings.Builder
	fmt.Fprintf(&b, "struct %s {", vt)
	for _, m := range methods {
		fmt.Fprintf(&b, " %s (*%s)(void*", m.res, cIdent(m.name))
		for _, p := range m.params {
			b.WriteString(", " + p)
		}
		b.WriteString(");")
	}
	if len(methods) == 0 {
		b.WriteString(" char _ogo_empty;") // C rejects a struct with no members
	}
	b.WriteString(" };\n")
	var deps []string
	for _, m := range methods {
		deps = append(deps, m.res)
		deps = append(deps, m.params...)
	}
	e.addTypedef(vt, b.String(), deps...)
	e.addTypedef(mn, "struct "+mn+" { void* data; const "+vt+"* vt; };\n", vt+"*")
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
	concrete, data, temp, ok := e.ifaceOperand(rhs)
	if !ok {
		if e.pkgScope {
			// A frame temporary is what gives an addressless value storage, and a
			// package variable's initializer has no frame to put one in.
			e.fail("a package variable of interface type needs a variable to point at: assign the value to one first")
			return ""
		}
		e.fail("only a variable, or the address of one, may be stored in an interface yet")
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
// `x` and `&x` name the same storage and differ only in which method set satisfies
// the interface -- a question the checker has already settled -- so both arrive
// here as the address of the variable, which is what the table's thunks unpack.
func (e *emitter) ifaceOperand(rhs []int32) (concrete, data string, temp, ok bool) {
	name, isName := e.exprIdent(rhs)
	if !isName {
		if root, isAddr := e.addrOfRoot(rhs); isAddr {
			name, isName = root, true
		}
	}
	if !isName {
		return e.ifaceTempOperand(rhs)
	}
	ct, ok := e.varType(name)
	if !ok {
		return e.ifaceTempOperand(rhs)
	}
	if e.isPointer(ct) {
		// A variable already of pointer type points at the value itself.
		return e.elemType(ct), e.varRef(name), false, true
	}
	return ct, "&" + e.varRef(name), false, true
}

// ifaceTempOperand gives storage to a value that has none of its own -- a composite
// literal, a call's result -- so an interface has an address to point at. It is a
// temporary of this frame, which is exactly what a local is, so the lifetime rules
// treat it as one: an interface made from it may not outlive the frame.
//
// A package variable's initializer has no frame to put one in (its temporaries are
// locals of ogo_pkg_init), so there the value is refused rather than pointed at.
func (e *emitter) ifaceTempOperand(rhs []int32) (concrete, data string, temp, ok bool) {
	if e.pkgScope {
		return "", "", false, false
	}
	ct, ok := e.inferCType(rhs)
	if !ok || ct == "" || e.isIfaceCType(ct) || !(e.isStruct(ct) || e.isUserType(ct)) {
		return "", "", false, false
	}
	return ct, "&" + e.hoist(ct, func() { e.emitExpr(rhs) }), true, true
}

// ifaceValueC renders a concrete value as an interface value, for the positions
// that need one expression rather than two statements: an argument, and a return.
// An operand that is already of the interface type is itself.
func (e *emitter) ifaceValueC(iface string, rhs []int32) (string, bool) {
	// Already an interface value -- a variable of one, or a call returning one --
	// so it is itself: the two words, copied.
	if ct, ok := e.inferCType(rhs); ok && ct == iface {
		return e.captureC(func() { e.emitExpr(rhs) }), true
	}
	concrete, data, _, ok := e.ifaceOperand(rhs)
	if !ok || !e.needVTable(iface, concrete) {
		return "", false
	}
	return "(" + iface + "){" + data + ", &" + ifaceVTVar(iface, concrete) + "}", true
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
		cname := methodCName(concrete, m.name)
		if _, has := e.funcRet[cname]; !has {
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
		b.WriteString(") { ")
		if m.res != "void" {
			b.WriteString("return ")
		}
		b.WriteString(cname + "(" + strings.Join(append([]string{recv}, args...), ", ") + "); }\n")
	}
	fmt.Fprintf(&b, "static const %s %s = {", ifaceVTName(iface), ifaceVTVar(iface, concrete))
	for i, m := range methods {
		if i != 0 {
			b.WriteString(",")
		}
		b.WriteString(" " + ifaceThunkName(iface, concrete, m.name))
	}
	if len(methods) == 0 {
		b.WriteString(" 0")
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
		var dim arrDim // dim.bound non-empty for a fixed-size array field
		for c := range it(n.ast) {
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
		if ctype == "" || len(names) == 0 {
			e.fail("embedded or untyped struct fields are not supported yet")
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
				e.emitArrayLitVar(e.globalC(names[0]), litType, lit, true)
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
			e.pkgInitAssign(gn, initExpr)
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
					e.fail("a package array initializer must be an array literal")
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
				defer e.pkgInitAssign(gn, initExpr)
			}
			e.emit(";\n")
			if e.isChanCType(ctype) {
				// The cell is a file-scope object like the variable pointing at it;
				// acquiring its lock is a call, so it waits for package init.
				elem := e.chanElemByName[ctype]
				cell := gn + "_cell"
				e.emit("static " + chanCellCName(elem) + " " + cell + ";\n")
				e.deferPkgInit(gn + " = &" + cell + ";")
				e.chanInitElems[elem] = true
				e.deferPkgInit(chanInitCName(elem) + "(" + gn + ");")
			}
		}
	}
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
		if recv == nil {
			// Kept so the name used as a value can mint its function type on demand.
			// Minting here instead would emit a typedef for every function in the
			// program, nearly all of them unused. Rendered now, while this package's
			// file is still the current one, since the use may be in another.
			e.funcValueTypes[cname] = e.funcSigCParts(sig)
		}
		e.funcSliceParams[cname] = e.paramSliceTypes(sig)
		e.funcParams[cname] = e.cParamTypes(sig)
		if len(resTypes) > 1 {
			text := e.captureC(func() {
				e.emit("typedef struct { ")
				for i, ct := range resTypes {
					fmt.Fprintf(e.w, "%s _%d; ", ct, i)
				}
				e.emit("} " + e.retStructName(cname) + ";\n")
			})
			// Each result is held by value, so every one of their typedefs comes first.
			e.addTypedef(e.retStructName(cname), text, resTypes...)
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
)

// crossEdge records that a call passes the caller's parameter `from` straight into
// the callee's parameter `to`, so whatever the callee does with it, the caller does.
type crossEdge struct {
	caller string
	from   int
	callee string
	to     int
}

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
		cname, srcName, params, body, ok := e.funcParamNames(d)
		if !ok {
			return
		}
		at := func(name string) int { return slices.Index(params, name) }
		if _, seen := e.crossParams[cname]; !seen {
			e.crossParams[cname] = make([]leak, len(params))
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
				for j, a := range c.args {
					if i := at(e.crossRoot(a.ast)); i >= 0 {
						e.crossEdges = append(e.crossEdges, crossEdge{caller: cname, from: i, callee: c.callee, to: j})
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
			if g.to >= len(callee) || g.from >= len(caller) || callee[g.to]&^caller[g.from] == 0 {
				continue
			}
			caller[g.from] |= callee[g.to]
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
// declared with, its parameter names in order, and its body. A method's receiver is
// not a parameter here: it is not one at the call sites this feeds, which name
// arguments positionally.
func (e *emitter) funcParamNames(d []int32) (cname, srcName string, params []string, body []int32, ok bool) {
	name, sig, body, recv, ok := e.funcParts(d)
	if !ok || name == "" || body == nil {
		return "", "", nil, nil, false
	}
	cname = mangle(e.curPkgPrefix, name)
	if recv != nil {
		_, rct, _ := e.receiverInfo(recv)
		cname = methodCName(methodBaseType(rct), name)
	}
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue // parameters are the only ParameterList; results are ResultList/Type
		}
		e.forEachParam(n.ast, func(nm string, _ []int32, _ bool) { params = append(params, nm) })
	}
	return cname, name, params, body, true
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

// storedInPackageVar returns the values a statement stores into a package variable,
// `g = v`, `g.f = v` or `g[i] = v`. The target's ROOT is what decides: a field or an
// element of a package variable outlives every frame exactly as the variable does.
//
// Only a plain "=" qualifies. A ":=" declares a local, and a compound assignment
// reads what is there rather than storing what it is given.
func (e *emitter) storedInPackageVar(nodes []Node) [][]int32 {
	if len(nodes) != 2 || nodes[0].sym != AssignHead || nodes[1].sym != Postfix {
		return nil
	}
	base := e.soleIdent(nodes[0].ast)
	if base == "" || !e.isPackageVar(base) {
		return nil
	}
	postfix := slices.Collect(it(nodes[1].ast))
	if len(postfix) == 0 || postfix[len(postfix)-1].sym != PostfixOp {
		return nil
	}
	op := slices.Collect(it(postfix[len(postfix)-1].ast))
	if len(op) != 2 || op[0].sym != 0 || e.f.ch(op[0].tok) != ASSIGN {
		return nil
	}
	var out [][]int32
	for n := range it(op[1].ast) {
		if n.sym == Expression {
			out = append(out, n.ast)
		}
	}
	return out
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
		e.forEachParam(n.ast, func(_ string, ta []int32, _ bool) {
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
func (e *emitter) retStructName(fn string) string { return "ogo_ret_" + fn }

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
		e.fail("func %q must have a body", name)
		return
	}

	if e.wroteDecl {
		e.emit("\n")
	}
	e.wroteDecl = true

	if recv == nil && name == "main" {
		e.emitMain(sig, body)
		return
	}
	e.locals = map[string]string{}
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
	if recv == nil {
		cname := e.funcDefCName(name, ast)
		proto = e.funcSignatureC(cname, sig)
		e.curFunc = cname
	} else {
		recvName, recvCType, recvNamed := e.receiverInfo(recv)
		cname := methodCName(methodBaseType(recvCType), name)
		proto = e.methodSignatureC(cname, recvName, recvCType, sig)
		e.curFunc = cname
		e.locals[recvName] = recvCType
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
	ctype = e.cType(d.TypeAST.ast)
	if e.isChanCType(methodBaseType(ctype)) {
		// A defined type over a channel is answered for by the channel cell's own C
		// name (see collectTypeDecl), so it has no C type of its own to hang a
		// method namespace on. Refused where the method is written rather than at
		// the call, where it reads as an unknown package.
		e.fail("a method on a defined type over a channel is not supported yet")
		return recvSynthName, "", false
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
func methodCName(baseType, method string) string { return baseType + "_" + cIdent(method) }

// emitMain emits `func main()` as `int main(void)`; main takes no parameters or
// results.
func (e *emitter) emitMain(sig, body []int32) {
	params, resTypes := e.cSig(sig)
	if params != "" || len(resTypes) != 0 {
		e.fail("func main must have no parameters or results")
		return
	}
	e.locals = map[string]string{}
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
	params, resTypes := e.cSig(sig)
	if params == "" {
		params = "void"
	}
	return e.cReturnType(name, resTypes) + " " + name + "(" + params + ")"
}

// methodSignatureC builds a method's C signature with the receiver as the leading
// parameter, e.g. `int Point_Sum(Point p)` or `void Point_Scale(Point* p, int f)`.
func (e *emitter) methodSignatureC(cname, recvName, recvCType string, sig []int32) string {
	params, resTypes := e.cSig(sig)
	recvParam := recvCType + " " + cIdent(recvName)
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
	e.forEachParam(ast, func(name string, ta []int32, _ bool) {
		if elem, _, ok := e.arrayType(ta); ok {
			out = append(out, elem+"* "+paramArgName(name))
			return
		}
		ct := e.cType(ta)
		e.refuseArrayStructABI(ct, "parameter "+name)
		out = append(out, ct+" "+cIdent(name)) // a parameter name may be Unicode
	})
	return out
}

// cParamTypes renders a Signature's parameters as C types alone, with no names.
// That is what a function-type typedef wants: the names are not part of the type,
// so writing them would make `func(a int)` and `func(b int)` mint two typedefs for
// what is one type.
func (e *emitter) cParamTypes(sig []int32) []string {
	var out []string
	for n := range it(sig) {
		if n.sym != ParameterList {
			continue
		}
		e.forEachParam(n.ast, func(name string, ta []int32, _ bool) {
			if elem, _, ok := e.arrayType(ta); ok {
				out = append(out, elem+"*")
				return
			}
			ct := e.cType(ta)
			e.refuseArrayStructABI(ct, "parameter "+name)
			out = append(out, ct)
		})
	}
	return out
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
		e.fail("cannot return an array by value; return a slice or a pointer to it")
		return ""
	}
	ct := e.cType(ta)
	e.refuseArrayStructABI(ct, "result")
	return ct
}

// forEachParam walks a ParameterList's `IdentifierList Type` groups, calling fn
// with each parameter's name and C type (a shared type "a, b int" yields two
// calls). It underlies both the C parameter rendering (cParamList) and the local
// type environment (bindParams).
func (e *emitter) forEachParam(ast []int32, fn func(name string, typeAST []int32, synthetic bool)) {
	i := 0
	for _, d := range e.f.paramDecls(ast) {
		if len(d.Names) == 0 {
			fn(unnamedParamName(i), d.TypeAST.ast, true)
			i++
			continue
		}
		for _, nm := range d.Names {
			name := nm.Src()
			if name == "_" {
				fn(unnamedParamName(i), d.TypeAST.ast, true)
			} else {
				fn(name, d.TypeAST.ast, false)
			}
			i++
		}
	}
}

// unnamedParamName is the synthetic C name of the i-th parameter when it is
// unnamed or blank ("_"). flexcc miscompiles a definition that leaves a parameter
// unnamed -- it drops that parameter's argument slot and shifts every following
// argument -- so each such parameter is given a name (and a "(void)" reference in
// the body, since the source never uses it, to stay -Wunused-parameter clean).
func unnamedParamName(i int) string { return "_ogo_unused" + strconv.Itoa(i) }

// paramArgName is the C name of a value-array parameter as it is received (a
// pointer), distinct from the local copy the body sees under the source name.
func paramArgName(name string) string { return "_ogo_" + cIdent(name) }

// bindParams records the current function's parameters in the local type
// environment, so a `x := p` short declaration can be typed from a parameter p. A
// fixed-array parameter is recorded as an array (its body sees a same-named local
// copy). It reads only the parameter list (before the signature's closing ")"),
// not the results.
func (e *emitter) bindParams(sig []int32) {
	seenRPar := false
	for n := range it(sig) {
		switch n.sym {
		case ParameterList:
			if !seenRPar {
				e.forEachParam(n.ast, func(name string, ta []int32, synthetic bool) {
					if synthetic {
						return // an unnamed parameter binds nothing; the body cannot name it
					}
					if elem, bound, ok := e.arrayType(ta); ok {
						e.arrays[name] = arrDim{elem: elem, bound: bound}
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
					if elem, bound, ok := e.arrayType(ta); ok {
						e.includes["string.h"] = true
						e.ind()
						e.emit(elem + " " + name + "[" + bound + "];\n")
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
					if _, _, ok := e.arrayType(ta); ok {
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
	locals, arrays, sliceVars := maps.Clone(e.locals), maps.Clone(e.arrays), maps.Clone(e.sliceVars)
	frameBacked, frameHolder := maps.Clone(e.frameBacked), maps.Clone(e.frameHolder)
	constInt, constStr := maps.Clone(e.constInt), maps.Clone(e.constStr)
	funcValueOf := maps.Clone(e.funcValueOf)
	return func() {
		e.locals, e.arrays, e.sliceVars = locals, arrays, sliceVars
		e.frameBacked, e.frameHolder = frameBacked, frameHolder
		e.constInt, e.constStr = constInt, constStr
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
		if lbl, ok := e.stmtLabelOperand(nodes); ok {
			target := e.labelContinue[lbl]
			e.emit("goto " + target + ";\n")
			e.labelUsed[target] = true
		} else {
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
			if initExpr != nil {
				e.emitVarDeclInit(ctype, nm, initExpr)
			} else {
				e.ind()
				e.emit(ctype + " " + nm + " = " + e.zeroInitC(ctype) + ";\n")
			}
			// A channel is storage, not a handle: the checker rejects make() for one
			// ("dynamic allocation not supported"), so the declaration is what
			// creates it. Acquiring the hardware lock here is what makes the cell
			// usable, and ties the lock's lifetime to the variable's.
			if e.isChanCType(ctype) {
				// The declaration owns the cell; the variable is a reference to it.
				e.ind()
				e.emit(nm + " = &" + e.localChanCell(e.chanElemByName[ctype]) + ";\n")
			}
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
		}
		e.emitLitElement(*v, expect, brace)
	}
	e.emit("}")
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
	if nm, sub, ok := e.soleCompositeLit(v.ast); brace && ok {
		if len(compositeLitElements(sub)) == 0 {
			e.emit(e.zeroBraceC(nm)) // "{0}" does not nest; see zeroBraceC
			return
		}
		e.emitCompositeLit(nm, sub, true)
		return
	}
	// A string is a { pointer, length } struct, so an element that is one is a
	// nested aggregate and takes braces here for the same reason a literal does.
	// Only a bare string literal qualifies: a call that returns one is an
	// expression, and bracing what it contains would not be C.
	if tok, ok := e.soleToken(v.ast); brace && ok && e.f.ch(tok) == STRING {
		saved := e.declInit
		e.declInit = true
		e.emitExpr(v.ast)
		e.declInit = saved
		return
	}
	e.emitExpr(v.ast)
}

// litKeyIndex evaluates an array or slice literal's element index -- the "2" in
// "[]int{2: 5}" -- to a non-negative integer. Only a constant is admitted: an
// integer literal or a name bound to an integer const, in any base normalizeIntLit
// produces. A non-constant, negative, or unparsable key yields ok=false.
func (e *emitter) litKeyIndex(keyAST []int32) (int, bool) {
	tok, ok := e.soleToken(keyAST)
	if !ok {
		return 0, false
	}
	var s string
	switch e.f.ch(tok) {
	case INT:
		s = normalizeIntLit(e.src(tok))
	case IDENT:
		v, ok := e.foldedInt(e.src(tok))
		if !ok {
			return 0, false
		}
		s = v
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(s, 0, 64)
	if err != nil || n < 0 {
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
	for i, v := range values {
		if i != 0 {
			e.emit(", ")
		}
		if v == nil {
			e.emit(e.zeroInitC(elemCType))
			continue
		}
		e.emitLitElement(*v, structField{ctype: elemCType}, true)
	}
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
	for i, v := range values {
		if i != 0 {
			e.emit(", ")
		}
		if v == nil {
			e.emit("{0}") // an index the literal skips: the whole row is zero
			continue
		}
		rowValues, ok := e.rowValues(*v, row)
		if !ok {
			return
		}
		e.emitArrayValues(rowValues, row)
	}
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
			e.fail("an element of a %s literal must itself be a literal", arrayTypeName(row))
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
		e.fail("too many values in %s literal: %s but the length is %s", arrayTypeName(row), countUnits(length, "value"), row.bound)
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
			e.fail("too many values in %s literal: %s but the length is %s", arrayTypeName(a), countUnits(length, "value"), a.bound)
			return
		}
		if static {
			e.globalArrays[name] = a
		} else {
			e.arrays[name] = a
		}
		lead()
		e.emit(a.elem + " " + name + a.declSuffix() + " = ")
		e.emitArrayValues(values, a)
		e.emit(";\n")
		return
	}
	elem, ok := e.litSliceType(typeAST)
	if !ok {
		e.fail("unsupported array or slice literal type")
		return
	}
	e.needSlice(elem)
	cname := sliceCName(elem)
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
	lead()
	e.emit(elem + " " + backing + "[" + n + "] = ")
	e.emitPositionalValues(values, elem)
	e.emit(";\n")
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
	e.fail("cannot use a %s literal as %s", e.litTypeName(litType), arrayTypeName(want))
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
		return arrayTypeName(a)
	}
	if elem, ok := e.sliceType(litType); ok {
		return "[]" + elem
	}
	return "array or slice"
}

// arrayTypeName spells an array type the way the source does, "[2][3]int", rather
// than the way C declares it, which puts the extents on the declarator.
func arrayTypeName(a arrDim) string {
	s := ""
	for _, b := range a.bounds() {
		s += "[" + b + "]"
	}
	return s + a.elem
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
func (e *emitter) emitVarInit(initExpr []int32) {
	if name, lit, ok := e.soleCompositeLit(initExpr); ok {
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
		e.pkgInitAssign(gn, inits[i])
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
	// An interface type written out. The checker admits it and its method set; the
	// representation is settled (a data pointer beside a static vtable, see
	// octogo.go) and not emitted yet. Said by name, because the tokens below hold no
	// identifier for the generic message to report and it would name the empty
	// string.

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
		if u := e.namedUnderlying[mn]; strings.HasPrefix(u, chanTypePrefix) {
			return u // see collectTypeDecl: a defined channel type is its cell
		}
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

// arrayConvOperand recognises a conversion to a defined ARRAY type, `row(a)`, and
// returns the operand. Such a conversion is the operand itself: the two types have
// one representation, so there is nothing to convert.
func (e *emitter) arrayConvOperand(ast []int32) ([]int32, bool) {
	recv, suffix, ok := e.directCall(ast)
	if !ok || len(suffix) != 1 || suffix[0].sym != CallSuffix {
		return nil, false
	}
	ct, isType := e.convType(recv)
	if !isType {
		return nil, false
	}
	if _, isArray := e.namedArrays[ct]; !isArray {
		return nil, false
	}
	args := e.callArgExprs(suffix[0].ast)
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
	src, ok := e.exprReprCType(arg.ast)
	if !ok || src != e.underlyingCType(ct) {
		if e.underlyingCType(ct) == cString {
			e.fail("a string conversion needs allocation, which the target does not have")
			return
		}
		e.fail("cannot convert to %s", ct)
		return
	}
	e.emitExpr(arg.ast)
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

// arrayType recognises a fixed-array type `[N]T`, returning the element C type and
// the C bound. A slice `[]T` (no bound) or a non-constant bound is not modelled.
func (e *emitter) arrayType(typeAST []int32) (elem, bound string, ok bool) {
	a, ok := e.arrayDim(typeAST)
	if !ok || a.dims() != 1 {
		return "", "", false // a multi-dimensional array has no single bound
	}
	return a.elem, a.bound, true
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
		a, ok := e.namedArrays[mangle(e.curPkgPrefix, e.src(nodes[0].tok))]
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
		return arrDim{elem: inner.elem, bound: bound, inner: inner.bounds()}, true
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
	if elem = e.cType(elemAST); elem == "" {
		return "", false
	}
	return elem, true
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
}

// accessBase resolves the start of a chain: a slice variable, an array variable,
// or a plain local/global.
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
	if a, ok := e.arrayVar(base); ok {
		return accessCur{elem: a.elem, dims: a.bounds()}, true
	}
	if ct, ok := e.varType(base); ok {
		return accessCur{ctype: ct}, true
	}
	return accessCur{}, false
}

// accessSelect advances the chain by a field selector.
func (e *emitter) accessSelect(cur accessCur, field string) (accessCur, bool) {
	if cur.slice || len(cur.dims) != 0 || cur.ctype == "" {
		return accessCur{}, false // only a plain struct value has fields
	}
	if a, ok := e.structFieldArray(cur.ctype, field); ok {
		return accessCur{elem: a.elem, dims: a.bounds()}, true
	}
	ct, ok := e.structFieldType(cur.ctype, field)
	if !ok {
		return accessCur{}, false
	}
	if el, ok := e.sliceElemByName[ct]; ok {
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
		rest := cur.dims[1:]
		if len(rest) != 0 {
			return accessCur{elem: cur.elem, dims: rest}, cur.dims[0], true
		}
		return e.plainOrSlice(cur.elem), cur.dims[0], true
	}
	return accessCur{}, "", false
}

// accessSlice advances the chain by a slice step, `[l:h]`. Slicing an array or a
// slice yields a slice of the same element; slicing a string yields a string. A
// multi-dimensional array cannot be sliced here for the reason it cannot be
// elsewhere: a slice of arrays has no element type C can name.
func (e *emitter) accessSlice(cur accessCur) (accessCur, bool) {
	switch {
	case cur.slice, len(cur.dims) == 1:
		return accessCur{elem: cur.elem, slice: true}, true
	case cur.ctype == cString:
		return accessCur{ctype: cString}, true
	}
	return accessCur{}, false
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
	case len(cur.dims) == 1:
		// An array decays to a pointer to its first element and its extent is both
		// its length and its capacity.
		return sliceSource{sliceCName(cur.elem), prefix, cur.dims[0], cur.dims[0]}, true
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
	return e.emitAccessChainAt(e.varRef(base), cur, steps, false)
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
			sep := "."
			if e.isPointer(cur.ctype) {
				sep = "->"
			}
			if prefix != "" {
				prefix += sep + e.fieldIdent(f)
			} else {
				e.emit(sep + e.fieldIdent(f))
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
			e.emit(pre + prefix + open)
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
	for _, n := range steps {
		if n.sym != Index && n.sym != Selector {
			return "", nil, false
		}
	}
	return e.src(kids[0].tok), steps, true
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
		return cIdent(name) // a local or parameter: Unicode-escaped, no package prefix
	}
	if _, ok := e.globals[e.globalC(name)]; ok {
		return e.globalC(name)
	}
	return cIdent(name)
}

// sliceSource describes what a slice expression slices: the C type of the result
// header, the base pointer, and the base's length and capacity. baseLen is the
// default when high is omitted; baseCap becomes the header's third field and is
// empty for a string, which has no capacity and slices to a 2-field ogo_string.
type sliceSource struct{ cname, ptr, baseLen, baseCap string }

// sliceableVar resolves a variable base to slice: a string, a fixed array, or a
// slice.
func (e *emitter) sliceableVar(base string) (sliceSource, bool) {
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
	case e.hasArrayVar(base):
		a, _ := e.arrayVar(base)
		if a.dims() > 1 {
			// "m[:]" over a [2][3]int would be a slice of [3]int, and a slice of
			// arrays has no element type C can name here. Slicing a *row*,
			// "m[0][:]", is a slice of int and does work (sliceableChainRow).
			e.fail("cannot slice %s: its element is an array; slice a row instead, %s[i][:]", arrayTypeName(a), base)
			return sliceSource{}, false
		}
		e.needSlice(a.elem)
		return sliceSource{sliceCName(a.elem), base, a.bound, a.bound}, true
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
		e.needSlice(a.elem)
		return sliceSource{sliceCName(a.elem), lv, a.bound, a.bound}, true
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
// Only a row that is itself one-dimensional can become a slice: a row of a
// [2][3][4]int is a [3][4]int, and a slice of arrays has no element type C can
// name here (the same limit that refuses a `[][2]int` literal).
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
	if !ok || cur.slice || len(cur.dims) != 1 {
		return sliceSource{}, nil, nil, nil, false
	}
	text, ok := e.accessChainCText(base, prefix)
	if !ok {
		return sliceSource{}, nil, nil, nil, false
	}
	e.needSlice(cur.elem)
	// The row decays to a pointer to its first element, and its extent is both the
	// length and the capacity: an array's storage is exactly its extent.
	return sliceSource{sliceCName(cur.elem), text, cur.dims[0], cur.dims[0]}, low, high, max, true
}

// accessChainCText renders an access chain to a string, the way argsCText does
// for a call's arguments, so a caller that needs the chain as an operand rather
// than as streamed output can have it.
func (e *emitter) accessChainCText(base string, steps []Node) (string, bool) {
	cur, ok := e.accessBase(base)
	if !ok {
		return "", false
	}
	return e.accessChainCTextAt(e.varRef(base), cur, steps, false)
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
func (e *emitter) failAt(n []int32, format string, args ...any) {
	at := ""
	for c := range it(n) {
		at = e.f.tok(c.Pos()).Position().String()
		break
	}
	e.fail("%s: "+format, append([]any{at}, args...)...)
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
	return strconv.Quote(v), strconv.Itoa(len(v)), true
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

// hasArrayVar reports whether base names a fixed-array variable.
func (e *emitter) hasArrayVar(base string) bool { _, ok := e.arrayVar(base); return ok }

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
	// The first argument is the slice type "[]T" as a factor; read its element type.
	if elem, ok = e.sliceType(e.peelToFactorAST(args[0].ast)); !ok {
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
	initLHS   []int32 // nil when there is no init statement
	initOp    Symbol  // ASSIGN or DEFINE
	initRHS   []int32
	cond      []int32 // nil for a conditionless loop
	postLHS   []int32 // nil when there is no post statement
	postOp    Symbol  // ASSIGN, DEFINE, INC or DEC
	postRHS   []int32
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
func (e *emitter) parseForRest(n Node, h *forHeader) bool {
	kids := slices.Collect(it(n.ast))
	// `, val := range x`: a comma makes this the two-variable range form, with the
	// leading expression as the key.
	if len(kids) >= 1 && kids[0].sym == 0 && e.f.ch(kids[0].tok) == COMMA {
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
	for c := range it(n.ast) {
		switch {
		case c.sym == Expression && h.postLHS == nil:
			h.postLHS = c.ast
		case c.sym == Expression:
			h.postRHS = c.ast
		case c.sym == 0:
			h.postOp = e.f.ch(c.tok)
		}
	}
	return h.postLHS != nil
}

func (e *emitter) emitFor(nodes []Node) {
	// A name a for header declares belongs to the statement, not to the block
	// around it (see enterScope).
	defer e.enterScope()()
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
			if _, ok := e.exprIdent(v); !ok {
				e.fail("a range target that is not a variable is not supported yet")
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
	switch {
	case name != "":
		if a, isArray := e.arrays[name]; isArray {
			e.emitRangeArray(h, body, key, a, name)
			break
		}
		e.emitRangeSlice(h, body, key, e.locals[name], name)
	case e.isSliceCType(ct):
		// Hoist the slice header so .len and .ptr come from one evaluation.
		hdr := e.newTmp()
		e.ind()
		e.emit(ct + " " + hdr + " = " + e.exprC(h.rangeExpr) + ";\n")
		e.emitRangeSlice(h, body, key, ct, hdr)
	case e.rangeArray(h.rangeExpr) != nil:
		base, _ := e.exprIdent(h.rangeExpr)
		e.emitRangeArray(h, body, key, *e.rangeArray(h.rangeExpr), base)
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
func (e *emitter) rangeValueInject(h *forHeader, key, elem, access string) func() {
	var lines []func()
	if h.keyStore != "" {
		store := h.keyStore
		lines = append(lines, func() { e.ind(); e.emit(store + " = " + key + ";\n") })
	}
	if h.valVar != nil {
		if val := e.exprC(h.valVar); val != "_" { // "_" discards the value
			decl := ""
			if h.rangeDef {
				e.locals[val] = elem
				decl = elem + " "
			}
			lines = append(lines, func() { e.ind(); e.emit(decl + val + " = " + access + ";\n") })
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
	e.emitLoopBody(body, e.rangeValueInject(h, key, a.elem, base+"["+key+"]"))
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

// rangeArray returns the array dimension of a range operand that is a bare array
// variable, or nil.
func (e *emitter) rangeArray(expr []int32) *arrDim {
	base, ok := e.exprIdent(expr)
	if !ok {
		return nil
	}
	if a, ok := e.arrayVar(base); ok {
		return &a
	}
	return nil
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
	var head Node
	var suffix []Node
	for _, n := range nodes {
		switch n.sym {
		case AssignHead:
			head = n
		case Selector, Index, CallSuffix:
			suffix = append(suffix, n)
		}
	}
	if head.sym != AssignHead || len(suffix) == 0 {
		e.fail("a defer statement must be a function call")
		return
	}
	if recv := e.soleIdent(head.ast); recv == "print" || recv == "println" {
		// emitPrint renders per-type printf calls and does not go through
		// emitCallArgs, so a captured temporary would be ignored and the argument
		// re-evaluated at the return. Reject rather than deviate silently.
		if len(e.callArgExprs(suffix[len(suffix)-1].ast)) != 0 {
			e.fail("deferring print with arguments is not supported yet")
			return
		}
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
		e.emit(deferRecvName(d.slot) + " = " + recvText + ";\n")
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
		return "", true
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
			return "", true // not a variable: leave it to the call path to report
		}
		ctype, text = ct, e.varRef(base)
	} else {
		cur, ok := e.accessChainType(base, chain)
		if !ok {
			return "", true
		}
		if ctype, ok = e.chainValueCType(cur); !ok {
			return "", true
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
			e.emit(d.recvCType + " " + deferRecvName(d.slot) + " = " + e.zeroInitC(d.recvCType) + ";\n")
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
			// The flag is a statement prefix, so the call emitCall writes lands as
			// the guarded statement: `if (_ogo_deferN) f(...);`.
			e.ind()
			e.emit("if (" + deferFlagName(d.slot) + ") ")
			saved := e.indent
			e.indent = 0
			e.emitCall(d.head, d.suffix)
			e.indent = saved
		} else {
			e.emitCall(d.head, d.suffix)
		}
		e.deferReplay, e.deferReplayArgs = -1, nil
	}
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
		if e.isNilExpr(ex.ast) && e.isSliceCType(e.curResultTypes[i]) {
			e.emit("(" + e.curResultTypes[i] + "){0}")
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
		e.fail("unsupported call target")
		return
	}
	if len(postfix) == 1 && postfix[0].sym == CallSuffix && (recv == "println" || recv == "print") {
		e.emitPrint(recv == "println", postfix[0].ast)
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
			e.emit(e.varRef(recv) + ".vt->" + cIdent(method) + "(" + e.varRef(recv) + ".data")
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
			e.emitCallArgs("", suffix[1].ast)
			e.emit(")")
			return true
		}
		if rct, ok := e.varType(recv); ok && e.isUserType(methodBaseType(rct)) {
			cname := methodCName(methodBaseType(rct), method)
			e.emit(cname + "(")
			e.emitMethodReceiver(recv, rct, e.methodPtr[cname])
			if len(e.callArgExprs(suffix[1].ast)) > 0 {
				e.emit(", ")
				e.emitCallArgs(cname, suffix[1].ast)
			}
			e.emit(")")
			return true
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
			text = fn + "(" + text + ", " + rhsText + ")"
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
		ct, isConv := e.convType(base)
		if !isConv || len(steps) < 2 || steps[0].sym != CallSuffix {
			return "", "", false, false
		}
		args := e.callArgExprs(steps[0].ast)
		if len(args) != 1 {
			return "", "", false, false
		}
		text = e.captureC(func() { e.emitConversion(ct, args[0]) })
		cur, steps = e.plainOrSlice(ct), steps[1:]
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
				text += "(" + e.argsCText("", n.ast) + ")"
				// Keyed by the function typedef, which a DEFINED function type is
				// only a name for: `type Fn func(int) int` reaches its results
				// through what it is defined over.
				rts := e.funcTypeRet[e.underlyingCType(cur.ctype)]
				if len(rts) != 1 {
					return "", "", false, false
				}
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
				call := text + ".vt->" + cIdent(field) + "(" + text + ".data"
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
			if okm && i+1 < len(steps) && steps[i+1].sym == CallSuffix && cur.ctype != "" && e.isUserType(bt) {
				// A void method (no result) is valid as the final step of a call
				// statement -- `xs[i].update()` mutating an element in place -- so 0
				// results is admitted; a further step then fails on the empty type. A
				// multi-result method is not a single value and cannot continue a chain.
				if len(rts) > 1 {
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
				if len(rts) == 1 {
					cur = e.plainOrSlice(rts[0])
				} else {
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
			sep := "."
			if e.isPointer(cur.ctype) {
				sep = "->"
			}
			text += sep + e.fieldIdent(field)
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
			text += open + e.indexCText(low, lenExpr) + "]"
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
			if i+1 < len(steps) && steps[i+1].sym == CallSuffix && cur.ctype != "" && e.isUserType(bt) {
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
// arrayFieldBound returns the declared extent of an array-typed struct field named
// by an expression, `r.buf` or `r.inner.buf`. It is the length and the capacity
// alike, and it is a compile-time constant, so no storage is read to produce it.
//
// A field reached through an index, `rs[i].buf`, is not matched here: that is a
// chain rather than a selector list, and it has never been asked for.
func (e *emitter) arrayFieldBound(arg []int32) (string, bool) {
	base, fields, ok := e.factorFieldAccess(e.factorKids(arg))
	if !ok {
		return "", false
	}
	a, ok := e.fieldArray(base, fields)
	if !ok {
		return "", false
	}
	return a.bound, true // the OUTER extent, as for a multi-dimensional variable
}

func (e *emitter) emitLen(callSuffix []int32) {
	args := e.callArgExprs(callSuffix)
	if len(args) != 1 {
		e.fail("len takes exactly one argument")
		return
	}
	arg := args[0].ast
	if tok, ok := e.soleToken(arg); ok && e.f.ch(tok) == IDENT {
		if a, ok := e.arrayVar(e.src(tok)); ok {
			e.emit(a.bound)
			return
		}
	}
	// An array-typed struct field, `len(r.buf)`: its length is the declared extent,
	// exactly as for an array variable. A slice-typed field carries a header and
	// falls through to it below.
	if b, ok := e.arrayFieldBound(arg); ok {
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
	if tok, ok := e.soleToken(arg); ok && e.f.ch(tok) == IDENT {
		if a, ok := e.arrayVar(e.src(tok)); ok {
			e.emit(a.bound)
			return
		}
	}
	if b, ok := e.arrayFieldBound(arg); ok {
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
	ct, ok := e.inferCType(args[0].ast)
	if !ok || !e.isSliceCType(ct) {
		e.fail("append's first argument must be a slice variable or slice field yet")
		return "", nil, false
	}
	elem = sliceElemFromCName(ct)
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
	e.appendElems[elem] = true
	e.needPanic()
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
	e.tryappendElems[elem] = true
	tmp := e.newTmp()
	e.ind()
	e.emit(appendokCName(elem) + " " + tmp + " = " + tryappendCName(elem) + "(")
	e.emitExpr(args[0].ast)
	e.emit(", ")
	e.emitExpr(args[1].ast)
	e.emit(");\n")
	// The slice target, then the ok target (int).
	if declare[0] && targets[0].plain() {
		e.sliceVars[targets[0].name] = elem
	}
	e.emitStore(targets[0], declare[0], sliceCName(elem), tmp+".slice")
	e.emitStore(targets[1], declare[1], "int", tmp+".ok")
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
		e.emitPrintOne(newline, args[0])
	default:
		e.emitPrintMulti(newline, args)
	}
}

// emitPrintOne emits print/println of a single argument, appending a newline when
// newline is set. Integer and string output are folded into one call (preserving
// the compact printf("%d\n", x) / ogo_println_str(x) forms); slices and arrays go
// through their per-element print helper.
func (e *emitter) emitPrintOne(newline bool, arg Node) {
	// How a value prints follows its representation, not its name: a value of
	// `type Name string` is a string and prints as one.
	if ct, ok := e.exprReprCType(arg.ast); ok {
		if ct == cString {
			e.usesStringPrint = true
			e.ind()
			if newline {
				e.emit("ogo_println_str(")
			} else {
				e.emit("ogo_print_str(")
			}
			e.emitExpr(arg.ast)
			e.emit(");\n")
			return
		}
		if e.isSliceCType(ct) {
			e.emitPrintSlice(newline, sliceElemFromCName(ct), func() { e.emitExpr(arg.ast) })
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
	if ct, ok := e.exprReprCType(arg.ast); ok && ct == cBool {
		e.ind()
		nl := ""
		if newline {
			nl = "\\n"
		}
		e.emit("printf(\"%s" + nl + "\", ")
		e.emitBoolWord(arg)
		e.emit(");\n")
		return
	}
	// Default: an integer, or an integer-typed expression. The conversion is %u for
	// an unsigned type so a large value prints unsigned, as in Go, rather than
	// wrapping negative.
	verb := e.scalarPrintVerbOf(arg)
	e.ind()
	if newline {
		e.emit("printf(\"" + verb + "\\n\", ")
	} else {
		e.emit("printf(\"" + verb + "\", ")
	}
	e.emitExpr(arg.ast)
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
	for _, arg := range args {
		if !e.isScalarPrint(arg) {
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
			if e.isBoolPrint(arg) {
				e.emit("%s")
			} else {
				e.emit(e.scalarPrintVerbOf(arg))
			}
		}
		if newline {
			e.emit("\\n")
		}
		e.emit("\"")
		for _, arg := range args {
			e.emit(", ")
			if e.isBoolPrint(arg) {
				e.emitBoolWord(arg)
			} else {
				e.emitExpr(arg.ast)
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
		e.emitPrintOne(false, arg)
	}
	if newline {
		e.ind()
		e.emit("printf(\"\\n\");\n")
	}
}

// isBoolPrint reports whether an argument prints as a bool word.
func (e *emitter) isBoolPrint(arg Node) bool {
	ct, ok := e.exprReprCType(arg.ast)
	return ok && ct == cBool
}

// emitBoolWord renders a bool argument as the string "true" or "false" via a
// ternary, so println(b) prints the word rather than 1 or 0.
func (e *emitter) emitBoolWord(arg Node) {
	e.emit("(")
	e.emitExpr(arg.ast)
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

// isIntCType reports whether ct is one of the integer C types an OctoGo numeric
// maps to. It is the printable-integer set: a named type over int (its own typedef
// name) is not in it, so a slice of one still fails honestly.
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
func (e *emitter) scalarPrintVerbOf(arg Node) string {
	if ct, ok := e.exprReprCType(arg.ast); ok {
		return scalarPrintVerb(ct)
	}
	return "%d"
}

// isScalarPrint reports whether arg prints via printf %d (an integer or integer-
// typed expression) -- i.e. it is neither a string, a slice nor an array.
func (e *emitter) isScalarPrint(arg Node) bool {
	if ct, ok := e.exprReprCType(arg.ast); ok {
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

// emitAssignment handles a single-target assignment `lhs = expr`, a field
// assignment `base.f = expr`, a short declaration `lhs := expr`, and increment /
// decrement. The PostfixOp is the postfix's last element; any Selectors before it
// form a field-access target. Indexed targets are not modelled.
func (e *emitter) emitAssignment(head Node, postfix []Node) {
	if len(postfix) == 0 || postfix[len(postfix)-1].sym != PostfixOp {
		e.fail("unsupported assignment target")
		return
	}
	base := e.soleIdent(head.ast)
	if base == "" {
		e.fail("only assignment to a simple variable is supported yet")
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
	e.noteFrameHolder(base, op)
	// `f = keep` rebinds which function f holds; anything else clears the binding.
	if len(postfix) == 1 && stars == "" && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if ct, ok := e.varType(base); ok && e.isFuncCType(ct) {
			if rhs := e.rhsExprs(op[1]); len(rhs) == 1 {
				e.bindFuncValue(base, rhs[0].ast)
			}
		}
	}
	// A concrete value written into an interface variable: two words rather than one
	// assignment, and the pair decides which table.
	if len(postfix) == 1 && stars == "" && len(op) == 2 && op[0].sym == 0 && e.f.ch(op[0].tok) == ASSIGN {
		if ct, ok := e.varType(base); ok && e.isIfaceCType(ct) {
			if rhs := e.rhsExprs(op[1]); len(rhs) == 1 && !e.isNilExpr(rhs[0].ast) {
				if text := e.ifaceStoreC(e.varRef(base), ct, rhs[0].ast); text != "" {
					e.ind()
					e.emit(text)
				}
				return
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
	if chain := postfix[:len(postfix)-1]; stars == "" && isAccessChain(chain) {
		// A slice-valued target is a header assignment, `s[i].v = xs`, which C makes
		// by copying the struct -- the view changes, the storage it names does not.
		// Only when an index put it out of the fixed shapes' reach, though: a plain
		// field target, `b.data = ...`, belongs to them, since they are what knows
		// how to give a `make` its backing array. An array-valued target is refused
		// either way, C having no array assignment.
		if cur, ok := e.accessChainType(base, chain); ok && len(cur.dims) == 0 &&
			(!cur.slice || hasIndexStep(chain)) {
			t, ok := e.assignTailOf(postfix[len(postfix)-1])
			if !ok {
				e.fail("unsupported assignment form for an access chain")
				return
			}
			t.targetCType = cur.ctype
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
		ct, ok := e.varType(lhs)
		if !ok || !e.isChanCType(ct) {
			e.fail("a send statement needs a channel on the left")
			return
		}
		if x, r, bad := e.frameRefIn([]Node{op[1]}); bad {
			e.fail("%v: cannot send %s: its storage does not outlive the function, and the receiver keeps "+
				"the value; declare the backing array at package scope",
				e.f.tok(x.Pos()).Position(), r.what)
			return
		}
		e.ind()
		e.chanSendElems[e.chanElemByName[ct]] = true
		elem := e.chanElemByName[ct]
		e.emit(chanSendCName(elem) + "(" + lhs + ", ")
		// A concrete value sent on a channel of interface type is wrapped, the
		// element type being the target here.
		if text, ok := e.ifaceValueC(elem, rhsAst); ok && e.isIfaceCType(elem) {
			e.emit(text)
		} else {
			e.emitExpr(rhsAst)
		}
		e.emit(");\n")
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
		// `s = nil` resets a slice variable to its zero header, not the integer 0.
		if e.isNilExpr(rhsAst) && len(fields) == 0 {
			if ct, ok := e.varType(base); ok && e.isSliceCType(ct) {
				e.ind()
				e.emit(lhs + " = (" + ct + "){0};\n")
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
	e.locals[name] = ct
	if e.isSliceCType(ct) {
		e.sliceVars[name] = sliceElemFromCName(ct)
		// A slice copied from another views the same storage, so it inherits where
		// that storage lives; otherwise returning the copy would dodge the check.
		if _, frame := e.sliceBackingIsFrame(initExpr); frame {
			e.frameBacked[name] = true
		}
	}
	e.noteDeclFrameHolder(ct, name, initExpr)
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
	if _, isStruct := e.structs[methodBaseType(ctype)]; !isStruct && !e.isPointer(ctype) {
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
	cn := cIdent(name)
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
	e.ind()
	e.emit(ctype + " " + cn + " = ")
	e.emitVarInit(initExpr)
	e.emit(";\n")
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
		if e.isPointer(ct) {
			e.emit("->" + e.fieldIdent(f))
		} else {
			e.emit("." + e.fieldIdent(f))
		}
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
}

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

// emitAssignTailOrCopy emits an indented assignment statement whose target is
// written by target and whose tail is t, lowering the one case C's own assignment
// cannot express here: a struct that holds an array is copied with memcpy (see
// hasArrayField). target is rendered rather than streamed for that case, since
// memcpy needs the destination as an argument; it is called exactly once either
// way, and not at all if the copy is refused, so a refusal leaves no half-written
// statement.
func (e *emitter) emitAssignTailOrCopy(target func(), t assignTail) {
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
	return text + " = " + fn + "(" + text + ", " + e.captureC(func() { e.emitExpr(t.rhs) }) + ")", true
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
	// A slice element is addressed through its backing pointer; an array directly.
	// The index is bounds-checked against the container length.
	lhs := base
	lenExpr, elem := "", ""
	if el, ok := e.sliceElem(base); ok {
		lhs = base + ".ptr"
		lenExpr = base + ".len"
		elem = el
	} else if a, ok := e.arrayVar(base); ok {
		if a.dims() > 1 {
			e.fail("a multi-dimensional array must be indexed in every dimension")
			return
		}
		lenExpr, elem = a.bound, a.elem
	}
	t, ok := e.assignTailOf(opNode)
	if !ok {
		e.fail("unsupported assignment form for an indexed target")
		return
	}
	t.targetCType = elem
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
			e.emit(ctype + " " + cIdent(t.name) + " = " + val + ";\n")
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
	for i, r := range rhs {
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
		e.emitStore(tgt, declare[i], types[i], tmps[i])
	}
}

// emitDestructure lowers a multi-result call bound to several targets, shared by
// `a, b = f()` / `a, b := f()` and the var form `var a, b T = f()`. C has no
// multiple assignment, so the call's result struct is bound to a temporary and each
// target reads its field: a defined target is declared with its result type, an
// assigned target is assigned, and a blank target is skipped. rhs is the call
// expression; define selects declaration (`:=` / `var`) over plain assignment.
func (e *emitter) emitDestructure(targets []assignTarget, declare []bool, rhs []int32) {
	callee, suffix, ok := e.directCall(rhs)
	if !ok {
		e.fail("multiple assignment requires a single function call on the right-hand side")
		return
	}
	if callee == "append" && len(suffix) == 1 && suffix[0].sym == CallSuffix {
		// Two-result append: s, ok = append(s, x) -- the ok form, no trap.
		e.emitTryAppend(targets, declare, suffix[0].ast)
		return
	}
	cname, resTypes, ok := e.callResultInfo(callee, suffix)
	if !ok || len(resTypes) != len(targets) {
		e.fail("multiple-assignment target/result count mismatch")
		return
	}
	tmp := e.newTmp()
	e.ind()
	e.emit(e.retStructName(cname) + " " + tmp + " = ")
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
	} else if !e.emitCallExpr(callee, suffix) {
		e.fail("unsupported call on the right-hand side of a multiple assignment")
		return
	}
	e.emit(";\n")
	for i, tgt := range targets {
		e.emitStore(tgt, declare[i], resTypes[i], fmt.Sprintf("%s._%d", tmp, i))
	}
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
	if !ok || !e.isUserType(methodBaseType(ct)) {
		return nil, "", false, false
	}
	cname = methodCName(methodBaseType(ct), e.soleIdent(sels[len(sels)-1].ast))
	return fields, cname, e.methodPtr[cname], true
}

func (e *emitter) callResultInfo(recv string, suffix []Node) (cname string, resTypes []string, ok bool) {
	if _, cn, _, isField := e.methodOnField(recv, suffix); isField {
		resTypes, ok = e.funcRet[cn]
		return cn, resTypes, ok
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
		if rct, isVar := e.varType(recv); isVar && e.isUserType(methodBaseType(rct)) {
			cname = methodCName(methodBaseType(rct), member)
		} else if prefix, isPkg := e.importQualifiers[recv]; isPkg {
			cname = mangle(prefix, member)
		} else {
			return "", nil, false
		}
		resTypes, ok = e.funcRet[cname]
		return cname, resTypes, ok
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
func (e *emitter) emitCallArgs(cname string, callSuffix []int32) {
	sliceParams := e.funcSliceParams[cname]
	args := e.callArgExprs(callSuffix)
	e.checkCrossArgs(cname, args)
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
	return e.src(kids[0].tok), suffix, true
}

// isStruct reports whether a C type name denotes a modelled struct type.
func (e *emitter) isStruct(ctype string) bool { _, ok := e.structs[ctype]; return ok }

// isSliceCType reports whether a C type name is a slice header type (ogo_slice_<T>).
func (e *emitter) isSliceCType(ctype string) bool { return strings.HasPrefix(ctype, sliceTypePrefix) }

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

// qualifiedGlobalRead resolves a read of an exported variable from an imported
// user package -- `pkg.V`, or a field chain `pkg.V.f` selecting into it -- to the C
// text and type of the mangled package global. The imported package's variables
// were recorded in e.globals under their mangled names when that package's files
// were emitted (every reachable package is collected before any body is emitted),
// so the read resolves just as a same-package `x := g` does. ok is false when base
// is not an import qualifier or the member is not one of that package's globals (a
// function or a type member, left to the caller's other shapes).
func (e *emitter) qualifiedGlobalRead(base string, fields []string) (text, ctype string, ok bool) {
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
		sep := "."
		if e.isPointer(ctype) {
			sep = "->"
		}
		text += sep + e.fieldIdent(f)
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
func (e *emitter) structFieldType(ctype, field string) (string, bool) {
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
	id := cIdent(name)
	for e.typeNames[id] {
		id += "_"
	}
	return id
}

// fieldAccessC renders a field access chain `base.f.g...` in C, choosing "->" for
// each pointer step (an auto-dereferenced Go field access) and "." otherwise.
func (e *emitter) fieldAccessC(base string, fields []string) string {
	ctype, _ := e.varType(base)
	s := e.varRef(base) // a global base is mangled, a Unicode local base escaped
	for _, f := range fields {
		if e.isPointer(ctype) {
			s += "->"
		} else {
			s += "."
		}
		s += e.fieldIdent(f) // the emitted field name matches the (cIdent'd) typedef
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
		return e.inferNode(n)
	}
	return "", false
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
				return e.fieldType(base, fields)
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
					if a, ok := e.arrayVar(base); ok {
						return sliceCName(a.elem), true
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
			return "int", true // a rune literal is an int32, C "int" on this target
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
		if rct, ok := e.varType(recv); ok && e.isUserType(methodBaseType(rct)) {
			method := e.soleIdent(suffix[0].ast)
			if rts, ok := e.funcRet[methodCName(methodBaseType(rct), method)]; ok && len(rts) == 1 {
				return rts[0], true
			}
			return "", false
		}
		if prefix, ok := e.importQualifiers[recv]; ok {
			// A call into an imported user package: its function's recorded result type
			// is keyed by its mangled name in that package's namespace.
			if rts, ok := e.funcRet[mangle(prefix, e.soleIdent(suffix[0].ast))]; ok && len(rts) == 1 {
				return rts[0], true
			}
			return "", false
		}
		if recv == "p2" {
			// A p2 intrinsic's result carries its declared C type (unsigned for a
			// uint32 one), so a high-bit value prints and compares unsigned. A void
			// intrinsic has no result type.
			if intr, ok := p2Intrinsics[e.soleIdent(suffix[0].ast)]; ok {
				return intr.ret, intr.ret != ""
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
	if a, ok := e.namedArrays[ct]; ok {
		return arrayTypeName(a)
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
	return op, ct, true
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
	if ct, ok := e.inferCType(operand.ast); ok && e.isSliceCType(ct) {
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
	e.emitExprNode(l)
	e.emit(", ")
	e.emitExprNode(r)
	e.emit(")")
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
		e.emitExprNode(kids[i])
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
				e.emitExprNode(c)
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
			e.emit(chanRecvCName(elem) + "(" + base + ")")
			return
		}
		if n.sym == Factor {
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
					e.fail("a %s literal is only supported as a variable's initializer", e.litTypeName(litType))
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
					if a, ok := e.arrayVar(base); ok && a.dims() > 1 {
						e.fail("a multi-dimensional array must be indexed in every dimension")
						return
					}
					e.emit(base + "[")
					if a, ok := e.arrayVar(base); ok {
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
// one byte). The literal text emits verbatim -- Go and C share the common escapes.
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
	body := strconv.Quote(v) + ", " + strconv.Itoa(len(v))
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
	e.usesString = true
	body := src + ", " + strconv.Itoa(len(decoded))
	if e.declInit {
		e.emit("{" + body + "}")
	} else {
		e.emit("(" + cString + "){" + body + "}")
	}
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

// arrayOperand reports the array type of a comparison operand that is a plain
// array variable, local or package-level.
func (e *emitter) arrayOperand(n Node) (arrDim, bool) {
	name, ok := e.exprIdent(n.ast)
	if !ok {
		return arrDim{}, false
	}
	return e.arrayVar(name)
}

// emitArrayCompareTriple emits an array equality as a call to the per-type helper,
// negated for "!=".
func (e *emitter) emitArrayCompareTriple(l, r Node, op string, a arrDim) {
	e.needArrayEq(a)
	if op == "!=" {
		e.emit("!")
	}
	e.emit(arrayEqName(a) + "(")
	e.emitExprNode(l)
	e.emit(", ")
	e.emitExprNode(r)
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
	// `a[:]` / `s[1:2]`: the result views the base's storage, so it is frame-backed
	// exactly when the base is.
	fac, ok := e.soleFactorNode(ast)
	if !ok {
		return "", false
	}
	kids := slices.Collect(it(fac.ast))
	base, indexAST, ok := e.factorIndex(kids)
	if !ok {
		return "", false
	}
	if _, _, _, isSlice := e.sliceParts(indexAST); !isSlice {
		return "", false
	}
	if _, isLocalArray := e.arrays[base]; isLocalArray {
		return base, true
	}
	return base, e.frameBacked[base]
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
	view   bool   // the value is itself a slice over that storage
}

func sliceRef(name string) frameRef {
	return frameRef{origin: "local " + name, what: "a slice backed by local " + name, view: true}
}

func addrRef(name string) frameRef {
	return frameRef{origin: "local " + name, what: "the address of local variable " + name}
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

func holderRef(name, origin string) frameRef {
	return frameRef{origin: origin, what: "local " + name + ", which holds a pointer into " + origin}
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
	if name, ok := e.addrOfRoot(ast); ok && e.isFrameVar(name) {
		return addrRef(name), true
	}
	if name, ok := e.exprIdent(ast); ok {
		if origin := e.frameHolder[name]; origin != "" {
			return holderRef(name, origin), true
		}
	}
	// A call whose result derives from one of its arguments hands the argument's
	// provenance back out: `id(&x)` reaches x's storage exactly as `&x` does. Without
	// this a single call launders a reference past every sink -- `return id(&x)`
	// compiled, and so did storing or sending one.
	if recv, suffix, ok := e.directCall(ast); ok && len(suffix) != 0 && suffix[len(suffix)-1].sym == CallSuffix {
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
func (e *emitter) soleSliceLit(ast []int32) (string, Node, bool) {
	typeAST, lit, ok := e.soleArrayLit(ast)
	if !ok {
		return "", Node{}, false
	}
	if elem, ok := e.sliceType(typeAST); ok {
		return elem, lit, true
	}
	return "", Node{}, false
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
		e.fail("%v: cannot return %s: its storage does not outlive the function; "+
			"take the backing array from the caller or declare it at package scope",
			e.f.tok(x.Pos()).Position(), r.what)
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
func (e *emitter) checkCrossArgs(cname string, args []Node) {
	crosses := e.crossParams[cname]
	for i, a := range args {
		if i >= len(crosses) || crosses[i] == 0 {
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
			e.f.tok(a.Pos()).Position(), r.what, e.funcSourceName(cname), i+1, why, r.advice())
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
