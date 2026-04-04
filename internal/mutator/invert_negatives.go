package mutator

import (
	"go/ast"
	"go/token"
)

type invertNegatives struct{}

func (i *invertNegatives) Type() MutationType { return InvertNegatives }

func (i *invertNegatives) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		unary, ok := n.(*ast.UnaryExpr)
		if !ok {
			return true
		}
		if unary.Op != token.SUB {
			return true
		}
		pos := fset.Position(unary.OpPos)
		candidates = append(candidates, MutantCandidate{
			Type:        InvertNegatives,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
			Original:    "-",
			Replacement: "+",
			StartOffset: pos.Offset,
			EndOffset:   pos.Offset + 1,
		})
		return true
	})
	return candidates
}
