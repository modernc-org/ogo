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
	// LoopVar marks a for-loop control variable. It may be read, but must not be
	// mutated by a generated statement (e.g. a compound assignment): the loop is
	// designed to run exactly once, and mutating its variable in the body could make
	// the condition stay true and loop forever at runtime, diverging from the VM,
	// which evaluated a single iteration.
	LoopVar bool
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

// matching returns the symbols of this scope and its parents whose type satisfies
// pred, innermost scope first.
//
// Each scope is walked in sorted name order: Go randomizes map iteration, and the
// caller picks from the result with the seeded RNG by index, so an unsorted order
// makes generation non-reproducible from a seed (mirrors flushUnused).
func (s *Scope) matching(pred func(Type) bool) []*Symbol {
	var matches []*Symbol
	names := make([]string, 0, len(s.Symbols))
	for name := range s.Symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if sym := s.Symbols[name]; pred(sym.Type) {
			matches = append(matches, sym)
		}
	}
	if s.Parent != nil {
		matches = append(matches, s.Parent.matching(pred)...)
	}
	return matches
}

// GetSymbolsOfType returns all symbols in scope matching the requested type.
// This is critical for generating expressions.
func (s *Scope) GetSymbolsOfType(typ Type) []*Symbol {
	// Basic type matching (we can refine this for assignability later)
	return s.matching(func(t Type) bool { return t.String() == typ.String() })
}

// GetArraySymbols returns the in-scope variables of a fixed integer-array type.
func (s *Scope) GetArraySymbols() []*Symbol {
	return s.matching(func(t Type) bool {
		at, ok := t.(ArrayType)
		return ok && isInt(at.Elem)
	})
}

// GetSliceSymbols returns the in-scope variables of an integer-slice type. A
// caller that indexes or appends must also consult the variable's SliceVal for its
// current length and capacity, which the type does not carry.
func (s *Scope) GetSliceSymbols() []*Symbol {
	return s.matching(func(t Type) bool {
		st, ok := t.(SliceType)
		return ok && isInt(st.Elem)
	})
}

func isInt(t Type) bool {
	bt, ok := t.(BasicType)
	return ok && bt.Kind == KindInt
}

// GetStructSymbols returns the in-scope variables of a generated struct type.
func (s *Scope) GetStructSymbols() []*Symbol {
	return s.matching(func(t Type) bool { _, ok := t.(StructType); return ok })
}
