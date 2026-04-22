package coverage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestProject creates a minimal Go project for integration tests.
func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod": "module testmod\n\ngo 1.26\n",
		"add.go": `package testmod

func Add(a, b int) int {
	return a + b
}

func Unused() int {
	return 42
}
`,
		"add_test.go": `package testmod

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}

func TestAddNegative(t *testing.T) {
	if Add(-1, -2) != -3 {
		t.Fatal("wrong")
	}
}
`,
	}

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBuildTestMap(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()

	tm, err := BuildTestMap(context.Background(), dir, []string{"testmod"}, "", tmpDir, 2)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}

	if tm == nil {
		t.Fatal("TestMap is nil")
	}

	// The Add function (line 4: return a + b) should be covered by TestAdd and TestAddNegative.
	tests := tm.TestsFor("testmod/add.go", 4)
	if len(tests) == 0 {
		t.Error("expected tests covering add.go:4, got none")
	}

	// Add's coverage block spans lines 3–5 (opening brace to closing brace).
	// Asserting coverage at line 5 (the block's EndLine) kills
	// CONDITIONALS_BOUNDARY on the indexer's `line <= b.EndLine` loop —
	// mutating to `<` would exclude EndLine, leaving this key unmapped.
	tests = tm.TestsFor("testmod/add.go", 5)
	if len(tests) == 0 {
		t.Error("expected tests covering add.go:5 (block EndLine), got none — CONDITIONALS_BOUNDARY on `line <= b.EndLine` would drop this")
	}

	// Unused() at lines 7-9 is not called by any test. Its block exists in
	// every test's coverage profile with Count=0. Kills BRANCH_IF on
	// `if b.Count == 0 { continue }`: under mutation the zero-count block
	// would still be indexed, falsely mapping Unused's lines to tests that
	// never exercised it.
	tests = tm.TestsFor("testmod/add.go", 8)
	if len(tests) != 0 {
		t.Errorf("Unused() line 8 should have no covering tests (Count=0 block), got %v", tests)
	}

	// TestsFor should return nil for uncovered lines.
	tests = tm.TestsFor("testmod/add.go", 999)
	if tests != nil {
		t.Errorf("expected nil for uncovered line, got %v", tests)
	}
}

// TestBuildTestMapListTestsErrorMessage kills STATEMENT_REMOVE on
// `cmd.Stderr = &stderr` in listTests: without stderr capture, the
// returned error wouldn't include the underlying go-tool stderr text.
//
// The error's format string already embeds the package name ("go test
// -list for %s: %w\n%s"), so asserting on the pkg name doesn't
// distinguish the two paths. We instead check for text only the go
// tool's stderr produces ("is not in std" / "no required module" /
// "cannot find").
func TestBuildTestMapListTestsErrorMessage(t *testing.T) {
	_, err := listTests(context.Background(), t.TempDir(), []string{"definitely/nonexistent/pkg/zzz"})
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
	msg := err.Error()
	if !strings.Contains(msg, "is not in std") && !strings.Contains(msg, "cannot find") && !strings.Contains(msg, "no required module") {
		t.Errorf("error should include stderr content (stderr-only text like 'is not in std'), got: %q", msg)
	}
}

// TestResolvePackagesErrorMessage kills STATEMENT_REMOVE on
// `cmd.Stderr = &stderr` in resolvePackages.
func TestResolvePackagesErrorMessage(t *testing.T) {
	_, err := resolvePackages(context.Background(), t.TempDir(), []string{"definitely/nonexistent/pkg/zzz"})
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
	msg := err.Error()
	if !strings.Contains(msg, "definitely/nonexistent/pkg/zzz") && !strings.Contains(msg, "cannot find") && !strings.Contains(msg, "no required module") {
		t.Errorf("error should include stderr content, got: %q", msg)
	}
}

// TestBuildTestMapCoverPkgNoMatch kills three mutations on the
// `if coverPkg != "" { args = append(args, "-coverpkg="+coverPkg) }` block:
//
//   - CONDITIONALS_NEGATION (!=  →  ==): a non-empty coverPkg would no
//     longer trigger the append; the test binary would be built with
//     default coverage (of the tested package) and the map populates.
//   - BRANCH_IF (body elided): the append is skipped; same as negation.
//   - STATEMENT_REMOVE on the append: same as BRANCH_IF.
//
// Strategy: pass a coverpkg pattern that matches no real package. Under
// original behavior, the test binary is built with `-coverpkg=<nomatch>`
// and records coverage for nothing — so no test lines end up in the map.
// Under any of the three mutations, the flag isn't passed, the default
// coverage of "testmod" kicks in, and add.go:4 gets mapped to TestAdd.
func TestBuildTestMapCoverPkgNoMatch(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()

	tm, err := BuildTestMap(context.Background(), dir, []string{"testmod"},
		"completely/nonexistent/zzz", tmpDir, 2)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}
	tests := tm.TestsFor("testmod/add.go", 4)
	if len(tests) != 0 {
		t.Errorf("with coverpkg=nomatch, no lines should be mapped; got %v — flag must have been dropped by mutation", tests)
	}
}

// TestBuildTestMapPackageArgPassed kills STATEMENT_REMOVE on
// `args = append(args, pkg.importPath)`. Root has go.mod only (no Go
// files), tests live in a subpackage. Under the original the compile
// command is `go test -c ... rootmod/sub` — builds sub's tests. Under
// mutation the command runs in projectDir (rootDir) without a package
// arg, fails with "no Go files", skips the package, and the map is
// empty.
func TestBuildTestMapPackageArgPassed(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "go.mod"), []byte("module rootmod\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(rootDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libSrc := "package sub\n\nfunc F() int { return 1 + 2 }\n"
	testSrc := "package sub\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F() != 3 { t.Fatal(\"wrong\") } }\n"
	if err := os.WriteFile(filepath.Join(subDir, "lib.go"), []byte(libSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "lib_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	tm, err := BuildTestMap(context.Background(), rootDir, []string{"rootmod/sub"}, "", t.TempDir(), 1)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}
	tests := tm.TestsFor("rootmod/sub/lib.go", 3)
	if len(tests) == 0 {
		t.Error("expected TestF mapped to lib.go:3 — without the package arg `go test -c` defaults to cwd (rootDir) which has no Go files")
	}
}

func TestBuildTestMapWithCoverpkg(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()

	tm, err := BuildTestMap(context.Background(), dir, []string{"testmod"}, "testmod", tmpDir, 2)
	if err != nil {
		t.Fatalf("BuildTestMap with coverpkg: %v", err)
	}
	if tm == nil {
		t.Fatal("TestMap is nil")
	}
}

func TestBuildTestMapContextCancelled(t *testing.T) {
	dir := setupTestProject(t)

	ctx, cancel := context.WithCancel(context.Background())

	// Stub listTests to return many fake tests and cancel the context.
	// The large number of tests ensures the feeder goroutine will attempt
	// to send on a full channel and hit ctx.Done().
	origList := listTestsFunc
	listTestsFunc = func(ctx context.Context, projectDir string, packages []string) ([]testEntry, error) {
		// Cancel after listTests succeeds — this means workers will start
		// but hit ctx.Err() when processing work items.
		cancel()
		var tests []testEntry
		for i := range 1000 {
			tests = append(tests, testEntry{name: fmt.Sprintf("Test%d", i), pkg: "testmod"})
		}
		return tests, nil
	}
	defer func() { listTestsFunc = origList }()

	// Should not hang — cancelled context propagates to workers and feeder.
	_, err := BuildTestMap(ctx, dir, []string{"testmod"}, "", t.TempDir(), 2)
	_ = err
}

func TestListTests(t *testing.T) {
	dir := setupTestProject(t)

	tests, err := listTests(context.Background(), dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("listTests: %v", err)
	}

	if len(tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(tests))
	}

	names := make(map[string]bool)
	for _, te := range tests {
		names[te.name] = true
		if te.pkg != "testmod" {
			t.Errorf("test %q has pkg=%q, want testmod", te.name, te.pkg)
		}
	}
	if !names["TestAdd"] {
		t.Error("missing TestAdd")
	}
	if !names["TestAddNegative"] {
		t.Error("missing TestAddNegative")
	}
}

func TestListTestsFailure(t *testing.T) {
	_, err := listTests(context.Background(), t.TempDir(), []string{"nonexistent/pkg"})
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
}

func TestResolvePackagesCoverage(t *testing.T) {
	dir := setupTestProject(t)

	pkgs, err := resolvePackages(context.Background(), dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("resolvePackages: %v", err)
	}

	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].importPath != "testmod" {
		t.Errorf("importPath=%q, want testmod", pkgs[0].importPath)
	}
	if pkgs[0].dir == "" {
		t.Error("dir should not be empty")
	}
}

func TestResolvePackagesFailure(t *testing.T) {
	_, err := resolvePackages(context.Background(), t.TempDir(), []string{"nonexistent/pkg"})
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
}

func TestStatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := statFile(path)
	if err != nil {
		t.Fatalf("statFile: %v", err)
	}
	if info.Size() != 2 {
		t.Errorf("size=%d, want 2", info.Size())
	}

	_, err = statFile("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestBuildTestMapListTestsError(t *testing.T) {
	// Package with syntax error — listTests fails.
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"bad.go":      "package testmod\n\nfunc Bad() { SYNTAX ERROR }\n",
		"bad_test.go": "package testmod\nimport \"testing\"\nfunc TestBad(t *testing.T) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := BuildTestMap(context.Background(), dir, []string{"testmod"}, "", t.TempDir(), 1)
	if err == nil {
		t.Fatal("expected error for package with syntax error")
	}
}

func TestBuildTestMapResolveError(t *testing.T) {
	dir := setupTestProject(t)

	// Stub resolvePackagesFunc to fail after listTests succeeds.
	origResolve := resolvePackagesFunc
	resolvePackagesFunc = func(ctx context.Context, projectDir string, patterns []string) ([]resolvedPkg, error) {
		return nil, fmt.Errorf("injected resolve error")
	}
	defer func() { resolvePackagesFunc = origResolve }()

	_, err := BuildTestMap(context.Background(), dir, []string{"testmod"}, "", t.TempDir(), 1)
	if err == nil {
		t.Fatal("expected error from resolvePackages")
	}
}

func TestBuildTestMapCompileFailure(t *testing.T) {
	dir := setupTestProject(t)

	// Stub resolvePackagesFunc to return a package that won't compile.
	origResolve := resolvePackagesFunc
	resolvePackagesFunc = func(ctx context.Context, projectDir string, patterns []string) ([]resolvedPkg, error) {
		return []resolvedPkg{{importPath: "nonexistent/package", dir: projectDir}}, nil
	}
	defer func() { resolvePackagesFunc = origResolve }()

	// listTests will return tests but the package binary won't compile.
	tm, err := BuildTestMap(context.Background(), dir, []string{"testmod"}, "", t.TempDir(), 1)
	if err != nil {
		t.Fatalf("BuildTestMap should not error: %v", err)
	}
	// Map should be empty since no binaries were compiled.
	tests := tm.TestsFor("testmod/add.go", 4)
	if len(tests) != 0 {
		t.Errorf("expected no tests mapped, got %d", len(tests))
	}
}

func TestBuildTestMapNoTestsPkg(t *testing.T) {
	// Package with no tests — go test -c produces no binary.
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module testmod\n\ngo 1.26\n",
		"lib.go": "package testmod\n\nfunc Add(a, b int) int { return a + b }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tmpDir := t.TempDir()

	tm, err := BuildTestMap(context.Background(), dir, []string{"testmod"}, "", tmpDir, 1)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}
	// No tests found, map should be empty.
	if tm == nil {
		t.Fatal("TestMap should not be nil")
	}
}

// TestRunCompiledTestStaleProfileNotReturned kills BRANCH_IF on
// runCompiledTest's `if err := cmd.Run(); err != nil { return nil }`.
// We pre-seed the profile path with valid coverage data, then hand in a
// nonexistent binary so cmd.Run fails. Under the original, the failing
// Run short-circuits and returns nil. Under mutation, execution falls
// through to parseFileFunc, which happily reads the pre-seeded stale
// data and returns its blocks.
func TestRunCompiledTestStaleProfileNotReturned(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	profilePath := filepath.Join(tmpDir, "stale.cov")
	staleProfile := "mode: set\ntestmod/fake.go:1.1,2.2 1 1\n"
	if err := os.WriteFile(profilePath, []byte(staleProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := &compiledPkg{
		binPath:    "/nonexistent/absolutely/not/a/binary",
		importPath: "testmod",
		dir:        tmpDir,
	}
	blocks := runCompiledTest(ctx, cp, "TestAnything", profilePath)
	if blocks != nil {
		t.Errorf("cmd.Run failed but got %d blocks from stale profile — BRANCH_IF on the err check lets it through", len(blocks))
	}
}

// TestProcessWorkContextCancelledSkipsWork kills BRANCH_IF on processWork's
// `if ctx.Err() != nil { return }`. Under the original, a cancelled
// context makes the worker return before reading the next entry, so the
// pre-filled work item is left unprocessed. Under mutation, the return
// is elided and the worker falls through to the test-run path, which
// would actually execute our real compiled binary and push a result.
func TestProcessWorkContextCancelledSkipsWork(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()

	binPath := filepath.Join(tmpDir, "testbin.test")
	cmd := exec.CommandContext(context.Background(), "go", "test", "-c", "-o", binPath, "-cover", "testmod")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("go test -c: %v", err)
	}
	cp := &compiledPkg{binPath: binPath, importPath: "testmod", dir: dir}
	pkgBins := map[string]*compiledPkg{"testmod": cp}

	work := make(chan testEntry, 1)
	results := make(chan testCoverage, 1)
	work <- testEntry{name: "TestAdd", pkg: "testmod"}
	close(work)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel before processWork reads

	processWork(ctx, work, pkgBins, tmpDir, 0, results)
	close(results)

	count := 0
	for range results {
		count++
	}
	if count != 0 {
		t.Errorf("processWork produced %d results on a cancelled ctx — BRANCH_IF on ctx.Err() check lets work through", count)
	}
}

func TestRunCompiledTestFailure(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()
	ctx := context.Background()

	pkgs, err := resolvePackages(ctx, dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("resolvePackages: %v", err)
	}

	// Use a non-existent binary path — cmd.Run will fail.
	cp := &compiledPkg{
		binPath:    "/nonexistent/binary",
		importPath: "testmod",
		dir:        pkgs[0].dir,
	}

	profilePath := filepath.Join(tmpDir, "test.cov")
	blocks := runCompiledTest(ctx, cp, "TestAdd", profilePath)
	if blocks != nil {
		t.Errorf("expected nil blocks for failed test binary, got %d", len(blocks))
	}
}

func TestRunCompiledTestParseError(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()
	ctx := context.Background()

	pkgs, err := resolvePackages(ctx, dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("resolvePackages: %v", err)
	}

	binPath := filepath.Join(tmpDir, "testbin.test")
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", binPath, "-cover", "testmod")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("go test -c: %v", err)
	}

	cp := &compiledPkg{
		binPath:    binPath,
		importPath: "testmod",
		dir:        pkgs[0].dir,
	}

	// Stub parseFileFunc to return an error.
	origParse := parseFileFunc
	parseFileFunc = func(path string) (*Profile, error) {
		return nil, fmt.Errorf("injected parse error")
	}
	defer func() { parseFileFunc = origParse }()

	profilePath := filepath.Join(tmpDir, "test.cov")
	blocks := runCompiledTest(ctx, cp, "TestAdd", profilePath)
	if blocks != nil {
		t.Errorf("expected nil blocks when ParseFile fails, got %d", len(blocks))
	}
}

func TestRunCompiledTestBadProfile(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()
	ctx := context.Background()

	pkgs, err := resolvePackages(ctx, dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("resolvePackages: %v", err)
	}

	// Compile the test binary.
	binPath := filepath.Join(tmpDir, "testbin.test")
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", binPath, "-cover", "testmod")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("go test -c: %v", err)
	}

	cp := &compiledPkg{
		binPath:    binPath,
		importPath: "testmod",
		dir:        pkgs[0].dir,
	}

	// Use a directory as the profile path — go test will fail to write to it.
	profileDir := filepath.Join(tmpDir, "profdir")
	os.MkdirAll(profileDir, 0o755)
	blocks := runCompiledTest(ctx, cp, "TestAdd", profileDir)
	// cmd.Run fails because -test.coverprofile can't write to a directory.
	if blocks != nil {
		t.Logf("blocks=%d (expected nil or empty)", len(blocks))
	}
}

func TestRunCompiledTest(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()
	ctx := context.Background()

	pkgs, err := resolvePackages(ctx, dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("resolvePackages: %v", err)
	}

	// Compile the test binary.
	binPath := filepath.Join(tmpDir, "testbin.test")
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", binPath, "-cover", "testmod")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("go test -c: %v", err)
	}

	cp := &compiledPkg{
		binPath:    binPath,
		importPath: "testmod",
		dir:        pkgs[0].dir,
	}

	profilePath := filepath.Join(tmpDir, "test.cov")
	blocks := runCompiledTest(ctx, cp, "TestAdd", profilePath)
	if len(blocks) == 0 {
		t.Error("expected coverage blocks from TestAdd")
	}

	// Running a non-existent test should return nil/empty blocks.
	blocks = runCompiledTest(ctx, cp, "TestNonExistent", profilePath)
	_ = blocks
}
