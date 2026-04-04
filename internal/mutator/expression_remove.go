package mutator

import (
	"go/ast"
	"go/token"
)

type expressionRemove struct{}

func (e *expressionRemove) Type() MutationType { return ExpressionRemove }

func (e *expressionRemove) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}

		var identity string
		switch bin.Op {
		case token.LAND: // &&
			identity = "true"
		case token.LOR: // ||
			identity = "false"
		default:
			return true
		}

		// Mutation 1: replace left operand with identity element.
		lPos := fset.Position(bin.X.Pos())
		lEnd := fset.Position(bin.X.End())
		lOriginal := string(src[lPos.Offset:lEnd.Offset])
		candidates = append(candidates, MutantCandidate{
			Type:        ExpressionRemove,
			Pos:         Position{Filename: lPos.Filename, Line: lPos.Line, Column: lPos.Column, Offset: lPos.Offset},
			Original:    lOriginal,
			Replacement: identity,
			StartOffset: lPos.Offset,
			EndOffset:   lEnd.Offset,
		})

		// Mutation 2: replace right operand with identity element.
		rPos := fset.Position(bin.Y.Pos())
		rEnd := fset.Position(bin.Y.End())
		rOriginal := string(src[rPos.Offset:rEnd.Offset])
		candidates = append(candidates, MutantCandidate{
			Type:        ExpressionRemove,
			Pos:         Position{Filename: rPos.Filename, Line: rPos.Line, Column: rPos.Column, Offset: rPos.Offset},
			Original:    rOriginal,
			Replacement: identity,
			StartOffset: rPos.Offset,
			EndOffset:   rEnd.Offset,
		})

		return true
	})
	return candidates
}
