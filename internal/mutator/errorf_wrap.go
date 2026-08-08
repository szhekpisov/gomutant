package mutator

import (
	"go/ast"
	"go/token"
)

type errorfWrap struct{}

func (e *errorfWrap) Type() MutationType { return ErrorfWrap }

// Discover downgrades the error-wrapping verb `%w` to `%v` in Errorf-style
// calls. The formatted message is byte-for-byte identical, but the returned
// error no longer wraps its cause, so `errors.Is` and `errors.As` against the
// original stop matching.
//
// That makes it a narrow probe for one thing: whether any test actually
// unwraps the error, or only asserts on its text. A suite that checks
// `err.Error()` and nothing else cannot kill this mutant, which is exactly
// the gap worth reporting.
func (e *errorfWrap) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isErrorfCall(call.Fun) {
			return true
		}
		lit, ok := firstStringLit(call.Args)
		if !ok {
			return true
		}
		// lit.Value is the literal's raw source text, opening quote
		// included, so an index into it is an offset from lit.Pos().
		// Deriving each candidate's position with token.Pos arithmetic
		// keeps line/column right even inside a multi-line raw string.
		for _, rel := range wrapVerbOffsets(lit.Value) {
			pos := fset.Position(lit.Pos() + token.Pos(rel))
			candidates = append(candidates, MutantCandidate{
				Type:        ErrorfWrap,
				Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
				Original:    "%w",
				Replacement: "%v",
				StartOffset: pos.Offset,
				EndOffset:   pos.Offset + len("%w"),
			})
		}
		return true
	})
	return candidates
}

// isErrorfCall reports whether fun names an Errorf-style function.
//
// Matching is syntactic, with no type resolution, so it covers `fmt.Errorf`
// alongside the drop-in replacements that share the verb set — an aliased
// import, `xerrors.Errorf`, or a project's own error package. The cost of
// that breadth is bounded: a `%w` in something that is not a wrapping Errorf
// is already a formatting bug, and mutating it changes the message either
// way.
func isErrorfCall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name == "Errorf"
	case *ast.Ident:
		return f.Name == "Errorf"
	}
	return false
}

// firstStringLit returns the first string-literal argument, which is the
// format string for both `Errorf(format, ...)` and the context-taking
// `Errorf(ctx, format, ...)` shape. Later string arguments are format
// *operands* — a `%w` inside one of those is literal text, and rewriting it
// would change the message without touching the wrapping.
func firstStringLit(args []ast.Expr) (*ast.BasicLit, bool) {
	for _, arg := range args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok {
			continue
		}
		if lit.Kind == token.STRING {
			return lit, true
		}
	}
	return nil, false
}

// wrapVerbOffsets returns the index of the `%` of every `%w` verb in s.
//
// `%%` is an escaped percent rather than the start of a verb, so it is
// consumed as a unit — otherwise `%%w` (a literal "%w" in the output) would
// be misread as a wrap verb. Flags and widths between the `%` and the verb
// are not handled, since `%w` takes none in practice; the effect of that
// simplification is a missed mutant, never a wrong one.
//
// The scan compares the two-byte window instead of testing for a leading `%`
// and then reading a lookahead byte. Once the code has established that s[i]
// is a `%`, a lookahead index is pinned to a value it already knows, and a
// mutation folding that index back onto s[i] is one no test can distinguish.
func wrapVerbOffsets(s string) []int {
	var out []int
	for i := 0; i+1 < len(s); i++ {
		pair := s[i : i+2]
		if pair == "%w" {
			out = append(out, i)
		}
		if pair == "%%" {
			i++
		}
	}
	return out
}
