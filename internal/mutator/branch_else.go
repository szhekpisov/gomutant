package mutator

import (
	"go/ast"
	"go/token"
)

type branchElse struct{}

func (b *branchElse) Type() MutationType { return BranchElse }

func (b *branchElse) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		// Skip else-if chains (Else is *ast.IfStmt) — only mutate plain else blocks.
		// A nil Else also fails this type assertion (returns nil, false), so the
		// separate nil check is redundant.
		elseBlock, ok := ifStmt.Else.(*ast.BlockStmt)
		if !ok {
			return true
		}
		if len(elseBlock.List) == 0 {
			return true
		}
		pos := fset.Position(elseBlock.Lbrace)
		startOffset := pos.Offset
		endOffset := fset.Position(elseBlock.End()).Offset
		original := string(src[startOffset:endOffset])
		candidates = append(candidates, MutantCandidate{
			Type:        BranchElse,
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
