package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

func TestGenerate(t *testing.T) {
	mutants := []mutator.Mutant{
		{ID: 1, Type: mutator.ArithmeticBase, RelFile: "a.go", Line: 10, Col: 5, Status: mutator.StatusKilled},
		{ID: 2, Type: mutator.ArithmeticBase, RelFile: "a.go", Line: 20, Col: 3, Status: mutator.StatusKilled},
		{ID: 3, Type: mutator.ConditionalsBoundary, RelFile: "b.go", Line: 5, Col: 1, Status: mutator.StatusLived},
		{ID: 4, Type: mutator.BranchIf, RelFile: "b.go", Line: 8, Col: 2, Status: mutator.StatusNotCovered},
		{ID: 5, Type: mutator.BranchElse, RelFile: "a.go", Line: 30, Col: 1, Status: mutator.StatusNotViable},
	}

	r := Generate(mutants, "example.com/mod", 120*time.Second)

	if r.GoModule != "example.com/mod" {
		t.Errorf("GoModule=%q", r.GoModule)
	}
	if r.MutantsTotal != 5 {
		t.Errorf("Total=%d, want 5", r.MutantsTotal)
	}
	if r.MutantsKilled != 2 {
		t.Errorf("Killed=%d, want 2", r.MutantsKilled)
	}
	if r.MutantsLived != 1 {
		t.Errorf("Lived=%d, want 1", r.MutantsLived)
	}
	if r.MutantsNotCovered != 1 {
		t.Errorf("NotCovered=%d, want 1", r.MutantsNotCovered)
	}
	if r.MutantsNotViable != 1 {
		t.Errorf("NotViable=%d, want 1", r.MutantsNotViable)
	}

	// Efficacy: 2 killed / (2 killed + 1 lived) = 66.67%
	wantEfficacy := float64(2) / float64(3) * 100
	if r.TestEfficacy != wantEfficacy {
		t.Errorf("TestEfficacy=%f, want %f", r.TestEfficacy, wantEfficacy)
	}

	// Coverage: (5 - 1 not covered) / 5 = 80%
	wantCoverage := float64(4) / float64(5) * 100
	if r.MutationsCoverage != wantCoverage {
		t.Errorf("MutationsCoverage=%f, want %f", r.MutationsCoverage, wantCoverage)
	}

	if r.ElapsedTime != 120.0 {
		t.Errorf("ElapsedTime=%f, want 120.0", r.ElapsedTime)
	}

	// Files should preserve insertion order.
	if len(r.Files) != 2 {
		t.Fatalf("Files count=%d, want 2", len(r.Files))
	}
	if r.Files[0].FileName != "a.go" {
		t.Errorf("Files[0]=%q, want a.go", r.Files[0].FileName)
	}
	if r.Files[1].FileName != "b.go" {
		t.Errorf("Files[1]=%q, want b.go", r.Files[1].FileName)
	}
	if len(r.Files[0].Mutations) != 3 {
		t.Errorf("Files[0] mutations=%d, want 3", len(r.Files[0].Mutations))
	}
	if len(r.Files[1].Mutations) != 2 {
		t.Errorf("Files[1] mutations=%d, want 2", len(r.Files[1].Mutations))
	}

	// Mutator statistics should exclude NOT_COVERED.
	if r.MutatorStatistics["arithmetic_base"] != 2 {
		t.Errorf("arithmetic_base stat=%d, want 2", r.MutatorStatistics["arithmetic_base"])
	}
	if r.MutatorStatistics["conditionals_boundary"] != 1 {
		t.Errorf("conditionals_boundary stat=%d, want 1", r.MutatorStatistics["conditionals_boundary"])
	}
	if _, ok := r.MutatorStatistics["branch_if"]; ok {
		t.Error("branch_if should not appear in stats (NOT_COVERED)")
	}
}

func TestGenerateEmpty(t *testing.T) {
	r := Generate(nil, "example.com/mod", 0)
	if r.MutantsTotal != 0 {
		t.Errorf("Total=%d, want 0", r.MutantsTotal)
	}
	if r.TestEfficacy != 0 {
		t.Errorf("Efficacy=%f, want 0 (no tested mutants)", r.TestEfficacy)
	}
	if r.MutationsCoverage != 0 {
		t.Errorf("Coverage=%f, want 0 (no mutants)", r.MutationsCoverage)
	}
}

func TestGenerateAllKilled(t *testing.T) {
	mutants := []mutator.Mutant{
		{ID: 1, Type: mutator.ArithmeticBase, RelFile: "a.go", Line: 1, Col: 1, Status: mutator.StatusKilled},
		{ID: 2, Type: mutator.ArithmeticBase, RelFile: "a.go", Line: 2, Col: 1, Status: mutator.StatusKilled},
	}
	r := Generate(mutants, "mod", time.Second)
	if r.TestEfficacy != 100 {
		t.Errorf("Efficacy=%f, want 100", r.TestEfficacy)
	}
	if r.MutationsCoverage != 100 {
		t.Errorf("Coverage=%f, want 100", r.MutationsCoverage)
	}
}

func TestWriteJSON(t *testing.T) {
	r := &Report{
		GoModule:    "example.com/mod",
		MutantsTotal: 10,
		MutantsKilled: 8,
		TestEfficacy: 100,
		MutatorStatistics: map[string]int{"arithmetic_base": 5},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "report.json")

	if err := WriteJSON(r, path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var loaded Report
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.GoModule != "example.com/mod" {
		t.Errorf("GoModule=%q", loaded.GoModule)
	}
	if loaded.MutantsTotal != 10 {
		t.Errorf("Total=%d", loaded.MutantsTotal)
	}
}
