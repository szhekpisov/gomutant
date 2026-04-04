package mutator

import (
	"go/ast"
	"go/token"
)

type conditionalsBoundary struct{}

func (c *conditionalsBoundary) Type() MutationType { return ConditionalsBoundary }

var boundarySwaps = map[token.Token]token.Token{
	token.LSS: token.LEQ,
	token.LEQ: token.LSS,
	token.GTR: token.GEQ,
	token.GEQ: token.GTR,
}

func (c *conditionalsBoundary) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		replacement, ok := boundarySwaps[bin.Op]
		if !ok {
			return true
		}
		pos := fset.Position(bin.OpPos)
		original := bin.Op.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        ConditionalsBoundary,
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
