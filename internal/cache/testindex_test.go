package cache

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestBuildTestIndex_IndexesTestFunctions(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.go"), `package x

import "testing"

func TestAlpha(t *testing.T)        {}
func TestBeta(t *testing.T)         {}
func BenchmarkGamma(b *testing.B)   {}
func ExampleDelta()                  {}
func FuzzEpsilon(f *testing.F)      {}
func helperNotATest()                {}
`)
	mustWrite(t, filepath.Join(dir, "x.go"), `package x
func Foo() {}
`)

	ti := BuildTestIndex([]string{dir})

	for _, name := range []string{"TestAlpha", "TestBeta", "BenchmarkGamma", "ExampleDelta", "FuzzEpsilon"} {
		got := ti.FilesFor(name)
		if len(got) != 1 {
			t.Errorf("FilesFor(%q) = %v, want 1 file", name, got)
		}
	}
	if got := ti.FilesFor("helperNotATest"); got != nil {
		t.Errorf("non-test function indexed: %v", got)
	}
	if got := ti.FilesFor("Foo"); got != nil {
		t.Errorf("production function indexed: %v", got)
	}

	all := ti.AllInDir(dir)
	if len(all) != 1 || filepath.Base(all[0]) != "a_test.go" {
		t.Errorf("AllInDir(dir) = %v, want [a_test.go]", all)
	}
}

// TestBuildTestIndex_CrossPackageNameCollision asserts that the same
// test name declared in two packages maps to both files. This is the
// case the per-mutant tests_hash needs to handle: an edit to either
// declaring file must invalidate the cache entry.
func TestBuildTestIndex_CrossPackageNameCollision(t *testing.T) {
	root := t.TempDir()
	pkgA := filepath.Join(root, "a")
	pkgB := filepath.Join(root, "b")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pkgA, "x_test.go"), "package a\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(pkgB, "x_test.go"), "package b\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n")

	ti := BuildTestIndex([]string{pkgA, pkgB})
	got := ti.FilesFor("TestSame")
	if len(got) != 2 {
		t.Fatalf("FilesFor(TestSame) = %v, want 2 entries", got)
	}
	sort.Strings(got)
	wantA := filepath.Join(pkgA, "x_test.go")
	wantB := filepath.Join(pkgB, "x_test.go")
	if got[0] != wantA || got[1] != wantB {
		t.Errorf("FilesFor(TestSame) = %v, want [%s %s]", got, wantA, wantB)
	}
}

// TestBuildTestIndex_MalformedFileRecordedInDirOnly asserts that a
// _test.go that fails to parse still appears in AllInDir — the
// fallback hashing path must include it — but contributes no byName
// entries. Mutants resolving through it then fall back to the
// directory-wide list, which correctly captures the malformed file.
//
// The malformed file is engineered to parse just far enough that
// `parser.ParseFile` returns a *non-nil* *ast.File with a complete
// FuncDecl for TestPartial before encountering the syntax error.
// Without the `if perr != nil { continue }` guard, that partial AST
// would be walked and TestPartial would be (incorrectly) indexed —
// so this test also catches the BRANCH_IF mutation on that guard.
func TestBuildTestIndex_MalformedFileRecordedInDirOnly(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good_test.go")
	bad := filepath.Join(dir, "bad_test.go")
	mustWrite(t, good, "package x\nimport \"testing\"\nfunc TestOK(t *testing.T) {}\n")
	mustWrite(t, bad, `package x

import "testing"

func TestPartial(t *testing.T) {}

func {{{
`)

	ti := BuildTestIndex([]string{dir})

	if got := ti.FilesFor("TestOK"); len(got) != 1 || got[0] != good {
		t.Errorf("FilesFor(TestOK) = %v, want [%s]", got, good)
	}
	if got := ti.FilesFor("TestPartial"); got != nil {
		t.Errorf("TestPartial from a malformed file was indexed (parse-error guard not honored): %v", got)
	}
	all := ti.AllInDir(dir)
	if len(all) != 2 {
		t.Errorf("AllInDir = %v, want both files (incl. malformed)", all)
	}
}

func TestBuildTestIndex_NilSafe(t *testing.T) {
	var ti *TestIndex
	if got := ti.FilesFor("anything"); got != nil {
		t.Errorf("nil index FilesFor returned %v", got)
	}
	if got := ti.AllInDir("/nope"); got != nil {
		t.Errorf("nil index AllInDir returned %v", got)
	}
}

// TestCoveringFiles_IncludesHelperOnlyFile is the regression this method
// exists for. assert_test.go declares no test entry point, so it is absent
// from byName and no coverage-derived test name can ever resolve to it —
// yet TestAdjust's verdict depends on the assertion it holds. Resolving
// only through the covering names would leave an edit to that helper
// invisible to every hash dimension: it is not a production file (so
// pkg_hash skips it) and it declares no test (so FilesFor misses it).
func TestCoveringFiles_IncludesHelperOnlyFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.go"), `package x

import "testing"

func TestAdjust(t *testing.T) { mustAdjust(t, 1, 2) }
`)
	mustWrite(t, filepath.Join(dir, "assert_test.go"), `package x

import "testing"

func mustAdjust(t *testing.T, in, want int) {}
`)

	ti := BuildTestIndex([]string{dir})

	if got := ti.FilesFor("mustAdjust"); got != nil {
		t.Fatalf("precondition failed: helper indexed by name as %v", got)
	}

	got := ti.CoveringFiles(dir, []string{"TestAdjust"})
	var names []string
	for _, f := range got {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "a_test.go" || names[1] != "assert_test.go" {
		t.Errorf("CoveringFiles = %v, want both a_test.go and assert_test.go — a "+
			"helper-only test file gates reuse but resolves through no test name", names)
	}
}

// TestCoveringFiles_AddsCrossPackageDeclarers asserts the other half: a
// covering test declared outside the mutant's package (what -coverpkg /
// --integration produces) is added to the local set, not substituted for it.
func TestCoveringFiles_AddsCrossPackageDeclarers(t *testing.T) {
	target := t.TempDir()
	importer := t.TempDir()
	mustWrite(t, filepath.Join(target, "unit_test.go"), `package x

import "testing"

func TestUnit(t *testing.T) {}
`)
	mustWrite(t, filepath.Join(importer, "e2e_test.go"), `package y

import "testing"

func TestEndToEnd(t *testing.T) {}
`)

	ti := BuildTestIndex([]string{target, importer})

	got := ti.CoveringFiles(target, []string{"TestEndToEnd"})
	if len(got) != 2 {
		t.Fatalf("CoveringFiles = %v, want the local test file plus the cross-package declarer", got)
	}
	var sawLocal, sawRemote bool
	for _, f := range got {
		switch filepath.Dir(f) {
		case target:
			sawLocal = true
		case importer:
			sawRemote = true
		}
	}
	if !sawLocal || !sawRemote {
		t.Errorf("CoveringFiles = %v, want one file from each package (local=%v remote=%v)",
			got, sawLocal, sawRemote)
	}
}

// TestCoveringFiles_DedupesLocalDeclarer covers the seen-set: a covering
// test declared in the mutant's own package is already in the local half,
// and must not be hashed twice. Duplicate paths would not break soundness
// but would make tests_hash depend on the coverage map's name ordering.
func TestCoveringFiles_DedupesLocalDeclarer(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.go"), `package x

import "testing"

func TestAlpha(t *testing.T) {}
`)

	ti := BuildTestIndex([]string{dir})

	if got := ti.CoveringFiles(dir, []string{"TestAlpha", "TestAlpha"}); len(got) != 1 {
		t.Errorf("CoveringFiles = %v, want 1 file", got)
	}
}

// TestCoveringFiles_UnknownNamesKeepLocalSet asserts that a coverage map
// naming tests the index never saw (a deleted test, a name from a package
// outside the scanned closure) degrades to the local set rather than to
// nothing — an empty covering set would hash the same for every mutant in
// the package and hand back stale verdicts.
func TestCoveringFiles_UnknownNamesKeepLocalSet(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.go"), `package x

import "testing"

func TestAlpha(t *testing.T) {}
`)

	ti := BuildTestIndex([]string{dir})

	if got := ti.CoveringFiles(dir, []string{"TestVanished"}); len(got) != 1 {
		t.Errorf("CoveringFiles = %v, want the package's own test file", got)
	}
}

// TestCoveringFiles_UnknownDirIsEmpty asserts a directory the index never
// scanned yields no files, and that CoveringFiles is nil-receiver safe.
func TestCoveringFiles_UnknownDirIsEmpty(t *testing.T) {
	ti := BuildTestIndex(nil)
	if got := ti.CoveringFiles("/nope", []string{"TestAlpha"}); got != nil {
		t.Errorf("CoveringFiles on unscanned dir = %v, want nil", got)
	}

	var nilIdx *TestIndex
	if got := nilIdx.CoveringFiles("/nope", []string{"TestAlpha"}); got != nil {
		t.Errorf("nil index CoveringFiles = %v, want nil", got)
	}
}
