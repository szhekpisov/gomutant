package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

// setupTestProject creates a minimal Go project in a temp directory.
func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	goMod := "module testmod\n\ngo 1.26\n"
	src := "package testmod\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	testSrc := "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n"

	for name, content := range map[string]string{
		"go.mod":      goMod,
		"add.go":      src,
		"add_test.go": testSrc,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPoolRunNoPending(t *testing.T) {
	p := NewPool(2, 30*time.Second, t.TempDir(), nil, ".", nil)
	mutants := []mutator.Mutant{
		{ID: 1, Status: mutator.StatusNotCovered},
		{ID: 2, Status: mutator.StatusKilled},
	}

	result := p.Run(context.Background(), mutants, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 mutants returned, got %d", len(result))
	}
	if result[0].Status != mutator.StatusNotCovered {
		t.Errorf("mutant 1 status=%v, want NOT_COVERED", result[0].Status)
	}
	if result[1].Status != mutator.StatusKilled {
		t.Errorf("mutant 2 status=%v, want KILLED", result[1].Status)
	}
}

func TestPoolRunEmpty(t *testing.T) {
	p := NewPool(2, 30*time.Second, t.TempDir(), nil, ".", nil)
	result := p.Run(context.Background(), nil, nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

func TestPoolRunWithPending(t *testing.T) {
	dir := setupTestProject(t)
	srcPath := filepath.Join(dir, "add.go")
	src, _ := os.ReadFile(srcPath)
	cache := map[string][]byte{srcPath: src}

	// Find the "+" offset.
	plusIdx := 0
	for i, c := range string(src) {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	tmpDir := t.TempDir()
	p := NewPool(2, 30*time.Second, tmpDir, cache, dir, nil)

	mutants := []mutator.Mutant{
		{
			ID:          1,
			File:        srcPath,
			Pkg:         "testmod",
			StartOffset: plusIdx,
			EndOffset:   plusIdx + 1,
			Replacement: "-",
			Status:      mutator.StatusPending,
		},
		{
			ID:          2,
			File:        srcPath,
			Pkg:         "testmod",
			StartOffset: plusIdx,
			EndOffset:   plusIdx + 1,
			Replacement: "*",
			Status:      mutator.StatusPending,
		},
		{
			ID:          3,
			Status:      mutator.StatusNotCovered,
		},
	}

	var callbackCount int
	result := p.Run(context.Background(), mutants, func(m mutator.Mutant) {
		callbackCount++
	})

	if len(result) != 3 {
		t.Fatalf("expected 3 mutants, got %d", len(result))
	}

	// Mutants 1 and 2 should be tested (KILLED since tests check result).
	if result[0].Status != mutator.StatusKilled {
		t.Errorf("mutant 1: status=%v, want KILLED", result[0].Status)
	}
	if result[1].Status != mutator.StatusKilled {
		t.Errorf("mutant 2: status=%v, want KILLED", result[1].Status)
	}
	// Mutant 3 unchanged.
	if result[2].Status != mutator.StatusNotCovered {
		t.Errorf("mutant 3: status=%v, want NOT_COVERED", result[2].Status)
	}
	if callbackCount != 2 {
		t.Errorf("callback called %d times, want 2", callbackCount)
	}
}

func TestPoolRunCancelled(t *testing.T) {
	dir := setupTestProject(t)
	srcPath := filepath.Join(dir, "add.go")
	src, _ := os.ReadFile(srcPath)
	cache := map[string][]byte{srcPath: src}

	plusIdx := 0
	for i, c := range string(src) {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	p := NewPool(1, 30*time.Second, t.TempDir(), cache, dir, nil)

	mutants := []mutator.Mutant{
		{ID: 1, File: srcPath, Pkg: "testmod", StartOffset: plusIdx, EndOffset: plusIdx + 1, Replacement: "-", Status: mutator.StatusPending},
	}

	// Should not hang — cancelled context means workers exit early.
	result := p.Run(ctx, mutants, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 mutant, got %d", len(result))
	}
}

func TestMeasureBaseline(t *testing.T) {
	dir := setupTestProject(t)
	duration, err := MeasureBaseline(context.Background(), dir, []string{"testmod"})
	if err != nil {
		t.Fatalf("MeasureBaseline: %v", err)
	}
	if duration <= 0 {
		t.Errorf("duration=%v, want > 0", duration)
	}
}

func TestMeasureBaselineFailure(t *testing.T) {
	_, err := MeasureBaseline(context.Background(), t.TempDir(), []string{"nonexistent/pkg"})
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
}

func TestRunCoverage(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()
	profilePath, err := RunCoverage(context.Background(), dir, []string{"testmod"}, "", tmpDir)
	if err != nil {
		t.Fatalf("RunCoverage: %v", err)
	}

	// Verify profile file was created and is non-empty.
	info, err := os.Stat(profilePath)
	if err != nil {
		t.Fatalf("profile not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("profile file is empty")
	}
}

func TestRunCoverageWithCoverpkg(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()
	_, err := RunCoverage(context.Background(), dir, []string{"testmod"}, "testmod", tmpDir)
	if err != nil {
		t.Fatalf("RunCoverage with coverpkg: %v", err)
	}
}

func TestRunCoverageFailure(t *testing.T) {
	_, err := RunCoverage(context.Background(), t.TempDir(), []string{"nonexistent/pkg"}, "", t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
}
