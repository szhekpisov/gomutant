package discover

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/mutator"
)

func TestDiscover(t *testing.T) {
	// Create a temp Go source file.
	dir := t.TempDir()
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	pkgs := []Package{
		{
			Dir:        dir,
			ImportPath: "example.com/test/testpkg",
			GoFiles:    []string{"add.go"},
		},
	}

	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	mutants := Discover(fset, pkgs, reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil), dir, "example.com/test")

	// "a + b" should produce at least 1 ARITHMETIC_BASE mutant.
	if len(mutants) == 0 {
		t.Fatal("expected at least 1 mutant")
	}

	m := mutants[0]
	if m.ID != 1 {
		t.Errorf("ID=%d, want 1", m.ID)
	}
	if m.Type != mutator.ArithmeticBase {
		t.Errorf("Type=%v, want ARITHMETIC_BASE", m.Type)
	}
	if m.Status != mutator.StatusPending {
		t.Errorf("Status=%v, want PENDING", m.Status)
	}
	if m.File != filepath.Join(dir, "add.go") {
		t.Errorf("File=%q", m.File)
	}
	if m.Pkg != "example.com/test/testpkg" {
		t.Errorf("Pkg=%q", m.Pkg)
	}
	if m.Original != "+" || m.Replacement != "-" {
		t.Errorf("mutation: %q→%q", m.Original, m.Replacement)
	}
}

func TestDiscoverUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte("not valid go"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkgs := []Package{
		{Dir: dir, ImportPath: "example.com/bad", GoFiles: []string{"bad.go"}},
	}

	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	// Should not panic — unparseable files are skipped.
	mutants := Discover(fset, pkgs, reg.Mutators(), dir, "example.com")
	if len(mutants) != 0 {
		t.Errorf("expected 0 mutants from unparseable file, got %d", len(mutants))
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		pkgs []Package
		want string
	}{
		{nil, ""},
		{
			[]Package{{ImportPath: "github.com/foo/bar/pkg/a"}},
			"github.com/foo/bar/pkg/a",
		},
		{
			[]Package{
				{ImportPath: "github.com/foo/bar/pkg/a"},
				{ImportPath: "github.com/foo/bar/pkg/b"},
			},
			"github.com/foo/bar/pkg",
		},
		{
			[]Package{
				{ImportPath: "github.com/foo/bar"},
				{ImportPath: "github.com/baz/qux"},
			},
			"github.com",
		},
		{
			[]Package{
				{ImportPath: "a/b/c"},
				{ImportPath: "x/y/z"},
			},
			"",
		},
	}
	for _, tc := range tests {
		got := longestCommonPrefix(tc.pkgs)
		if got != tc.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", tc.pkgs, got, tc.want)
		}
	}
}

func TestFilterByCoverage(t *testing.T) {
	profile := &coverage.Profile{}
	// Use the exported IsCovered via the profile — it has no blocks, so nothing is covered.

	pkgs := []Package{
		{
			Dir:        "/abs/path/pkg",
			ImportPath: "example.com/mod/pkg",
			GoFiles:    []string{"file.go"},
		},
	}

	mutants := []mutator.Mutant{
		{ID: 1, File: "/abs/path/pkg/file.go", Line: 10, Col: 5, Status: mutator.StatusPending},
		{ID: 2, File: "/abs/path/pkg/file.go", Line: 20, Col: 3, Status: mutator.StatusPending},
		{ID: 3, File: "/unknown/file.go", Line: 5, Col: 1, Status: mutator.StatusPending},
		{ID: 4, File: "/abs/path/pkg/file.go", Line: 10, Col: 5, Status: mutator.StatusKilled}, // Not pending — skip.
	}

	FilterByCoverage(mutants, profile, pkgs, "example.com/mod")

	// mutants 1 and 2 should be NOT_COVERED (profile has no covered blocks).
	if mutants[0].Status != mutator.StatusNotCovered {
		t.Errorf("mutant 1: status=%v, want NOT_COVERED", mutants[0].Status)
	}
	if mutants[1].Status != mutator.StatusNotCovered {
		t.Errorf("mutant 2: status=%v, want NOT_COVERED", mutants[1].Status)
	}
	// mutant 3: unknown file → NOT_COVERED.
	if mutants[2].Status != mutator.StatusNotCovered {
		t.Errorf("mutant 3: status=%v, want NOT_COVERED", mutants[2].Status)
	}
	// mutant 4: already KILLED, should remain unchanged.
	if mutants[3].Status != mutator.StatusKilled {
		t.Errorf("mutant 4: status=%v, want KILLED (unchanged)", mutants[3].Status)
	}
}

func TestPreReadFiles(t *testing.T) {
	dir := t.TempDir()
	content := []byte("package p\n")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	pkgs := []Package{
		{Dir: dir, GoFiles: []string{"a.go", "b.go"}},
	}

	files, err := PreReadFiles(pkgs)
	if err != nil {
		t.Fatalf("PreReadFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if string(files[dir+"/a.go"]) != "package p\n" {
		t.Errorf("unexpected content for a.go: %q", files[dir+"/a.go"])
	}
}

func TestPreReadFilesMissing(t *testing.T) {
	pkgs := []Package{
		{Dir: "/nonexistent", GoFiles: []string{"missing.go"}},
	}

	_, err := PreReadFiles(pkgs)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolvePackagesIntegration(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":   "module testmod\n\ngo 1.26\n",
		"add.go":   "package testmod\n\nfunc Add(a, b int) int { return a + b }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pkgs, err := ResolvePackages(context.Background(), dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("ResolvePackages: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].ImportPath != "testmod" {
		t.Errorf("ImportPath=%q", pkgs[0].ImportPath)
	}
	if len(pkgs[0].GoFiles) == 0 {
		t.Error("GoFiles should not be empty")
	}
}

func TestDecodeGoListJSONValid(t *testing.T) {
	input := `{"Dir":"/a","ImportPath":"mod/a","GoFiles":["a.go"],"TestGoFiles":["a_test.go"]}
{"Dir":"/b","ImportPath":"mod/b","GoFiles":["b.go"]}
`
	pkgs, err := decodeGoListJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("decodeGoListJSON: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	if pkgs[0].ImportPath != "mod/a" {
		t.Errorf("pkgs[0].ImportPath=%q", pkgs[0].ImportPath)
	}
}

func TestDecodeGoListJSONDecodeError(t *testing.T) {
	input := `{invalid json`
	_, err := decodeGoListJSON(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDecodeGoListJSONPkgError(t *testing.T) {
	input := `{"ImportPath":"bad/pkg","Error":{"Err":"some error message"}}`
	_, err := decodeGoListJSON(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for package with Error field")
	}
}

func TestResolvePackagesError(t *testing.T) {
	_, err := ResolvePackages(context.Background(), t.TempDir(), []string{"nonexistent/pkg"})
	if err == nil {
		t.Fatal("expected error")
	}
}


func TestDiscoverMultiPackage(t *testing.T) {
	dir := t.TempDir()
	// Two packages with the same import path prefix.
	pkgA := filepath.Join(dir, "a")
	pkgB := filepath.Join(dir, "b")
	os.MkdirAll(pkgA, 0o755)
	os.MkdirAll(pkgB, 0o755)

	os.WriteFile(filepath.Join(pkgA, "a.go"), []byte("package a\nfunc F() int { return 1 + 2 }\n"), 0o644)
	os.WriteFile(filepath.Join(pkgB, "b.go"), []byte("package b\nfunc G() int { return 3 - 4 }\n"), 0o644)

	pkgs := []Package{
		{Dir: pkgA, ImportPath: "example.com/mod/a", GoFiles: []string{"a.go"}},
		{Dir: pkgB, ImportPath: "example.com/mod/b", GoFiles: []string{"b.go"}},
	}

	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	mutants := Discover(fset, pkgs, reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil), dir, "example.com/mod")

	if len(mutants) < 2 {
		t.Fatalf("expected at least 2 mutants across packages, got %d", len(mutants))
	}

	// Verify RelFile contains subpackage prefix.
	for _, m := range mutants {
		if m.RelFile == "" {
			t.Errorf("mutant ID=%d has empty RelFile", m.ID)
		}
	}
}

func TestDiscoverRelFileNoCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	pkgA := filepath.Join(dir, "a")
	pkgB := filepath.Join(dir, "b")
	os.MkdirAll(pkgA, 0o755)
	os.MkdirAll(pkgB, 0o755)

	os.WriteFile(filepath.Join(pkgA, "a.go"), []byte("package a\nfunc F() int { return 1 + 2 }\n"), 0o644)
	os.WriteFile(filepath.Join(pkgB, "b.go"), []byte("package b\nfunc G() int { return 3 + 4 }\n"), 0o644)

	// Two packages with no common import path prefix.
	pkgs := []Package{
		{Dir: pkgA, ImportPath: "alpha/pkg", GoFiles: []string{"a.go"}},
		{Dir: pkgB, ImportPath: "beta/pkg", GoFiles: []string{"b.go"}},
	}

	fset := token.NewFileSet()
	reg := mutator.NewRegistry()
	mutants := Discover(fset, pkgs, reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil), dir, "unrelated/module")

	if len(mutants) < 2 {
		t.Fatalf("expected at least 2 mutants, got %d", len(mutants))
	}
	// When no common prefix, RelFile should fall back to filepath.Rel from moduleRoot.
	for _, m := range mutants {
		if m.RelFile == "" {
			t.Errorf("mutant ID=%d has empty RelFile", m.ID)
		}
	}
}

func TestDiscoverSortSameLineDifferentCol(t *testing.T) {
	dir := t.TempDir()
	// Multiple operators on different lines and same line — exercises sort comparisons.
	src := `package p

func f() int {
	a := 1 + 2
	b := 3 - 4
	return a + b - 1
}
`
	os.WriteFile(filepath.Join(dir, "multi.go"), []byte(src), 0o644)

	pkgs := []Package{
		{Dir: dir, ImportPath: "example.com/sort", GoFiles: []string{"multi.go"}},
	}

	fset := token.NewFileSet()
	reg := mutator.NewRegistry()
	mutants := Discover(fset, pkgs, reg.Mutators(), dir, "example.com")

	// Should have multiple mutants on the same line, testing sort comparisons.
	if len(mutants) < 2 {
		t.Fatalf("expected at least 2 mutants on same line, got %d", len(mutants))
	}
	// Verify sort: line should be non-decreasing.
	for i := 1; i < len(mutants); i++ {
		if mutants[i].Line < mutants[i-1].Line {
			t.Errorf("mutants not sorted by line: %d < %d", mutants[i].Line, mutants[i-1].Line)
		}
		if mutants[i].Line == mutants[i-1].Line && mutants[i].Col < mutants[i-1].Col {
			t.Errorf("same-line mutants not sorted by col: %d < %d", mutants[i].Col, mutants[i-1].Col)
		}
	}
}

func TestDiscoverSamePositionDifferentType(t *testing.T) {
	dir := t.TempDir()
	// Binary subtraction produces both ARITHMETIC_BASE and INVERT_NEGATIVES at the same position.
	src := `package p

func f(a, b int) int { return a - b }
`
	os.WriteFile(filepath.Join(dir, "sub.go"), []byte(src), 0o644)

	pkgs := []Package{
		{Dir: dir, ImportPath: "example.com/sub", GoFiles: []string{"sub.go"}},
	}

	fset := token.NewFileSet()
	reg := mutator.NewRegistry()
	mutants := Discover(fset, pkgs, reg.EnabledMutators([]string{"ARITHMETIC_BASE", "INVERT_NEGATIVES"}, nil), dir, "example.com")

	// Should have 2 mutants at the same position but different types.
	if len(mutants) != 2 {
		t.Fatalf("expected 2 mutants, got %d", len(mutants))
	}
	if mutants[0].Line != mutants[1].Line || mutants[0].Col != mutants[1].Col {
		t.Errorf("expected same position, got (%d,%d) and (%d,%d)",
			mutants[0].Line, mutants[0].Col, mutants[1].Line, mutants[1].Col)
	}
	if mutants[0].Type == mutants[1].Type {
		t.Errorf("expected different types at same position, both are %v", mutants[0].Type)
	}
}

func TestParseFileReadError(t *testing.T) {
	dir := t.TempDir()
	// Create a valid Go file.
	goSrc := "package p\nfunc F() int { return 1 + 2 }\n"
	path := filepath.Join(dir, "ok.go")
	os.WriteFile(path, []byte(goSrc), 0o644)

	// Stub readFileBytesFunc to fail.
	orig := readFileBytesFunc
	readFileBytesFunc = func(string) ([]byte, error) {
		return nil, fmt.Errorf("injected read error")
	}
	defer func() { readFileBytesFunc = orig }()

	pkgs := []Package{
		{Dir: dir, ImportPath: "example.com/err", GoFiles: []string{"ok.go"}},
	}

	fset := token.NewFileSet()
	reg := mutator.NewRegistry()
	// parseFile will succeed on parser.ParseFile but fail on readFileBytes — file is skipped.
	mutants := Discover(fset, pkgs, reg.Mutators(), dir, "example.com")
	if len(mutants) != 0 {
		t.Errorf("expected 0 mutants when readFileBytes fails, got %d", len(mutants))
	}
}

func TestDiscoverRelPathEmpty(t *testing.T) {
	// Exercise the relPath == "" fallback (line 140-142).
	// This happens when filepath.Rel returns "" — which occurs when moduleRoot
	// and absPath have no relationship. We can trigger this by using a path on a
	// different volume/root.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nfunc F() int { return 1 + 2 }\n"), 0o644)

	fset := token.NewFileSet()
	reg := mutator.NewRegistry()
	// Use two packages with no common prefix to force commonPrefix = "".
	pkgsTwo := []Package{
		{Dir: dir, ImportPath: "alpha", GoFiles: []string{"x.go"}},
		{Dir: dir, ImportPath: "beta", GoFiles: []string{"x.go"}},
	}

	mutants := Discover(fset, pkgsTwo, reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil), dir, dir)
	for _, m := range mutants {
		if m.RelFile == "" {
			t.Error("RelFile should not be empty")
		}
	}
}

func TestPreReadFilesDeduplicate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Same file in two packages — should only read once.
	pkgs := []Package{
		{Dir: dir, GoFiles: []string{"a.go"}},
		{Dir: dir, GoFiles: []string{"a.go"}},
	}

	files, err := PreReadFiles(pkgs)
	if err != nil {
		t.Fatalf("PreReadFiles: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 unique file, got %d", len(files))
	}
}
