package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutants/internal/atomicfile"
	"github.com/szhekpisov/gomutants/internal/mutator"
)

// baselineMutant builds a mutant whose Anchor agrees with its stable ID, as
// discovery guarantees: the ID's middle segment is the enclosing declaration.
func baselineMutant(root, id string, line int, status mutator.MutantStatus) mutator.Mutant {
	anchor := ""
	if parts := strings.Split(id, ":"); len(parts) == 3 {
		anchor = parts[1]
	}
	return mutator.Mutant{
		StableID:    id,
		Anchor:      anchor,
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

// baselineMutantIn is baselineMutant for a file other than its default p.go,
// so a case can span the two files one comparison has to keep apart.
func baselineMutantIn(root, file, id string, line int, status mutator.MutantStatus) mutator.Mutant {
	m := baselineMutant(root, id, line, status)
	m.File, m.RelFile = filepath.Join(root, file), file
	return m
}

// mustEntries renders mutants as baseline entries, failing the test on the
// path error that only a mutant outside the module root can produce.
func mustEntries(t *testing.T, mutants []mutator.Mutant, root string) []BaselineEntry {
	t.Helper()
	entries, err := baselineEntries(mutants, root)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func mustEntry(t *testing.T, m mutator.Mutant, root string) BaselineEntry {
	t.Helper()
	return mustEntries(t, []mutator.Mutant{m}, root)[0]
}

func TestCompareBaselineClassifiesAndRetainsInconclusive(t *testing.T) {
	root := t.TempDir()
	before := []mutator.Mutant{
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#1", 10, mutator.StatusLived),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#2", 20, mutator.StatusLived),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#3", 30, mutator.StatusLived),
	}
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{Packages: []string{"./..."}}, before, root)
	if err != nil {
		t.Fatal(err)
	}
	current := []mutator.Mutant{
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#1", 10, mutator.StatusLived),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#2", 20, mutator.StatusKilled),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#3", 30, mutator.StatusNotCovered),
		baselineMutant(root, "p.go:F:ARITHMETIC_BASE#4", 40, mutator.StatusLived),
	}

	got, err := CompareBaseline(b, current, root, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	b := &Baseline{Survivors: mustEntries(t, baselineMutants, root)}
	current := []mutator.Mutant{
		mutant("z-new", 10, mutator.StatusLived),
		mutant("z-unresolved", 6, mutator.StatusNotCovered),
		mutant("z-known", 2, mutator.StatusLived),
		mutant("a-unresolved", 3, mutator.StatusTimedOut),
		mutant("a-known", 5, mutator.StatusLived),
		mutant("a-new", 11, mutator.StatusLived),
	}

	got, err := CompareBaseline(b, current, root, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{
		baselineMutant(root, "p.go:Old:ARITHMETIC_BASE#1", 10, mutator.StatusLived),
	}, root)
	if err != nil {
		t.Fatal(err)
	}

	rename := baselineMutant(root, "p.go:New:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	got, err := CompareBaseline(b, []mutator.Mutant{rename}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || len(got.New) != 0 || len(got.Fallbacks) != 1 || got.Fallbacks[0].Kind != "location" {
		t.Fatalf("rename comparison=%+v, want one location fallback known survivor", got)
	}

	// A shifted mutant whose ordinal changed — another mutation of the same
	// type was added earlier in the same declaration — keeps neither its ID
	// nor its position, but stays inside Old. That is what the structure
	// fallback is for.
	lineShift := baselineMutant(root, "p.go:Old:ARITHMETIC_BASE#2", 25, mutator.StatusLived)
	got, err = CompareBaseline(b, []mutator.Mutant{lineShift}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || len(got.New) != 0 || len(got.Fallbacks) != 1 || got.Fallbacks[0].Kind != "structure" {
		t.Fatalf("line-shift comparison=%+v, want one structure fallback known survivor", got)
	}

	// Losing both the position and the declaration is indistinguishable from
	// deleting one function and adding an unrelated one that happens to
	// contain the same kind of mutation, so it must not be matched: brand-new
	// untested debt would otherwise inherit the accepted status.
	moved := baselineMutant(root, "p.go:Elsewhere:ARITHMETIC_BASE#1", 25, mutator.StatusLived)
	got, err = CompareBaseline(b, []mutator.Mutant{moved}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.New) != 1 || len(got.Known) != 0 || len(got.Fallbacks) != 0 {
		t.Fatalf("relocated comparison=%+v, want a fail-safe new survivor", got)
	}

	ambiguous := &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      "example.com/p",
		Survivors: []BaselineEntry{
			mustEntry(t, baselineMutant(root, "p.go:A:ARITHMETIC_BASE#1", 5, mutator.StatusLived), root),
			mustEntry(t, baselineMutant(root, "p.go:B:ARITHMETIC_BASE#1", 15, mutator.StatusLived), root),
		},
	}
	got, err = CompareBaseline(ambiguous, []mutator.Mutant{lineShift}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	b := &Baseline{Survivors: mustEntries(t, []mutator.Mutant{oldZ, oldA, oldM}, root)}
	newZ, newA, newM := oldZ, oldA, oldM
	newZ.StableID, newZ.Line = "z-new", 30
	newA.StableID, newA.Line = "a-new", 40
	newM.StableID, newM.Line = "m-new", 50

	got, err := CompareBaseline(b, []mutator.Mutant{newZ, newA, newM}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	b := &Baseline{Survivors: mustEntries(t, []mutator.Mutant{oldA, oldB}, root)}

	got, err := CompareBaseline(b, []mutator.Mutant{newA, newB}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 2 || len(got.New) != 0 || len(got.Fallbacks) != 2 {
		t.Fatalf("comparison=%+v, want two unique location fallbacks", got)
	}
}

func baselineTestEntry(id, file, original string, line int) BaselineEntry {
	return BaselineEntry{ID: id, File: file, Line: line, Column: 3, Type: "T", Original: original, Replacement: "r"}
}

// TestFallbackMatchPairsOnlyUniqueKeys drives the one generic pass both
// fallbacks share. The location key separates entries by position and the
// structure key by file and descriptor, so each is fed inputs that are
// distinct, ambiguous, and already matched under its own key.
func TestFallbackMatchPairsOnlyUniqueKeys(t *testing.T) {
	cases := []struct {
		name                    string
		byStructure             bool
		skipFamilies            map[familyKey]struct{}
		baseline, current       []BaselineEntry
		baseMatch, currentMatch []int
		wantBase, wantCurrent   []int
	}{
		{
			name:        "location: distinct keys pair across input order",
			baseline:    []BaselineEntry{baselineTestEntry("old-a", "p.go", "+", 10), baselineTestEntry("old-b", "p.go", "+", 20)},
			current:     []BaselineEntry{baselineTestEntry("new-b", "p.go", "+", 20), baselineTestEntry("new-a", "p.go", "+", 10)},
			wantBase:    []int{1, 0},
			wantCurrent: []int{1, 0},
		},
		{
			name:        "location: two baseline entries on one key match nothing",
			baseline:    []BaselineEntry{baselineTestEntry("old-a", "p.go", "+", 10), baselineTestEntry("old-b", "p.go", "+", 10)},
			current:     []BaselineEntry{baselineTestEntry("new", "p.go", "+", 10)},
			wantBase:    []int{-1, -1},
			wantCurrent: []int{-1},
		},
		{
			name:        "location: two current entries on one key match nothing",
			baseline:    []BaselineEntry{baselineTestEntry("old", "p.go", "+", 10)},
			current:     []BaselineEntry{baselineTestEntry("new-a", "p.go", "+", 10), baselineTestEntry("new-b", "p.go", "+", 10)},
			wantBase:    []int{-1},
			wantCurrent: []int{-1, -1},
		},
		{
			name:         "location: already-matched entries are not reconsidered",
			baseline:     []BaselineEntry{baselineTestEntry("old-a", "p.go", "+", 10), baselineTestEntry("old-b", "p.go", "+", 20)},
			current:      []BaselineEntry{baselineTestEntry("new-a", "p.go", "+", 20), baselineTestEntry("new-b", "p.go", "+", 10)},
			baseMatch:    []int{0, -1},
			currentMatch: []int{0, -1},
			wantBase:     []int{0, -1},
			wantCurrent:  []int{0, -1},
		},
		{
			name:        "structure: distinct keys pair despite moved lines",
			byStructure: true,
			baseline:    []BaselineEntry{baselineTestEntry("old-a", "a.go", "+", 10), baselineTestEntry("old-b", "b.go", "*", 20)},
			current:     []BaselineEntry{baselineTestEntry("new-b", "b.go", "*", 40), baselineTestEntry("new-a", "a.go", "+", 30)},
			wantBase:    []int{1, 0},
			wantCurrent: []int{1, 0},
		},
		{
			name:        "structure: two baseline entries on one key match nothing",
			byStructure: true,
			baseline:    []BaselineEntry{baselineTestEntry("one", "p.go", "+", 10), baselineTestEntry("two", "p.go", "+", 20)},
			current:     []BaselineEntry{baselineTestEntry("new", "p.go", "+", 30)},
			wantBase:    []int{-1, -1},
			wantCurrent: []int{-1},
		},
		{
			name:         "structure: a skipped family is withheld even when unique",
			byStructure:  true,
			baseline:     []BaselineEntry{baselineTestEntry("one", "p.go", "+", 10)},
			current:      []BaselineEntry{baselineTestEntry("new", "p.go", "+", 30)},
			skipFamilies: map[familyKey]struct{}{{file: "p.go"}: {}},
			wantBase:     []int{-1},
			wantCurrent:  []int{-1},
		},
		{
			name:        "structure: two current entries on one key match nothing",
			byStructure: true,
			baseline:    []BaselineEntry{baselineTestEntry("one", "p.go", "+", 10)},
			current:     []BaselineEntry{baselineTestEntry("new-a", "p.go", "+", 30), baselineTestEntry("new-b", "p.go", "+", 40)},
			wantBase:    []int{-1},
			wantCurrent: []int{-1, -1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseMatch, currentMatch := tc.baseMatch, tc.currentMatch
			if baseMatch == nil {
				baseMatch, currentMatch = unmatchedIndexes(len(tc.baseline), len(tc.current))
			}
			if tc.byStructure {
				fallbackMatch(tc.baseline, tc.current, baseMatch, currentMatch, structureOf, tc.skipFamilies)
			} else {
				fallbackMatch(tc.baseline, tc.current, baseMatch, currentMatch, locationOf, nil)
			}
			if !slices.Equal(baseMatch, tc.wantBase) || !slices.Equal(currentMatch, tc.wantCurrent) {
				t.Fatalf("baseMatch=%v currentMatch=%v, want %v and %v", baseMatch, currentMatch, tc.wantBase, tc.wantCurrent)
			}
		})
	}
}

func TestCompareBaselineReassignedExactIDDoesNotStealOldMutation(t *testing.T) {
	root := t.TempDir()
	old := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{old}, root)
	if err != nil {
		t.Fatal(err)
	}

	inserted := baselineMutant(root, old.StableID, 5, mutator.StatusKilled)
	inserted.Original = "*"
	inserted.Replacement = "/"
	movedOld := old
	movedOld.StableID = "p.go:init~2:ARITHMETIC_BASE#1"
	movedOld.Anchor = "init~2"
	movedOld.Line = 20

	got, err := CompareBaseline(b, []mutator.Mutant{inserted, movedOld}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || got.Known[0].ID != movedOld.StableID || len(got.New) != 0 {
		t.Fatalf("comparison=%+v, want the moved old mutation matched and no new survivor", got)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0].OldID != old.StableID || got.Fallbacks[0].NewID != movedOld.StableID {
		t.Fatalf("Fallbacks=%+v, want old init ID migrated to ~2", got.Fallbacks)
	}
}

// TestCompareBaselineReassignedExactIDWithSameDescriptorIsNotTrusted covers
// the case the descriptor check alone cannot see: the inserted declaration
// contains the *same* kind of mutation, so the old ID's new owner is
// structurally identical to the accepted debt. Matching it would report a
// brand-new untested survivor as KNOWN while the real one was killed.
func TestCompareBaselineReassignedExactIDWithSameDescriptorIsNotTrusted(t *testing.T) {
	root := t.TempDir()
	old := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 30, mutator.StatusLived)
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{old}, root)
	if err != nil {
		t.Fatal(err)
	}

	// A second init() above the first takes the bare anchor; the original
	// becomes init~2 and shifts down. The newcomer survives, the original is
	// now killed.
	inserted := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 5, mutator.StatusLived)
	movedOld := baselineMutant(root, "p.go:init~2:ARITHMETIC_BASE#1", 40, mutator.StatusKilled)

	got, err := CompareBaseline(b, []mutator.Mutant{inserted, movedOld}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 0 {
		t.Fatalf("Known=%+v, want none: neither candidate is provably the accepted debt", got.Known)
	}
	if len(got.New) != 1 || got.New[0].ID != inserted.StableID {
		t.Fatalf("New=%+v, want the inserted survivor reported as new debt", got.New)
	}
}

// TestCompareBaselineKeepsUnmovedMembersOfARepeatedFamily pins the other half
// of that guard: distrusting the ID inside a repeated family must not cost
// accepted debt, because the location fallback still matches every member that
// has not moved — by position, which no insertion can forge.
func TestCompareBaselineKeepsUnmovedMembersOfARepeatedFamily(t *testing.T) {
	root := t.TempDir()
	first := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	second := baselineMutant(root, "p.go:init~2:ARITHMETIC_BASE#1", 20, mutator.StatusLived)
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{first, second}, root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := CompareBaseline(b, []mutator.Mutant{first, second}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 2 || len(got.New) != 0 || len(got.Fallbacks) != 0 {
		t.Fatalf("comparison=%+v, want both matched by exact ID", got)
	}
}

// TestCompareBaselineKeepsARepeatedFamilyThroughALineShift pins the case a
// blanket distrust of repeated families gets wrong: every stable ID is intact
// and no declaration was added or removed, so an edit that only moves lines
// must cost nothing. Distrusting the IDs here strands the whole family — the
// location fallback misses because both moved, and the structure fallback
// refuses the two-on-two bucket the identical descriptors make — turning
// accepted debt into new survivors that fail CI, on a run whose own gate
// ordering then refuses to migrate them.
func TestCompareBaselineKeepsARepeatedFamilyThroughALineShift(t *testing.T) {
	root := t.TempDir()
	first := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	second := baselineMutant(root, "p.go:init~2:ARITHMETIC_BASE#1", 20, mutator.StatusLived)
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{first, second}, root)
	if err != nil {
		t.Fatal(err)
	}

	// An added import shifts both declarations down; nothing else changes.
	movedFirst, movedSecond := first, second
	movedFirst.Line, movedSecond.Line = 12, 22

	got, err := CompareBaseline(b, []mutator.Mutant{movedFirst, movedSecond}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 2 || len(got.New) != 0 || len(got.Resolved) != 0 {
		t.Fatalf("comparison=%+v, want both survivors still known after a pure line shift", got)
	}
}

// TestCompareBaselineDoesNotInheritADeletedNamesakesDebt covers the deletion
// half of anchor reassignment. The accepted debt belonged to a declaration
// that no longer exists, so the surviving namesake now renders under the bare
// anchor the debt was written with — matching them would report a genuine
// KILLED-to-LIVED regression as accepted debt and pass the run.
func TestCompareBaselineDoesNotInheritADeletedNamesakesDebt(t *testing.T) {
	root := t.TempDir()
	// Two init()s. Only the first one's mutant survives, so only it becomes
	// accepted debt — but the baseline still records that the file held two.
	first := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	second := baselineMutant(root, "p.go:init~2:ARITHMETIC_BASE#1", 20, mutator.StatusKilled)
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{first, second}, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Survivors) != 1 || b.Survivors[0].FamilySize != 2 {
		t.Fatalf("survivors=%+v, want the one survivor recording a family of two", b.Survivors)
	}

	// The first init() is deleted, so the second takes the bare anchor and
	// shifts up — and the same edit costs it the test that killed its mutant.
	regressed := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 14, mutator.StatusLived)

	got, err := CompareBaseline(b, []mutator.Mutant{regressed}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.New) != 1 || got.New[0].ID != regressed.StableID {
		t.Fatalf("New=%+v, want the regressed namesake reported as new debt", got.New)
	}
	if len(got.Known) != 0 || len(got.Resolved) != 1 {
		t.Fatalf("comparison=%+v, want the deleted declaration's debt resolved, not inherited", got)
	}
}

// TestCompareBaselineMigratesAnIDChurnInsideAStableRepeatedFamily is the other
// side of that guard: a family whose declaration count is unchanged still
// migrates debt by descriptor, because every declaration the baseline
// described is still there to own it.
func TestCompareBaselineMigratesAnIDChurnInsideAStableRepeatedFamily(t *testing.T) {
	root := t.TempDir()
	first := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	second := baselineMutant(root, "p.go:init~2:ARITHMETIC_BASE#1", 20, mutator.StatusLived)
	second.Original, second.Replacement = "*", "/"
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{first, second}, root)
	if err != nil {
		t.Fatal(err)
	}

	// A mutation added ahead of it in the same init() renumbers the first
	// mutant and shifts it down. Both declarations are still there.
	renumbered := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#2", 12, mutator.StatusLived)

	got, err := CompareBaseline(b, []mutator.Mutant{renumbered, second}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 2 || len(got.New) != 0 {
		t.Fatalf("comparison=%+v, want both survivors known", got)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0].NewID != renumbered.StableID || got.Fallbacks[0].Kind != "structure" {
		t.Fatalf("Fallbacks=%+v, want the renumbered mutant migrated by structure", got.Fallbacks)
	}
}

// TestCompareBaselineTrustsALegacyEntryOutsideARepeatedFamily pins the
// compatibility path: an entry from a baseline written before family sizes
// were recorded has nothing to compare, so it falls back to trusting the ID
// wherever the run's family is not repeated. Here nothing else can do the job
// — the mutant moved, and its new sibling makes the descriptor ambiguous.
func TestCompareBaselineTrustsALegacyEntryOutsideARepeatedFamily(t *testing.T) {
	root := t.TempDir()
	entry := mustEntry(t, baselineMutant(root, "p.go:F:ARITHMETIC_BASE#1", 10, mutator.StatusLived), root)
	entry.FamilySize = 0
	b := &Baseline{Survivors: []BaselineEntry{entry}}

	moved := baselineMutant(root, "p.go:F:ARITHMETIC_BASE#1", 20, mutator.StatusLived)
	sibling := baselineMutant(root, "p.go:F:ARITHMETIC_BASE#2", 30, mutator.StatusLived)

	got, err := CompareBaseline(b, []mutator.Mutant{moved, sibling}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || got.Known[0].ID != moved.StableID {
		t.Fatalf("Known=%+v, want the legacy entry matched by its exact ID", got.Known)
	}
	if len(got.New) != 1 || got.New[0].ID != sibling.StableID {
		t.Fatalf("New=%+v, want only the new sibling reported as new debt", got.New)
	}
}

// TestCompareBaselineDistrustsALegacyEntryInsideARepeatedFamily is the other
// half of the compatibility path: with no recorded count to compare, a
// repeated family in the run is itself the signal that the ID may have been
// reassigned, so the entry must fall through to the fallbacks — which here
// refuse the ambiguous bucket, exactly as they do for a recorded family that
// grew.
func TestCompareBaselineDistrustsALegacyEntryInsideARepeatedFamily(t *testing.T) {
	root := t.TempDir()
	entry := mustEntry(t, baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 30, mutator.StatusLived), root)
	entry.FamilySize = 0
	b := &Baseline{Survivors: []BaselineEntry{entry}}

	// A second init() above the first takes the bare anchor and survives; the
	// original becomes init~2, shifts down, and is now killed.
	inserted := baselineMutant(root, "p.go:init:ARITHMETIC_BASE#1", 5, mutator.StatusLived)
	movedOld := baselineMutant(root, "p.go:init~2:ARITHMETIC_BASE#1", 40, mutator.StatusKilled)

	got, err := CompareBaseline(b, []mutator.Mutant{inserted, movedOld}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 0 || len(got.New) != 1 || got.New[0].ID != inserted.StableID {
		t.Fatalf("comparison=%+v, want the inserted survivor reported as new debt", got)
	}
}

// TestCompareBaselineSkipsOnlyTheShrunkFamilyInTheStructurePass pins that
// withholding a family from the structure pass costs nothing outside it: the
// entries that follow it still match.
func TestCompareBaselineSkipsOnlyTheShrunkFamilyInTheStructurePass(t *testing.T) {
	root := t.TempDir()
	// a.go repeats init(); b.go holds one ordinary survivor. Sorted survivor
	// order puts a.go's entry first, ahead of the one that must still match.
	first := baselineMutantIn(root, "a.go", "a.go:init:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	second := baselineMutantIn(root, "a.go", "a.go:init~2:ARITHMETIC_BASE#1", 20, mutator.StatusKilled)
	other := baselineMutantIn(root, "b.go", "b.go:F:ARITHMETIC_BASE#1", 10, mutator.StatusLived)
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{first, second, other}, root)
	if err != nil {
		t.Fatal(err)
	}

	// a.go's first init() is deleted and its namesake regresses; b.go's
	// mutant is renumbered and moved, so only the structure pass can match it.
	regressed := baselineMutantIn(root, "a.go", "a.go:init:ARITHMETIC_BASE#1", 14, mutator.StatusLived)
	movedOther := baselineMutantIn(root, "b.go", "b.go:F:ARITHMETIC_BASE#2", 30, mutator.StatusLived)

	got, err := CompareBaseline(b, []mutator.Mutant{regressed, movedOther}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || got.Known[0].ID != movedOther.StableID {
		t.Fatalf("Known=%+v, want b.go's survivor still matched by structure", got.Known)
	}
	if len(got.New) != 1 || got.New[0].ID != regressed.StableID {
		t.Fatalf("New=%+v, want only a.go's regressed namesake reported as new debt", got.New)
	}
}

func TestCompareBaselineDoesNotMatchOneCurrentMutantTwice(t *testing.T) {
	root := t.TempDir()
	current := baselineMutant(root, "duplicate-id", 10, mutator.StatusLived)
	entry := mustEntry(t, current, root)
	b := &Baseline{Survivors: []BaselineEntry{entry, entry}}
	got, err := CompareBaseline(b, []mutator.Mutant{current}, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || len(got.Resolved) != 1 {
		t.Fatalf("comparison=%+v, want one known and one unmatched baseline entry", got)
	}
}

func TestBaselinePolicyCanonicalAndDifferences(t *testing.T) {
	p := BaselinePolicy{
		Packages:     []string{"./b", "./a", "./a"},
		Only:         []string{"B", "A", "A"},
		Disable:      []string{"D", "C", "C"},
		ExcludeFiles: []string{"z/**", "a/**", "a/**"},
		ExcludeCalls: []string{"log.*", "fmt.Print*"},
	}
	canonical := p.Canonical()
	if !slices.Equal(canonical.Packages, []string{"./a", "./b"}) {
		t.Fatalf("Packages=%v", canonical.Packages)
	}
	if !slices.Equal(canonical.Only, []string{"A", "B"}) ||
		!slices.Equal(canonical.Disable, []string{"C", "D"}) ||
		!slices.Equal(canonical.ExcludeFiles, []string{"a/**", "z/**"}) ||
		!slices.Equal(canonical.ExcludeCalls, []string{"fmt.Print*", "log.*"}) {
		t.Fatalf("canonical policy=%+v", canonical)
	}
	if diff := p.Differences(BaselinePolicy{
		Packages:     []string{"./b", "./a"},
		Only:         []string{"A", "B"},
		Disable:      []string{"D", "C"},
		ExcludeFiles: []string{"z/**", "a/**"},
		ExcludeCalls: []string{"fmt.Print*", "log.*"},
	}); len(diff) != 0 {
		t.Fatalf("equivalent policies differ: %v", diff)
	}
	p.Packages[0], p.Only[0], p.Disable[0], p.ExcludeFiles[0], p.ExcludeCalls[0] = "changed", "changed", "changed", "changed", "changed"
	if !slices.Equal(canonical.Packages, []string{"./a", "./b"}) ||
		!slices.Equal(canonical.Only, []string{"A", "B"}) ||
		!slices.Equal(canonical.Disable, []string{"C", "D"}) ||
		!slices.Equal(canonical.ExcludeFiles, []string{"a/**", "z/**"}) ||
		!slices.Equal(canonical.ExcludeCalls, []string{"fmt.Print*", "log.*"}) {
		t.Fatalf("Canonical aliases its input: %+v", canonical)
	}

	base := BaselinePolicy{
		Packages: []string{"./..."}, Only: []string{"A"}, Disable: []string{"D"}, BuildTags: "tag", TestFlags: "-short",
		Integration: true, CoverPkg: "./...", DetectEquivalent: true,
		ExcludeFiles: []string{"vendor/**"}, ExcludeCalls: []string{"fmt.Print*"},
	}
	cases := []struct {
		want   string
		change func(*BaselinePolicy)
	}{
		{"packages", func(p *BaselinePolicy) { p.Packages = []string{"./other"} }},
		{"only", func(p *BaselinePolicy) { p.Only = []string{"B"} }},
		{"disable", func(p *BaselinePolicy) { p.Disable = []string{"E"} }},
		{"tags", func(p *BaselinePolicy) { p.BuildTags = "other" }},
		{"test-flags", func(p *BaselinePolicy) { p.TestFlags = "-run TestOne" }},
		{"integration", func(p *BaselinePolicy) { p.Integration = false }},
		{"coverpkg", func(p *BaselinePolicy) { p.CoverPkg = "./internal/..." }},
		{"detect-equivalent", func(p *BaselinePolicy) { p.DetectEquivalent = false }},
		{"exclude-files", func(p *BaselinePolicy) { p.ExcludeFiles = []string{"generated/**"} }},
		{"exclude-calls", func(p *BaselinePolicy) { p.ExcludeCalls = []string{"log.*"} }},
		{"exclude-calls-defaults", func(p *BaselinePolicy) { p.ExcludeCallsNoDefaults = true }},
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
		{"negative family size", "invalid family_size", func(b *Baseline) { b.Survivors[0].FamilySize = -1 }},
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
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{Packages: []string{"z", "a"}}, []mutator.Mutant{z, dead, a}, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{b.Survivors[0].ID, b.Survivors[1].ID}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("new baseline survivors=%v", got)
	}
	if !slices.Equal(b.Policy.Packages, []string{"a", "z"}) {
		t.Fatalf("new baseline policy=%v", b.Policy.Packages)
	}

	comparison := BaselineComparison{Retained: []BaselineEntry{{ID: "z"}, {ID: "a"}}}
	updated := UpdatedBaseline("example.com/p", "v2", BaselinePolicy{Only: []string{"Z", "A"}}, comparison)
	comparison.Retained[0].ID = "changed"
	if updated.SchemaVersion != BaselineSchemaVersion || updated.GoModule != "example.com/p" || updated.GeneratedBy != "v2" {
		t.Fatalf("updated baseline metadata=%+v", updated)
	}
	if got := []string{updated.Survivors[0].ID, updated.Survivors[1].ID}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("updated baseline survivors=%v", got)
	}
	if !slices.Equal(updated.Policy.Only, []string{"A", "Z"}) {
		t.Fatalf("updated baseline policy=%v", updated.Policy.Only)
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

// WriteBaseline's atomic-write flow lives in internal/atomicfile, which owns
// the per-error-path tests. What this pins is the contract WriteBaseline
// asks of it: an indented, sorted, canonical snapshot at the shared mode, in
// a file the next ReadBaseline accepts.
func TestWriteBaselineWritesSortedIndentedSnapshotAtSharedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "baseline.json")
	b := validBaselineForWrite()
	b.Policy = BaselinePolicy{Packages: []string{"./z", "./a"}}
	b.Survivors = []BaselineEntry{
		{ID: "z", File: "p.go", Line: 2, Column: 1, Type: "T"},
		{ID: "a", File: "p.go", Line: 1, Column: 1, Type: "T"},
	}
	if err := WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	// The caller's slice must not be reordered under it.
	if b.Survivors[0].ID != "z" {
		t.Fatalf("WriteBaseline sorted the caller's slice in place: %v", b.Survivors)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"schema_version\"") {
		t.Fatalf("baseline is not indented: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != atomicfile.Mode {
		t.Fatalf("permissions=%#o, want %#o: a committed baseline is not a secret", info.Mode().Perm(), atomicfile.Mode)
	}
	written, err := ReadBaseline(path)
	if err != nil {
		t.Fatalf("written baseline does not read back: %v", err)
	}
	if got := []string{written.Survivors[0].ID, written.Survivors[1].ID}; !slices.Equal(got, []string{"a", "z"}) {
		t.Fatalf("survivors=%v, want sorted", got)
	}
	if !slices.Equal(written.Policy.Packages, []string{"./a", "./z"}) {
		t.Fatalf("policy=%v, want canonical", written.Policy.Packages)
	}
}

func TestBaselineEntryUsesModuleRelativeSlashPath(t *testing.T) {
	root := t.TempDir()
	m := baselineMutant(root, "id", 1, mutator.StatusLived)
	m.File = filepath.Join(root, "nested", "p.go")
	m.RelFile = "wrong.go"
	entry := mustEntry(t, m, root)
	if entry.File != "nested/p.go" {
		t.Fatalf("File=%q, want module-relative slash path", entry.File)
	}
}

// TestBaselineEntryRejectsFileOutsideModuleRoot pins that an unrelatable path
// is an error rather than a silent fall back to RelFile, whose value depends
// on the run's package arguments. A baseline keyed in that path space matches
// nothing on the next run, so every known survivor would resurface as new.
func TestBaselineEntryRejectsFileOutsideModuleRoot(t *testing.T) {
	m := baselineMutant(t.TempDir(), "id", 1, mutator.StatusLived)
	m.File = "relative/p.go"
	m.RelFile = "p.go"
	err := func() error {
		_, err := baselineEntry(m, "/abs/module")
		return err
	}()
	if err == nil {
		t.Fatal("a path that cannot be made module-relative must be an error")
	}
	// %w, not %v: callers inspecting the cause must reach filepath.Rel's.
	if errors.Unwrap(err) == nil {
		t.Fatalf("error does not wrap the underlying failure: %v", err)
	}
	if _, err := baselineEntries([]mutator.Mutant{m}, "/abs/module"); err == nil {
		t.Fatal("baselineEntries must propagate the entry error")
	}
	if _, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{m}, "/abs/module"); err == nil {
		t.Fatal("NewBaseline must propagate the entry error")
	}
	if _, err := CompareBaseline(&Baseline{}, []mutator.Mutant{m}, "/abs/module", nil); err == nil {
		t.Fatal("CompareBaseline must propagate the entry error")
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

func TestCompareBaselineRetainsSurvivorsOutsideTheRunScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other", "q.go"), []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inScope := baselineMutant(root, "kept:in-scope", 10, mutator.StatusLived)
	outOfScope := baselineMutant(root, "kept:out-of-scope", 20, mutator.StatusLived)
	outOfScope.File = filepath.Join(root, "other", "q.go")
	b, err := NewBaseline("example.com/p", "v1", BaselinePolicy{}, []mutator.Mutant{inScope, outOfScope}, root)
	if err != nil {
		t.Fatal(err)
	}

	// The narrowed rerun resolves only the root package, and its one mutant
	// is now killed. The other package was never examined.
	killed := baselineMutant(root, "kept:in-scope", 10, mutator.StatusKilled)
	scope := NewBaselineScope(root, []string{root})
	got, err := CompareBaseline(b, []mutator.Mutant{killed}, root, scope)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Resolved) != 1 || got.Resolved[0].ID != "kept:in-scope" {
		t.Fatalf("Resolved=%v, want only the examined package's fixed survivor", got.Resolved)
	}
	if len(got.OutOfScope) != 1 || got.OutOfScope[0].ID != "kept:out-of-scope" {
		t.Fatalf("OutOfScope=%v, want the unexamined package's survivor", got.OutOfScope)
	}
	if len(got.Retained) != 1 || got.Retained[0].ID != "kept:out-of-scope" {
		t.Fatalf("Retained=%v: a narrowed run must not drop unexamined debt", got.Retained)
	}
	if len(got.New) != 0 {
		t.Fatalf("New=%v, want none", got.New)
	}

	// Deleting the unexamined package's source proves the debt is gone, so
	// the same narrowed run must now shrink the baseline to nothing.
	if err := os.Remove(filepath.Join(root, "other", "q.go")); err != nil {
		t.Fatal(err)
	}
	deleted, err := CompareBaseline(b, []mutator.Mutant{killed}, root, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Resolved) != 2 || len(deleted.Retained) != 0 {
		t.Fatalf("Resolved=%v Retained=%v: a deleted file must still resolve", deleted.Resolved, deleted.Retained)
	}
}

func TestNewBaselineScopeIgnoresDirectoriesOutsideTheModule(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "p.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scope := NewBaselineScope(root, []string{root, filepath.Join(root, "a", "b"), filepath.Dir(root)})
	if len(scope.dirs) != 2 {
		t.Fatalf("scope dirs=%v, want only the two directories inside the module", scope.dirs)
	}
	if !scope.resolvable(BaselineEntry{File: "main.go"}) || !scope.resolvable(BaselineEntry{File: "a/b/p.go"}) {
		t.Fatal("entries in a resolved package must stay resolvable")
	}
	if scope.resolvable(BaselineEntry{File: "a/p.go"}) {
		t.Fatal("an existing file in an unexamined directory must not be resolvable")
	}
	if !scope.resolvable(BaselineEntry{File: "a/gone.go"}) {
		t.Fatal("a file that no longer exists must be resolvable even unexamined")
	}
}

// TestMatchExactIDsWalksTheWholeBaseline drives every branch of the exact-ID
// pass in one comparison, with the entries whose ID must NOT be trusted placed
// before the entries whose ID must be. F's two mutants are the load-bearing
// assertion: they share a structure key and both moved, so neither fallback
// can recover them and only the exact ID can. Anything that stops the walk
// early, or that widens the repeated-family guard to cover them, loses both.
func TestMatchExactIDsWalksTheWholeBaseline(t *testing.T) {
	root := t.TempDir()
	entry := func(id, original, replacement string, line int) BaselineEntry {
		m := baselineMutant(root, id, line, mutator.StatusLived)
		m.Original, m.Replacement = original, replacement
		return mustEntry(t, m, root)
	}
	b := &Baseline{Survivors: []BaselineEntry{
		// Deleted outright: its ID is absent from the run.
		entry("p.go:Gone:ARITHMETIC_BASE#1", "+", "-", 5),
		// Same ID, different mutation: the ID was reused by an edit.
		entry("p.go:Changed:ARITHMETIC_BASE#1", "+", "-", 15),
		// Same ID, but a second init() shifted the family's suffixes.
		entry("p.go:init:ARITHMETIC_BASE#1", "+", "-", 25),
		// Same IDs, moved, structurally indistinguishable from each other.
		entry("p.go:F:ARITHMETIC_BASE#1", "+", "-", 30),
		entry("p.go:F:ARITHMETIC_BASE#2", "+", "-", 31),
	}}

	mutant := func(id, original, replacement string, line int) mutator.Mutant {
		m := baselineMutant(root, id, line, mutator.StatusLived)
		m.Original, m.Replacement = original, replacement
		return m
	}
	current := []mutator.Mutant{
		mutant("p.go:Changed:ARITHMETIC_BASE#1", "*", "/", 15),
		mutant("p.go:init:ARITHMETIC_BASE#1", "+", "-", 1),
		mutant("p.go:init~2:ARITHMETIC_BASE#1", "+", "-", 40),
		mutant("p.go:F:ARITHMETIC_BASE#1", "+", "-", 60),
		mutant("p.go:F:ARITHMETIC_BASE#2", "+", "-", 61),
	}

	got, err := CompareBaseline(b, current, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	known := make([]string, len(got.Known))
	for i, e := range got.Known {
		known[i] = e.ID
	}
	if !slices.Equal(known, []string{"p.go:F:ARITHMETIC_BASE#1", "p.go:F:ARITHMETIC_BASE#2"}) {
		t.Fatalf("Known=%v, want exactly F's two mutants matched by exact ID", known)
	}
	if len(got.Resolved) != 3 || len(got.New) != 3 {
		t.Fatalf("Resolved=%v New=%v, want the three untrusted entries resolved and their three candidates new", got.Resolved, got.New)
	}
}

// TestCompareBaselineSortsAndClassifiesPastOutOfScopeEntries pins that an
// out-of-scope entry neither stops the classification walk nor lands in the
// output unsorted.
func TestCompareBaselineSortsAndClassifiesPastOutOfScopeEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := func(id, name string) BaselineEntry {
		if err := os.WriteFile(filepath.Join(root, "other", name), []byte("package other\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m := baselineMutant(root, id, 1, mutator.StatusLived)
		m.File = filepath.Join(root, "other", name)
		return mustEntry(t, m, root)
	}
	inScope := baselineMutant(root, "z-in-scope", 10, mutator.StatusLived)
	b := &Baseline{Survivors: []BaselineEntry{
		outside("b-outside", "b.go"),
		outside("a-outside", "a.go"),
		mustEntry(t, inScope, root),
	}}

	got, err := CompareBaseline(b, []mutator.Mutant{inScope}, root, NewBaselineScope(root, []string{root}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Known) != 1 || got.Known[0].ID != "z-in-scope" {
		t.Fatalf("Known=%v: the walk stopped at the first out-of-scope entry", got.Known)
	}
	ids := []string{got.OutOfScope[0].ID, got.OutOfScope[1].ID}
	if !slices.Equal(ids, []string{"a-outside", "b-outside"}) {
		t.Fatalf("OutOfScope=%v, want sorted by ID", ids)
	}
}

// TestNewBaselineScopeSkipsPastDirectoriesOutsideTheModule pins that a
// directory the run resolved outside the module root is skipped rather than
// stored, and that skipping it does not abandon the directories after it.
func TestNewBaselineScopeSkipsPastDirectoriesOutsideTheModule(t *testing.T) {
	root := t.TempDir()
	sibling := filepath.Join(filepath.Dir(root), "sibling")
	inside := filepath.Join(root, "pkg")
	scope := NewBaselineScope(root, []string{sibling, filepath.Dir(root), inside})
	if len(scope.dirs) != 1 {
		t.Fatalf("scope dirs=%v, want only the directory inside the module", scope.dirs)
	}
	if !scope.resolvable(BaselineEntry{File: "pkg/p.go"}) {
		t.Fatal("the directory after the skipped ones was dropped")
	}
}

// TestBaselineScopeResolvesExaminedPackagesWithoutTouchingDisk pins that
// membership in the examined set is the whole answer for an in-scope entry:
// the run had its chance to produce that mutant, so the file being present is
// not evidence the debt survives.
func TestBaselineScopeResolvesExaminedPackagesWithoutTouchingDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NewBaselineScope(root, []string{root}).resolvable(BaselineEntry{File: "p.go"}) {
		t.Fatal("an entry in an examined package must be resolvable even though its file still exists")
	}
}
