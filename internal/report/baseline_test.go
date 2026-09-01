package report

import (
	"encoding/json"
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

func TestCompareBaselineSortsEveryResultAndPopulatesReportIDs(t *testing.T) {
	root := t.TempDir()
	mutant := func(id string, line int, status mutator.MutantStatus) mutator.Mutant {
		m := baselineMutant(root, id, line, status)
		m.Original = id
		m.Replacement = "replacement-" + id
		return m
	}
	baselineMutants := []mutator.Mutant{
		mutant("z-resolved", 1, mutator.StatusLived),
		mutant("z-known", 2, mutator.StatusLived),
		mutant("z-unresolved", 6, mutator.StatusLived),
		mutant("a-resolved", 4, mutator.StatusLived),
		mutant("a-known", 5, mutator.StatusLived),
		mutant("a-unresolved", 3, mutator.StatusLived),
	}
	b := &Baseline{Survivors: baselineEntries(baselineMutants, root)}
	current := []mutator.Mutant{
		mutant("z-new", 10, mutator.StatusLived),
		mutant("z-unresolved", 6, mutator.StatusNotCovered),
		mutant("z-known", 2, mutator.StatusLived),
		mutant("a-unresolved", 3, mutator.StatusTimedOut),
		mutant("a-known", 5, mutator.StatusLived),
		mutant("a-new", 11, mutator.StatusLived),
	}

	got := CompareBaseline(b, current, root)
	ids := func(entries []BaselineEntry) []string {
		out := make([]string, len(entries))
		for i, entry := range entries {
			out[i] = entry.ID
		}
		return out
	}
	if value := ids(got.Known); !slices.Equal(value, []string{"a-known", "z-known"}) {
		t.Fatalf("Known=%v", value)
	}
	if value := ids(got.New); !slices.Equal(value, []string{"a-new", "z-new"}) {
		t.Fatalf("New=%v", value)
	}
	if value := ids(got.Resolved); !slices.Equal(value, []string{"a-resolved", "z-resolved"}) {
		t.Fatalf("Resolved=%v", value)
	}
	if value := ids(got.Unresolved); !slices.Equal(value, []string{"a-unresolved", "z-unresolved"}) {
		t.Fatalf("Unresolved=%v", value)
	}
	if value := ids(got.Retained); !slices.Equal(value, []string{"a-known", "a-unresolved", "z-known", "z-unresolved"}) {
		t.Fatalf("Retained=%v", value)
	}

	r := &Report{Files: []FileReport{{Mutations: []MutationReport{{ID: "z-known"}, {ID: "a-new"}}}}}
	ApplyBaselineComparison(r, got)
	if statuses := []string{r.Files[0].Mutations[0].BaselineStatus, r.Files[0].Mutations[1].BaselineStatus}; !slices.Equal(statuses, []string{BaselineStatusKnown, BaselineStatusNew}) {
		t.Fatalf("baseline statuses=%v", statuses)
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

func TestCompareBaselineSortsFallbacks(t *testing.T) {
	root := t.TempDir()
	oldZ := baselineMutant(root, "z-old", 10, mutator.StatusLived)
	oldZ.Original = "+"
	oldA := baselineMutant(root, "a-old", 20, mutator.StatusLived)
	oldA.Original = "*"
	oldM := baselineMutant(root, "m-old", 30, mutator.StatusLived)
	oldM.Original = "/"
	b := &Baseline{Survivors: baselineEntries([]mutator.Mutant{oldZ, oldA, oldM}, root)}
	newZ, newA, newM := oldZ, oldA, oldM
	newZ.StableID, newZ.Line = "z-new", 30
	newA.StableID, newA.Line = "a-new", 40
	newM.StableID, newM.Line = "m-new", 50

	got := CompareBaseline(b, []mutator.Mutant{newZ, newA, newM}, root)
	if len(got.Fallbacks) != 3 || got.Fallbacks[0].OldID != "a-old" ||
		got.Fallbacks[1].OldID != "m-old" || got.Fallbacks[2].OldID != "z-old" {
		t.Fatalf("Fallbacks=%+v, want sorted by old ID", got.Fallbacks)
	}
}

func TestCompareBaselineNeedsLocationFallbackWhenStructureIsAmbiguous(t *testing.T) {
	root := t.TempDir()
	oldA := baselineMutant(root, "old-a", 10, mutator.StatusLived)
	oldB := baselineMutant(root, "old-b", 20, mutator.StatusLived)
	newA, newB := oldA, oldB
	newA.StableID, newB.StableID = "new-a", "new-b"
	b := &Baseline{Survivors: baselineEntries([]mutator.Mutant{oldA, oldB}, root)}

	got := CompareBaseline(b, []mutator.Mutant{newA, newB}, root)
	if len(got.Known) != 2 || len(got.New) != 0 || len(got.Fallbacks) != 2 {
		t.Fatalf("comparison=%+v, want two unique location fallbacks", got)
	}
}

func baselineTestEntry(id, file, original string, line int) BaselineEntry {
	return BaselineEntry{ID: id, File: file, Line: line, Column: 3, Type: "T", Original: original, Replacement: "r"}
}

func TestFallbackMatchLocationMatchesDistinctKeys(t *testing.T) {
	baseline := []BaselineEntry{baselineTestEntry("old-a", "p.go", "+", 10), baselineTestEntry("old-b", "p.go", "+", 20)}
	current := []BaselineEntry{baselineTestEntry("new-b", "p.go", "+", 20), baselineTestEntry("new-a", "p.go", "+", 10)}
	baseMatch, currentMatch := unmatchedIndexes(len(baseline), len(current))
	fallbackMatchLocation(baseline, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{1, 0}) || !slices.Equal(currentMatch, []int{1, 0}) {
		t.Fatalf("baseMatch=%v currentMatch=%v", baseMatch, currentMatch)
	}
}

func TestFallbackMatchLocationRejectsDuplicateBaseline(t *testing.T) {
	baseline := []BaselineEntry{baselineTestEntry("old-a", "p.go", "+", 10), baselineTestEntry("old-b", "p.go", "+", 10)}
	current := []BaselineEntry{baselineTestEntry("new", "p.go", "+", 10)}
	baseMatch, currentMatch := unmatchedIndexes(len(baseline), len(current))
	fallbackMatchLocation(baseline, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{-1, -1}) || !slices.Equal(currentMatch, []int{-1}) {
		t.Fatalf("ambiguous baseline matched: %v %v", baseMatch, currentMatch)
	}
}

func TestFallbackMatchLocationRejectsDuplicateCurrent(t *testing.T) {
	baseline := []BaselineEntry{baselineTestEntry("old", "p.go", "+", 10)}
	current := []BaselineEntry{baselineTestEntry("new-a", "p.go", "+", 10), baselineTestEntry("new-b", "p.go", "+", 10)}
	baseMatch, currentMatch := unmatchedIndexes(len(baseline), len(current))
	fallbackMatchLocation(baseline, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{-1}) || !slices.Equal(currentMatch, []int{-1, -1}) {
		t.Fatalf("ambiguous current matched: %v %v", baseMatch, currentMatch)
	}
}

func TestFallbackMatchLocationSkipsPreMatchedIndexZero(t *testing.T) {
	baseline := []BaselineEntry{baselineTestEntry("old-a", "p.go", "+", 10), baselineTestEntry("old-b", "p.go", "+", 20)}
	current := []BaselineEntry{baselineTestEntry("new-a", "p.go", "+", 20), baselineTestEntry("new-b", "p.go", "+", 10)}
	baseMatch, currentMatch := []int{0, -1}, []int{0, -1}
	fallbackMatchLocation(baseline, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{0, -1}) || !slices.Equal(currentMatch, []int{0, -1}) {
		t.Fatalf("pre-matched entries were reconsidered: %v %v", baseMatch, currentMatch)
	}
}

func TestFallbackMatchStructureMatchesDistinctKeys(t *testing.T) {
	baseline := []BaselineEntry{baselineTestEntry("old-a", "a.go", "+", 10), baselineTestEntry("old-b", "b.go", "*", 20)}
	current := []BaselineEntry{baselineTestEntry("new-b", "b.go", "*", 40), baselineTestEntry("new-a", "a.go", "+", 30)}
	baseMatch, currentMatch := unmatchedIndexes(len(baseline), len(current))
	fallbackMatchStructure(baseline, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{1, 0}) || !slices.Equal(currentMatch, []int{1, 0}) {
		t.Fatalf("baseMatch=%v currentMatch=%v", baseMatch, currentMatch)
	}
}

func TestFallbackMatchStructureRejectsDuplicateBaseline(t *testing.T) {
	one := baselineTestEntry("one", "p.go", "+", 10)
	two := baselineTestEntry("two", "p.go", "+", 20)
	current := []BaselineEntry{baselineTestEntry("new", "p.go", "+", 30)}
	baseMatch, currentMatch := unmatchedIndexes(2, 1)
	fallbackMatchStructure([]BaselineEntry{one, two}, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{-1, -1}) || !slices.Equal(currentMatch, []int{-1}) {
		t.Fatalf("ambiguous baseline matched: %v %v", baseMatch, currentMatch)
	}
}

func TestFallbackMatchStructureRejectsDuplicateCurrent(t *testing.T) {
	baseline := []BaselineEntry{baselineTestEntry("one", "p.go", "+", 10)}
	current := []BaselineEntry{baselineTestEntry("new-a", "p.go", "+", 30), baselineTestEntry("new-b", "p.go", "+", 40)}
	baseMatch, currentMatch := unmatchedIndexes(1, 2)
	fallbackMatchStructure(baseline, current, baseMatch, currentMatch)
	if !slices.Equal(baseMatch, []int{-1}) || !slices.Equal(currentMatch, []int{-1, -1}) {
		t.Fatalf("ambiguous current matched: %v %v", baseMatch, currentMatch)
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

func TestCompareBaselineDoesNotMatchOneCurrentMutantTwice(t *testing.T) {
	root := t.TempDir()
	current := baselineMutant(root, "duplicate-id", 10, mutator.StatusLived)
	entry := baselineEntry(current, root)
	b := &Baseline{Survivors: []BaselineEntry{entry, entry}}
	got := CompareBaseline(b, []mutator.Mutant{current}, root)
	if len(got.Known) != 1 || len(got.Resolved) != 1 {
		t.Fatalf("comparison=%+v, want one known and one unmatched baseline entry", got)
	}
}

func TestBaselinePolicyCanonicalAndDifferences(t *testing.T) {
	p := BaselinePolicy{
		Packages:     []string{"./b", "./a", "./a"},
		Mutators:     []string{"B", "A", "A"},
		ExcludeFiles: []string{"z/**", "a/**", "a/**"},
		ExcludeCalls: []string{"log.*", "fmt.Print*"},
	}
	canonical := p.Canonical()
	if !slices.Equal(canonical.Packages, []string{"./a", "./b"}) {
		t.Fatalf("Packages=%v", canonical.Packages)
	}
	if !slices.Equal(canonical.Mutators, []string{"A", "B"}) ||
		!slices.Equal(canonical.ExcludeFiles, []string{"a/**", "z/**"}) ||
		!slices.Equal(canonical.ExcludeCalls, []string{"fmt.Print*", "log.*"}) {
		t.Fatalf("canonical policy=%+v", canonical)
	}
	if diff := p.Differences(BaselinePolicy{
		Packages:     []string{"./b", "./a"},
		Mutators:     []string{"A", "B"},
		ExcludeFiles: []string{"z/**", "a/**"},
		ExcludeCalls: []string{"fmt.Print*", "log.*"},
	}); len(diff) != 0 {
		t.Fatalf("equivalent policies differ: %v", diff)
	}
	p.Packages[0], p.Mutators[0], p.ExcludeFiles[0], p.ExcludeCalls[0] = "changed", "changed", "changed", "changed"
	if !slices.Equal(canonical.Packages, []string{"./a", "./b"}) ||
		!slices.Equal(canonical.Mutators, []string{"A", "B"}) ||
		!slices.Equal(canonical.ExcludeFiles, []string{"a/**", "z/**"}) ||
		!slices.Equal(canonical.ExcludeCalls, []string{"fmt.Print*", "log.*"}) {
		t.Fatalf("Canonical aliases its input: %+v", canonical)
	}

	base := BaselinePolicy{
		Packages: []string{"./..."}, Mutators: []string{"A"}, BuildTags: "tag", TestFlags: "-short",
		Integration: true, CoverPkg: "./...", DetectEquivalent: true,
		ExcludeFiles: []string{"vendor/**"}, ExcludeCalls: []string{"fmt.Print*"},
	}
	cases := []struct {
		want   string
		change func(*BaselinePolicy)
	}{
		{"packages", func(p *BaselinePolicy) { p.Packages = []string{"./other"} }},
		{"mutators", func(p *BaselinePolicy) { p.Mutators = []string{"B"} }},
		{"tags", func(p *BaselinePolicy) { p.BuildTags = "other" }},
		{"test-flags", func(p *BaselinePolicy) { p.TestFlags = "-run TestOne" }},
		{"integration", func(p *BaselinePolicy) { p.Integration = false }},
		{"coverpkg", func(p *BaselinePolicy) { p.CoverPkg = "./internal/..." }},
		{"detect-equivalent", func(p *BaselinePolicy) { p.DetectEquivalent = false }},
		{"exclude-files", func(p *BaselinePolicy) { p.ExcludeFiles = []string{"generated/**"} }},
		{"exclude-calls", func(p *BaselinePolicy) { p.ExcludeCalls = []string{"log.*"} }},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			other := base
			tc.change(&other)
			if diff := base.Differences(other); !slices.Equal(diff, []string{tc.want}) {
				t.Fatalf("Differences=%v, want [%s]", diff, tc.want)
			}
		})
	}
}

func TestBaselineValidateRejectsEveryInvalidField(t *testing.T) {
	valid := func() Baseline {
		return Baseline{
			SchemaVersion: BaselineSchemaVersion,
			GoModule:      "example.com/p",
			Survivors: []BaselineEntry{{
				ID: "id", File: "p.go", Line: 1, Column: 1, Type: "T",
			}},
		}
	}
	cases := []struct {
		name, want string
		change     func(*Baseline)
	}{
		{"schema", "schema_version", func(b *Baseline) { b.SchemaVersion = BaselineSchemaVersion + 1 }},
		{"module", "go_module", func(b *Baseline) { b.GoModule = "  " }},
		{"empty id", ".id is empty", func(b *Baseline) { b.Survivors[0].ID = "" }},
		{"duplicate id", "duplicate survivor", func(b *Baseline) { b.Survivors = append(b.Survivors, b.Survivors[0]) }},
		{"empty file", ".file is empty", func(b *Baseline) { b.Survivors[0].File = "" }},
		{"empty type", ".type is empty", func(b *Baseline) { b.Survivors[0].Type = "" }},
		{"zero line", "invalid position", func(b *Baseline) { b.Survivors[0].Line = 0 }},
		{"negative line", "invalid position", func(b *Baseline) { b.Survivors[0].Line = -1 }},
		{"zero column", "invalid position", func(b *Baseline) { b.Survivors[0].Column = 0 }},
		{"negative column", "invalid position", func(b *Baseline) { b.Survivors[0].Column = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := valid()
			tc.change(&b)
			if err := b.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestNewAndUpdatedBaselineCanonicalizeSortAndClone(t *testing.T) {
	root := t.TempDir()
	z := baselineMutant(root, "z", 2, mutator.StatusLived)
	a := baselineMutant(root, "a", 1, mutator.StatusLived)
	dead := baselineMutant(root, "dead", 3, mutator.StatusKilled)
	b := NewBaseline("example.com/p", "v1", BaselinePolicy{Packages: []string{"z", "a"}}, []mutator.Mutant{z, dead, a}, root)
	if got := []string{b.Survivors[0].ID, b.Survivors[1].ID}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("new baseline survivors=%v", got)
	}
	if !slices.Equal(b.Policy.Packages, []string{"a", "z"}) {
		t.Fatalf("new baseline policy=%v", b.Policy.Packages)
	}

	comparison := BaselineComparison{Retained: []BaselineEntry{{ID: "z"}, {ID: "a"}}}
	updated := UpdatedBaseline("example.com/p", "v2", BaselinePolicy{Mutators: []string{"Z", "A"}}, comparison)
	comparison.Retained[0].ID = "changed"
	if updated.SchemaVersion != BaselineSchemaVersion || updated.GoModule != "example.com/p" || updated.GeneratedBy != "v2" {
		t.Fatalf("updated baseline metadata=%+v", updated)
	}
	if got := []string{updated.Survivors[0].ID, updated.Survivors[1].ID}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("updated baseline survivors=%v", got)
	}
	if !slices.Equal(updated.Policy.Mutators, []string{"A", "Z"}) {
		t.Fatalf("updated baseline policy=%v", updated.Policy.Mutators)
	}
}

func TestBaselineWriteReadRoundTripAndSort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "baseline.json")
	b := &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      "example.com/p",
		GeneratedBy:   "v1",
		Policy:        BaselinePolicy{Packages: []string{"./z", "./a"}},
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
	if b.Survivors[0].ID != "z.go:F:ARITHMETIC_BASE#1" {
		t.Fatalf("WriteBaseline mutated caller survivors: %+v", b.Survivors)
	}
	if !slices.Equal(got.Policy.Packages, []string{"./a", "./z"}) || !slices.Equal(b.Policy.Packages, []string{"./z", "./a"}) {
		t.Fatalf("policy canonicalization got=%v original=%v", got.Policy.Packages, b.Policy.Packages)
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
	} else {
		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Fatalf("malformed error does not wrap json.SyntaxError: %v", err)
		}
	}
	duplicate := filepath.Join(dir, "duplicate.json")
	data := `{"schema_version":1,"go_module":"example.com/p","policy":{},"survivors":[{"id":"x","file":"p.go","line":1,"column":1,"type":"T"},{"id":"x","file":"p.go","line":2,"column":1,"type":"T"}]}`
	if err := os.WriteFile(duplicate, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBaseline(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate survivor") {
		t.Fatalf("duplicate error=%v", err)
	} else if errors.Unwrap(err) == nil {
		t.Fatalf("duplicate error does not wrap validation error: %v", err)
	}
}

type fakeBaselineSink struct {
	name     string
	writeErr error
	closeErr error
	closed   bool
}

func (f *fakeBaselineSink) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *fakeBaselineSink) Close() error {
	f.closed = true
	return f.closeErr
}

func (f *fakeBaselineSink) Name() string { return f.name }

var errBaselineIO = errors.New("baseline I/O sentinel")

func resetBaselineIOHooks(t *testing.T) {
	t.Helper()
	mkdirAll, newSink, remove, rename := baselineMkdirAll, newBaselineSink, baselineRemove, baselineRename
	t.Cleanup(func() {
		baselineMkdirAll, newBaselineSink, baselineRemove, baselineRename = mkdirAll, newSink, remove, rename
	})
}

func validBaselineForWrite() *Baseline {
	return &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      "example.com/p",
		Survivors:     []BaselineEntry{{ID: "id", File: "p.go", Line: 1, Column: 1, Type: "T"}},
	}
}

func TestWriteBaselineRejectsEmptyPath(t *testing.T) {
	if err := WriteBaseline("", validBaselineForWrite()); err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("error=%v", err)
	}
}

func TestWriteBaselineRejectsInvalidSnapshot(t *testing.T) {
	b := validBaselineForWrite()
	b.SchemaVersion = 0
	if err := WriteBaseline(filepath.Join(t.TempDir(), "baseline.json"), b); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error=%v", err)
	}
}

func TestWriteBaselinePropagatesMkdirError(t *testing.T) {
	resetBaselineIOHooks(t)
	var mode os.FileMode
	baselineMkdirAll = func(_ string, got os.FileMode) error {
		mode = got
		return errBaselineIO
	}
	err := WriteBaseline(filepath.Join(t.TempDir(), "nested", "baseline.json"), validBaselineForWrite())
	if !errors.Is(err, errBaselineIO) || mode != 0o755 {
		t.Fatalf("error=%v mode=%#o", err, mode)
	}
}

func TestWriteBaselinePropagatesCreateTempError(t *testing.T) {
	resetBaselineIOHooks(t)
	newBaselineSink = func(string, string) (baselineSink, error) { return nil, errBaselineIO }
	if err := WriteBaseline(filepath.Join(t.TempDir(), "baseline.json"), validBaselineForWrite()); !errors.Is(err, errBaselineIO) {
		t.Fatalf("error=%v", err)
	}
}

func TestWriteBaselinePropagatesEncodeErrorAndCleansUp(t *testing.T) {
	resetBaselineIOHooks(t)
	sink := &fakeBaselineSink{name: filepath.Join(t.TempDir(), "tmp"), writeErr: errBaselineIO}
	newBaselineSink = func(string, string) (baselineSink, error) { return sink, nil }
	var removes int
	baselineRemove = func(string) error { removes++; return nil }
	if err := WriteBaseline(filepath.Join(t.TempDir(), "baseline.json"), validBaselineForWrite()); !errors.Is(err, errBaselineIO) {
		t.Fatalf("error=%v", err)
	}
	if !sink.closed || removes != 1 {
		t.Fatalf("closed=%v removes=%d", sink.closed, removes)
	}
}

func TestWriteBaselinePropagatesCloseError(t *testing.T) {
	resetBaselineIOHooks(t)
	sink := &fakeBaselineSink{name: filepath.Join(t.TempDir(), "tmp"), closeErr: errBaselineIO}
	newBaselineSink = func(string, string) (baselineSink, error) { return sink, nil }
	if err := WriteBaseline(filepath.Join(t.TempDir(), "baseline.json"), validBaselineForWrite()); !errors.Is(err, errBaselineIO) {
		t.Fatalf("error=%v", err)
	}
}

func TestWriteBaselinePropagatesRenameErrorAndCleansUp(t *testing.T) {
	resetBaselineIOHooks(t)
	baselineRename = func(string, string) error { return errBaselineIO }
	var removes int
	baselineRemove = func(name string) error { removes++; return os.Remove(name) }
	if err := WriteBaseline(filepath.Join(t.TempDir(), "baseline.json"), validBaselineForWrite()); !errors.Is(err, errBaselineIO) {
		t.Fatalf("error=%v", err)
	}
	if removes != 1 {
		t.Fatalf("remove calls=%d, want 1", removes)
	}
}

func TestWriteBaselineSuccessDoesNotRunFailureCleanup(t *testing.T) {
	resetBaselineIOHooks(t)
	var removes int
	baselineRemove = func(name string) error { removes++; return os.Remove(name) }
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaseline(path, validBaselineForWrite()); err != nil {
		t.Fatal(err)
	}
	if removes != 0 {
		t.Fatalf("remove calls=%d, want 0", removes)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("baseline permissions=%#o, want owner-only", info.Mode().Perm())
	}
}

func TestBaselineEntryUsesModuleRelativeSlashPath(t *testing.T) {
	root := t.TempDir()
	m := baselineMutant(root, "id", 1, mutator.StatusLived)
	m.File = filepath.Join(root, "nested", "p.go")
	m.RelFile = "wrong.go"
	entry := baselineEntry(m, root)
	if entry.File != "nested/p.go" {
		t.Fatalf("File=%q, want module-relative slash path", entry.File)
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
