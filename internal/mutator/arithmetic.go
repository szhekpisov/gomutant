package mutator

import (
	"go/ast"
	"go/token"
)

type arithmeticBase struct{}

func (a *arithmeticBase) Type() MutationType { return ArithmeticBase }

// arithmeticSwaps maps each arithmetic operator to a single replacement.
// Asymmetric by design: REM → MUL catches divisor/remainder confusion,
// but MUL → REM isn't a useful mutation because Go's `%` operator is
// undefined on float operands — the mutant would be a compile error on
// any code that multiplies floats, and gomutant already classifies those
// as NotViable (no signal for the test suite to catch). Keep only one side.
var arithmeticSwaps = map[token.Token]token.Token{
	token.ADD: token.SUB,
	token.SUB: token.ADD,
	token.MUL: token.QUO,
	token.QUO: token.MUL,
	token.REM: token.MUL,
}

func (a *arithmeticBase) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		replacement, ok := arithmeticSwaps[bin.Op]
		if !ok {
			return true
		}
		pos := fset.Position(bin.OpPos)
		original := bin.Op.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        ArithmeticBase,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
			Original:    original,
			Replacement: repl,
			StartOffset: pos.Offset,
			EndOffset:   pos.Offset + len(original),
		})
		return true
	})
	return candidates
}
