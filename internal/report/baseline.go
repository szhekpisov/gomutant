package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/szhekpisov/gomutants/internal/atomicfile"
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
// accepted. Survivors is what a run must not regress against: LIVED mutants at
// write time, plus prior entries whose latest outcome was inconclusive
// (NOT_COVERED, TIMED_OUT, INFRA_ERROR) or that this run's package selection
// never examined. Those are retained precisely because nothing proved them
// fixed. Every other outcome is derived from the next run.
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
	Packages []string `json:"packages"`
	// Only and Disable record the user's mutator selection rather than the
	// mutator set it resolved to. The resolved set grows whenever gomutants
	// ships a new mutator, and fingerprinting it would make every such
	// release a policy change — see Differences.
	Only             []string `json:"only,omitempty"`
	Disable          []string `json:"disable,omitempty"`
	BuildTags        string   `json:"tags,omitempty"`
	TestFlags        string   `json:"test_flags,omitempty"`
	Integration      bool     `json:"integration,omitempty"`
	CoverPkg         string   `json:"coverpkg,omitempty"`
	DetectEquivalent bool     `json:"detect_equivalent,omitempty"`
	ExcludeFiles     []string `json:"exclude_files,omitempty"`
	// ExcludeCalls records the user's own call-exclusion patterns, not the
	// list they resolve to. That list is prefixed with gomutants' built-in
	// stdlib-logging set, which grows between releases, so fingerprinting it
	// would make every such release a policy change that rejects every
	// committed baseline with exit 2 — exactly what Only and Disable above
	// avoid for the mutator set. Switching the built-ins off is the user's
	// decision, so that is recorded, inverted so the default reads as an
	// absent field.
	ExcludeCalls           []string `json:"exclude_calls,omitempty"`
	ExcludeCallsNoDefaults bool     `json:"exclude_calls_no_defaults,omitempty"`
}

// BaselineEntry carries the stable ID plus enough source identity to recover
// conservatively from the documented stable-ID churn cases. File is always
// relative to the module root and slash-separated.
type BaselineEntry struct {
	ID   string `json:"id"`
	File string `json:"file"`
	// Anchor is the enclosing function as rendered in ID. Empty for
	// package-level declarations, so it is omitted rather than written blank.
	Anchor string `json:"anchor,omitempty"`
	// FamilySize is how many mutant-carrying declarations in File shared this
	// entry's declaration name when the entry was written — the run's own view
	// of the family, which is what the next run's count is comparable with.
	// A declaration losing its last mutant therefore reads as a removal, which
	// is the conservative direction. Anything above one means
	// the anchors in that family carry source-order suffixes, which shift
	// whenever such a declaration is added or removed — so comparing this
	// count with the next run's is what tells a stable family apart from one
	// whose anchors have been reassigned. Zero means "not recorded": baselines
	// written before this field existed, which matchExactIDs handles
	// conservatively.
	FamilySize  int    `json:"family_size,omitempty"`
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
	OutOfScope []BaselineEntry
	Fallbacks  []BaselineFallback
	Retained   []BaselineEntry

	knownIDs map[string]struct{}
	newIDs   map[string]struct{}
}

// baselineTmpPattern names the temp file WriteBaseline renames into place. It
// is matched by .gitignore, so an interrupted --baseline-update cannot leave a
// file `git add -A` would commit.
const baselineTmpPattern = ".gomutants-baseline-*.tmp"

// Canonical returns a deterministic policy representation for comparison and
// serialization. All slices are copied, sorted, and deduplicated.
func (p BaselinePolicy) Canonical() BaselinePolicy {
	p.Packages = canonicalStrings(p.Packages)
	p.Only = canonicalStrings(p.Only)
	p.Disable = canonicalStrings(p.Disable)
	p.ExcludeFiles = canonicalStrings(p.ExcludeFiles)
	p.ExcludeCalls = canonicalStrings(p.ExcludeCalls)
	return p
}

// Differences names policy dimensions that differ. Neither the generated tool
// version nor the mutator set it resolved is policy: a baseline is meant to
// survive tool upgrades, with descriptor fallback covering conservative ID
// migrations. A release that adds a mutator would otherwise fail every run
// with a policy mismatch, and the suggested --baseline-update recovery would
// itself be refused by the new-survivor gate the added mutator just tripped.
func (p BaselinePolicy) Differences(other BaselinePolicy) []string {
	p = p.Canonical()
	other = other.Canonical()
	var fields []string
	if !slices.Equal(p.Packages, other.Packages) {
		fields = append(fields, "packages")
	}
	if !slices.Equal(p.Only, other.Only) {
		fields = append(fields, "only")
	}
	if !slices.Equal(p.Disable, other.Disable) {
		fields = append(fields, "disable")
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
	if p.ExcludeCallsNoDefaults != other.ExcludeCallsNoDefaults {
		fields = append(fields, "exclude-calls-defaults")
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
		if entry.FamilySize < 0 {
			return fmt.Errorf("survivors[%d] has invalid family_size %d", i, entry.FamilySize)
		}
	}
	return nil
}

// NewBaseline creates an initial baseline from the LIVED outcomes in mutants.
func NewBaseline(goModule, generatedBy string, policy BaselinePolicy, mutants []mutator.Mutant, moduleRoot string) (*Baseline, error) {
	b := &Baseline{
		SchemaVersion: BaselineSchemaVersion,
		GoModule:      goModule,
		GeneratedBy:   generatedBy,
		Policy:        policy.Canonical(),
	}
	// Entries are rendered for the whole run, not just the survivors, so that
	// every FamilySize counts the declarations the run actually saw. Counting
	// only the survivors would undercount any family whose other members were
	// killed, and the next run — which counts over its own full mutant set —
	// would read that undercount as a declaration having been removed.
	entries, err := baselineEntries(mutants, moduleRoot)
	if err != nil {
		return nil, err
	}
	for i, m := range mutants {
		if m.Status != mutator.StatusLived {
			continue
		}
		b.Survivors = append(b.Survivors, entries[i])
	}
	sortBaselineEntries(b.Survivors)
	return b, nil
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

// BaselineScope names the package directories a run actually examined, as
// module-relative slash paths. A run that asks for fewer packages than the
// baseline covers generates no mutants for the rest, so an unmatched entry
// there means "never looked", not "fixed" — resolving it would erase accepted
// debt for packages nothing inspected. The scope is what lets the comparison
// tell those two apart. A nil scope disables the distinction and treats every
// unmatched entry as resolved; only tests and callers that genuinely ran the
// whole module should pass one.
type BaselineScope struct {
	moduleRoot string
	dirs       map[string]struct{}
}

// NewBaselineScope builds a scope from the absolute package directories a run
// resolved. Directories outside moduleRoot are dropped: nothing under them can
// match a module-relative entry anyway.
func NewBaselineScope(moduleRoot string, pkgDirs []string) *BaselineScope {
	s := &BaselineScope{moduleRoot: moduleRoot, dirs: make(map[string]struct{}, len(pkgDirs))}
	for _, dir := range pkgDirs {
		rel, err := filepath.Rel(moduleRoot, dir)
		// A ".." prefix means dir escaped the module root. Match the
		// separator too, so a real directory named "..cache" still counts.
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		s.dirs[filepath.ToSlash(rel)] = struct{}{}
	}
	return s
}

// resolvable reports whether an unmatched baseline entry may be dropped as
// fixed. Entries in an examined package always may: the run had its chance to
// produce them. For the rest, only a source file that no longer exists proves
// the debt is gone — a deleted package must still shrink the baseline, while
// untouched code this run never asked about must not.
func (s *BaselineScope) resolvable(entry BaselineEntry) bool {
	if s == nil {
		return true
	}
	if _, ok := s.dirs[path.Dir(entry.File)]; ok {
		return true
	}
	_, err := os.Stat(filepath.Join(s.moduleRoot, filepath.FromSlash(entry.File)))
	return errors.Is(err, os.ErrNotExist)
}

// CompareBaseline matches the completed mutant set against known survivor
// debt. Exact IDs win; two unique descriptor fallbacks cover anchor churn and
// line shifts without ever guessing among ambiguous candidates. scope may be
// nil; see BaselineScope for when it must not be.
func CompareBaseline(b *Baseline, mutants []mutator.Mutant, moduleRoot string, scope *BaselineScope) (BaselineComparison, error) {
	current, err := baselineEntries(mutants, moduleRoot)
	if err != nil {
		return BaselineComparison{}, err
	}
	baseMatch, currentMatch := unmatchedIndexes(len(b.Survivors), len(current))
	matchExactIDs(b.Survivors, current, baseMatch, currentMatch)

	// Position is evidence no edit can forge, so the location pass runs
	// against every unmatched entry. The structure pass matches by base name
	// alone, which is exactly what makes it able to hand a deleted
	// declaration's accepted debt to a surviving namesake, so it skips the
	// families that lost a member.
	fallbackMatch(b.Survivors, current, baseMatch, currentMatch, locationOf, nil)
	fallbackMatch(b.Survivors, current, baseMatch, currentMatch, structureOf, shrunkFamilies(b.Survivors, current))

	c := classifyBaselineMatches(b.Survivors, current, mutants, baseMatch, scope)
	appendNewSurvivors(&c, current, mutants, currentMatch)
	sortBaselineComparison(&c)
	return c, nil
}

func baselineEntries(mutants []mutator.Mutant, moduleRoot string) ([]BaselineEntry, error) {
	current := make([]BaselineEntry, len(mutants))
	for i, m := range mutants {
		entry, err := baselineEntry(m, moduleRoot)
		if err != nil {
			return nil, err
		}
		current[i] = entry
	}
	stampFamilySizes(current)
	return current, nil
}

// stampFamilySizes records on every entry how many distinct declarations in
// its file share its declaration name, so the count travels into the committed
// baseline and can be compared with the next run's.
func stampFamilySizes(entries []BaselineEntry) {
	families := anchorFamilies(entries)
	for i := range entries {
		entries[i].FamilySize = families[familyOf(entries[i])]
	}
}

func unmatchedIndexes(baseLen, currentLen int) ([]int, []int) {
	baseMatch := make([]int, baseLen)
	currentMatch := make([]int, currentLen)
	for i := range baseMatch {
		baseMatch[i] = -1
	}
	for i := range currentMatch {
		currentMatch[i] = -1
	}
	return baseMatch, currentMatch
}

func matchExactIDs(baseline, current []BaselineEntry, baseMatch, currentMatch []int) {
	byID := make(map[string]int, len(current))
	for i, entry := range current {
		byID[entry.ID] = i
	}
	for i, entry := range baseline {
		j, ok := byID[entry.ID]
		if !ok || currentMatch[j] != -1 {
			continue
		}
		// Trust the ID only while its structural mutation descriptor still
		// agrees; otherwise leave both sides for the unique fallback passes
		// below. This is conservative for edits to the mutant itself.
		if structureOf(entry) != structureOf(current[j]) {
			continue
		}
		if !stableFamily(entry, current[j]) {
			continue
		}
		baseMatch[i], currentMatch[j] = j, i
	}
}

// stableFamily reports whether a matched ID still names the declaration it
// named when the baseline was written.
//
// A repeated declaration such as init() is disambiguated by source order, so
// adding or removing one hands an old stable ID to a different function while
// the old mutant lives on under a shifted anchor. The descriptor check cannot
// see that — both are the same kind of mutation in a same-named function — so
// across such a shift the ID is not evidence of identity at all. An unchanged
// count is the evidence that no such shift happened: line moves, edits
// elsewhere in the file, and new declarations under other names all leave it
// alone, so the overwhelmingly common case of a pure line shift keeps matching
// by ID instead of being resolved and re-reported as new debt.
//
// Entries written before family sizes were recorded have nothing to compare,
// so they keep the older, blunter rule of distrusting every repeated family.
// That is conservative for insertions and blind to deletions; rewriting the
// baseline once with --baseline-update closes it.
func stableFamily(entry, cur BaselineEntry) bool {
	if entry.FamilySize == 0 {
		return cur.FamilySize <= 1
	}
	return entry.FamilySize == cur.FamilySize
}

// shrunkFamilies names the declaration families that have fewer same-named
// declarations in this run than they had when the baseline was written.
// Something the baseline knew about is gone, and because every member of a
// family renders under one base name, nothing in an entry says whether it
// belongs to the declaration that was deleted or to one of its surviving
// namesakes. Debt must not migrate across that gap: a survivor inheriting a
// deleted namesake's accepted status is a regression reported as KNOWN.
//
// A family missing from current entirely is included too — its declarations
// are gone, so the same reasoning applies, and no bucket can pair with it
// anyway. Families that grew are not: every declaration the baseline described
// still exists, so a unique descriptor still identifies which one it was.
func shrunkFamilies(baseline, current []BaselineEntry) map[familyKey]struct{} {
	sizes := make(map[familyKey]int, len(current))
	for _, entry := range current {
		sizes[familyOf(entry)] = entry.FamilySize
	}
	shrunk := make(map[familyKey]struct{})
	for _, entry := range baseline {
		key := familyOf(entry)
		if entry.FamilySize > sizes[key] {
			shrunk[key] = struct{}{}
		}
	}
	return shrunk
}

// anchorFamilies counts the distinct anchors sharing each file and base
// declaration name — that is, how many declarations of that name the entries
// describe. A count above one means the file repeats a name, so its anchors
// carry source-order suffixes that adding or removing one shifts.
func anchorFamilies(entries []BaselineEntry) map[familyKey]int {
	anchors := make(map[familyKey]map[string]struct{})
	for _, entry := range entries {
		key := familyOf(entry)
		if anchors[key] == nil {
			anchors[key] = make(map[string]struct{})
		}
		anchors[key][entry.Anchor] = struct{}{}
	}
	counts := make(map[familyKey]int, len(anchors))
	for key, set := range anchors {
		counts[key] = len(set)
	}
	return counts
}

func classifyBaselineMatches(baseline, current []BaselineEntry, mutants []mutator.Mutant, baseMatch []int, scope *BaselineScope) BaselineComparison {
	c := BaselineComparison{
		knownIDs: make(map[string]struct{}),
		newIDs:   make(map[string]struct{}),
	}
	for i, entry := range baseline {
		j := baseMatch[i]
		if j < 0 {
			if !scope.resolvable(entry) {
				c.OutOfScope = append(c.OutOfScope, entry)
				c.Retained = append(c.Retained, entry)
				continue
			}
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
	return c
}

func appendNewSurvivors(c *BaselineComparison, current []BaselineEntry, mutants []mutator.Mutant, currentMatch []int) {
	for i, m := range mutants {
		if m.Status != mutator.StatusLived || currentMatch[i] >= 0 {
			continue
		}
		entry := current[i]
		c.New = append(c.New, entry)
		c.newIDs[entry.ID] = struct{}{}
	}
}

func sortBaselineComparison(c *BaselineComparison) {
	sortBaselineEntries(c.Known)
	sortBaselineEntries(c.New)
	sortBaselineEntries(c.Resolved)
	sortBaselineEntries(c.Unresolved)
	sortBaselineEntries(c.OutOfScope)
	sortBaselineEntries(c.Retained)
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="baseline survivor IDs are unique by validation, so < and <= differ only for an unreachable equal-ID comparison"
	sort.Slice(c.Fallbacks, func(i, j int) bool { return c.Fallbacks[i].OldID < c.Fallbacks[j].OldID })
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
			// else-if rather than two independent assignments: an ID in both
			// sets would otherwise be relabelled NEW by whichever check runs
			// last, turning accepted debt into a reported regression.
			if _, ok := comparison.knownIDs[m.ID]; ok {
				m.BaselineStatus = BaselineStatusKnown
			} else if _, ok := comparison.newIDs[m.ID]; ok {
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
	return atomicfile.WriteJSON(path, baselineTmpPattern, "  ", &snapshot)
}

type familyKey struct {
	file, baseAnchor string
}

type locationKey struct {
	file, typ, original, replacement string
	line, column                     int
}

type structureKey struct {
	file, baseAnchor, typ, original, replacement string
}

// fallbackMatch pairs entries whose key is unique on both sides, leaving
// everything else unmatched. Uniqueness is the whole safety property: a key
// shared by two candidates says nothing about which one the accepted debt
// belongs to, so neither is matched and an unmatched current survivor stays
// NEW.
//
// Baseline entries in a skipFamilies family are withheld from the pass. Only
// the baseline side needs withholding: skipFamilies is used with the key that
// includes the family's base anchor, so a current entry in a skipped family
// only ever shares a bucket with baseline entries from that same family, and a
// bucket with no baseline candidate pairs with nothing.
func fallbackMatch[K comparable](baseline, current []BaselineEntry, baseMatch, currentMatch []int, key func(BaselineEntry) K, skipFamilies map[familyKey]struct{}) {
	baseBuckets := make(map[K][]int)
	currentBuckets := make(map[K][]int)
	for i, entry := range baseline {
		if _, skip := skipFamilies[familyOf(entry)]; skip {
			continue
		}
		if baseMatch[i] < 0 {
			k := key(entry)
			baseBuckets[k] = append(baseBuckets[k], i)
		}
	}
	for i, entry := range current {
		if currentMatch[i] < 0 {
			k := key(entry)
			currentBuckets[k] = append(currentBuckets[k], i)
		}
	}
	for k, bi := range baseBuckets {
		ci := currentBuckets[k]
		if len(bi) == 1 && len(ci) == 1 {
			baseMatch[bi[0]], currentMatch[ci[0]] = ci[0], bi[0]
		}
	}
}

func familyOf(e BaselineEntry) familyKey {
	return familyKey{file: e.File, baseAnchor: baseAnchor(e.Anchor)}
}

func locationOf(e BaselineEntry) locationKey {
	return locationKey{file: e.File, line: e.Line, column: e.Column, typ: e.Type, original: e.Original, replacement: e.Replacement}
}

// structureOf keys the line-shift fallback. It includes the enclosing
// declaration, without which the key is bounded only by the file: deleting a
// function and adding an unrelated one that happens to contain the same kind
// of mutation would then match, and brand-new untested debt would inherit the
// deleted function's accepted status. Anchored to the base name so the
// suffix churn a repeated declaration produces does not defeat the match.
func structureOf(e BaselineEntry) structureKey {
	return structureKey{file: e.File, baseAnchor: baseAnchor(e.Anchor), typ: e.Type, original: e.Original, replacement: e.Replacement}
}

// baseAnchor strips the source-order suffix a repeated declaration name
// carries, so "init" and "init~2" share one family.
func baseAnchor(anchor string) string {
	base, _, _ := strings.Cut(anchor, mutator.AnchorRepeatSep)
	return base
}

func sameLocation(a, b BaselineEntry) bool { return locationOf(a) == locationOf(b) }

// baselineEntry renders one mutant in the baseline's own path space: relative
// to the module root, always slash-separated.
//
// There is deliberately no fallback to mutator.Mutant.RelFile. RelFile strips
// the longest common import-path prefix of the packages in the run, so it
// names the same file differently under `./...` than under `./internal/a`.
// Writing it into a baseline would key every entry in a path space the next
// run does not share: nothing would match, every known survivor would be
// reported as new, and the run would fail with a wall of false regressions.
// An unrelatable path is a bug in the caller, so say so instead.
func baselineEntry(m mutator.Mutant, moduleRoot string) (BaselineEntry, error) {
	rel, err := filepath.Rel(moduleRoot, m.File)
	if err != nil {
		return BaselineEntry{}, fmt.Errorf("baseline entry %s: %s is not relative to module root %s: %w", m.StableID, m.File, moduleRoot, err)
	}
	return BaselineEntry{
		ID:          m.StableID,
		File:        filepath.ToSlash(rel),
		Anchor:      m.Anchor,
		Line:        m.Line,
		Column:      m.Col,
		Type:        string(m.Type),
		Original:    m.Original,
		Replacement: m.Replacement,
	}, nil
}

func canonicalStrings(values []string) []string {
	values = slices.Clone(values)
	sort.Strings(values)
	return slices.Compact(values)
}

func sortBaselineEntries(entries []BaselineEntry) {
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="baseline survivor IDs are unique by validation, so < and <= differ only for an unreachable equal-ID comparison"
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}
