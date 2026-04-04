package mutator

import (
	"go/ast"
	"go/token"
)

type branchCase struct{}

func (b *branchCase) Type() MutationType { return BranchCase }

func (b *branchCase) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		if len(cc.Body) == 0 {
			return true
		}
		// Replace the body statements with a noop.
		// Body starts after the colon, ends at the last statement's end.
		firstStmt := cc.Body[0]
		lastStmt := cc.Body[len(cc.Body)-1]
		startPos := fset.Position(firstStmt.Pos())
		endPos := fset.Position(lastStmt.End())
		startOffset := startPos.Offset
		endOffset := endPos.Offset
		original := string(src[startOffset:endOffset])
		candidates = append(candidates, MutantCandidate{
			Type:        BranchCase,
			Pos:         Position{Filename: startPos.Filename, Line: startPos.Line, Column: startPos.Column, Offset: startOffset},
			Original:    original,
			Replacement: "_ = 0",
			StartOffset: startOffset,
			EndOffset:   endOffset,
		})
		return true
	})
	return candidates
}
