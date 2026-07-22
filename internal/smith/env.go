// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith

import "sort"

// Symbol represents a declared entity (variable, constant, func).
type Symbol struct {
	Name string
	Type Type
	// We can expand this to differentiate between Var, Const, and Func
	IsConst bool
	Used    bool // Track if the symbol has been referenced in an expression
}

// Scope tracks variables and types available at a given block level.
type Scope struct {
	Parent  *Scope
	Symbols map[string]*Symbol
}

func NewScope(parent *Scope) *Scope {
	return &Scope{
		Parent:  parent,
		Symbols: make(map[string]*Symbol),
	}
}

// Declare adds a new symbol to the current scope.
func (s *Scope) Declare(name string, typ Type, isConst bool) {
	s.Symbols[name] = &Symbol{Name: name, Type: typ, IsConst: isConst}
}

// Lookup searches for a symbol in the current and parent scopes.
func (s *Scope) Lookup(name string) *Symbol {
	if sym, ok := s.Symbols[name]; ok {
		return sym
	}
	if s.Parent != nil {
		return s.Parent.Lookup(name)
	}
	return nil
}

// GetSymbolsOfType returns all symbols in scope matching the requested type.
// This is critical for generating expressions.
func (s *Scope) GetSymbolsOfType(typ Type) []*Symbol {
	var matches []*Symbol
	// Iterate in sorted name order: Go randomizes map iteration, and the caller
	// picks from the result with the seeded RNG by index, so an unsorted order
	// makes generation non-reproducible from a seed (mirrors flushUnused).
	names := make([]string, 0, len(s.Symbols))
	for name := range s.Symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sym := s.Symbols[name]
		// Basic type matching (we can refine this for assignability later)
		if sym.Type.String() == typ.String() {
			matches = append(matches, sym)
		}
	}
	if s.Parent != nil {
		matches = append(matches, s.Parent.GetSymbolsOfType(typ)...)
	}
	return matches
}
