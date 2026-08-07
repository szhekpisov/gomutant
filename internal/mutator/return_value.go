package mutator

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// slotCtx is everything a predicate or replacement needs to reason about one
// return slot: the slot's declared type expression, the type declarations the
// enclosing file makes, and the fset/source needed to recover a type's
// original text.
type slotCtx struct {
	typ  ast.Expr
	expr ast.Expr // the returned expression this slot would replace
	// declared maps each type name the file declares to the type it is
	// declared as. Membership answers "does this file shadow a predeclared
	// name"; the value answers "what does this name actually resolve to".
	declared map[string]ast.Expr
	fset     *token.FileSet
	src      []byte
}

// predeclared reports whether the slot's type is the predeclared identifier
// name — an unqualified ident that the file does not itself redeclare.
func (c slotCtx) predeclared(name string) bool {
	id, ok := c.typ.(*ast.Ident)
	_, shadowed := c.declared[name]
	return ok && id.Name == name && !shadowed
}

// text returns e exactly as written in the source.
func (c slotCtx) text(e ast.Expr) string {
	start := c.fset.Position(e.Pos()).Offset
	end := c.fset.Position(e.End()).Offset
	return string(c.src[start:end])
}

// typeText returns the slot type exactly as written in the source. Because
// the type is spelled in the enclosing function's signature, that text is by
// construction a valid type expression at every return inside it — which is
// what lets zeroValueExpr fall back to *new(T) without resolving imports or
// consulting go/types.
func (c slotCtx) typeText() string { return c.text(c.typ) }

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
//
// The span mutated is the expression with any enclosing parentheses stripped,
// so that guard compares like with like: `(true)` is different source text
// from `true` and would otherwise slip past it, leaving RETURN_TRUE an
// unkillable rewrite of `return (true)` into itself. Narrowing the span also
// keeps the patch minimal — RETURN_FALSE emits `return (false)`.
func (m *returnValue) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	declared := declaredTypes(file)
	var out []MutantCandidate
	returnSites(file, func(ret *ast.ReturnStmt, results []ast.Expr) {
		if len(ret.Results) != len(results) {
			return
		}
		for i, slot := range ret.Results {
			expr := ast.Unparen(slot)
			ctx := slotCtx{typ: results[i], expr: expr, declared: declared, fset: fset, src: src}
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
// Only shapes whose zero-ness is certain from syntax count, which for an empty
// composite literal takes more than counting its elements — see emptyLitIsZero.
func isSyntacticZero(c slotCtx) bool {
	switch e := c.expr.(type) {
	case *ast.CompositeLit:
		return len(e.Elts) == 0 && c.emptyLitIsZero(e)
	case *ast.Ident:
		return e.Name == "nil" || e.Name == "false"
	case *ast.BasicLit:
		return isZeroLit(e)
	}
	return false
}

// emptyLitIsZero reports whether an empty composite literal is the zero value
// of the slot it is being returned into, and so denotes the same value as the
// *new(T) this mutator would put in its place.
//
// Two independent things have to hold, and an empty element list alone implies
// neither.
//
// The literal must be spelled as the slot's own type. `return Impl{}` in a
// slot declared as an interface is a non-nil interface holding a zero Impl,
// which differs from the nil that *new(I) yields.
//
// The type must also not be a slice or map. Those are the two composite-literal
// kinds whose empty literal is not their zero value: `S{}` is a non-nil empty
// slice and `M{}` a non-nil empty map, while *new(S) and *new(M) are both nil.
// The difference is observable under ==, under reflect.DeepEqual, in JSON
// ([] versus null), and for a map by writing to it — the nil one panics.
//
// An unnamed slice or map never reaches this check, because zeroValueExpr
// answers *ast.ArrayType and *ast.MapType with a plain nil before the fallback.
// A named one does, which is what makes resolving the name necessary.
func (c slotCtx) emptyLitIsZero(lit *ast.CompositeLit) bool {
	// A composite literal at the top of a return statement always carries its
	// type; the elided form exists only nested inside another literal.
	return c.text(lit.Type) == c.typeText() && !c.nilableType(c.typ)
}

// nilableType reports whether typ resolves, through this file's own type
// declarations, to a slice or map.
//
// Only same-file declarations resolve, matching the single-file scope the rest
// of this mutator works in. A named slice or map declared in a sibling file or
// another package stays unresolved and keeps the suppressing default: that
// loses a mutant, where the opposite default would emit an equivalent one, and
// an equivalent mutant can never be cleared from a report while a missing one
// costs only coverage.
func (c slotCtx) nilableType(typ ast.Expr) bool {
	seen := make(map[string]bool)
	for {
		switch t := typ.(type) {
		case *ast.ArrayType:
			// A slice has no length; a fixed-size array is not nilable.
			return t.Len == nil
		case *ast.MapType:
			return true
		case *ast.Ident:
			// Guard the chain `type A B; type B A`. It cannot compile, but it
			// parses, and Discover is only promised a file that parsed.
			if seen[t.Name] {
				return false
			}
			seen[t.Name] = true
			// A name this file does not declare reads back as a nil
			// expression, which the default arm answers on the next turn —
			// so an explicit "not found" branch here would be dead weight.
			typ = c.declared[t.Name]
		default:
			// Includes the nil left by an unresolved name above, and every
			// type that has no composite literal to begin with.
			return false
		}
	}
}

// isZeroLit reports whether a basic literal denotes zero, in every form Go
// can spell one: 0, 00, 0b0, 0x0, 0.0, 0e10, 0x0p0, 0i, a NUL rune literal,
// and both the interpreted and raw empty string.
//
// The three numeric kinds share a path because they share a grammar — an
// imaginary literal is its real counterpart plus a trailing `i`, and zero
// scaled by i is still zero.
func isZeroLit(lit *ast.BasicLit) bool {
	switch lit.Kind {
	case token.INT, token.FLOAT, token.IMAG:
		// Digit separators carry no value and strconv rejects them; the
		// imaginary suffix is not part of the number either. Neither rewrite
		// is safe for the quoted kinds — an underscore rune literal and
		// "a_b" would both change — so this is not hoisted out of the switch.
		text := strings.TrimSuffix(strings.ReplaceAll(lit.Value, "_", ""), "i")
		// Both parsers are consulted because neither subsumes the other: only
		// ParseInt accepts the binary and octal prefixes (`0b0`, `0o0`, `00`),
		// and only ParseFloat accepts the fractional, exponent and hex-float
		// forms (`0.0`, `0e10`, `0x0p0`). Each contributes a zero verdict
		// solely when it parsed the text it was handed — ParseInt yields 0 on
		// a syntax error, so an unguarded `n == 0` would call `1.5` zero.
		//
		// gomutants:disable-next-line INTEGER_DECREMENT reason="ParseInt's bitSize only has to admit zero, which 63 and 64 both do, so decrementing it is observably identical (incrementing past 64 is rejected outright, and is killed)"
		n, intErr := strconv.ParseInt(text, 0, 64)
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="strconv.ParseFloat's bitSize argument only branches at 32 vs ≠32 — values 63/64/65 all use the float64 parser, so mutating 64 is observably identical"
		f, floatErr := strconv.ParseFloat(text, 64)
		return (intErr == nil && n == 0) || (floatErr == nil && f == 0)
	case token.CHAR:
		// Unquote resolves every escape form a NUL rune can be written in.
		// It cannot fail on a literal the parser accepted, and the ""
		// it returns on failure is not NUL either way, so the error needs no
		// branch of its own.
		s, _ := strconv.Unquote(lit.Value)
		return s == "\x00"
	}
	// STRING is the only other kind the parser puts in a BasicLit, so this is
	// the string arm rather than an unreachable default.
	return lit.Value == `""` || lit.Value == "``"
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

// spelledSameZero reports whether expr already denotes the value that lit —
// the shortest spelling of the slot type's zero — would substitute.
//
// It guards on the zeroLiterals path the equivalence isSyntacticZero guards on
// the *new(T) path. Discover's phantom check compares source text, so `0.0` in
// a float64 slot and a raw empty string in a string slot escape it while being
// the very value the patch installs, leaving a mutant no test can kill.
//
// Only basic literals qualify, and only against a concrete zero. The one
// zeroLiterals entry spelling nil is `any`, where the equivalence does not
// hold: an interface holding 0 is not a nil interface, so `return 0` ->
// `return nil` is a genuine mutation and must survive this guard.
func spelledSameZero(expr ast.Expr, lit string) bool {
	if lit == "nil" {
		return false
	}
	basic, ok := expr.(*ast.BasicLit)
	return ok && isZeroLit(basic)
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
		_, shadowed := c.declared[t.Name]
		if lit, ok := zeroLiterals[t.Name]; ok && !shadowed {
			if spelledSameZero(c.expr, lit) {
				return "", false
			}
			return lit, true
		}
	case *ast.StarExpr, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType:
		return "nil", true
	}
	if isSyntacticZero(c) {
		// Already the zero value, written another way — see isSyntacticZero.
		return "", false
	}
	return "*new(" + c.typeText() + ")", true
}
