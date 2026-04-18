package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/szhekpisov/gomutant/internal/report"
)

func TestIntegrationSimple(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir, _ := os.Getwd()
	outPath := filepath.Join(t.TempDir(), "report.json")

	err := run(context.Background(), []string{
		"-w", "4",
		"-o", outPath,
		"./testdata/simple/",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	r := loadReport(t, outPath)

	// Expected: 34 total, 6 not covered, 28 tested.
	if r.MutantsTotal != 34 {
		t.Errorf("total=%d, want 34", r.MutantsTotal)
	}
	if r.MutantsNotCovered != 6 {
		t.Errorf("not_covered=%d, want 6", r.MutantsNotCovered)
	}

	// All covered mutants should be either killed, lived, or not viable.
	tested := r.MutantsKilled + r.MutantsLived + r.MutantsNotViable
	if tested != 28 {
		t.Errorf("tested=%d (killed=%d lived=%d not_viable=%d), want 28 total",
			tested, r.MutantsKilled, r.MutantsLived, r.MutantsNotViable)
	}

	// Efficacy should be > 80% (strong tests).
	if r.TestEfficacy < 80 {
		t.Errorf("efficacy=%.1f%%, want >= 80%%", r.TestEfficacy)
	}

	_ = dir
}

func TestIntegrationUntested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	outPath := filepath.Join(t.TempDir(), "report.json")

	err := run(context.Background(), []string{
		"-w", "4",
		"-o", outPath,
		"./testdata/untested/",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	r := loadReport(t, outPath)

	// Expected: 6 total, 2 not covered (IsEven has no test).
	if r.MutantsTotal != 6 {
		t.Errorf("total=%d, want 6", r.MutantsTotal)
	}
	if r.MutantsNotCovered != 2 {
		t.Errorf("not_covered=%d, want 2", r.MutantsNotCovered)
	}

	// Weak tests — some mutants should survive.
	if r.MutantsLived == 0 {
		t.Error("expected some lived mutants with weak tests")
	}

	// Efficacy should be < 100% (intentionally weak tests).
	if r.TestEfficacy >= 100 {
		t.Errorf("efficacy=%.1f%%, want < 100%% with weak tests", r.TestEfficacy)
	}
}

func loadReport(t *testing.T, path string) *report.Report {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	var r report.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("parsing report: %v", err)
	}
	return &r
}
