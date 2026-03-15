// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith // import "modernc.org/octogo/lib/internal/smith"

import (
	"io"
)

// Node is the base interface for our generated AST.
type Node interface {
	Write(w io.Writer, indent int)
}

// writeIndent is a helper to format the output C-style (or Go-style).
func writeIndent(w io.Writer, indent int) {
	for i := 0; i < indent; i++ {
		io.WriteString(w, "\t")
	}
}

// Block represents a sequence of statements.
type Block struct {
	Statements []Node
}

func (b *Block) Write(w io.Writer, indent int) {
	io.WriteString(w, "{\n")
	for _, stmt := range b.Statements {
		stmt.Write(w, indent+1)
		io.WriteString(w, "\n")
	}
	writeIndent(w, indent)
	io.WriteString(w, "}")
}

// We will define specific statement and expression nodes in gemini.go
// as they map directly to the LL(1) EBNF productions.
