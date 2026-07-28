package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Workers != DefaultWorkers() {
		t.Errorf("Workers=%d, want %d", cfg.Workers, DefaultWorkers())
	}
	if cfg.TimeoutCoefficient != 10 {
		t.Errorf("TimeoutCoefficient=%d, want 10", cfg.TimeoutCoefficient)
	}
	if cfg.Output != "mutation-report.json" {
		t.Errorf("Output=%q, want %q", cfg.Output, "mutation-report.json")
	}
	if cfg.CheckpointInterval != DefaultCheckpointInterval {
		t.Errorf("CheckpointInterval=%v, want %v", cfg.CheckpointInterval, DefaultCheckpointInterval)
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := Load("/nonexistent/.gomutants.yml")
	if err != nil {
		t.Fatalf("Load of missing file should not error: %v", err)
	}
	if cfg.Workers != DefaultWorkers() {
		t.Errorf("Workers=%d, want default %d", cfg.Workers, DefaultWorkers())
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")

	yaml := `workers: 4
test-cpu: 2
timeout-coefficient: 20
coverpkg: "./pkg/..."
test-flags: "-rapid.checks=20 -short"
output: report.json
dry-run: true
verbose: true
quiet: true
disable:
  - BRANCH_IF
only:
  - ARITHMETIC_BASE
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Workers != 4 {
		t.Errorf("Workers=%d, want 4", cfg.Workers)
	}
	if cfg.TestCPU != 2 {
		t.Errorf("TestCPU=%d, want 2", cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != 20 {
		t.Errorf("TimeoutCoefficient=%d, want 20", cfg.TimeoutCoefficient)
	}
	if cfg.CoverPkg != "./pkg/..." {
		t.Errorf("CoverPkg=%q, want %q", cfg.CoverPkg, "./pkg/...")
	}
	if cfg.TestFlags != "-rapid.checks=20 -short" {
		t.Errorf("TestFlags=%q, want %q", cfg.TestFlags, "-rapid.checks=20 -short")
	}
	if cfg.Output != "report.json" {
		t.Errorf("Output=%q, want %q", cfg.Output, "report.json")
	}
	if !cfg.DryRun {
		t.Error("DryRun should be true")
	}
	if !cfg.Verbose {
		t.Error("Verbose should be true")
	}
	if !cfg.Quiet {
		t.Error("Quiet should be true")
	}
	if len(cfg.Disable) != 1 || cfg.Disable[0] != "BRANCH_IF" {
		t.Errorf("Disable=%v, want [BRANCH_IF]", cfg.Disable)
	}
	if len(cfg.Only) != 1 || cfg.Only[0] != "ARITHMETIC_BASE" {
		t.Errorf("Only=%v, want [ARITHMETIC_BASE]", cfg.Only)
	}
}

func TestLoadZeroValuesGetDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")
	// Explicitly set fields to zero — should fall back to defaults.
	yaml := "workers: 0\ntimeout-coefficient: 0\noutput: \"\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != DefaultWorkers() {
		t.Errorf("Workers=%d, want default %d", cfg.Workers, DefaultWorkers())
	}
	if cfg.TimeoutCoefficient != 10 {
		t.Errorf("TimeoutCoefficient=%d, want default 10", cfg.TimeoutCoefficient)
	}
	if cfg.Output != "mutation-report.json" {
		t.Errorf("Output=%q, want default", cfg.Output)
	}
}

func TestLoadCheckpointInterval(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want time.Duration
	}{
		{"absent keeps default", "workers: 4\n", DefaultCheckpointInterval},
		{"explicit duration parses", "checkpoint-interval: 30s\n", 30 * time.Second},
		{"zero survives as disable", "checkpoint-interval: 0s\n", 0},
		{"negative reverts to default", "checkpoint-interval: -5s\n", DefaultCheckpointInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".gomutants.yml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.CheckpointInterval != tt.want {
				t.Errorf("CheckpointInterval=%v, want %v", cfg.CheckpointInterval, tt.want)
			}
		})
	}
}

// TestApplyFlagsCheckpointIntervalZeroOverride pins the three-state merge:
// an explicit --checkpoint-interval=0 must override a non-zero YAML value
// (a plain "non-zero means user-set" check would silently drop it).
func TestApplyFlagsCheckpointIntervalZeroOverride(t *testing.T) {
	cfg := Default()
	cfg.CheckpointInterval = 30 * time.Second

	cfg.ApplyFlags(Flags{CheckpointInterval: CheckpointIntervalFlag{Set: true, Value: 0}})

	if cfg.CheckpointInterval != 0 {
		t.Errorf("CheckpointInterval=%v, want 0 (explicit --checkpoint-interval=0 must override YAML)", cfg.CheckpointInterval)
	}
}

// TestApplyFlagsIntegration pins the --integration toggle merge: passing the
// flag sets Integration; not passing it leaves the value untouched. Kills
// BRANCH_IF and STATEMENT_REMOVE on the `if f.Integration { c.Integration = true }`
// merge.
func TestApplyFlagsIntegration(t *testing.T) {
	cfg := Default()
	if cfg.Integration {
		t.Fatal("default Integration should be false")
	}

	cfg.ApplyFlags(Flags{Integration: true})
	if !cfg.Integration {
		t.Error("ApplyFlags(Integration: true) must set Integration; the merge guard or assignment was dropped")
	}

	// Omitting the flag must not turn Integration on from its zero value.
	other := Default()
	other.ApplyFlags(Flags{})
	if other.Integration {
		t.Error("ApplyFlags without Integration must leave it false")
	}
}

func TestLoadReadError(t *testing.T) {
	// Use a directory path as config file — will cause a read error (not IsNotExist).
	dir := t.TempDir()
	_, err := Load(dir) // dir exists but is a directory, not a file.
	if err == nil {
		t.Fatal("expected error when reading a directory as config file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")
	if err := os.WriteFile(path, []byte("{{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestApplyFlags(t *testing.T) {
	cfg := Default()

	cfg.ApplyFlags(Flags{
		Workers:            8,
		TestCPU:            4,
		TimeoutCoefficient: 15,
		TimeoutMargin:      4.5,
		TimeoutMin:         5 * time.Second,
		AdaptiveTimeout:    AdaptiveTimeoutFlag{Set: true, Value: false},
		CheckpointInterval: CheckpointIntervalFlag{Set: true, Value: 30 * time.Second},
		CoverPkg:           "./pkg/...",
		Tags:               "integration,debug",
		TestFlags:          "-rapid.checks=20",
		Output:             "out.json",
		Disable:            "BRANCH_IF,BRANCH_ELSE",
		Only:               "ARITHMETIC_BASE",
		ExcludeFiles:       "vendor/, _gen\\.go",
		ChangedSince:       "main",
		Cache:              "cache.json",
		DryRun:             true,
		Verbose:            true,
		Quiet:              true,
	})

	if cfg.Workers != 8 {
		t.Errorf("Workers=%d, want 8", cfg.Workers)
	}
	if cfg.TestCPU != 4 {
		t.Errorf("TestCPU=%d, want 4", cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != 15 {
		t.Errorf("TimeoutCoefficient=%d, want 15", cfg.TimeoutCoefficient)
	}
	if cfg.TimeoutMargin != 4.5 {
		t.Errorf("TimeoutMargin=%v, want 4.5", cfg.TimeoutMargin)
	}
	if cfg.TimeoutMin != 5*time.Second {
		t.Errorf("TimeoutMin=%v, want 5s", cfg.TimeoutMin)
	}
	if cfg.AdaptiveTimeoutEnabled() {
		t.Errorf("AdaptiveTimeoutEnabled=true, want false (CLI override)")
	}
	if cfg.CheckpointInterval != 30*time.Second {
		t.Errorf("CheckpointInterval=%v, want 30s", cfg.CheckpointInterval)
	}
	if cfg.CoverPkg != "./pkg/..." {
		t.Errorf("CoverPkg=%q", cfg.CoverPkg)
	}
	if cfg.Tags != "integration,debug" {
		t.Errorf("Tags=%q, want integration,debug", cfg.Tags)
	}
	if cfg.TestFlags != "-rapid.checks=20" {
		t.Errorf("TestFlags=%q, want -rapid.checks=20", cfg.TestFlags)
	}
	if cfg.Output != "out.json" {
		t.Errorf("Output=%q", cfg.Output)
	}
	if len(cfg.Disable) != 2 {
		t.Errorf("Disable=%v, want 2 entries", cfg.Disable)
	}
	if len(cfg.Only) != 1 || cfg.Only[0] != "ARITHMETIC_BASE" {
		t.Errorf("Only=%v", cfg.Only)
	}
	// Comma-split with surrounding whitespace trimmed, mirroring Disable/Only.
	if len(cfg.ExcludeFiles) != 2 || cfg.ExcludeFiles[0] != "vendor/" || cfg.ExcludeFiles[1] != "_gen\\.go" {
		t.Errorf("ExcludeFiles=%v, want [vendor/ _gen\\.go]", cfg.ExcludeFiles)
	}
	if cfg.ChangedSince != "main" {
		t.Errorf("ChangedSince=%q, want main", cfg.ChangedSince)
	}
	if cfg.Cache != "cache.json" {
		t.Errorf("Cache=%q, want cache.json", cfg.Cache)
	}
	if !cfg.DryRun {
		t.Error("DryRun should be true")
	}
	if !cfg.Verbose {
		t.Error("Verbose should be true")
	}
	if !cfg.Quiet {
		t.Error("Quiet should be true")
	}
}

func TestApplyFlagsZeroValuesNoOverride(t *testing.T) {
	cfg := Default()
	cfg.TestCPU = 7
	cfg.Tags = "integration" // e.g. set via YAML
	cfg.TestFlags = "-short" // e.g. set via YAML
	orig := cfg

	// Zero/empty values should not override defaults.
	cfg.ApplyFlags(Flags{})

	if cfg.Workers != orig.Workers {
		t.Errorf("Workers changed from %d to %d", orig.Workers, cfg.Workers)
	}
	if cfg.TestCPU != orig.TestCPU {
		t.Errorf("TestCPU changed from %d to %d", orig.TestCPU, cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != orig.TimeoutCoefficient {
		t.Errorf("TimeoutCoefficient changed")
	}
	// Pin the new adaptive-timeout knobs against CONDITIONALS_BOUNDARY
	// (`> 0` → `>= 0`) on their respective ApplyFlags guards. Without
	// these checks a `>= 0` mutation would silently overwrite the
	// default with the caller's zero.
	if cfg.TimeoutMargin != orig.TimeoutMargin {
		t.Errorf("TimeoutMargin changed from %v to %v — CONDITIONALS_BOUNDARY on `> 0` would let zero override the default", orig.TimeoutMargin, cfg.TimeoutMargin)
	}
	if cfg.TimeoutMin != orig.TimeoutMin {
		t.Errorf("TimeoutMin changed from %v to %v", orig.TimeoutMin, cfg.TimeoutMin)
	}
	if cfg.AdaptiveTimeout != orig.AdaptiveTimeout {
		t.Errorf("AdaptiveTimeout pointer changed; AdaptiveTimeoutFlag{Set:false} must be a no-op")
	}
	if cfg.CheckpointInterval != orig.CheckpointInterval {
		t.Errorf("CheckpointInterval changed from %v to %v; CheckpointIntervalFlag{Set:false} must be a no-op", orig.CheckpointInterval, cfg.CheckpointInterval)
	}
	if cfg.Output != orig.Output {
		t.Errorf("Output changed")
	}
	if cfg.Tags != orig.Tags {
		t.Errorf("Tags changed from %q to %q; an empty --tags must not clobber a YAML value", orig.Tags, cfg.Tags)
	}
	if cfg.TestFlags != orig.TestFlags {
		t.Errorf("TestFlags changed from %q to %q; an empty --test-flags must not clobber a YAML value", orig.TestFlags, cfg.TestFlags)
	}
}

// TestTestFlagFields pins the split contract every consumer depends on:
// the argv fragments appended to the inner `go test`. Unset must yield
// nothing appendable (not a one-element slice holding ""), which would
// otherwise reach `go test` as an empty argument and be read as a package
// pattern. Runs of whitespace collapse, and repeated --test-flags values
// (joined with a space by the CLI layer) come back in order.
func TestTestFlagFields(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"unset", "", nil},
		{"whitespace only", "   ", nil},
		{"single", "-short", []string{"-short"}},
		{"multiple", "-short -rapid.checks=20", []string{"-short", "-rapid.checks=20"}},
		{"collapses runs", "  -short   -race  ", []string{"-short", "-race"}},
		{"preserves order", "-a -b -c", []string{"-a", "-b", "-c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{TestFlags: tc.in}
			got := cfg.TestFlagFields()
			if !slices.Equal(got, tc.want) {
				t.Errorf("TestFlagFields(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestAdaptiveTimeoutEnabledNilDefault kills BRANCH_IF on the
// `if c.AdaptiveTimeout == nil { return true }` body. Without that
// early return the function dereferences a nil pointer and panics; the
// recover-then-fail wrapper distinguishes the panic from a clean
// `false` return that some mutations might also produce.
func TestAdaptiveTimeoutEnabledNilDefault(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("AdaptiveTimeoutEnabled panicked on nil pointer — BRANCH_IF on the nil-guard body lets execution fall through to *c.AdaptiveTimeout: %v", r)
		}
	}()
	c := Config{AdaptiveTimeout: nil}
	if !c.AdaptiveTimeoutEnabled() {
		t.Errorf("nil AdaptiveTimeout must default to true")
	}
}

// TestAdaptiveTimeoutEnabledExplicitFalse pins the *c.AdaptiveTimeout
// dereference path so a STATEMENT_REMOVE on the deref still has a
// targeted assertion.
func TestAdaptiveTimeoutEnabledExplicitFalse(t *testing.T) {
	f := false
	c := Config{AdaptiveTimeout: &f}
	if c.AdaptiveTimeoutEnabled() {
		t.Errorf("explicit false must propagate through AdaptiveTimeoutEnabled")
	}
}

// TestDefaultTimeoutMinValue pins DefaultTimeoutMin against
// ARITHMETIC_BASE (`*` → `/` collapses 2*time.Second to 0). Asserting
// the literal value ensures any arithmetic mutation on the constant
// shows up here.
func TestDefaultTimeoutMinValue(t *testing.T) {
	if DefaultTimeoutMin != 2*time.Second {
		t.Errorf("DefaultTimeoutMin = %v, want 2s — ARITHMETIC_BASE on `2 * time.Second` would change this", DefaultTimeoutMin)
	}
}

// TestDefaultCheckpointIntervalValue pins DefaultCheckpointInterval
// against ARITHMETIC_BASE on its initializer. `10 * time.Second` mutated
// to `10 / time.Second` collapses to 0 (integer division), which silently
// disables periodic checkpointing without any other test noticing because
// every other assertion compares against the const symbolically.
func TestDefaultCheckpointIntervalValue(t *testing.T) {
	if DefaultCheckpointInterval != 10*time.Second {
		t.Errorf("DefaultCheckpointInterval = %v, want 10s — ARITHMETIC_BASE on `10 * time.Second` would change this", DefaultCheckpointInterval)
	}
}

// TestLoadAppliesDefaultsForZeroAdaptiveFields kills BRANCH_IF and
// CONDITIONALS_NEGATION on the two `if cfg.TimeoutMargin == 0` /
// `cfg.TimeoutMin == 0` defaulting blocks in Load. We write a YAML
// that explicitly sets both to zero and assert the defaults take over.
// Without the guards (or with their condition negated) the user would
// run with TimeoutMargin=0 → per-mutant timeouts always clamp to Min,
// hiding genuine slowdowns.
func TestLoadAppliesDefaultsForZeroAdaptiveFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")
	// timeout-min is time.Duration; YAML needs a string-compatible zero.
	// "0s" is the zero duration so the defaulting block in Load() must
	// still kick in (its trigger is the int64 zero, not the YAML token).
	body := []byte("timeout-margin: 0\ntimeout-min: 0s\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TimeoutMargin != DefaultTimeoutMargin {
		t.Errorf("TimeoutMargin=%v, want default %v — BRANCH_IF / CONDITIONALS_NEGATION on the zero-guard would skip the default", cfg.TimeoutMargin, DefaultTimeoutMargin)
	}
	if cfg.TimeoutMin != DefaultTimeoutMin {
		t.Errorf("TimeoutMin=%v, want default %v", cfg.TimeoutMin, DefaultTimeoutMin)
	}
}

func TestResolveCache(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty resolves to default path", "", ".gomutants-cache.json"},
		{"off disables", "off", ""},
		{"explicit path passes through", "/tmp/x.json", "/tmp/x.json"},
		{"relative path passes through", ".cache.json", ".cache.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Cache: tc.in}
			c.ResolveCache()
			if c.Cache != tc.want {
				t.Errorf("Cache=%q, want %q", c.Cache, tc.want)
			}
		})
	}
}

// TestLoadExampleFile asserts the in-repo .gomutants.yml.example matches
// the Config struct field-for-field. The production Load() path is
// permissive (yaml.v3 silently ignores unknown keys), so a separate
// strict decode with KnownFields(true) is what actually catches drift —
// e.g. a key removed from Config but still documented in the example,
// or a typo in the example that would silently no-op for users.
//
// Hard-fails (not Skip) on a missing file: the example is committed and
// referenced from the README; a missing file should break CI rather
// than be quietly tolerated.
func TestLoadExampleFile(t *testing.T) {
	path := filepath.Join("..", "..", ".gomutants.yml.example")

	// Permissive Load() must succeed.
	if _, err := Load(path); err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}

	// Strict decode catches keys that aren't on the Config struct.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("strict decode of %s failed — example contains keys absent from Config: %v", path, err)
	}
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}}, // Kills STATEMENT_REMOVE on TrimSpace.
		{"single", []string{"single"}},
		{"  spaced  ", []string{"spaced"}}, // Explicit trimming check.
		{"", nil},
		{",,,", nil},
	}
	for _, tc := range tests {
		got := splitAndTrim(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("splitAndTrim(%q) = %v (len %d), want %v (len %d)",
				tc.input, got, len(got), tc.want, len(tc.want))
			continue
		}
		for i, g := range got {
			if g != tc.want[i] {
				t.Errorf("splitAndTrim(%q)[%d] = %q, want %q", tc.input, i, g, tc.want[i])
			}
		}
	}
}

// TestDetectEquivalentEnabled pins the three-state default: nil → false
// (opt-in), and the two explicit pointer states. Kills the nil-guard
// CONDITIONALS_NEGATION / BRANCH_IF in DetectEquivalentEnabled.
func TestDetectEquivalentEnabled(t *testing.T) {
	if (&Config{}).DetectEquivalentEnabled() {
		t.Error("nil DetectEquivalent should be false (opt-in default)")
	}
	tr, fa := true, false
	if !(&Config{DetectEquivalent: &tr}).DetectEquivalentEnabled() {
		t.Error("true pointer should report enabled")
	}
	if (&Config{DetectEquivalent: &fa}).DetectEquivalentEnabled() {
		t.Error("false pointer should report disabled")
	}
}

// TestApplyFlagsDetectEquivalent pins the three-state merge: a Set flag
// overrides, an unset flag leaves the YAML-loaded value intact. Kills the
// BRANCH_IF + STATEMENT_REMOVE on the applyToggleFlags merge branch.
func TestApplyFlagsDetectEquivalent(t *testing.T) {
	cfg := Default()
	cfg.ApplyFlags(Flags{DetectEquivalent: DetectEquivalentFlag{Set: true, Value: true}})
	if !cfg.DetectEquivalentEnabled() {
		t.Error("Set=true,Value=true not merged into config")
	}

	yes := true
	cfg2 := Config{DetectEquivalent: &yes}
	cfg2.ApplyFlags(Flags{DetectEquivalent: DetectEquivalentFlag{Set: false}})
	if !cfg2.DetectEquivalentEnabled() {
		t.Error("unset flag wrongly overrode the config value")
	}
}
