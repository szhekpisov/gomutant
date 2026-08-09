package mutator

import (
	"go/ast"
	"go/token"
)

type removeLogicalNot struct{}

func (r *removeLogicalNot) Type() MutationType { return RemoveLogicalNot }

// Discover drops the `!` from logical-negation expressions, so `if !ok`
// becomes `if ok`. Where CONDITIONALS_NEGATION reaches only binary
// comparisons, this reaches negated identifiers, calls, and field
// selections — `!ok`, `!strings.HasPrefix(s, p)`, `!cfg.Enabled` — which
// no other mutator touches.
func (r *removeLogicalNot) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		unary, ok := n.(*ast.UnaryExpr)
		if !ok {
			return true
		}
		if unary.Op != token.NOT {
			return true
		}
		if negatesComparison(unary.X) {
			return true
		}
		pos := fset.Position(unary.OpPos)
		candidates = append(candidates, MutantCandidate{
			Type:        RemoveLogicalNot,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
			Original:    "!",
			Replacement: "",
			// `!` is a single byte at OpPos; deleting just that byte leaves
			// any spacing between it and the operand intact, so `! x`
			// patches to ` x` rather than needing the operand re-emitted.
			StartOffset: pos.Offset,
			EndOffset:   pos.Offset + 1,
		})
		return true
	})
	return candidates
}

// negatesComparison reports whether e — after unwrapping parentheses — is a
// comparison whose operator CONDITIONALS_NEGATION already inverts.
//
// Dropping the `!` from `!(a == b)` yields `a == b`, and negating the inner
// operator yields `!(a != b)`, which is the same thing. Emitting both would
// put two mutants with identical behaviour in the efficacy denominator: they
// live and die together, so the second one measures nothing and only dilutes
// the score.
func negatesComparison(e ast.Expr) bool {
	bin, ok := unwrapParens(e).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	_, ok = negationSwaps[bin.Op]
	return ok
}

// unwrapParens strips any layers of parentheses from e. Written as recursion
// rather than a loop: the loop form has no exit condition a mutation can
// leave intact, so every mutant on it spins forever and reports a timeout
// instead of a verdict.
func unwrapParens(e ast.Expr) ast.Expr {
	paren, ok := e.(*ast.ParenExpr)
	if !ok {
		return e
	}
	return unwrapParens(paren.X)
}
