package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// BaselineSchemaVersion is the on-disk format understood by this version of
// gomutants. Baselines are committed project policy, so incompatible formats
// are rejected rather than silently treated as empty.
const BaselineSchemaVersion = 1

const (
	BaselineStatusKnown = "KNOWN"
	BaselineStatusNew   = "NEW"
)

// Baseline records the surviving-mutant debt a project has explicitly
// accepted. Only survivors are persisted; every other outcome is derived from
// the next run.
type Baseline struct {
	SchemaVersion int             `json:"schema_version"`
	GoModule      string          `json:"go_module"`
	GeneratedBy   string          `json:"generated_by"`
	Policy        BaselinePolicy  `json:"policy"`
	Survivors     []BaselineEntry `json:"survivors"`
}

// BaselinePolicy fingerprints every user-facing setting that changes the
// mutant universe or the tests used to classify it. Operational knobs such as
// workers, cache path, and timeout margins deliberately stay out.
type BaselinePolicy struct {
	Packages         []string `json:"packages"`
	Mutators         []string `json:"mutators"`
	BuildTags        string   `json:"tags,omitempty"`
	TestFlags        string   `json:"test_flags,omitempty"`
	Integration      bool     `json:"integration,omitempty"`
	CoverPkg         string   `json:"coverpkg,omitempty"`
	DetectEquivalent bool     `json:"detect_equivalent,omitempty"`
	ExcludeFiles     []string `json:"exclude_files,omitempty"`
	ExcludeCalls     []string `json:"exclude_calls,omitempty"`
}

// BaselineEntry carries the stable ID plus enough source identity to recover
// conservatively from the documented stable-ID churn cases. File is always
// relative to the module root and slash-separated.
type BaselineEntry struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Type        string `json:"type"`
	Original    string `json:"original,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

// BaselineFallback describes one non-ID match. Callers surface these on
// stderr: fallback is useful for a rename, but must never be silent.
type BaselineFallback struct {
	OldID string
	NewID string
	Kind  string
}

// BaselineComparison is the result of comparing one completed run with a
// baseline. Retained is the shrink-only next baseline: known LIVED mutants and
// prior entries whose current outcome is inconclusive.
type BaselineComparison struct {
	Known      []BaselineEntry
	New        []BaselineEntry
	Resolved   []BaselineEntry
	Unresolved []BaselineEntry
	Fallbacks  []BaselineFallback
	Retained   []BaselineEntry

	knownIDs map[string]struct{}
	newIDs   map[string]struct{}
}

// Canonical returns a deterministic policy representation for comparison and
// serialization. All slices are copied, sorted, and deduplicated.
func (p BaselinePolicy) Canonical() BaselinePolicy {
	p.Packages = canonicalStrings(p.Packages)
	p.Mutators = canonicalStrings(p.Mutators)
	p.ExcludeFiles = canonicalStrings(p.ExcludeFiles)
	p.ExcludeCalls = canonicalStrings(p.ExcludeCalls)
	return p
}

// Differences names policy dimensions that differ. The generated tool
// version is intentionally not policy: a baseline is meant to survive tool
// upgrades, with descriptor fallback covering conservative ID migrations.
func (p BaselinePolicy) Differences(other BaselinePolicy) []string {
	p = p.Canonical()
	other = other.Canonical()
	var fields []string
	if !slices.Equal(p.Packages, other.Packages) {
		fields = append(fields, "packages")
	}
	if !slices.Equal(p.Mutators, other.Mutators) {
		fields = append(fields, "mutators")
	}
	if p.BuildTags != other.BuildTags {
		fields = append(fields, "tags")
	}
	if p.TestFlags != other.TestFlags {
		fields = append(fields, "test-flags")
	}
	if p.Integration != other.Integration {
		fields = append(fields, "integration")
	}
	if p.CoverPkg != other.CoverPkg {
		fields = append(fields, "coverpkg")
	}
	if p.DetectEquivalent != other.DetectEquivalent {
		fields = append(fields, "detect-equivalent")
	}
	if !slices.Equal(p.ExcludeFiles, other.ExcludeFiles) {
		fields = append(fields, "exclude-files")
	}
	if !slices.Equal(p.ExcludeCalls, other.ExcludeCalls) {
		fields = append(fields, "exclude-calls")
	}
	return fields
}

// ReadBaseline loads and validates a committed baseline. os.ErrNotExist stays
// discoverable through errors.Is so --baseline-update can distinguish initial
// bootstrap from malformed input.
func ReadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading baseline %s: %w", path, err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing baseline %s: %w", path, err)
	}
	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("invalid baseline %s: %w", path, err)
	}
	return &b, nil
}

// Validate checks invariants required for unambiguous matching.
func (b *Baseline) Validate() error {
	if b.SchemaVersion != BaselineSchemaVersion {
		return fmt.Errorf("schema_version=%d is unsupported (want %d)", b.SchemaVersion, BaselineSchemaVersion)
	}
	if strings.TrimSpace(b.GoModule) == "" {
		return errors.New("go_module is empty")
	}
	seen := make(map[string]struct{}, len(b.Survivors))
	for i, entry := range b.Survivors {
		if entry.ID == "" {
			return fmt.Errorf("survivors[%d].id is empty", i)
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return fmt.Errorf("duplicate survivor id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if entry.File == "" {
			return fmt.Errorf("survivors[%d].file is empty", i)
		}
		if entry.Type == "" {
			return fmt.Errorf("survivors[%d].type is empty", i)
		}
		if entry.Line <= 0 || entry.Column <= 0 {
			return fmt.Errorf("survivors[%d] has invalid position %d:%d", i, entry.Line, entry.Column)
		}
	}
	return nil
}

// NewBaseline creates an initial baseline from the LIVED outcomes in mutants.
func NewBaseline(goModule, generatedBy string, policy BaselinePolicy, mutants []mutator.Mutant, moduleRoot string) *Baseline {
	b := &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      goModule,
		GeneratedBy:   generatedBy,
		Policy:        policy.Canonical(),
	}
	for _, m := range mutants {
		if m.Status == mutator.StatusLived {
			b.Survivors = append(b.Survivors, baselineEntry(m, moduleRoot))
		}
	}
	sortBaselineEntries(b.Survivors)
	return b
}

// UpdatedBaseline builds the next shrink-only snapshot after CompareBaseline.
// The caller must refuse the update when comparison.New is non-empty.
func UpdatedBaseline(goModule, generatedBy string, policy BaselinePolicy, comparison BaselineComparison) *Baseline {
	survivors := slices.Clone(comparison.Retained)
	sortBaselineEntries(survivors)
	return &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      goModule,
		GeneratedBy:   generatedBy,
		Policy:        policy.Canonical(),
		Survivors:     survivors,
	}
}

// CompareBaseline matches the completed mutant set against known survivor
// debt. Exact IDs win; two unique descriptor fallbacks cover anchor churn and
// line shifts without ever guessing among ambiguous candidates.
func CompareBaseline(b *Baseline, mutants []mutator.Mutant, moduleRoot string) BaselineComparison {
	current := make([]BaselineEntry, len(mutants))
	for i, m := range mutants {
		current[i] = baselineEntry(m, moduleRoot)
	}

	baseMatch := make([]int, len(b.Survivors))
	currentMatch := make([]int, len(current))
	for i := range baseMatch {
		baseMatch[i] = -1
	}
	for i := range currentMatch {
		currentMatch[i] = -1
	}

	byID := make(map[string]int, len(current))
	for i, entry := range current {
		byID[entry.ID] = i
	}
	for i, entry := range b.Survivors {
		// A repeated declaration such as init() can hand an old stable ID to
		// newly inserted code. Trust the ID only while its structural mutation
		// descriptor still agrees; otherwise leave both sides for the unique
		// fallback passes below. This is conservative for edits to the mutant
		// itself and prevents an ID collision from accepting new debt.
		if j, ok := byID[entry.ID]; ok && currentMatch[j] == -1 && structureOf(entry) == structureOf(current[j]) {
			baseMatch[i], currentMatch[j] = j, i
		}
	}

	fallbackMatchLocation(b.Survivors, current, baseMatch, currentMatch)
	fallbackMatchStructure(b.Survivors, current, baseMatch, currentMatch)

	c := BaselineComparison{
		knownIDs: make(map[string]struct{}),
		newIDs:   make(map[string]struct{}),
	}
	for i, entry := range b.Survivors {
		j := baseMatch[i]
		if j < 0 {
			c.Resolved = append(c.Resolved, entry)
			continue
		}
		cur := current[j]
		if entry.ID != cur.ID {
			kind := "structure"
			if sameLocation(entry, cur) {
				kind = "location"
			}
			c.Fallbacks = append(c.Fallbacks, BaselineFallback{OldID: entry.ID, NewID: cur.ID, Kind: kind})
		}
		switch mutants[j].Status {
		case mutator.StatusLived:
			c.Known = append(c.Known, cur)
			c.Retained = append(c.Retained, cur)
			c.knownIDs[cur.ID] = struct{}{}
		case mutator.StatusKilled, mutator.StatusEquivalent, mutator.StatusNotViable:
			c.Resolved = append(c.Resolved, entry)
		default:
			// NOT_COVERED, TIMED_OUT, INFRA_ERROR, and a defensive PENDING
			// all fail to prove that a former survivor was fixed.
			c.Unresolved = append(c.Unresolved, cur)
			c.Retained = append(c.Retained, cur)
		}
	}

	for i, m := range mutants {
		if m.Status != mutator.StatusLived || currentMatch[i] >= 0 {
			continue
		}
		entry := current[i]
		c.New = append(c.New, entry)
		c.newIDs[entry.ID] = struct{}{}
	}

	sortBaselineEntries(c.Known)
	sortBaselineEntries(c.New)
	sortBaselineEntries(c.Resolved)
	sortBaselineEntries(c.Unresolved)
	sortBaselineEntries(c.Retained)
	sort.Slice(c.Fallbacks, func(i, j int) bool { return c.Fallbacks[i].OldID < c.Fallbacks[j].OldID })
	return c
}

// ApplyBaselineComparison adds machine-readable baseline classifications to
// an already generated report without changing the gremlins-compatible core
// counters.
func ApplyBaselineComparison(r *Report, comparison BaselineComparison) {
	r.Baseline = &BaselineReport{
		KnownSurvivors:      len(comparison.Known),
		NewSurvivors:        len(comparison.New),
		ResolvedSurvivors:   len(comparison.Resolved),
		UnresolvedSurvivors: len(comparison.Unresolved),
		FallbackMatches:     len(comparison.Fallbacks),
	}
	for fi := range r.Files {
		for mi := range r.Files[fi].Mutations {
			m := &r.Files[fi].Mutations[mi]
			if _, ok := comparison.knownIDs[m.ID]; ok {
				m.BaselineStatus = BaselineStatusKnown
			}
			if _, ok := comparison.newIDs[m.ID]; ok {
				m.BaselineStatus = BaselineStatusNew
			}
		}
	}
}

// WriteBaseline atomically replaces path with b. A failed encode or rename
// leaves the previous committed baseline untouched.
func WriteBaseline(path string, b *Baseline) error {
	if path == "" {
		return errors.New("baseline path is empty")
	}
	snapshot := *b
	snapshot.Policy = b.Policy.Canonical()
	snapshot.Survivors = slices.Clone(b.Survivors)
	sortBaselineEntries(snapshot.Survivors)
	if err := snapshot.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gomutants-baseline-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	encodeErr := enc.Encode(&snapshot)
	closeErr := tmp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}

type locationKey struct {
	file, typ, original, replacement string
	line, column                     int
}

type structureKey struct {
	file, typ, original, replacement string
}

func fallbackMatchLocation(baseline, current []BaselineEntry, baseMatch, currentMatch []int) {
	baseBuckets := make(map[locationKey][]int)
	currentBuckets := make(map[locationKey][]int)
	for i, entry := range baseline {
		if baseMatch[i] < 0 {
			baseBuckets[locationOf(entry)] = append(baseBuckets[locationOf(entry)], i)
		}
	}
	for i, entry := range current {
		if currentMatch[i] < 0 {
			currentBuckets[locationOf(entry)] = append(currentBuckets[locationOf(entry)], i)
		}
	}
	for key, bi := range baseBuckets {
		ci := currentBuckets[key]
		if len(bi) == 1 && len(ci) == 1 {
			baseMatch[bi[0]], currentMatch[ci[0]] = ci[0], bi[0]
		}
	}
}

func fallbackMatchStructure(baseline, current []BaselineEntry, baseMatch, currentMatch []int) {
	baseBuckets := make(map[structureKey][]int)
	currentBuckets := make(map[structureKey][]int)
	for i, entry := range baseline {
		if baseMatch[i] < 0 {
			baseBuckets[structureOf(entry)] = append(baseBuckets[structureOf(entry)], i)
		}
	}
	for i, entry := range current {
		if currentMatch[i] < 0 {
			currentBuckets[structureOf(entry)] = append(currentBuckets[structureOf(entry)], i)
		}
	}
	for key, bi := range baseBuckets {
		ci := currentBuckets[key]
		if len(bi) == 1 && len(ci) == 1 {
			baseMatch[bi[0]], currentMatch[ci[0]] = ci[0], bi[0]
		}
	}
}

func locationOf(e BaselineEntry) locationKey {
	return locationKey{file: e.File, line: e.Line, column: e.Column, typ: e.Type, original: e.Original, replacement: e.Replacement}
}

func structureOf(e BaselineEntry) structureKey {
	return structureKey{file: e.File, typ: e.Type, original: e.Original, replacement: e.Replacement}
}

func sameLocation(a, b BaselineEntry) bool { return locationOf(a) == locationOf(b) }

func baselineEntry(m mutator.Mutant, moduleRoot string) BaselineEntry {
	file := m.RelFile
	if rel, err := filepath.Rel(moduleRoot, m.File); err == nil {
		file = filepath.ToSlash(rel)
	}
	return BaselineEntry{
		ID:          m.StableID,
		File:        file,
		Line:        m.Line,
		Column:      m.Col,
		Type:        string(m.Type),
		Original:    m.Original,
		Replacement: m.Replacement,
	}
}

func canonicalStrings(values []string) []string {
	values = slices.Clone(values)
	sort.Strings(values)
	return slices.Compact(values)
}

func sortBaselineEntries(entries []BaselineEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}
