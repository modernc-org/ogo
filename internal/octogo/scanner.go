// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo // import "modernc.org/ogo/internal/ogo"

import (
	"bytes"
	"fmt"
	"go/token"
	"strings"

	mtoken "modernc.org/token"
)

var (
	_ error = ErrWithPosition{}
	_ error = ErrList{}
)

// isCompoundAssign reports whether sym is one of the compound assignment
// operators produced by the AssignOp production ("+=", "&^=", "<<=", ...).
func isCompoundAssign(sym Symbol) bool {
	switch sym {
	case ADD_ASSIGN, SUB_ASSIGN, MUL_ASSIGN, QUO_ASSIGN, REM_ASSIGN,
		AND_ASSIGN, OR_ASSIGN, XOR_ASSIGN, ANDNOT_ASSIGN, SHL_ASSIGN, SHR_ASSIGN:
		return true
	}
	return false
}

// isShiftAssign reports whether sym is a shift-assignment ("<<=", ">>="). Their
// right operand is a shift count rather than a value of the target's type, so it
// is not checked for assignability to the target the way the others are.
func isShiftAssign(sym Symbol) bool { return sym == SHL_ASSIGN || sym == SHR_ASSIGN }

// Renamed Symbols
const (
	EOF  = TOK_EOF      // EOF
	NEQ  = TOK_0021003d // "!="
	LAND = TOK_00260026 // "&&"
	LOR  = TOK_007c007c // "||"
	INC  = TOK_002b002b // "++"
	DEC  = TOK_002d002d // "--"

	// The compound assignment operators. Each pairs with the binary operator of
	// the same name: "x op= y" is "x = x op y" with the target evaluated once.
	ADD_ASSIGN    = TOK_002b003d     // "+="
	SUB_ASSIGN    = TOK_002d003d     // "-="
	MUL_ASSIGN    = TOK_002a003d     // "*="
	QUO_ASSIGN    = TOK_002f003d     // "/="
	REM_ASSIGN    = TOK_0025003d     // "%="
	AND_ASSIGN    = TOK_0026003d     // "&="
	OR_ASSIGN     = TOK_007c003d     // "|="
	XOR_ASSIGN    = TOK_005e003d     // "^="
	ANDNOT_ASSIGN = TOK_0026005e003d // "&^="
	SHL_ASSIGN    = TOK_003c003c003d // "<<="
	SHR_ASSIGN    = TOK_003e003e003d // ">>="

	DEFINE    = TOK_003a003d  // ":="
	ARROW     = TOK_003c002d  // "<-"
	SHL       = TOK_003c003c  // "<<"
	LEQ       = TOK_003c003d  // "<="
	EQL       = TOK_003d003d  // "=="
	GEQ       = TOK_003e003d  // ">="
	SHR       = TOK_003e003e  // ">>"
	CASE      = TOK_case      // "case"
	CHAN      = TOK_chan      // "chan"
	CONST     = TOK_const     // "const"
	DEFAULT   = TOK_default   // "default"
	DEFER     = TOK_defer     // "defer"
	ELSE      = TOK_else      // "else"
	FOR       = TOK_for       // "for"
	FUNC      = TOK_func      // "func"
	GO        = TOK_go        // "go"
	IF        = TOK_if        // "if"
	IMPORT    = TOK_import    // "import"
	INTERFACE = TOK_interface // "interface"
	RETURN    = TOK_return    // "return"
	SELECT    = TOK_select    // "select"
	STRUCT    = TOK_struct    // "struct"
	SWITCH    = TOK_switch    // "switch"
	TYPE      = TOK_type      // "type"
	VAR       = TOK_var       // "var"
	NOT       = TOK_0021      // '!'
	AND       = TOK_0026      // '&'
	LPAREN    = TOK_0028      // '('
	RPAREN    = TOK_0029      // ')'
	MUL       = TOK_002a      // '*'
	ADD       = TOK_002b      // '+'
	COMMA     = TOK_002c      // ','
	SUB       = TOK_002d      // '-'
	PERIOD    = TOK_002e      // '.'
	QUO       = TOK_002f      // '/'
	COLON     = TOK_003a      // ':'
	SEMICOLON = TOK_003b      // ';'
	LSS       = TOK_003c      // '<'
	ASSIGN    = TOK_003d      // '='
	GTR       = TOK_003e      // '>'
	LBRACK    = TOK_005b      // '['
	RBRACK    = TOK_005d      // ']'
	XOR       = TOK_005e      // '^'
	LBRACE    = TOK_007b      // '{'
	OR        = TOK_007c      // '|'
	RBRACE    = TOK_007d      // '}'
	TILDE     = TOK_007e      // '~'
	IDENT     = identifier    // identifier
	INT       = int_lit       // int_lit
	FLOAT     = float_lit     // float_lit
	CHAR      = rune_lit      // rune_lit
	STRING    = string_lit    // string_lit
	// white_space    // white_space
)

// ErrWithPosition augments an error with optional position information.
type ErrWithPosition struct {
	Pos token.Position
	Err error
}

// Error implements error.
func (e ErrWithPosition) Error() string {
	switch {
	case e.Pos.IsValid():
		return fmt.Sprintf("%v: %v", e.Pos, e.Err)
	default:
		return fmt.Sprintf("%v", e.Err)
	}
}

func (e ErrWithPosition) less(f ErrWithPosition) bool {
	switch {
	case !e.Pos.IsValid():
		switch {
		case !f.Pos.IsValid():
			return e.Err.Error() < f.Err.Error()
		default:
			return true
		}
	default:
		switch {
		case !f.Pos.IsValid():
			return false
		}
	}

	if e.Pos.Filename < f.Pos.Filename {
		return true
	}

	if e.Pos.Filename > f.Pos.Filename {
		return false
	}

	if e.Pos.Line < f.Pos.Line {
		return true
	}

	if e.Pos.Line > f.Pos.Line {
		return false
	}

	if e.Pos.Column < f.Pos.Column {
		return true
	}

	if e.Pos.Column > f.Pos.Column {
		return false
	}

	return e.Err.Error() < f.Err.Error()
}

func (e ErrWithPosition) sameFileAndLine(f ErrWithPosition) bool {
	if !e.Pos.IsValid() && !f.Pos.IsValid() {
		return e.Error() == f.Error()
	}

	return e.Pos.Filename == f.Pos.Filename && e.Pos.Line == f.Pos.Line
}

// ErrList is a list of errors.
type ErrList []ErrWithPosition

// Err returns e if len(e) != 0 or nil.
func (e ErrList) Err() (r error) {
	if len(e) == 0 {
		return nil
	}

	return e
}

// Error implements error.
func (e ErrList) Error() string {
	w := 0
	prev := ErrWithPosition{Pos: token.Position{Offset: -1}}
	for _, v := range e {
		if v.Pos.Line == 0 || v.Pos.Offset != prev.Pos.Offset || v.Err.Error() != prev.Err.Error() {
			e[w] = v
			w++
			prev = v
		}
	}

	var a []string
	for _, v := range e[:w] {
		a = append(a, fmt.Sprint(v))
	}
	return strings.Join(a, "\n")
}

// AddErr adds an error message associated with an optional position.
func (e *ErrList) AddErr(pos token.Position, msg string, args ...interface{}) {
	// trc("%v: %s", pos, fmt.Sprintf(msg, args...))
	switch {
	case len(args) == 0:
		*e = append(*e, ErrWithPosition{pos, fmt.Errorf("%s", msg)})
	default:
		*e = append(*e, ErrWithPosition{pos, fmt.Errorf(msg, args...)})
	}
}

type tok struct { // 12 bytes
	ch  rune
	sep int32
	src int32
}

// source represents a single source file, editor text buffer etc.
type source struct {
	buf        []byte
	file       *mtoken.File
	name       string
	sepPatches map[int32]string
	srcPatches map[int32]string
	toks       []tok

	off int
}

// 'buf' becomes owned by the result and must not be modified afterwards.
func newSource(name string, buf []byte) *source {
	file := mtoken.NewFile(name, len(buf))
	return &source{
		buf:  buf,
		file: file,
		name: name,
	}
}

// Token represents a lexeme, its position and semantic value.
type Token struct { // 16 bytes on 64 bit arch
	// Ch represents the semantic value of the token as determined by the Scan
	// function.
	Ch     rune
	index  int32
	source *source
}

// Position reports the position of t.
func (t Token) Position() (r token.Position) {
	s := t.source
	if s == nil {
		return r
	}

	return token.Position(s.file.PositionFor(mtoken.Pos(s.toks[t.index].src+int32(s.file.Base())), true))
}

// Prev returns the token preceding t or a zero value if no such token exists.
func (t Token) Prev() (r Token) {
	s := t.source
	if s == nil {
		return r
	}

	if index := t.index - 1; index >= 0 {
		return Token{source: s, Ch: s.toks[index].ch, index: index}
	}

	return r
}

// Next returns the token following t or a zero value if no such token exists.
func (t Token) Next() (r Token) {
	s := t.source
	if s == nil {
		return r
	}

	if index := t.index + 1; index < int32(len(t.source.toks)) {
		return Token{source: s, Ch: s.toks[index].ch, index: index}
	}

	return r
}

// Sep returns the separator preceding t.
func (t Token) Sep() string {
	s := t.source
	if s == nil {
		return ""
	}

	if p, ok := s.sepPatches[t.index]; ok {
		return p
	}

	return string(s.buf[s.toks[t.index].sep:s.toks[t.index].src])
}

// SepBytes returns the separator preceding t. The
// result must not be mutated.
func (t Token) SepBytes() []byte {
	s := t.source
	if s == nil {
		return nil
	}

	if p, ok := s.sepPatches[t.index]; ok {
		return []byte(p)
	}

	return s.buf[s.toks[t.index].sep:s.toks[t.index].src]
}

// SetSep sets t's separator.
func (t Token) SetSep(s string) {
	src := t.source
	if src == nil {
		return
	}

	if src.sepPatches == nil {
		src.sepPatches = map[int32]string{}
	}
	src.sepPatches[t.index] = s
}

// SrcBytes returns t's source form, without its preceding separator. The
// result must not be mutated.
func (t Token) SrcBytes() []byte {
	s := t.source
	if s == nil {
		return nil
	}

	if p, ok := s.srcPatches[t.index]; ok {
		return []byte(p)
	}

	tok := s.toks[t.index]
	if int(tok.src) >= len(s.buf) {
		return nil
	}

	if int(t.index+1) < len(s.toks) {
		return s.buf[tok.src:s.toks[t.index+1].sep]
	}

	return s.buf[tok.src:s.off]
}

// Src returns t's source form, without its preceding separator.
func (t Token) Src() string {
	s := t.source
	if s == nil {
		return ""
	}

	if p, ok := s.srcPatches[t.index]; ok {
		return p
	}

	tok := s.toks[t.index]
	if int(tok.src) >= len(s.buf) {
		return ""
	}

	if int(t.index+1) < len(s.toks) {
		return string(s.buf[tok.src:s.toks[t.index+1].sep])
	}

	return string(s.buf[tok.src:s.off])
}

// SetSrc sets t's source form.
func (t Token) SetSrc(s string) {
	src := t.source
	if src == nil {
		return
	}

	if src.srcPatches == nil {
		src.srcPatches = map[int32]string{}
	}
	src.srcPatches[t.index] = s
}

// IsValid reports whether t is a valid token. Zero values of Token report
// false.
func (t Token) IsValid() bool { return t.source != nil }

// String implements fmt.Stringer.
func (t Token) String() string {
	return fmt.Sprintf("%v: %q %q %#U", t.Position(), t.Sep(), t.Src(), t.Ch)
}

// RecScanner represents the data structures and methods common to some/many
// lexical scanners, specialized for using scan functions produced by the
// [modernc.org/rec compiler].
//
// [modernc.org/rec compiler]: https://pkg.go.dev/modernc.org/rec
type RecScanner struct {
	*source
	errList ErrList
	scan    func([]byte) (id, length int)

	errBudget  int
	whiteSpace int

	insertSemi bool
	isClosed   bool
}

// NewRecScanner returns a newly created RecScanner. The 'name' argument is used to
// report positions. 'buf' becomes owned by the scanner and should not be
// mutated by the caller afterwards.
//
// The 'scan' function that is compatible with functions that the
// modernc.org/rec compiler produces. 'whiteSpace' is the id the 'scan'
// function returns for white space. The production for white space does not
// have to handle sequences of white space. RecScanner handles sequences of
// white space automatically. You can still write your regular expression for
// white_space like for example
//
//	`(// |\t|\n|\r)*`
//
// or
//
//	`( |\t|\n|\r)*`
//
// But you can also write it simply as
//
//	` |\t|\n|\r`
//
// with the same effect. This helps avoiding the problems described at [egg issue 1].
//
// Calling AddLine is done automatically by the [RecScanner.Scan] method.
//
// [egg issue 1]: https://gitlab.com/cznic/egg/-/issues/1
func NewRecScanner(name string, buf []byte, scan func(s []byte) (id, length int), whiteSpace int) *RecScanner {
	r := &RecScanner{
		errBudget:  10,
		scan:       scan,
		source:     newSource(name, buf),
		whiteSpace: whiteSpace,
	}
	return r
}

// AddErr registers an error.
func (s *RecScanner) AddErr(pos token.Position, msg string, args ...interface{}) {
	switch {
	case s.errBudget > 0:
		s.errList.AddErr(pos, msg, args...)
	case s.errBudget == 0:
		s.errList.AddErr(token.Position{}, "too many errors")
	}
	s.errBudget--
}

// Position returns the position determined by offset.
func (s *RecScanner) Position(offset int) (r token.Position) {
	return token.Position(s.file.PositionFor(mtoken.Pos(offset+s.file.Base()), true))
}

// Err reports any errors from reported by AddErr()
func (s *RecScanner) Err() error { return s.errList.Err() }

// AddLine adds the line offset for a new line.  The line offset must be larger
// than the offset for the previous line and smaller than the scanner buffer
// size; otherwise the line offset is ignored.
func (s *RecScanner) AddLine(offset int) { s.file.AddLine(offset + s.file.Base()) }

// AddLineColumnInfo adds alternative file, line, and column number information
// for a given scanner buffer offset. The offset must be larger than the offset
// for the previously added alternative line info and smaller than the scanner
// buffer size; otherwise the information is ignored.
//
// AddLineColumnInfo is typically used to register alternative position
// information for line directives such as //line filename:line:column.
func (s *RecScanner) AddLineColumnInfo(offset int, filename string, line, column int) {
	s.file.AddLineColumnInfo(offset, filename, line, column)
}

// Scan returns the next token.
func (s *RecScanner) Scan() (r Token) {
	// trc("[I] len(s.toks)=%v", len(s.toks))
	// defer func() { trc("[O] len(s.toks)=%v r.Ch=%v %s", len(s.toks), Symbol(r.Ch), r.Src()) }()
	if !s.isClosed {
		off := s.off // Offset of the separator = starting offset
		defer func() {
			// func addLines(off int, b []byte) {
			for b := s.buf[off:s.off]; len(b) != 0; {
				x := bytes.IndexByte(b, '\n')
				if x < 0 { // No newline found
					return
				}

				s.AddLine(off + x) // register the line break
				b = b[x+1:]        // move after the line break
				off += x + 1
			}
		}()

	}

	var t tok
	x := int32(len(s.toks))
	sep := s.off
out:
	for {
		off := s.off
		switch {
		case !s.isClosed:
			switch id, length := s.scan(s.buf[off:]); {
			case id < 0: // no lexeme was recognized
				length = max(1, length) // Ensure we do not get stuck.
				s.AddErr(s.Position(off), "invalid token")
				s.off += length
			case id == s.whiteSpace:
				if s.insertSemi {
					// Check if this whitespace chunk contains a newline
					if bytes.IndexByte(s.buf[sep:off+length], '\n') >= 0 {
						// Yield a synthetic semicolon token.
						t = tok{ch: rune(SEMICOLON), sep: int32(sep), src: int32(off + length)}
						s.insertSemi = false // Reset state
						s.toks = append(s.toks, t)
						s.off += length
						break out
					}
				}

				s.off += length
				continue
			default:
				if s.insertSemi && off == len(s.buf) {
					// Yield a synthetic semicolon token at EOF
					t = tok{ch: rune(SEMICOLON), sep: int32(sep), src: int32(off + length)}
					s.insertSemi = false // Reset state
					s.toks = append(s.toks, t)
					s.off += length
					break out
				}

				t = tok{ch: rune(id), sep: int32(sep), src: int32(off)}
				s.off += length
				switch Symbol(id) {
				case
					INC, // "++"
					DEC, // "--"
					//TODO TOK_break
					//TODO TOK_continue
					//TODO TOK_fallthrough
					//TODO? imag_lit,
					RPAREN, // ')'
					RBRACK, // ']'
					RBRACE, // '}'
					RETURN,
					identifier,
					int_lit,
					float_lit,
					rune_lit,
					string_lit:

					s.insertSemi = true
				default:
					s.insertSemi = false
				}
				s.toks = append(s.toks, t)
				if length == 0 {
					s.isClosed = true
				}
			}
			break out
		default:
			x--
			t = s.toks[x]
			break out
		}
	}
	return Token{
		Ch:     t.ch,
		index:  x,
		source: s.source,
	}
}

// Len reports the number of tokens in 's'.
func (s *RecScanner) Len() int {
	return len(s.toks)
}

// Ch returns the Ch field of the n-th token in 's'. Ch panics if n is out of
// range [0..Len()-1].
func (s *RecScanner) Ch(n int) rune {
	return s.toks[n].ch
}

// Token returns the n-th token in 's'. Ch panics if n is out of range
// [0..Len()-1].
func (s *RecScanner) Token(n int) Token {
	return Token{
		Ch:     s.toks[n].ch,
		index:  int32(n),
		source: s.source,
	}
}
