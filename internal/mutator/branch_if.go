package mutator

import (
	"go/ast"
	"go/token"
)

type branchIf struct{}

func (b *branchIf) Type() MutationType { return BranchIf }

func (b *branchIf) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		body := ifStmt.Body
		if body == nil || len(body.List) == 0 {
			return true
		}
		pos := fset.Position(body.Lbrace)
		startOffset := pos.Offset
		endOffset := fset.Position(body.Rbrace).Offset + 1 // include '}'
		original := string(src[startOffset:endOffset])
		candidates = append(candidates, MutantCandidate{
			Type:        BranchIf,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: startOffset},
			Original:    original,
			Replacement: "{ _ = 0 }",
			StartOffset: startOffset,
			EndOffset:   endOffset,
		})
		return true
	})
	return candidates
}
