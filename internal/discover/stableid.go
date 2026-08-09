package discover

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// funcSpan is one top-level function's byte range in a source file,
// paired with the name used to anchor stable mutant IDs.
type funcSpan struct {
	start int // Byte offset of the `func` keyword.
	end   int // Byte offset just past the closing brace (exclusive).
	name  string
}

// funcSpans returns the top-level function declarations of f in source
// order. Function literals are not listed: a closure's mutants anchor to
// the enclosing declaration, which is what makes the anchor survive edits
// that add or remove closures.
//
// Declarations that render to the same name — two `func init()`, or two
// methods whose receiver shape receiverType cannot render — are given
// distinct anchors by occurrence, so they do not share an ordinal counter.
func funcSpans(fset *token.FileSet, f *ast.File) []funcSpan {
	var spans []funcSpan
	occurrences := make(map[string]int)
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := funcName(fn)
		occurrences[name]++
		spans = append(spans, funcSpan{
			start: fset.Position(fn.Pos()).Offset,
			end:   fset.Position(fn.End()).Offset,
			name:  anchorName(name, occurrences[name]),
		})
	}
	return spans
}

// anchorRepeatSep joins a repeated declaration name to its occurrence
// number. It is deliberately a character Go identifiers cannot contain, so
// the disambiguated anchor of one declaration can never collide with the
// plain name of another.
const anchorRepeatSep = "~"

// anchorName renders the anchor for the occurrence'th declaration sharing
// name in a file. The first occurrence keeps the bare name: only files that
// actually repeat a name pay the suffix, which keeps IDs unchanged for the
// overwhelmingly common case of every declaration being uniquely named.
func anchorName(name string, occurrence int) string {
	if occurrence == 1 {
		return name
	}
	return name + anchorRepeatSep + strconv.Itoa(occurrence)
}

// funcName renders a declaration's anchor name: "Run" for a plain
// function, "(*Runner).Run" or "(Runner).Run" for a method. Including the
// receiver keeps same-named methods on different types in one file apart.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + receiverType(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

// receiverType renders a receiver's type as it appears in source, minus
// any type parameters: `*Runner` → "*Runner", `Stack[T]` → "Stack".
// An unrecognized shape yields "", so the anchor degrades to "()." rather
// than dropping the method's identity entirely.
func receiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + receiverType(t.X)
	case *ast.IndexExpr: // Generic receiver: Stack[T].
		return receiverType(t.X)
	case *ast.IndexListExpr: // Generic receiver: Pair[K, V].
		return receiverType(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return ""
	}
}

// anchorFor returns the name of the function containing offset, or "" when
// the offset falls outside every span — package-level var and const
// initializers are the usual case. spans must be in source order, which is
// what funcSpans returns.
func anchorFor(spans []funcSpan, offset int) string {
	// Index of the first span starting after offset; the candidate span is
	// therefore the one before it.
	i := sort.Search(len(spans), func(i int) bool {
		return spans[i].start > offset
	})
	if i == 0 {
		return ""
	}
	s := spans[i-1]
	if offset < s.end {
		return s.name
	}
	return ""
}

// ordinalKey groups mutants that a stable ID cannot otherwise tell apart.
// It holds exactly the fields stableID renders, which is what guarantees
// the rendered strings are unique within a run.
type ordinalKey struct {
	file   string
	anchor string
	typ    mutator.MutationType
}

// stableID renders a mutant's cross-run identity.
func stableID(k ordinalKey, ordinal int) string {
	return fmt.Sprintf("%s:%s:%s#%d", k.file, k.anchor, k.typ, ordinal)
}

// stableIDFile renders the file segment of a stable ID: absPath relative to
// the module root, always slash-separated so an ID minted on Windows matches
// one minted on Linux.
//
// This is deliberately not mutator.Mutant.RelFile. RelFile strips the
// longest common import-path prefix of the packages in the run, so it is a
// function of the package arguments rather than of the source: the same
// mutant would be "a.go:F:T#1" under `gomutants ./internal/a/` and
// "internal/a/a.go:F:T#1" under `gomutants ./...`, and adding a new
// top-level package would renumber every ID in a whole-repo report. Anchoring
// to the module root keeps one mutant's ID the same under every scope.
//
// A path outside moduleRoot (or an empty moduleRoot) has no relative form;
// falling back to absPath keeps the ID unique within the report, which is
// what the ordinal counter depends on.
func stableIDFile(absPath, moduleRoot string) string {
	rel, err := filepath.Rel(moduleRoot, absPath)
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	return filepath.ToSlash(rel)
}
