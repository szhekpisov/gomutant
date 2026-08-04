package mutator

import (
	"go/ast"
	"go/token"
)

// resultTypes flattens a function's result list into one entry per return
// slot, so the expression at ret.Results[i] can be matched against the type
// it must satisfy. A grouped field contributes one entry per name:
// `func f() (a, b error)` is one *ast.Field with two Names and yields two
// entries, while `func f() (int, error)` is two Fields with no Names and
// also yields two.
//
// Returns nil for a function with no results, which makes every return
// statement in it length-mismatched and therefore skipped.
func resultTypes(ft *ast.FuncType) []ast.Expr {
	if ft.Results == nil {
		return nil
	}
	var out []ast.Expr
	for _, f := range ft.Results.List {
		slots := len(f.Names)
		if slots == 0 {
			// Unnamed result: the field is exactly one slot.
			slots = 1
		}
		for range slots {
			out = append(out, f.Type)
		}
	}
	return out
}

// returnSites calls fn for every *ast.ReturnStmt in file, paired with the
// result types of the function or function literal that lexically encloses
// it.
//
// The attribution is what makes this non-trivial: a `return` inside a
// closure belongs to the closure's signature, not the enclosing
// declaration's. Rather than maintain a stack across ast.Inspect's
// post-order nil callbacks, the walk is nested — the outer Inspect visits
// every func decl and func literal in the file and computes that one
// function's slots, while the inner Inspect collects its returns but stops
// at any nested *ast.FuncLit. Each literal is then reached independently by
// the outer walk and handled against its own result list.
func returnSites(file *ast.File, fn func(ret *ast.ReturnStmt, results []ast.Expr)) {
	ast.Inspect(file, func(n ast.Node) bool {
		var ft *ast.FuncType
		var body *ast.BlockStmt
		switch d := n.(type) {
		case *ast.FuncDecl:
			ft, body = d.Type, d.Body
		case *ast.FuncLit:
			ft, body = d.Type, d.Body
		default:
			return true
		}
		if body == nil {
			// Declaration without a Go body (assembly- or linkname-backed).
			return true
		}
		results := resultTypes(ft)
		ast.Inspect(body, func(m ast.Node) bool {
			if _, ok := m.(*ast.FuncLit); ok {
				// Belongs to that literal's signature; the outer walk
				// reaches it with its own results.
				return false
			}
			if ret, ok := m.(*ast.ReturnStmt); ok {
				fn(ret, results)
			}
			return true
		})
		return true
	})
}

// declaredTypeNames returns the package-level type names declared in file.
//
// Go's predeclared identifiers (`error`, `bool`, `string`, `int`, …) are not
// reserved words — a package may declare its own type of the same name. The
// return-value mutators identify slot types purely by identifier text, so a
// redeclared name must not be treated as the predeclared one.
//
// Only this file's declarations are visible: Mutator.Discover receives a
// single *ast.File, so a shadow declared in a sibling file of the same
// package is missed. The consequence is a mutant that fails to compile and
// is reported NOT VIABLE, never a wrong result.
func declaredTypeNames(file *ast.File) map[string]bool {
	out := make(map[string]bool)
	for _, d := range file.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			// A token.TYPE GenDecl holds only *ast.TypeSpec, and Discover
			// only ever sees files that parsed without error, so the
			// assertion needs no comma-ok guard (which would be a branch no
			// test could reach).
			out[spec.(*ast.TypeSpec).Name.Name] = true
		}
	}
	return out
}
