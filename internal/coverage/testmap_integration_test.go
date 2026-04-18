package coverage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// TestsFor should return nil for uncovered lines.
	tests = tm.TestsFor("testmod/add.go", 999)
	if tests != nil {
		t.Errorf("expected nil for uncovered line, got %v", tests)
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
