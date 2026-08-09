package discover

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// parseSrc parses src as a file named name and returns the AST alongside
// the FileSet, so span offsets resolve the same way Discover resolves them.
func parseSrc(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return fset, f
}

func TestFuncSpansSkipsNonFuncDecls(t *testing.T) {
	fset, f := parseSrc(t, "p.go", `package p

import "fmt"

var V = 1

type T struct{}

func One() {}

const C = 2

func Two() {}
`)

	spans := funcSpans(fset, f)
	if len(spans) != 2 {
		t.Fatalf("len(spans)=%d, want 2 (import/var/type/const decls must be skipped)", len(spans))
	}
	if spans[0].name != "One" || spans[1].name != "Two" {
		t.Errorf("names = %q, %q; want One, Two", spans[0].name, spans[1].name)
	}
	// Source order, non-overlapping, and each span must actually cover text.
	if spans[0].start >= spans[0].end {
		t.Errorf("spans[0] covers no text: %+v", spans[0])
	}
	if spans[0].end > spans[1].start {
		t.Errorf("spans overlap: %+v", spans)
	}
	if spans[1].start >= spans[1].end {
		t.Errorf("spans[1] covers no text: %+v", spans[1])
	}
}

func TestFuncSpansRecordsReceivers(t *testing.T) {
	fset, f := parseSrc(t, "p.go", `package p

type A struct{}
type B struct{}
type Stack[T any] struct{}
type Pair[K, V any] struct{}

func Plain() {}
func (a A) M() {}
func (b *B) M() {}
func (s Stack[T]) Push() {}
func (p *Pair[K, V]) Get() {}
`)

	want := []string{"Plain", "(A).M", "(*B).M", "(Stack).Push", "(*Pair).Get"}
	spans := funcSpans(fset, f)
	if len(spans) != len(want) {
		t.Fatalf("len(spans)=%d, want %d", len(spans), len(want))
	}
	for i, w := range want {
		if spans[i].name != w {
			t.Errorf("spans[%d].name=%q, want %q", i, spans[i].name, w)
		}
	}
}

func TestFuncNameWithoutReceiver(t *testing.T) {
	// A nil Recv and a present-but-empty Recv must both fall back to the
	// bare function name rather than rendering an empty "()." prefix.
	fn := &ast.FuncDecl{Name: ast.NewIdent("Run")}
	if got := funcName(fn); got != "Run" {
		t.Errorf("nil Recv: funcName=%q, want %q", got, "Run")
	}

	fn.Recv = &ast.FieldList{}
	if got := funcName(fn); got != "Run" {
		t.Errorf("empty Recv.List: funcName=%q, want %q", got, "Run")
	}
}

func TestReceiverTypeUnrecognized(t *testing.T) {
	// Any shape the switch doesn't know renders as empty, so the method
	// still gets an anchor ("().M") instead of merging with plain funcs.
	if got := receiverType(&ast.BasicLit{Value: "1"}); got != "" {
		t.Errorf("receiverType=%q, want empty", got)
	}

	fn := &ast.FuncDecl{
		Name: ast.NewIdent("M"),
		Recv: &ast.FieldList{List: []*ast.Field{{Type: &ast.BasicLit{Value: "1"}}}},
	}
	if got := funcName(fn); got != "().M" {
		t.Errorf("funcName=%q, want %q", got, "().M")
	}
}

func TestAnchorFor(t *testing.T) {
	spans := []funcSpan{
		{start: 10, end: 20, name: "f"},
		{start: 30, end: 40, name: "g"},
	}

	tests := []struct {
		name   string
		offset int
		want   string
	}{
		{"before first span", 5, ""},
		{"exactly at start of first span", 10, "f"},
		{"inside first span", 15, "f"},
		{"exactly at end of first span (exclusive)", 20, ""},
		{"between spans", 25, ""},
		{"exactly at start of second span", 30, "g"},
		{"inside second span", 35, "g"},
		{"past last span", 45, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anchorFor(spans, tt.offset); got != tt.want {
				t.Errorf("anchorFor(offset=%d)=%q, want %q", tt.offset, got, tt.want)
			}
		})
	}
}

func TestAnchorForNoSpans(t *testing.T) {
	// A file of only package-level declarations has no spans at all; every
	// offset must resolve to the empty anchor rather than indexing spans[-1].
	if got := anchorFor(nil, 0); got != "" {
		t.Errorf("anchorFor(nil, 0)=%q, want empty", got)
	}
	if got := anchorFor([]funcSpan{}, 100); got != "" {
		t.Errorf("anchorFor(empty, 100)=%q, want empty", got)
	}
}

func TestStableIDFormat(t *testing.T) {
	k := ordinalKey{file: "internal/cli/cli.go", anchor: "parseArgs", typ: mutator.ConditionalsBoundary}
	want := "internal/cli/cli.go:parseArgs:CONDITIONALS_BOUNDARY#2"
	if got := stableID(k, 2); got != want {
		t.Errorf("stableID=%q, want %q", got, want)
	}

	// An empty anchor (package-level declaration) leaves an empty segment
	// rather than collapsing the separators.
	k.anchor = ""
	want = "internal/cli/cli.go::CONDITIONALS_BOUNDARY#1"
	if got := stableID(k, 1); got != want {
		t.Errorf("stableID=%q, want %q", got, want)
	}
}

// discoverArithmetic runs Discover over a single-package tempdir with only
// the ARITHMETIC_BASE mutator enabled, which keeps expected IDs predictable.
func discoverArithmetic(t *testing.T, files map[string]string) []mutator.Mutant {
	t.Helper()
	dir := t.TempDir()
	names := make([]string, 0, len(files))
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	pkgs := []Package{{Dir: dir, ImportPath: "example.com/test/p", GoFiles: names}}
	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	return Discover(fset, pkgs, reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil), dir, "example.com/test").Mutants
}

func TestDiscoverStableIDAnchorsAndOrdinals(t *testing.T) {
	src := `package p

var V = 1 + 2

type A struct{}
type B struct{}

func (a A) Same() int { return 3 + 4 }

func (b *B) Same() int { return 5 + 6 }

func Plain() int {
	x := 7 + 8
	y := 9 + 10
	return x + y
}

func WithClosure() int {
	f := func() int { return 11 + 12 }
	return f()
}
`
	mutants := discoverArithmetic(t, map[string]string{"p.go": src})

	// One ARITHMETIC_BASE mutant per `+`, in (line, col) order. The anchors
	// pin three behaviors: package-level declarations get the empty anchor,
	// same-named methods stay apart via their receivers, and a closure's
	// mutant anchors to the enclosing declaration rather than the literal.
	want := []string{
		"::ARITHMETIC_BASE#1",            // var V = 1 + 2
		":(A).Same:ARITHMETIC_BASE#1",    // return 3 + 4
		":(*B).Same:ARITHMETIC_BASE#1",   // return 5 + 6
		":Plain:ARITHMETIC_BASE#1",       // x := 7 + 8
		":Plain:ARITHMETIC_BASE#2",       // y := 9 + 10
		":Plain:ARITHMETIC_BASE#3",       // return x + y
		":WithClosure:ARITHMETIC_BASE#1", // closure body: 11 + 12
	}
	if len(mutants) != len(want) {
		for _, m := range mutants {
			t.Logf("line %d col %d: %s", m.Line, m.Col, m.StableID)
		}
		t.Fatalf("len(mutants)=%d, want %d", len(mutants), len(want))
	}
	for i, suffix := range want {
		w := mutants[i].RelFile + suffix
		if mutants[i].StableID != w {
			t.Errorf("mutants[%d].StableID=%q, want %q", i, mutants[i].StableID, w)
		}
	}
}

func TestDiscoverStableIDsAreUniqueAndFileScoped(t *testing.T) {
	// Byte-identical files: every anchor, type and ordinal collides, so the
	// rel-file segment is the only thing keeping the IDs apart.
	src := `package p

func F() int {
	return 1 + 2
}
`
	mutants := discoverArithmetic(t, map[string]string{"a.go": src, "b.go": src})
	if len(mutants) != 2 {
		t.Fatalf("len(mutants)=%d, want 2", len(mutants))
	}

	seen := make(map[string]bool, len(mutants))
	for _, m := range mutants {
		if m.StableID == "" {
			t.Fatalf("mutant at %s:%d has empty StableID", m.RelFile, m.Line)
		}
		if seen[m.StableID] {
			t.Errorf("duplicate StableID %q", m.StableID)
		}
		seen[m.StableID] = true
		if !strings.HasSuffix(m.StableID, ":F:ARITHMETIC_BASE#1") {
			t.Errorf("StableID=%q, want the ordinal to reset per file", m.StableID)
		}
	}
}

func TestDiscoverStableIDsSurviveLineShifts(t *testing.T) {
	// The whole point of anchoring: inserting lines above a function must
	// not change the IDs of the mutants inside it.
	body := `
func F() int {
	return 1 + 2
}

func G() int {
	return 3 + 4
}
`
	before := discoverArithmetic(t, map[string]string{"p.go": "package p\n" + body})
	after := discoverArithmetic(t, map[string]string{
		"p.go": "package p\n\nvar Pad = \"inserted\"\n\n// a comment too\n" + body,
	})

	if len(before) != len(after) || len(before) != 2 {
		t.Fatalf("mutant counts: before=%d after=%d, want 2 each", len(before), len(after))
	}
	for i := range before {
		if before[i].Line == after[i].Line {
			t.Fatalf("mutants[%d] did not shift lines; the fixture no longer tests anything", i)
		}
		if before[i].StableID != after[i].StableID {
			t.Errorf("mutants[%d].StableID changed across a line shift: %q → %q",
				i, before[i].StableID, after[i].StableID)
		}
	}
}

func TestAnchorName(t *testing.T) {
	// The first occurrence is bare; only a repeat pays the suffix.
	if got := anchorName("init", 1); got != "init" {
		t.Errorf("anchorName(init, 1)=%q, want %q", got, "init")
	}
	if got := anchorName("init", 2); got != "init~2" {
		t.Errorf("anchorName(init, 2)=%q, want %q", got, "init~2")
	}
	if got := anchorName("(*B).Same", 3); got != "(*B).Same~3" {
		t.Errorf("anchorName((*B).Same, 3)=%q, want %q", got, "(*B).Same~3")
	}
}

func TestFuncSpansDisambiguatesRepeatedNames(t *testing.T) {
	// Two `func init()` are legal Go and render to the same name. Left
	// sharing an anchor they would also share an ordinal counter, so an
	// edit inside the first would renumber the second's mutants.
	fset, f := parseSrc(t, "p.go", `package p

func init() {}

func F() {}

func init() {}
`)
	spans := funcSpans(fset, f)
	want := []string{"init", "F", "init~2"}
	if len(spans) != len(want) {
		t.Fatalf("len(spans)=%d, want %d", len(spans), len(want))
	}
	for i, w := range want {
		if spans[i].name != w {
			t.Errorf("spans[%d].name=%q, want %q", i, spans[i].name, w)
		}
	}
}

func TestDiscoverStableIDsSurviveEditsToASameNamedNeighbour(t *testing.T) {
	// Adding a mutation point to the *first* init() must leave the second
	// init()'s ID alone — the guarantee the README states for edits outside
	// a function.
	before := discoverArithmetic(t, map[string]string{"p.go": `package p

func init() { _ = 1 + 2 }

func init() { _ = 3 + 4 }
`})
	after := discoverArithmetic(t, map[string]string{"p.go": `package p

func init() { _ = 1 + 2; _ = 5 + 6 }

func init() { _ = 3 + 4 }
`})

	if len(before) != 2 {
		t.Fatalf("len(before)=%d, want 2", len(before))
	}
	if len(after) != 3 {
		t.Fatalf("len(after)=%d, want 3", len(after))
	}
	if before[0].StableID != "p.go:init:ARITHMETIC_BASE#1" {
		t.Errorf("before[0].StableID=%q, want %q", before[0].StableID, "p.go:init:ARITHMETIC_BASE#1")
	}
	if before[1].StableID != "p.go:init~2:ARITHMETIC_BASE#1" {
		t.Errorf("before[1].StableID=%q, want %q", before[1].StableID, "p.go:init~2:ARITHMETIC_BASE#1")
	}
	// The added mutant takes #2 inside the first init(); the second init()
	// keeps #1 because it counts against its own anchor.
	if after[1].StableID != "p.go:init:ARITHMETIC_BASE#2" {
		t.Errorf("after[1].StableID=%q, want %q", after[1].StableID, "p.go:init:ARITHMETIC_BASE#2")
	}
	if after[2].StableID != before[1].StableID {
		t.Errorf("second init()'s StableID shifted: %q → %q", before[1].StableID, after[2].StableID)
	}
}

func TestStableIDFile(t *testing.T) {
	// The ordinary case: a path under the module root renders relative.
	if got := stableIDFile(filepath.Join("/mod", "internal", "a", "a.go"), "/mod"); got != "internal/a/a.go" {
		t.Errorf("stableIDFile under root=%q, want %q", got, "internal/a/a.go")
	}
	// An empty module root has no relative form, so the absolute path stands
	// in. It still differs per file, which is all the ordinal counter needs.
	abs := filepath.Join("/elsewhere", "a.go")
	if got := stableIDFile(abs, ""); got != filepath.ToSlash(abs) {
		t.Errorf("stableIDFile with empty root=%q, want %q", got, filepath.ToSlash(abs))
	}
}

// discoverArithmeticMulti runs Discover over a module root holding one
// package per entry of pkgFiles (keyed by directory relative to the root),
// scoped to the named packages. It exists to vary the *package argument*
// while holding the source fixed.
func discoverArithmeticMulti(t *testing.T, pkgFiles map[string]map[string]string, scope []string) []mutator.Mutant {
	t.Helper()
	root := t.TempDir()
	byDir := make(map[string]Package, len(pkgFiles))
	for dir, files := range pkgFiles {
		abs := filepath.Join(root, dir)
		if err := os.MkdirAll(abs, 0o755); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(files))
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(abs, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		byDir[dir] = Package{Dir: abs, ImportPath: "example.com/test/" + dir, GoFiles: names}
	}

	pkgs := make([]Package, 0, len(scope))
	for _, dir := range scope {
		pkgs = append(pkgs, byDir[dir])
	}
	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	return Discover(fset, pkgs, reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil), root, "example.com/test").Mutants
}

func TestDiscoverStableIDsAreIndependentOfThePackageArgument(t *testing.T) {
	// Same source, two scopes: the narrow run mirrors `gomutants ./a/`, the
	// wide one `gomutants ./...`. RelFile is a gremlins-compat path derived
	// from the packages in the run, so it differs between them; the stable
	// ID must not, or an id copied out of a whole-repo report would not
	// resolve under a per-package run.
	files := map[string]map[string]string{
		"a":   {"a.go": "package a\n\nfunc F() int {\n\treturn 1 + 2\n}\n"},
		"cmd": {"main.go": "package cmd\n\nfunc G() int {\n\treturn 3 + 4\n}\n"},
	}

	narrow := discoverArithmeticMulti(t, files, []string{"a"})
	wide := discoverArithmeticMulti(t, files, []string{"a", "cmd"})

	if len(narrow) != 1 {
		t.Fatalf("len(narrow)=%d, want 1", len(narrow))
	}
	if len(wide) != 2 {
		t.Fatalf("len(wide)=%d, want 2", len(wide))
	}

	const wantID = "a/a.go:F:ARITHMETIC_BASE#1"
	if narrow[0].StableID != wantID {
		t.Errorf("narrow StableID=%q, want %q", narrow[0].StableID, wantID)
	}
	if wide[0].StableID != wantID {
		t.Errorf("wide StableID=%q, want %q", wide[0].StableID, wantID)
	}

	// The gremlins-compat RelFile is left alone, and still varies with the
	// scope — which is exactly why the ID needed its own path.
	if narrow[0].RelFile != "a.go" {
		t.Errorf("narrow RelFile=%q, want %q", narrow[0].RelFile, "a.go")
	}
	if wide[0].RelFile != "a/a.go" {
		t.Errorf("wide RelFile=%q, want %q", wide[0].RelFile, "a/a.go")
	}
}
