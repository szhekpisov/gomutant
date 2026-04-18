package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunVersion(t *testing.T) {
	err := run(context.Background(), []string{"--version"})
	if err != nil {
		t.Fatalf("run --version: %v", err)
	}
}

func TestRunUnleash(t *testing.T) {
	// "unleash" should be stripped — then --version runs normally.
	err := run(context.Background(), []string{"unleash", "--version"})
	if err != nil {
		t.Fatalf("run unleash --version: %v", err)
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
	err := run(context.Background(), []string{
		"--only", "ARITHMETIC_BASE",
		"-w", "1",
		"-o", outPath,
		"testmod",
	})
	if err != nil {
		t.Fatalf("run full pipeline: %v", err)
	}

	// Verify report was written.
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("report not written: %v", err)
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
