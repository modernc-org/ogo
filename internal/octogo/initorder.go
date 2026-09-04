// Package-variable initialization order, Go's way: a variable is initialized
// after everything it depends on, and a dependency is any reference its
// initializer makes -- directly, or through the body of any function or method
// the initializer reaches, transitively. What cannot be ordered is a cycle, and
// is refused with Go's own trace.
//
// The pieces:
//
//   - Every emitted function and method leaves a funcRefTask behind: the ASTs and
//     the per-file context (token file, package prefix, import qualifiers) needed
//     to read them later. The walk itself is deferred to pkgInitDefs, when every
//     package variable and method of the whole program is registered, so a method
//     receiver's type resolves the same wherever its declaration sits.
//
//   - refWalker names what a body or an initializer references: every identifier
//     that no local declaration accounts for, mangled into its package. Locals
//     are tracked with Go's declaration-point rule -- `b := b * 2` reads the
//     package b before declaring the local -- and with real block scoping, so a
//     shadow inside a block does not hide a read after it (probe g5).
//
//   - A selector member may be a METHOD reference, which Go also counts. The
//     walker records it as a marker resolved at assembly time: exactly, when the
//     receiver is a package variable or a local of known type (probe g7 -- two
//     types with a same-named method must not blur into each other); as the union
//     of all same-named methods when the receiver's type is unknown (a chain, a
//     call result), which errs toward more ordering. A receiver of interface
//     type resolves to no method, which is Go's rule too: dispatch through an
//     interface is not a dependency.
//
//   - pkgInitDefs flattens each step's raw references -- function names expand to
//     their bodies' references, markers to method C names -- into package
//     variable dependencies for the stable topological order, and reports a
//     cycle by walking the RAW graph, so the trace goes through the functions the
//     way Go's does: "a refers to f", "f refers to a".
package octogo

import (
	"fmt"
	"slices"
	"strings"
)

// funcRefTask is one emitted function or method, kept until the reference walk
// runs. The token file and mangling context travel with the ASTs: a cross-file
// AST read with the wrong file is silently wrong, so the walk restores exactly
// the context the function was emitted under.
type funcRefTask struct {
	cname   string // the C name the function was emitted under
	member  string // a method's source name, "" for a plain function
	srcName string // what a cycle diagnostic calls it
	pos     string // where it is declared, rendered while the file was current

	f      *File
	prefix string

	recvName string // the receiver's name bound in the body, "" for none
	recvBase string // the receiver's method-base C type
	sig      []int32
	body     []int32
}

// funcRefMeta is how a cycle diagnostic names a function node.
type funcRefMeta struct {
	srcName string
	pos     string
}

// Method-reference markers, recorded beside plain dependencies and resolved at
// assembly time. \x01 cannot occur in a C identifier, so a marker collides with
// nothing real.
const (
	refMarkTyped  = "\x01t\x01" // + receiver base C type + \x01 + member
	refMarkGlobal = "\x01m\x01" // + mangled package variable + \x01 + member
	refMarkUnion  = "\x01u\x01" // + member
)

// refWalker collects the package-level references of one function body or one
// initializer expression. Frames carry the local declarations in scope, each
// name with its type's base C name when a bare type name was written ("" when
// unknown); an identifier no frame accounts for is a package reference.
type refWalker struct {
	e      *emitter
	quals  map[string]string // the CURRENT file's import qualifiers -> package prefix
	frames []map[string]string
	out    []string
	seen   map[string]bool
}

func newRefWalker(e *emitter) *refWalker {
	w := &refWalker{e: e, seen: map[string]bool{}, quals: map[string]string{}}
	// The file's own imports, not the program-wide qualifier map: another file's
	// qualifier must not swallow a package variable that happens to share its
	// name.
	for _, spec := range e.f.ImportSpecs {
		if spec.Pkg != nil && spec.Pkg != noPkg && spec.ImportQualifier != "" {
			w.quals[spec.ImportQualifier] = pkgPrefix(spec.Pkg.ImportPath)
		}
	}
	w.push()
	return w
}

func (w *refWalker) push() { w.frames = append(w.frames, map[string]string{}) }
func (w *refWalker) pop()  { w.frames = w.frames[:len(w.frames)-1] }

func (w *refWalker) declare(name, base string) {
	if name == "" || name == "_" {
		return
	}
	w.frames[len(w.frames)-1][name] = base
}

func (w *refWalker) local(name string) (base string, isLocal bool) {
	for i := len(w.frames) - 1; i >= 0; i-- {
		if b, ok := w.frames[i][name]; ok {
			return b, true
		}
	}
	return "", false
}

func (w *refWalker) add(dep string) {
	if !w.seen[dep] {
		w.seen[dep] = true
		w.out = append(w.out, dep)
	}
}

// ident records a bare identifier: a local is no reference, anything else is,
// mangled into the current package. A name that turns out to be no package
// variable or function -- a constant, a type, a label -- matches no step and no
// task, and the ordering ignores it.
func (w *refWalker) ident(name string) {
	if name == "_" || name == "" {
		return
	}
	if _, isLocal := w.local(name); isLocal {
		return
	}
	w.add(w.e.globalC(name))
}

// memberRef records what a selector member may depend on. resolvable says the
// member hangs directly off the base identifier; deeper in a chain the static
// type is not tracked and the union marker stands in.
func (w *refWalker) memberRef(baseName string, resolvable bool, m string) {
	if m == "" || m == "_" {
		return
	}
	if !resolvable || baseName == "" {
		w.add(refMarkUnion + m)
		return
	}
	if base, isLocal := w.local(baseName); isLocal {
		if base == "" {
			w.add(refMarkUnion + m)
		} else {
			w.add(refMarkTyped + base + "\x01" + m)
		}
		return
	}
	if _, isImport := w.quals[baseName]; isImport {
		// A qualified reference into another package. Its package's own steps
		// precede this one structurally, and an import cycle cannot exist, so
		// there is nothing to order and nothing that can cycle.
		return
	}
	w.add(refMarkGlobal + w.e.globalC(baseName) + "\x01" + m)
}

// selectorMember reads a Selector's member identifier; a type assertion
// `.(T)` / `.(type)` has none.
func (w *refWalker) selectorMember(n Node) (string, bool) {
	for c := range it(n.ast) {
		if c.sym == 0 && w.e.f.ch(c.tok) == IDENT {
			return w.e.src(c.tok), true
		}
	}
	return "", false
}

// chain walks a suffix run -- Selector, Index, CallSuffix -- hanging off
// baseName. Only the first selector can resolve against the base's static type;
// after any hop the type is no longer tracked.
func (w *refWalker) chain(baseName string, nodes []Node) {
	first := true
	for _, n := range nodes {
		switch n.sym {
		case Selector:
			if m, ok := w.selectorMember(n); ok {
				w.memberRef(baseName, first, m)
			}
			// Nothing inside is walked: the member is a field or method name,
			// not a variable, and an assertion's type is not one either. That
			// distinction is load-bearing: reading members as plain references
			// once made `var a = s.a` report "a refers to itself".
			first = false
		case Index:
			w.expr(n.ast)
			first = false
		case CallSuffix:
			w.expr(n.ast)
			first = false
		}
	}
}

// expr walks an expression generically, dispatching factors to their own rule.
func (w *refWalker) expr(ast []int32) {
	for n := range it(ast) {
		switch n.sym {
		case Factor, HeaderFactor:
			w.factor(n)
		case Selector:
			// A suffix reached outside a factor or a paired statement head:
			// nothing tracks its base here.
			if m, ok := w.selectorMember(n); ok {
				w.memberRef("", false, m)
			}
		case Element:
			// A keyed element's KEY names a field or a constant index, so only
			// the VALUE reads anything: `S{q: 1}` refers to no variable q.
			val := Node{}
			for c := range it(n.ast) {
				if c.sym == ElementValue {
					val = c
				}
			}
			if val.sym != 0 {
				w.expr(val.ast)
				continue
			}
			w.expr(n.ast)
		case FuncLiteral:
			w.funcLit(n)
		case Type:
			// A type name carries no variable reference, and a struct type's
			// field names must not be read as ones.
		case 0:
			if w.e.f.ch(n.tok) == IDENT {
				w.ident(w.e.src(n.tok))
			}
		default:
			w.expr(n.ast)
		}
	}
}

// factor walks one Factor: a leading identifier is the base its suffix chain and
// composite literal hang off.
func (w *refWalker) factor(n Node) {
	kids := slices.Collect(it(n.ast))
	base := ""
	for i, c := range kids {
		switch {
		case c.sym == 0 && i == 0 && w.e.f.ch(c.tok) == IDENT:
			base = w.e.src(c.tok)
			w.ident(base)
		case c.sym == FactorSuffix:
			w.chain(base, slices.Collect(it(c.ast)))
		case c.sym == CompositeLit:
			w.expr(c.ast)
		case c.sym == FuncLiteral:
			w.funcLit(c)
		case c.sym == Type:
			// see expr
		case c.sym == 0:
			// parens, brackets, literals, "chan"
		default:
			w.expr(c.ast)
		}
	}
}

func (w *refWalker) funcLit(n Node) {
	w.push()
	for c := range it(n.ast) {
		switch c.sym {
		case Signature:
			w.declareSig(c.ast)
		case Block:
			w.stmts(c.ast)
		}
	}
	w.pop()
}

// declareSig declares a signature's named parameters and results into the
// current frame, each with its type's base name when one is written.
func (w *refWalker) declareSig(sig []int32) {
	for c := range it(sig) {
		if c.sym != ParameterList && c.sym != ResultList {
			continue
		}
		for _, d := range w.e.f.paramDecls(c.ast) {
			base := w.typeBaseName(d.TypeAST)
			for _, nm := range d.Names {
				w.declare(nm.Src(), base)
			}
		}
	}
}

// typeBaseName names the method-base C type a written type stands for: a bare
// or pointered type name, possibly qualified, mangled into its package. Anything
// else -- a slice, a channel, a literal struct -- carries no method set worth
// resolving and yields "".
func (w *refWalker) typeBaseName(t Node) string {
	var idents []string
	var walk func([]int32)
	walk = func(ast []int32) {
		for c := range it(ast) {
			switch {
			case c.sym == 0:
				switch w.e.f.ch(c.tok) {
				case IDENT:
					idents = append(idents, w.e.src(c.tok))
				case MUL:
					// a pointer's star: the base carries the methods
				default:
					return
				}
			default:
				walk(c.ast)
			}
		}
	}
	walk(t.ast)
	switch len(idents) {
	case 1:
		return mangle(w.e.curPkgPrefix, idents[0])
	case 2:
		if prefix, ok := w.quals[idents[0]]; ok {
			return mangle(prefix, idents[1])
		}
	}
	return ""
}

// stmts walks a statement list (a Block's or a clause's).
func (w *refWalker) stmts(ast []int32) {
	for n := range it(ast) {
		if n.sym == Statement {
			w.stmt(n.ast)
		}
	}
}

func (w *refWalker) stmt(ast []int32) {
	nodes := slices.Collect(it(ast))
	if len(nodes) == 0 {
		return
	}
	if _, inner, ok := w.e.stmtLabelParts(nodes); ok {
		// The label is neither a reference nor a declaration.
		w.stmt(inner)
		return
	}
	first := nodes[0]
	switch {
	case first.sym == VarDecl:
		w.declSpecs(first.ast, VarSpec)
	case first.sym == ConstDecl:
		w.declSpecs(first.ast, ConstSpec)
	case first.sym == TypeDecl:
		// A local type shadows a package name for what follows; its body is
		// type syntax and reads nothing.
		for c := range it(first.ast) {
			if c.sym == TypeSpec {
				for d := range it(c.ast) {
					if d.sym == 0 && w.e.f.ch(d.tok) == IDENT {
						w.declare(w.e.src(d.tok), "")
						break
					}
				}
			}
		}
	case first.sym == IfStmt:
		w.ifStmt(first.ast)
	case first.sym == SwitchStmt:
		w.switchStmt(first.ast)
	case first.sym == SelectStmt:
		w.selectStmt(first.ast)
	case first.sym == Block:
		w.push()
		w.stmts(first.ast)
		w.pop()
	case first.sym == 0 && w.e.f.ch(first.tok) == FOR:
		w.forStmt(nodes)
	case first.sym == 0 && (w.e.f.ch(first.tok) == BREAK || w.e.f.ch(first.tok) == CONTINUE || w.e.f.ch(first.tok) == GOTO):
		// the operand is a label
	case first.sym == 0 && w.e.f.ch(first.tok) == FALLTHROUGH:
	case first.sym == 0 && (w.e.f.ch(first.tok) == GO || w.e.f.ch(first.tok) == DEFER):
		w.headRun(nodes[1:])
	default:
		// return, a send, an expression statement, an assignment: one uniform
		// engine decides declarations against a `:=` and walks the rest.
		w.headerConstruct(nodes)
	}
}

// declSpecs walks a var or const declaration: each spec's expressions first,
// then its names, which is Go's declaration point -- `var x = x` reads the outer
// x.
func (w *refWalker) declSpecs(ast []int32, spec Symbol) {
	for c := range it(ast) {
		if c.sym != spec {
			continue
		}
		var names []string
		base := ""
		sawType := false
		for d := range it(c.ast) {
			switch {
			case d.sym == 0 && w.e.f.ch(d.tok) == IDENT && !sawType:
				names = append(names, w.e.src(d.tok))
			case d.sym == IdentifierList && !sawType:
				for id := range it(d.ast) {
					if id.sym == 0 && w.e.f.ch(id.tok) == IDENT {
						names = append(names, w.e.src(id.tok))
					}
				}
			case d.sym == Type:
				sawType = true
				base = w.typeBaseName(d)
			case d.sym == ExpressionList || d.sym == Expression:
				sawType = true
				w.expr(d.ast)
			case d.sym != 0:
				sawType = true
				w.expr(d.ast)
			}
		}
		for _, nm := range names {
			w.declare(nm, base)
		}
	}
}

// headerConstruct is the uniform engine for every construct that may carry a
// `:=` at its own level: an assignment or expression statement, an if or switch
// header, a for header, a select comm. The identifier-bearing nodes wholly
// before the DEFINE token are declarations; the first node after it is the right
// side, read before the names exist; everything after that reads the new names.
// With no DEFINE, everything is a reference -- writes count as references, which
// is Go's rule too (probe g3).
func (w *refWalker) headerConstruct(nodes []Node) {
	flat := flattenHeader(nodes)
	defineAt := int32(-1)
	for _, n := range flat {
		if n.sym == 0 && w.e.f.ch(n.tok) == DEFINE {
			defineAt = n.tok
			break
		}
	}
	if defineAt < 0 {
		w.headRun(flat)
		return
	}
	var decls []string
	rhsWalked := false
	declared := false
	declareNow := func() {
		for _, nm := range decls {
			w.declare(nm, "")
		}
		declared = true
	}
	for i := 0; i < len(flat); i++ {
		n := flat[i]
		if n.sym == 0 {
			continue
		}
		if n.tok < defineAt {
			// a declaration position: its sole identifier is the new name
			if nm, ok := w.declNodeName(n); ok {
				decls = append(decls, nm)
			} else {
				w.exprNode(n)
			}
			continue
		}
		if !rhsWalked {
			// the right side, read before the names exist
			w.exprNode(n)
			rhsWalked = true
			// `v := T{...}`: one name over a sole composite literal tracks the
			// receiver type for exact method resolution later in the body.
			if len(decls) == 1 {
				if base, ok := w.soleLitType(n); ok {
					declareNow()
					w.frames[len(w.frames)-1][decls[0]] = base
					continue
				}
			}
			declareNow()
			continue
		}
		if !declared {
			declareNow()
		}
		w.exprNode(n)
	}
	if !declared {
		declareNow()
	}
}

// flattenHeader flattens the header-level structure -- Postfix, PostfixOp,
// PostfixComm, IfInit, SwitchGuard and the for-header ladder -- to one ordered
// node list, stopping at blocks, clauses and nested statements, whose insides
// are their own scopes.
func flattenHeader(nodes []Node) []Node {
	var out []Node
	for _, n := range nodes {
		switch n.sym {
		case Postfix, PostfixOp, PostfixComm, IfInit, SwitchGuard, SwitchTag,
			ForHeader, ForRest, ForAssignRest, ForPost, CommOp:
			out = append(out, flattenHeader(slices.Collect(it(n.ast)))...)
		case Block, Statement, CaseClause, CommClause:
			// not header material
		default:
			out = append(out, n)
		}
	}
	return out
}

// declNodeName reads the sole identifier a declaration-position node binds: an
// AssignHead, an LhsItem, or a header expression that is nothing but a name.
func (w *refWalker) declNodeName(n Node) (string, bool) {
	switch n.sym {
	case AssignHead, HeaderExpression, Expression:
		return w.e.exprIdent(n.ast)
	case LhsItem:
		for c := range it(n.ast) {
			if c.sym == AssignHead {
				return w.e.exprIdent(c.ast)
			}
		}
	}
	return "", false
}

// exprNode walks one header-level node as references, pairing a statement
// head's identifier with the suffix chain that follows it.
func (w *refWalker) exprNode(n Node) {
	switch n.sym {
	case AssignHead:
		if nm, ok := w.e.exprIdent(n.ast); ok {
			w.ident(nm)
			return
		}
		w.expr(n.ast)
	case LhsItem:
		w.headRun(slices.Collect(it(n.ast)))
	default:
		w.expr(n.ast)
	}
}

// headRun walks an ordered run of header nodes, pairing each AssignHead with
// the Selector/Index/CallSuffix siblings that follow it so a method reference
// resolves against its receiver.
func (w *refWalker) headRun(nodes []Node) {
	i := 0
	for i < len(nodes) {
		n := nodes[i]
		if n.sym == AssignHead {
			base, isIdent := w.e.exprIdent(n.ast)
			if isIdent {
				w.ident(base)
			} else {
				w.expr(n.ast)
				base = ""
			}
			j := i + 1
			for j < len(nodes) && (nodes[j].sym == Selector || nodes[j].sym == Index || nodes[j].sym == CallSuffix) {
				j++
			}
			w.chain(base, nodes[i+1:j])
			i = j
			continue
		}
		if n.sym == LhsItem {
			w.headRun(slices.Collect(it(n.ast)))
			i++
			continue
		}
		if n.sym == FuncLiteral {
			w.funcLit(n)
			i++
			continue
		}
		if n.sym != 0 {
			w.expr(n.ast)
		}
		i++
	}
}

// soleLitType recognises a right side that is exactly `T{...}` and names T's
// mangled base, for local receiver-type tracking.
func (w *refWalker) soleLitType(n Node) (string, bool) {
	ast := n.ast
	for {
		kids := slices.Collect(it(ast))
		if len(kids) != 1 {
			return "", false
		}
		switch kids[0].sym {
		case Expression, HeaderExpression, SimpleExpr, HeaderSimpleExpr, Term, HeaderTerm,
			UnaryExpr, HeaderUnaryExpr, ExpressionList:
			ast = kids[0].ast
		case Factor, HeaderFactor:
			fk := slices.Collect(it(kids[0].ast))
			if len(fk) == 2 && fk[0].sym == 0 && w.e.f.ch(fk[0].tok) == IDENT && fk[1].sym == CompositeLit {
				return mangle(w.e.curPkgPrefix, w.e.src(fk[0].tok)), true
			}
			return "", false
		default:
			return "", false
		}
	}
}

func (w *refWalker) ifStmt(ast []int32) {
	w.push()
	defer w.pop()
	kids := slices.Collect(it(ast))
	var header []Node
	var rest []Node
	for _, c := range kids {
		switch c.sym {
		case Block:
			rest = append(rest, c)
		case IfStmt:
			rest = append(rest, c)
		default:
			header = append(header, c)
		}
	}
	w.headerConstruct(header)
	for _, c := range rest {
		switch c.sym {
		case Block:
			w.push()
			w.stmts(c.ast)
			w.pop()
		case IfStmt:
			w.ifStmt(c.ast)
		}
	}
}

func (w *refWalker) switchStmt(ast []int32) {
	w.push()
	defer w.pop()
	var header []Node
	var clauses []Node
	for c := range it(ast) {
		switch c.sym {
		case CaseClause:
			clauses = append(clauses, c)
		default:
			if c.sym != 0 {
				header = append(header, c)
			}
		}
	}
	w.headerConstruct(header)
	for _, cl := range clauses {
		w.push()
		for c := range it(cl.ast) {
			switch c.sym {
			case CaseHead:
				w.expr(c.ast)
			case Statement:
				w.stmt(c.ast)
			}
		}
		w.pop()
	}
}

func (w *refWalker) selectStmt(ast []int32) {
	for c := range it(ast) {
		if c.sym != CommClause {
			continue
		}
		w.push()
		var head []Node
		var stmts []Node
		for d := range it(c.ast) {
			switch d.sym {
			case CommHead:
				head = append(head, slices.Collect(it(d.ast))...)
			case Statement:
				stmts = append(stmts, d)
			}
		}
		w.headerConstruct(head)
		for _, s := range stmts {
			w.stmt(s.ast)
		}
		w.pop()
	}
}

func (w *refWalker) forStmt(nodes []Node) {
	w.push()
	defer w.pop()
	var header []Node
	var body Node
	for _, c := range nodes {
		switch c.sym {
		case Block:
			body = c
		default:
			if c.sym != 0 {
				header = append(header, c)
			}
		}
	}
	w.headerConstruct(header)
	if body.sym != 0 {
		w.push()
		w.stmts(body.ast)
		w.pop()
	}
}

// initRefs names every package-level variable, function and method an
// initializer expression could reference. It is deliberately generous -- every
// identifier no local accounts for, mangled into this package -- because a name
// that turns out to be none of those matches no step and no task, and the
// ordering ignores it. Mangling has to happen here, while the file being
// emitted says which package that is.
func (e *emitter) initRefs(ast []int32) []string {
	w := newRefWalker(e)
	w.expr(ast)
	return w.out
}

// taskRefs walks one recorded function under the context it was emitted in.
func (e *emitter) taskRefs(t funcRefTask) []string {
	savedF, savedPrefix := e.f, e.curPkgPrefix
	e.f, e.curPkgPrefix = t.f, t.prefix
	defer func() {
		e.f, e.curPkgPrefix = savedF, savedPrefix
	}()
	w := newRefWalker(e)
	if t.recvName != "" {
		w.declare(t.recvName, t.recvBase)
	}
	w.declareSig(t.sig)
	w.stmts(t.body)
	return w.out
}

// resolveFuncRefs turns the recorded tasks into the reference tables the
// ordering reads: per-function raw references, the method-name index, and how a
// diagnostic names each function.
func (e *emitter) resolveFuncRefs() {
	if e.funcRefs != nil {
		return
	}
	e.funcRefs = map[string][]string{}
	e.funcRefMeta = map[string]funcRefMeta{}
	e.methodsByName = map[string][]string{}
	for _, t := range e.funcRefTasks {
		if t.member != "" {
			e.methodsByName[t.member] = append(e.methodsByName[t.member], t.cname)
		}
		e.funcRefMeta[t.cname] = funcRefMeta{srcName: t.srcName, pos: t.pos}
	}
	for _, t := range e.funcRefTasks {
		e.funcRefs[t.cname] = e.taskRefs(t)
	}
}

// methodBaseOfGlobal resolves a package variable's method-base C type, the
// global half of methodRecvCType. An interface-typed variable resolves to
// nothing, which is Go's rule: a reference through an interface is not a
// dependency.
func (e *emitter) methodBaseOfGlobal(gn string) (string, bool) {
	if ct, ok := e.globals[gn]; ok {
		base := methodBaseType(ct)
		if e.isMethodBase(base) {
			return base, true
		}
		if _, isArr := e.namedArrays[base]; isArr {
			return base, true
		}
		return "", false
	}
	if a, ok := e.globalArrays[gn]; ok && a.name != "" {
		return a.name, true
	}
	return "", false
}

// resolveMarker resolves a method-reference marker to the method C names it may
// depend on. A member that names no recorded method -- a field access -- resolves
// to nothing.
func (e *emitter) resolveMarker(d string) []string {
	parts := strings.Split(d[1:], "\x01")
	methodDep := func(base, m string) []string {
		cn := methodCName(base, m)
		if _, ok := e.funcRefs[cn]; ok {
			return []string{cn}
		}
		return nil
	}
	switch parts[0] {
	case "t":
		return methodDep(parts[1], parts[2])
	case "m":
		if base, ok := e.methodBaseOfGlobal(parts[1]); ok {
			return methodDep(base, parts[2])
		}
		return nil
	case "u":
		return e.methodsByName[parts[1]]
	}
	return nil
}

// flattenDeps reduces one step's raw references to package-variable
// dependencies: a function reference stands for everything its body references,
// transitively, and a method marker for the methods it may name. Recursion
// among functions is no cycle -- the seen set closes it -- which is Go's rule
// too (probe g1).
func (e *emitter) flattenDeps(raw []string) []string {
	var out []string
	seen := map[string]bool{}
	var add func(string)
	add = func(d string) {
		if seen[d] {
			return
		}
		seen[d] = true
		if strings.HasPrefix(d, "\x01") {
			for _, m := range e.resolveMarker(d) {
				add(m)
			}
			return
		}
		if refs, isFn := e.funcRefs[d]; isFn {
			for _, r := range refs {
				add(r)
			}
			return
		}
		out = append(out, d)
	}
	for _, d := range raw {
		add(d)
	}
	return out
}

// reportInitCycle reports a cycle among the package variables' initializers,
// which is what the ordering pass cannot place. Go refuses such a program --
// there is no order that makes every initializer see the value it reads -- and
// the trace is Go's, walked over the RAW reference graph so it goes through the
// functions and methods the way Go's does: "a refers to f", "f refers to a".
func (e *emitter) reportInitCycle(cyclic []int) {
	if len(cyclic) == 0 {
		return
	}
	// A step that assigns nothing (a channel cell, a deferred statement)
	// provides no name, so it cannot be part of a cycle; it only ends up here by
	// depending on something that is. The report is about the variables.
	stepAt := map[string]int{}
	for i, st := range e.pkgInit {
		if st.target != "" && st.srcName != "" {
			stepAt[st.target] = i
		}
	}
	var starts []string
	for _, i := range cyclic {
		t := e.pkgInit[i].target
		if _, named := stepAt[t]; named {
			starts = append(starts, t)
		}
	}
	if len(starts) == 0 {
		return // nothing to name: leave it to whatever else the build reports
	}
	edges := func(node string) []string {
		var raw []string
		if i, isStep := stepAt[node]; isStep {
			raw = e.pkgInit[i].deps
		} else {
			raw = e.funcRefs[node]
		}
		var out []string
		for _, d := range raw {
			if strings.HasPrefix(d, "\x01") {
				out = append(out, e.resolveMarker(d)...)
				continue
			}
			if _, isStep := stepAt[d]; isStep {
				out = append(out, d)
				continue
			}
			if _, isFn := e.funcRefs[d]; isFn {
				out = append(out, d)
			}
		}
		return out
	}
	// Depth-first from a cyclic variable until the path re-enters itself. A ring
	// with no variable in it is function recursion, which is legal; keep looking
	// past it. A barrier stall marks later packages' steps cyclic too, so each
	// candidate is tried until one closes a real ring.
	var ring []string
	var onPath map[string]int
	var path []string
	var dfs func(node string) bool
	dfs = func(node string) bool {
		for _, d := range edges(node) {
			if at, on := onPath[d]; on {
				candidate := append(slices.Clone(path[at:]), d)
				hasVar := false
				for _, n := range candidate {
					if _, isStep := stepAt[n]; isStep {
						hasVar = true
						break
					}
				}
				if hasVar {
					ring = candidate
					return true
				}
				continue
			}
			onPath[d] = len(path)
			path = append(path, d)
			if dfs(d) {
				return true
			}
			path = path[:len(path)-1]
			delete(onPath, d)
		}
		return false
	}
	start := starts[0]
	for _, st := range starts {
		onPath = map[string]int{st: 0}
		path = []string{st}
		if dfs(st) {
			start = st
			break
		}
	}
	nameOf := func(node string) (srcName, pos string) {
		if i, isStep := stepAt[node]; isStep {
			return e.pkgInit[i].srcName, e.pkgInit[i].pos
		}
		m := e.funcRefMeta[node]
		return m.srcName, m.pos
	}
	if ring == nil {
		src, pos := nameOf(start)
		e.fail("%s: initialization cycle for %s", pos, src)
		return
	}
	// Rotate the ring so a variable leads: Go's headline names one.
	for i, n := range ring[:len(ring)-1] {
		if _, isStep := stepAt[n]; isStep {
			ring = append(ring[i:len(ring)-1], ring[:i+1]...)
			break
		}
	}
	firstSrc, firstPos := nameOf(ring[0])
	if len(ring) == 2 && ring[0] == ring[1] {
		e.fail("%s: initialization cycle: %s refers to itself", firstPos, firstSrc)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: initialization cycle for %s", firstPos, firstSrc)
	for i := 0; i+1 < len(ring); i++ {
		fromSrc, fromPos := nameOf(ring[i])
		toSrc, _ := nameOf(ring[i+1])
		fmt.Fprintf(&b, "\n\t%s: %s refers to %s", fromPos, fromSrc, toSrc)
	}
	e.fail("%s", b.String())
}
