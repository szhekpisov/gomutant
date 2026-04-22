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
	if !strings.Contains(out, "Resolving packages...") {
		t.Errorf("missing resolving phase: %q", out)
	}
	if !strings.Contains(out, "Collecting coverage...") {
		t.Errorf("missing coverage phase: %q", out)
	}
	if !strings.Contains(out, "Measuring baseline...") {
		t.Errorf("missing baseline phase: %q", out)
	}
	if !strings.Contains(out, "Discovering mutants...") {
		t.Errorf("missing discover phase: %q", out)
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
