package mutator

import (
	"go/ast"
	"go/token"
)

type incrementDecrement struct{}

func (i *incrementDecrement) Type() MutationType { return IncrementDecrement }

func (i *incrementDecrement) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		inc, ok := n.(*ast.IncDecStmt)
		if !ok {
			return true
		}
		var replacement token.Token
		switch inc.Tok {
		case token.INC:
			replacement = token.DEC
		case token.DEC:
			replacement = token.INC
		}
		pos := fset.Position(inc.TokPos)
		original := inc.Tok.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        IncrementDecrement,
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
