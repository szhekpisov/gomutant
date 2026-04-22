package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureOutput swaps the package-level stdout writer with a bytes.Buffer
// and returns the captured text plus the function's error.
func captureOutput(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	orig := stdout
	stdout = &buf
	defer func() { stdout = orig }()
	err := fn()
	return buf.String(), err
}

func TestRunVersion(t *testing.T) {
	out, err := captureOutput(t, func() error {
		return run(context.Background(), []string{"--version"})
	})
	if err != nil {
		t.Fatalf("run --version: %v", err)
	}
	want := "gomutant v0.1.0\n"
	if out != want {
		t.Errorf("version output: got %q, want %q", out, want)
	}
}

func TestRunUnleash(t *testing.T) {
	// "unleash" should be stripped — then --version runs normally.
	out, err := captureOutput(t, func() error {
		return run(context.Background(), []string{"unleash", "--version"})
	})
	if err != nil {
		t.Fatalf("run unleash --version: %v", err)
	}
	if !strings.Contains(out, "gomutant v0.1.0") {
		t.Errorf("unleash: expected version output, got %q", out)
	}
}

func TestRunInvalidFlag(t *testing.T) {
	err := run(context.Background(), []string{"--invalid-flag"})
	if err == nil {
		t.Fatal("expected error for invalid flag")
	}
}

func TestReadModuleName(t *testing.T) {
	dir := t.TempDir()
	goMod := `module github.com/example/project

go 1.26
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	name, err := readModuleName(dir)
	if err != nil {
		t.Fatalf("readModuleName: %v", err)
	}
	if name != "github.com/example/project" {
		t.Errorf("module name=%q, want %q", name, "github.com/example/project")
	}
}

func TestReadModuleNameMissing(t *testing.T) {
	_, err := readModuleName("/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing go.mod")
	}
}

func TestReadModuleNameNoModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("go 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readModuleName(dir)
	if err == nil {
		t.Fatal("expected error for go.mod without module line")
	}
}

// TestRunAllLongFlags exercises each long-form flag so removing any
// fs.XxxVar registration breaks the parse. Uses --dry-run to avoid the
// slow mutation phase.
func TestRunAllLongFlags(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"add.go":      "package testmod\n\nfunc Add(a, b int) int { return a + b }\n",
		"add_test.go": "package testmod\nimport \"testing\"\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"wrong\") } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	outPath := filepath.Join(dir, "report.json")
	args := []string{
		"--workers", "1",
		"--timeout-coefficient", "5",
		"--coverpkg", "testmod",
		"--output", outPath,
		"--config", ".gomutant.yml",
		"--disable", "BRANCH_IF",
		"--dry-run",
		"--verbose",
		"testmod",
	}
	out, err := captureOutput(t, func() error {
		return run(context.Background(), args)
	})
	if err != nil {
		t.Fatalf("run with all long flags: %v", err)
	}
	// Dry-run prints "[PENDING]" or "[NOT COVERED]" markers per mutant.
	if !strings.Contains(out, "gomutant v0.1.0") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "Target: [testmod]") {
		t.Errorf("missing target line: %q", out)
	}
	if !strings.Contains(out, "Workers: 1") {
		t.Errorf("missing workers line: %q", out)
	}
	// Phase banners AND their paired PhaseDone outputs, as joined strings.
	// Checking only the prefix lets STATEMENT_REMOVE on PhaseDone(...) calls
	// survive (the overall output still has "done (" strings from other
	// phases). We check the full "Phase... PhaseDone" pair.
	joined := []string{
		"Resolving packages... done (1 packages)",
		"Collecting coverage... done (",   // "done (Ns)" — duration varies
		"Measuring baseline... done (",    // "done (Ns, timeout: Ns)"
		"Discovering mutants... ",         // "N found (N not covered, N to test)"
	}
	for _, s := range joined {
		if !strings.Contains(out, s) {
			t.Errorf("missing output %q: full output:\n%s", s, out)
		}
	}
}

// TestRunOnlyFlag exercises --only (separate test because it disables all
// other mutators).
func TestRunOnlyFlag(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"add.go":      "package testmod\n\nfunc Add(a, b int) int { return a + b }\n",
		"add_test.go": "package testmod\nimport \"testing\"\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"wrong\") } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	_, err := captureOutput(t, func() error {
		return run(context.Background(), []string{"--only", "ARITHMETIC_BASE", "--dry-run", "testmod"})
	})
	if err != nil {
		t.Fatalf("run --only: %v", err)
	}
}

// TestRunShortFlags exercises -w, -o, -v short-form flags, killing
// STATEMENT_REMOVE mutants on those fs.XxxVar shorthand registrations.
func TestRunShortFlags(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"add.go":      "package testmod\n\nfunc Add(a, b int) int { return a + b }\n",
		"add_test.go": "package testmod\nimport \"testing\"\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"wrong\") } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	outPath := filepath.Join(dir, "r.json")
	out, err := captureOutput(t, func() error {
		return run(context.Background(), []string{
			"-w", "1", "-o", outPath, "-v", "--dry-run", "testmod",
		})
	})
	if err != nil {
		t.Fatalf("run with short flags: %v", err)
	}
	if !strings.Contains(out, "Workers: 1") {
		t.Errorf("short -w didn't set workers: %q", out)
	}
	// Verbose mode triggers per-mutant markers like "[PENDING]" or "[NOT COVERED]".
	if !strings.Contains(out, "[") || !strings.Contains(out, "]") {
		t.Errorf("short -v verbose output missing brackets: %q", out)
	}
}

// TestRunPendingCountExact asserts the exact "N found (N not covered, N to test)"
// output for a known-shape testmod. Kills INCREMENT_DECREMENT on pendingCount++
// and notCoveredCount++, STATEMENT_REMOVE on those assignments, and
// CONDITIONALS_NEGATION on the status-comparison branches.
func TestRunPendingCountExact(t *testing.T) {
	dir := t.TempDir()
	// Two functions: Add is covered by TestAdd; Unused is not covered.
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"lib.go":      "package testmod\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Unused(x, y int) int { return x + y }\n",
		"lib_test.go": "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"wrong\") } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	out, err := captureOutput(t, func() error {
		return run(context.Background(), []string{
			"--only", "ARITHMETIC_BASE",
			"--dry-run",
			"testmod",
		})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Expected: 2 ARITHMETIC_BASE mutants total (Add's +, Unused's +).
	// Add is covered -> 1 to test. Unused is not covered -> 1 not covered.
	want := "2 found (1 not covered, 1 to test)"
	if !strings.Contains(out, want) {
		t.Errorf("expected counts %q in output, got: %q", want, out)
	}
}

// TestRunConfigLoadError kills BRANCH_IF on the config.Load error check.
// An invalid YAML file forces Load to return an error.
func TestRunConfigLoadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testmod\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Config with invalid YAML.
	cfgPath := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(cfgPath, []byte("not: valid: yaml: at: all:\n  : : :"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)
	err := run(context.Background(), []string{"--config", cfgPath, "--dry-run", "testmod"})
	if err == nil {
		t.Fatal("expected run() to return config.Load error")
	}
}

// TestRunResolvePackagesError kills BRANCH_IF on the discover.ResolvePackages
// error check by pointing at a package pattern go-list can't resolve.
func TestRunResolvePackagesError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testmod\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)
	err := run(context.Background(), []string{"--dry-run", "completely/nonexistent/package/xyz"})
	if err == nil {
		t.Fatal("expected run() to error on unresolvable package")
	}
}

// TestRunCoverageError kills BRANCH_IF on the runner.RunCoverage error check
// by providing a package with a failing test.
func TestRunCoverageError(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       "module testmod\n\ngo 1.26\n",
		"add.go":       "package testmod\n\nfunc Add(a, b int) int { return a + b }\n",
		"add_test.go":  "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { t.Fatal(\"always fail\") }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)
	// Non-dry-run so coverage collection actually runs.
	err := run(context.Background(), []string{
		"--only", "ARITHMETIC_BASE",
		"-w", "1",
		"-o", filepath.Join(dir, "report.json"),
		"testmod",
	})
	if err == nil {
		t.Fatal("expected run() to return coverage error when baseline tests fail")
	}
}

func TestRunDryRun(t *testing.T) {
	// Create a minimal Go project.
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"add.go":      "package testmod\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
		"add_test.go": "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Change to the temp dir so go list works.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	err := run(context.Background(), []string{"--dry-run", "--only", "ARITHMETIC_BASE", "testmod"})
	if err != nil {
		t.Fatalf("run --dry-run: %v", err)
	}
}

func TestRunFullPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess-spawning test in short mode (self-mutation guard)")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"add.go":      "package testmod\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
		"add_test.go": "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	outPath := filepath.Join(dir, "report.json")
	out, err := captureOutput(t, func() error {
		return run(context.Background(), []string{
			"--only", "ARITHMETIC_BASE",
			"-w", "1",
			"-o", outPath,
			"testmod",
		})
	})
	if err != nil {
		t.Fatalf("run full pipeline: %v", err)
	}

	// Verify every phase-banner line and the final report line.
	mustContain := []string{
		"gomutant v0.1.0",
		"Target: [testmod]",
		"Workers: 1 | Mutations: 1 types enabled",
		"Resolving packages...",
		"done (1 packages)",
		"Collecting coverage...",
		"Measuring baseline...",
		"Discovering mutants...",
		"Building per-test coverage map...",
		"Killed:",
		"Lived:",
		"Efficacy:",
		"Report: " + outPath,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q; full output:\n%s", s, out)
		}
	}

	// Verify report was written.
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("report not written: %v", err)
	}
}

// TestRunDryRunOutput asserts the exact dry-run line format, which kills
// STATEMENT_REMOVE on the dry-run Printf.
func TestRunDryRunOutput(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module testmod\n\ngo 1.26\n",
		"add.go":      "package testmod\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
		"add_test.go": "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	}
	for name, content := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	out, err := captureOutput(t, func() error {
		return run(context.Background(), []string{"--dry-run", "--only", "ARITHMETIC_BASE", "testmod"})
	})
	if err != nil {
		t.Fatalf("run dry-run: %v", err)
	}
	// Dry-run should print at least one "[PENDING]" line for the + mutation.
	if !strings.Contains(out, "[PENDING]") {
		t.Errorf("expected [PENDING] marker in dry-run output: %q", out)
	}
	if !strings.Contains(out, "+ → -") {
		t.Errorf("expected '+ → -' in dry-run output: %q", out)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a\nb\nc", 3},
		{"single", 1},
		{"a\nb\n", 2}, // trailing newline: "a", "b" (newline consumed, no trailing empty)
	}
	for _, tc := range tests {
		lines := splitLines([]byte(tc.input))
		if len(lines) != tc.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tc.input, len(lines), tc.want)
		}
	}
}
