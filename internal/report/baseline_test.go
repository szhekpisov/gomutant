package report

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

func baselineMutant(root, id string, line int, status mutator.MutantStatus) mutator.Mutant {
	return mutator.Mutant{
		StableID:    id,
		File:        filepath.Join(root, "p.go"),
		RelFile:     "p.go",
		Line:        line,
		Col:         3,
		Type:        mutator.ArithmeticBase,
		Original:    "+",
		Replacement: "-",
		Status:      status,
	}
}

func TestCompareBaselineClassifiesAndRetainsInconclusive(t *testing.T) {
	root := t.TempDir()
	before := []mutator.Mutant{
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#1", 10, mutator.StatusLived),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#2", 20, mutator.StatusLived),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#3", 30, mutator.StatusLived),
	}
	b := NewBaseline("example.com/p", "v1", BaselinePolicy{Packages: []string{"./..."}}, before, root)
	current := []mutator.Mutant{
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#1", 10, mutator.StatusLived),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#2", 20, mutator.StatusKilled),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#3", 30, mutator.StatusNotCovered),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#4", 40, mutator.StatusLived),
	}

	got := CompareBaseline(b, current, root)
	if len(got.Known) != 1 || got.Known[0].ID != "p.go:F:ARITHMETIC_BASE#1" {
		t.Fatalf("Known=%v, want #1", got.Known)
	}
	if len(got.New) != 1 || got.New[0].ID != "p.go:F:ARITHMETIC_BASE#4" {
		t.Fatalf("New=%v, want #4", got.New)
	}
	if len(got.Resolved) != 1 || got.Resolved[0].ID != "p.go:F:ARITHMETIC_BASE#2" {
		t.Fatalf("Resolved=%v, want #2", got.Resolved)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0].ID != "p.go:F:ARITHMETIC_BASE#3" {
		t.Fatalf("Unresolved=%v, want #3", got.Unresolved)
	}
	if ids := []string{got.Retained[0].ID, got.Retained[1].ID}; !slices.Equal(ids, []string{
		"p.go:F:ARITHMETIC_BASE#1", "p.go:F:ARITHMETIC_BASE#3",
	}) {
		t.Fatalf("Retained IDs=%v, want known + inconclusive and never the new survivor", ids)
	}
}

func TestCompareBaselineFallbacksAreConservative(t *testing.T) {
	root := t.TempDir()
	b := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{
		baselineMutant(root, "p.go:Old:ARITHMETIC_BASE#1", 10, mutator.StatusLived),
	}, root)

	rename := baselineMutant(root, "p.go:New:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	got := CompareBaseline(b, []mutator.Mutant{rename}, root)
	if len(got.Known) != 1 || len(got.New) != 0 || len(got.Fallbacks) != 1 || got.Fallbacks[0].Kind != "location" {
		t.Fatalf("rename comparison=%+v, want one location fallback known survivor", got)
	}

	lineShift := rename
	lineShift.Line = 25
	got = CompareBaseline(b, []mutator.Mutant{lineShift}, root)
	if len(got.Known) != 1 || len(got.New) != 0 || len(got.Fallbacks) != 1 || got.Fallbacks[0].Kind != "structure" {
		t.Fatalf("line-shift comparison=%+v, want one structure fallback known survivor", got)
	}

	ambiguous := &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      "example.com/p",
		Survivors: []BaselineEntry{
			baselineEntry(baselineMutant(root, "p.go:A:ARITHMETIC_BASE#1", 5, mutator.StatusLived), root),
			baselineEntry(baselineMutant(root, "p.go:B:ARITHMETIC_BASE#1", 15, mutator.StatusLived), root),
		},
	}
	got = CompareBaseline(ambiguous, []mutator.Mutant{lineShift}, root)
	if len(got.New) != 1 || len(got.Known) != 0 || len(got.Fallbacks) != 0 {
		t.Fatalf("ambiguous comparison=%+v, want fail-safe new survivor", got)
	}
}

func TestCompareBaselineReassignedExactIDDoesNotStealOldMutation(t *testing.T) {
	root := t.TempDir()
	old := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	b := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{old}, root)

	inserted := baselineMutant(root, old.StableID, 5, mutator.StatusKilled)
	inserted.Original = "*"
	inserted.Replacement = "/"
	movedOld := old
	movedOld.StableID = "p.go:init~2:ARITHMETIC_BASE#1"
	movedOld.Line = 20

	got := CompareBaseline(b, []mutator.Mutant{inserted, movedOld}, root)
	if len(got.Known) != 1 || got.Known[0].ID != movedOld.StableID || len(got.New) != 0 {
		t.Fatalf("comparison=%+v, want the moved old mutation matched and no new survivor", got)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0].OldID != old.StableID || got.Fallbacks[0].NewID != movedOld.StableID {
		t.Fatalf("Fallbacks=%+v, want old init ID migrated to ~2", got.Fallbacks)
	}
}

func TestBaselinePolicyCanonicalAndDifferences(t *testing.T) {
	p := BaselinePolicy{
		Packages:     []string{"./b", "./a", "./a"},
		Mutators:     []string{"B", "A"},
		ExcludeCalls: []string{"log.*", "fmt.Print*"},
	}
	canonical := p.Canonical()
	if !slices.Equal(canonical.Packages, []string{"./a", "./b"}) {
		t.Fatalf("Packages=%v", canonical.Packages)
	}
	if diff := canonical.Differences(BaselinePolicy{
		Packages:     []string{"./b", "./a"},
		Mutators:     []string{"A", "B"},
		ExcludeCalls: []string{"fmt.Print*", "log.*"},
	}); len(diff) != 0 {
		t.Fatalf("equivalent policies differ: %v", diff)
	}
	other := canonical
	other.TestFlags = "-short"
	if diff := canonical.Differences(other); !slices.Equal(diff, []string{"test-flags"}) {
		t.Fatalf("Differences=%v, want [test-flags]", diff)
	}
}

func TestBaselineWriteReadRoundTripAndSort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "baseline.json")
	b := &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      "example.com/p",
		GeneratedBy:   "v1",
		Policy:        BaselinePolicy{Packages: []string{"./..."}},
		Survivors: []BaselineEntry{
			{ID: "z.go:F:ARITHMETIC_BASE#1", File: "z.go", Line: 1, Column: 1, Type: "ARITHMETIC_BASE"},
			{ID: "a.go:F:ARITHMETIC_BASE#1", File: "a.go", Line: 1, Column: 1, Type: "ARITHMETIC_BASE"},
		},
	}
	if err := WriteBaseline(path, b); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"schema_version\"") {
		t.Fatalf("baseline is not indented: %s", data)
	}
	got, err := ReadBaseline(path)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if got.Survivors[0].ID != "a.go:F:ARITHMETIC_BASE#1" {
		t.Fatalf("Survivors not sorted: %+v", got.Survivors)
	}
}

func TestReadBaselineRejectsMissingMalformedAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadBaseline(filepath.Join(dir, "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing error=%v, want os.ErrNotExist", err)
	}
	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBaseline(malformed); err == nil || !strings.Contains(err.Error(), "parsing baseline") {
		t.Fatalf("malformed error=%v", err)
	}
	duplicate := filepath.Join(dir, "duplicate.json")
	data := `{"schema_version":1,"go_module":"example.com/p","policy":{},"survivors":[{"id":"x","file":"p.go","line":1,"column":1,"type":"T"},{"id":"x","file":"p.go","line":2,"column":1,"type":"T"}]}`
	if err := os.WriteFile(duplicate, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBaseline(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate survivor") {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestApplyBaselineComparisonClassifiesReport(t *testing.T) {
	r := &Report{Files: []FileReport{{Mutations: []MutationReport{{ID: "known"}, {ID: "new"}, {ID: "other"}}}}}
	c := BaselineComparison{
		Known:    []BaselineEntry{{ID: "known"}},
		New:      []BaselineEntry{{ID: "new"}},
		Resolved: []BaselineEntry{{ID: "gone"}},
		knownIDs: map[string]struct{}{"known": {}},
		newIDs:   map[string]struct{}{"new": {}},
	}
	ApplyBaselineComparison(r, c)
	if r.Baseline == nil || r.Baseline.KnownSurvivors != 1 || r.Baseline.NewSurvivors != 1 || r.Baseline.ResolvedSurvivors != 1 {
		t.Fatalf("Baseline=%+v", r.Baseline)
	}
	got := r.Files[0].Mutations
	if got[0].BaselineStatus != BaselineStatusKnown || got[1].BaselineStatus != BaselineStatusNew || got[2].BaselineStatus != "" {
		t.Fatalf("mutation classifications=%+v", got)
	}
}
