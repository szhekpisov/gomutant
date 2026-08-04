package mutator

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// slotCtx is everything a predicate or replacement needs to reason about one
// return slot: the slot's declared type expression, the predeclared names the
// enclosing file shadows, and the fset/source needed to recover the type's
// original text.
type slotCtx struct {
	typ      ast.Expr
	expr     ast.Expr // the returned expression this slot would replace
	shadowed map[string]bool
	fset     *token.FileSet
	src      []byte
}

// predeclared reports whether the slot's type is the predeclared identifier
// name — an unqualified ident that the file does not itself redeclare.
func (c slotCtx) predeclared(name string) bool {
	id, ok := c.typ.(*ast.Ident)
	return ok && id.Name == name && !c.shadowed[name]
}

// typeText returns the slot type exactly as written in the source. Because
// the type is spelled in the enclosing function's signature, that text is by
// construction a valid type expression at every return inside it — which is
// what lets zeroValueExpr fall back to *new(T) without resolving imports or
// consulting go/types.
func (c slotCtx) typeText() string {
	start := c.fset.Position(c.typ.Pos()).Offset
	end := c.fset.Position(c.typ.End()).Offset
	return string(c.src[start:end])
}

// returnValue is the single Mutator implementation behind the four
// return-value mutators (RETURN_ERROR_NIL / RETURN_ZERO / RETURN_TRUE /
// RETURN_FALSE). Each registered instance carries the (MutationType, slot
// predicate, replacement) triple it stamps onto its candidates; the walk that
// pairs return expressions with declared result types is shared, and lives in
// returnslots.go.
//
// The four owns predicates partition the slot space by declared type — error,
// bool, everything else — so no two instances ever emit a candidate on the
// same span. That disjointness is deliberate: overlapping return mutators
// would produce byte-identical patches under two different names, inflating
// the mutant count without testing anything new.
//
// Collapsing the four into one type keeps the behaviour identical to separate
// structs while avoiding the duplication SonarCloud's new-code gate flags.
type returnValue struct {
	typ MutationType
	// owns reports whether this mutator is responsible for a slot with the
	// given declared type.
	owns func(slotCtx) bool
	// replacement returns the substitute source text for such a slot, or
	// ok=false to decline the slot because no useful mutation exists.
	replacement func(slotCtx) (string, bool)
}

func (m *returnValue) Type() MutationType { return m.typ }

// Discover emits a mutant for every return expression whose slot this mutator
// owns, replacing just that expression's span with the substitute value.
//
// Two shapes are skipped wholesale, both via the length comparison:
//   - a bare `return` under named results, which carries no expression to
//     replace (mutating it would need `err = nil; return`, a statement
//     rewrite rather than a span swap);
//   - `return f()` spreading one call across several slots, where no per-slot
//     span exists to replace.
//
// A candidate whose replacement matches the source text it would overwrite is
// dropped: the patched file would be byte-identical to the original, giving a
// LIVED mutant no test could ever kill. This is what keeps RETURN_TRUE and
// RETURN_FALSE from colliding on a literal `return true` / `return false`,
// and what skips the already-propagating `return nil` for RETURN_ERROR_NIL.
func (m *returnValue) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	shadowed := declaredTypeNames(file)
	var out []MutantCandidate
	returnSites(file, func(ret *ast.ReturnStmt, results []ast.Expr) {
		if len(ret.Results) != len(results) {
			return
		}
		for i, expr := range ret.Results {
			ctx := slotCtx{typ: results[i], expr: expr, shadowed: shadowed, fset: fset, src: src}
			if !m.owns(ctx) {
				continue
			}
			replacement, ok := m.replacement(ctx)
			if !ok {
				continue
			}
			pos := fset.Position(expr.Pos())
			endOffset := fset.Position(expr.End()).Offset
			original := string(src[pos.Offset:endOffset])
			if original == replacement {
				continue
			}
			out = append(out, MutantCandidate{
				Type:        m.typ,
				Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
				Original:    original,
				Replacement: replacement,
				StartOffset: pos.Offset,
				EndOffset:   endOffset,
			})
		}
	})
	return out
}

// ownsError matches slots declared as the predeclared `error`.
func ownsError(c slotCtx) bool { return c.predeclared("error") }

// ownsBool matches slots declared as the predeclared `bool`. A named boolean
// type (`type Flag bool`) is deliberately excluded — it falls to ownsOther,
// where *new(Flag) is unambiguous, rather than producing a bare `true` whose
// readability depends on the reader knowing Flag's underlying type.
func ownsBool(c slotCtx) bool { return c.predeclared("bool") }

// ownsOther matches every slot the other two don't, keeping the partition
// total so no return slot goes unmutated.
func ownsOther(c slotCtx) bool { return !ownsError(c) && !ownsBool(c) }

// fixed builds a replacement that always substitutes the same literal text,
// used by the three mutators whose value doesn't depend on the slot type.
func fixed(text string) func(slotCtx) (string, bool) {
	return func(slotCtx) (string, bool) { return text, true }
}

// isSyntacticZero reports whether expr is visibly already a zero value.
//
// It exists because the *new(T) fallback spells a zero value differently from
// how source normally writes one: `Block{}` and `*new(Block)` are the same
// value, as are `0` and `*new(time.Duration)`. The phantom guard in Discover
// compares source text, so it cannot see that — without this check those slots
// yield mutants that are byte-different but semantically identical, and no
// test can ever kill them.
//
// Only shapes whose zero-ness is certain from syntax count. An empty composite
// literal qualifies only for the types that reach the *new(T) fallback (named
// types, structs, fixed arrays): for a slice or map, `[]int{}` and `map[k]v{}`
// are non-nil and genuinely differ from the nil this mutator would emit, and
// those types never reach the fallback anyway.
func isSyntacticZero(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return len(e.Elts) == 0
	case *ast.Ident:
		return e.Name == "nil" || e.Name == "false"
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT, token.FLOAT:
			v, err := strconv.ParseFloat(strings.ReplaceAll(e.Value, "_", ""), 64)
			return err == nil && v == 0
		case token.STRING:
			return e.Value == `""` || e.Value == "``"
		}
	}
	return false
}

// zeroLiterals maps a predeclared type name to the shortest source text for
// its zero value.
//
// `bool` and `error` are absent by design rather than by oversight: those
// slots belong to RETURN_TRUE/RETURN_FALSE and RETURN_ERROR_NIL, so ownsOther
// never routes one here and an entry for either would be dead.
var zeroLiterals = map[string]string{
	"any":        "nil",
	"string":     `""`,
	"int":        "0",
	"int8":       "0",
	"int16":      "0",
	"int32":      "0",
	"int64":      "0",
	"uint":       "0",
	"uint8":      "0",
	"uint16":     "0",
	"uint32":     "0",
	"uint64":     "0",
	"uintptr":    "0",
	"byte":       "0",
	"rune":       "0",
	"float32":    "0",
	"float64":    "0",
	"complex64":  "0",
	"complex128": "0",
}

// zeroValueExpr renders the zero value of a slot's declared type as source
// text, using the shortest spelling that reads naturally and falling back to
// *new(T) for everything else.
//
// The fallback is what makes the whole approach work without type
// information: named types, package-qualified types, structs, fixed-size
// arrays, generic instantiations and type parameters all have a zero value
// that is awkward or impossible to spell directly, but *new(T) is valid for
// every one of them and needs no import that the signature hasn't already
// required.
func zeroValueExpr(c slotCtx) (string, bool) {
	switch t := c.typ.(type) {
	case *ast.ArrayType:
		if t.Len == nil {
			// Slice. A fixed-size array has no nil, and falls through.
			return "nil", true
		}
	case *ast.Ident:
		// A file that redeclares the name means its own type, whose zero
		// value the literal spelling would misrepresent.
		if lit, ok := zeroLiterals[t.Name]; ok && !c.shadowed[t.Name] {
			return lit, true
		}
	case *ast.StarExpr, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType:
		return "nil", true
	}
	if isSyntacticZero(c.expr) {
		// Already the zero value, written another way — see isSyntacticZero.
		return "", false
	}
	return "*new(" + c.typeText() + ")", true
}
