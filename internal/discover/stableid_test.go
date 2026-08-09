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
	k := ordinalKey{relFile: "internal/cli/cli.go", anchor: "parseArgs", typ: mutator.ConditionalsBoundary}
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
