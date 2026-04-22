package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

func TestNewWorker(t *testing.T) {
	dir := t.TempDir()
	cache := map[string][]byte{"/src/file.go": []byte("package p\n")}

	w, err := NewWorker(0, dir, 30*time.Second, cache, "/src", nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	if w.id != 0 {
		t.Errorf("id=%d, want 0", w.id)
	}

	// Verify temp files were created.
	if _, err := os.Stat(w.tmpSrcPath); err != nil {
		t.Errorf("tmpSrcPath not created: %v", err)
	}
	if _, err := os.Stat(w.overlayPath); err != nil {
		t.Errorf("overlayPath not created: %v", err)
	}
}

func TestWorkerTestMissingSource(t *testing.T) {
	dir := t.TempDir()
	cache := map[string][]byte{} // Empty cache.

	w, err := NewWorker(0, dir, 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID:   1,
		File: "/nonexistent/file.go",
		Status: mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for missing source", result.Status)
	}
	// Duration must be set even on early return paths.
	if result.Duration <= 0 {
		t.Errorf("Duration should be > 0 on early-return path, got %v", result.Duration)
	}
}

func TestWorkerTestInvalidPatch(t *testing.T) {
	dir := t.TempDir()
	src := []byte("package p\n")
	cache := map[string][]byte{"/src/file.go": src}

	w, err := NewWorker(0, dir, 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID:          1,
		File:        "/src/file.go",
		StartOffset: 100, // Beyond file length.
		EndOffset:   200,
		Replacement: "x",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for invalid patch", result.Status)
	}
	if result.Duration <= 0 {
		t.Errorf("Duration should be > 0 on early-return path, got %v", result.Duration)
	}
}

func TestWorkerTestNotViable(t *testing.T) {
	// Create a small Go project that will fail to compile with the mutation.
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	// Replace entire file with code that has an undefined symbol (compile error).
	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: 0,
		EndOffset:   len(src),
		Replacement: "package testpkg\n\nfunc Add(a, b int) int {\n\treturn UNDEFINED_SYMBOL\n}\n",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for compile error", result.Status)
	}
}

func TestWorkerTestKilled(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	// Mutate + to - (test should fail → KILLED).
	plusIdx := 51 // "a + b" — the "+" position
	for i, c := range src {
		if c == '+' && i > 30 { // Skip package line
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusKilled {
		t.Errorf("Status=%v, want KILLED", result.Status)
	}
	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}
}

func TestWorkerTestLived(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	// This function's test doesn't check the operator, so the mutant survives.
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	// Weak test: doesn't verify the result.
	_ = Add(1, 2)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusLived {
		t.Errorf("Status=%v, want LIVED", result.Status)
	}
}

func TestWorkerTestTimeout(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	// Test that will run forever.
	testSrc := `package testpkg

import "testing"
import "time"

func TestAdd(t *testing.T) {
	time.Sleep(10 * time.Minute)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	// Very short timeout.
	w, err := NewWorker(0, t.TempDir(), 3*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusTimedOut {
		t.Errorf("Status=%v, want TIMED_OUT", result.Status)
	}
}

func TestCompileErrorRegex(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"./file.go:10:5: undefined: foo", true},
		{"main.go:1:1: expected declaration", true},
		{"FAIL\ttestmod\t0.001s", false},
		{"ok  \ttestmod\t0.001s", false},
	}
	for _, tc := range tests {
		if got := compileErrorRe.MatchString(tc.input); got != tc.match {
			t.Errorf("compileErrorRe.Match(%q) = %v, want %v", tc.input, got, tc.match)
		}
	}
}
