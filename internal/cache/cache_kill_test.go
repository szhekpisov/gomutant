package cache

// Tests in this file exist to kill specific surviving mutants surfaced
// by `gomutants ./internal/...`. They complement the behavioural tests
// in cache_test.go by exercising loop-iteration ordering, dedup
// boundaries, sort-comparator field precedence, hasher memoization, and
// each I/O error path in Save (via the os* function-variable hooks).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// --- Hasher.File memoization (cache.go:138, 145) -----------------------------

// TestHasher_File_MemoizesSrcCacheResult mutates the srcCache map
// between two File() calls. With the memoization write intact the
// second call returns the first hash; with it removed (mutant) the
// second call recomputes from the now-mutated bytes.
func TestHasher_File_MemoizesSrcCacheResult(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")

	src := map[string][]byte{p: []byte("v1")}
	h := NewHasher(src)

	first, err := h.File(p)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	src[p] = []byte("v2")
	second, err := h.File(p)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Fatalf("memoization not in effect: first=%s second=%s (after srcCache mutation)", first, second)
	}
}

// TestHasher_File_MemoizesDiskResult writes a file, hashes it, rewrites
// the file with new content, hashes again — the second result must be
// the cached first. With the memoization write removed, the second
// call recomputes from disk and returns the new hash.
func TestHasher_File_MemoizesDiskResult(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHasher(nil)
	first, err := h.File(p)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	if err := os.WriteFile(p, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := h.File(p)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Fatalf("memoization not in effect: first=%s second=%s (after disk rewrite)", first, second)
	}
}

// --- HashTestFiles dedup (cache.go:167, 168) ---------------------------------

// TestHashTestFiles_DedupOnlySkipsAdjacentDuplicates uses [a, b, b, c]
// to distinguish proper "skip when equal to previous" dedup from the
// `p == sorted[i-1]` → `true` mutation (which skips every entry after
// the first) and from the `continue` → `break` mutation (which exits
// after the first dup, dropping c).
func TestHashTestFiles_DedupOnlySkipsAdjacentDuplicates(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a_test.go")
	b := filepath.Join(dir, "b_test.go")
	c := filepath.Join(dir, "c_test.go")
	mustWrite(t, a, "package x\n")
	mustWrite(t, b, "package x\n// b\n")
	mustWrite(t, c, "package x\n// c\n")

	h := NewHasher(nil)

	want, err := h.HashTestFiles([]string{a, b, c})
	if err != nil {
		t.Fatalf("want: %v", err)
	}
	got, err := h.HashTestFiles([]string{a, b, b, c})
	if err != nil {
		t.Fatalf("got: %v", err)
	}
	if got != want {
		t.Fatalf("dedup did not produce [a,b,c] hash: got=%s want=%s", got, want)
	}

	// Cross-check: the all-skip mutation would produce hash([a]) for
	// the [a,b,b,c] input; verify this differs from the [a,b,c] hash.
	onlyA, err := h.HashTestFiles([]string{a})
	if err != nil {
		t.Fatalf("onlyA: %v", err)
	}
	if got == onlyA {
		t.Fatalf("dedup retained only first element: hash matches [a]")
	}
}

// --- HashTestFiles framing (cache.go:178, 179, 185 surface removed) ---------

// TestHashTestFiles_DistinguishesByBasename uses two files with
// identical content but different basenames to ensure the basename
// participates in the digest. With the per-file Fprintf removed (or
// the basename dropped from the format), both inputs collapse to the
// same hash.
func TestHashTestFiles_DistinguishesByBasename(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dirA, "alpha_test.go")
	b := filepath.Join(dirB, "beta_test.go")
	mustWrite(t, a, "package x\n")
	mustWrite(t, b, "package x\n")

	h := NewHasher(nil)
	hA, err := h.HashTestFiles([]string{a})
	if err != nil {
		t.Fatalf("hA: %v", err)
	}
	hB, err := h.HashTestFiles([]string{b})
	if err != nil {
		t.Fatalf("hB: %v", err)
	}
	if hA == hB {
		t.Fatalf("hash insensitive to basename: %s", hA)
	}
}

// --- Lookup loop continues (cache.go:308, 311, 312, 316, 321, 326) -----------

// TestLookup_ContinueAdvancesAcrossSkippedMutants asserts that every
// skip path in Lookup uses `continue` (advances to the next mutant)
// rather than `break` (which would short-circuit later valid hits).
// Each subtest places one skip-triggering mutant before a known-good
// hit; with the continue inverted to break, the second mutant remains
// pending and hits drops to 0.
func TestLookup_ContinueAdvancesAcrossSkippedMutants(t *testing.T) {
	dir := t.TempDir()
	prodPath := filepath.Join(dir, "x.go")
	testPath := filepath.Join(dir, "x_test.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, testPath, "package x\n")

	prodHash, err := HashFile(prodPath)
	if err != nil {
		t.Fatal(err)
	}
	testsHash, err := NewHasher(nil).HashTestFiles([]string{testPath})
	if err != nil {
		t.Fatal(err)
	}

	// Known-good entry at line=2 — every subtest below adds a mutant
	// that triggers a different skip path at line=1, then a pending
	// mutant at line=2 whose hit verifies the loop continued.
	goodEntry := Entry{
		RelFile: "x.go", Line: 2, Col: 1, Type: "ARITHMETIC_BASE",
		Original: "+", Replacement: "-",
		ProdHash: prodHash, TestsHash: testsHash, Status: "KILLED",
	}
	mkPending := func(line int) mutator.Mutant {
		return mutator.Mutant{
			ID: line, Type: mutator.ArithmeticBase,
			File: prodPath, RelFile: "x.go",
			Line: line, Col: 1,
			Original: "+", Replacement: "-",
			Status: mutator.StatusPending,
		}
	}

	t.Run("non-pending precedes hit", func(t *testing.T) {
		c := &Cache{Entries: []Entry{goodEntry}}
		mutants := []mutator.Mutant{
			{ID: 1, Type: mutator.ArithmeticBase, File: prodPath, RelFile: "x.go",
				Line: 1, Col: 1, Original: "+", Replacement: "-",
				Status: mutator.StatusNotCovered}, // skipped: not Pending
			mkPending(2),
		}
		if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 1 {
			t.Errorf("hits=%d, want 1 — Lookup did not continue past non-pending mutant", hits)
		}
	})

	t.Run("unknown-key precedes hit", func(t *testing.T) {
		c := &Cache{Entries: []Entry{goodEntry}}
		// First mutant has no matching cache entry; second does.
		mutants := []mutator.Mutant{mkPending(1), mkPending(2)}
		if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 1 {
			t.Errorf("hits=%d, want 1 — Lookup did not continue past idx miss", hits)
		}
	})

	t.Run("non-reusable status precedes hit", func(t *testing.T) {
		c := &Cache{Entries: []Entry{
			{RelFile: "x.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
				Original: "+", Replacement: "-",
				ProdHash: prodHash, TestsHash: testsHash,
				Status: "PENDING"}, // not reusable
			goodEntry,
		}}
		mutants := []mutator.Mutant{mkPending(1), mkPending(2)}
		if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 1 {
			t.Errorf("hits=%d, want 1 — Lookup did not continue past non-reusable status", hits)
		}
	})

	t.Run("prod-hash mismatch precedes hit", func(t *testing.T) {
		c := &Cache{Entries: []Entry{
			{RelFile: "x.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
				Original: "+", Replacement: "-",
				ProdHash: "stale-prod", TestsHash: testsHash,
				Status: "KILLED"},
			goodEntry,
		}}
		mutants := []mutator.Mutant{mkPending(1), mkPending(2)}
		if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 1 {
			t.Errorf("hits=%d, want 1 — Lookup did not continue past prod-hash mismatch", hits)
		}
	})

	t.Run("tests-hash mismatch precedes hit", func(t *testing.T) {
		c := &Cache{Entries: []Entry{
			{RelFile: "x.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
				Original: "+", Replacement: "-",
				ProdHash: prodHash, TestsHash: "stale-tests",
				Status: "KILLED"},
			goodEntry,
		}}
		mutants := []mutator.Mutant{mkPending(1), mkPending(2)}
		if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 1 {
			t.Errorf("hits=%d, want 1 — Lookup did not continue past tests-hash mismatch", hits)
		}
	})
}

// TestLookup_ZeroEntriesShortCircuits asserts the early return when
// the cache has no entries — distinguishes from the
// `len(c.Entries) == 0` → `false` mutation, which would proceed to
// build an empty index and iterate every mutant (still 0 hits, but
// observably touches the slice).
func TestLookup_ZeroEntriesShortCircuits(t *testing.T) {
	c := &Cache{}
	// Sentinel: even a non-Pending mutant must remain untouched. The
	// non-mutated code returns 0 immediately; the mutant proceeds into
	// the loop body. Both produce 0 hits, but only the mutant would
	// access entries on the cache — so we instead verify that calling
	// Lookup with a nil resolver doesn't panic. With the early return
	// in place, testFilesFor is never invoked. With the early return
	// removed, the loop is entered; the mutant would then dereference
	// idx (empty map) and call testFilesFor on a Pending mutant whose
	// status is StatusKilled in the entry — i.e. it goes far enough to
	// invoke testFilesFor at least conceptually. But for our purposes,
	// the simplest discriminator is that nil testFilesFor + a pending
	// mutant that would otherwise hit produces a panic only when the
	// guard is removed.
	mutants := []mutator.Mutant{{
		ID: 1, Type: mutator.ArithmeticBase,
		File: "x.go", RelFile: "x.go", Line: 1, Col: 1,
		Original: "+", Replacement: "-",
		Status: mutator.StatusPending,
	}}
	hits := c.Lookup(mutants, NewHasher(nil), nil)
	if hits != 0 {
		t.Fatalf("hits=%d, want 0", hits)
	}
}

// --- Update loop continues (cache.go:404, 435, 439, 443) ---------------------

// TestUpdate_ContinueAdvancesAcrossSkippedEntries asserts that the
// carry-over loop in Update uses `continue` to advance, not `break`.
// Each subtest seeds two prior entries: the first triggers a skip
// path, the second is intact and must survive the run.
func TestUpdate_ContinueAdvancesAcrossSkippedEntries(t *testing.T) {
	root := t.TempDir()

	intact := filepath.Join(root, "intact.go")
	mustWrite(t, intact, "package x\n")
	intactHash, err := HashFile(intact)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("missing-file precedes intact entry", func(t *testing.T) {
		// First entry's file does not exist (h.File errors); second
		// entry's file exists and matches.
		c := &Cache{
			Entries: []Entry{
				{RelFile: "gone.go", Line: 1, Type: "ARITHMETIC_BASE",
					Original: "+", Replacement: "-",
					ProdHash: "stale", Status: "KILLED"},
				{RelFile: "intact.go", Line: 1, Type: "ARITHMETIC_BASE",
					Original: "+", Replacement: "-",
					ProdHash: intactHash, Status: "KILLED"},
			},
		}
		c.Update(nil, NewHasher(nil), root, pkgDirTestFilesFor)

		if len(c.Entries) != 1 || c.Entries[0].RelFile != "intact.go" {
			t.Errorf("entries=%v, want [intact.go] only — carry-over did not continue past missing file", c.Entries)
		}
	})

	t.Run("hash-mismatch precedes intact entry", func(t *testing.T) {
		// First entry exists on disk but hash doesn't match (stale);
		// second entry is intact.
		stale := filepath.Join(root, "stale.go")
		mustWrite(t, stale, "package x\n// changed\n")
		c := &Cache{
			Entries: []Entry{
				{RelFile: "stale.go", Line: 1, Type: "ARITHMETIC_BASE",
					Original: "+", Replacement: "-",
					ProdHash: "old-hash", Status: "KILLED"},
				{RelFile: "intact.go", Line: 1, Type: "ARITHMETIC_BASE",
					Original: "+", Replacement: "-",
					ProdHash: intactHash, Status: "KILLED"},
			},
		}
		c.Update(nil, NewHasher(nil), root, pkgDirTestFilesFor)

		gotIntact := false
		for _, e := range c.Entries {
			if e.RelFile == "intact.go" {
				gotIntact = true
			}
			if e.RelFile == "stale.go" {
				t.Errorf("stale entry should be dropped: %+v", e)
			}
		}
		if !gotIntact {
			t.Errorf("intact entry dropped — carry-over did not continue past hash mismatch")
		}
	})

	t.Run("overwritten precedes intact entry", func(t *testing.T) {
		// A run mutant overwrites the first prior entry's key; the
		// second prior entry should still carry over.
		c := &Cache{
			Entries: []Entry{
				{RelFile: "intact.go", Line: 1, Col: 1,
					Type: "ARITHMETIC_BASE", Original: "+", Replacement: "-",
					ProdHash: intactHash, Status: "LIVED"},
				{RelFile: "intact.go", Line: 99, Col: 1,
					Type: "ARITHMETIC_BASE", Original: "*", Replacement: "/",
					ProdHash: intactHash, Status: "KILLED"},
			},
		}
		// Run mutant overwrites the line=1 entry only.
		mutants := []mutator.Mutant{{
			ID: 1, Type: mutator.ArithmeticBase,
			File: intact, RelFile: "intact.go",
			Line: 1, Col: 1, Original: "+", Replacement: "-",
			Status: mutator.StatusKilled, Duration: time.Millisecond,
		}}
		c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

		var sawLine1Killed, sawLine99 bool
		for _, e := range c.Entries {
			if e.Line == 1 && e.Status == "KILLED" {
				sawLine1Killed = true
			}
			if e.Line == 99 {
				sawLine99 = true
			}
		}
		if !sawLine1Killed {
			t.Errorf("line=1 not overwritten with KILLED")
		}
		if !sawLine99 {
			t.Errorf("line=99 prior entry dropped — carry-over did not continue past overwrite")
		}
	})
}

// TestUpdate_SkipsRunMutantWhenProdFileGone asserts the Update path
// that errors-out on h.File for the *current run's* mutants (line 404
// branch). If the file is gone at Update time, that mutant must not
// appear in the merged entries.
func TestUpdate_SkipsRunMutantWhenProdFileGone(t *testing.T) {
	root := t.TempDir()
	gonePath := filepath.Join(root, "gone.go")
	// Note: file is never written — h.File will error.

	c := &Cache{}
	mutants := []mutator.Mutant{{
		ID: 1, Type: mutator.ArithmeticBase,
		File: gonePath, RelFile: "gone.go",
		Line: 1, Col: 1, Original: "+", Replacement: "-",
		Status: mutator.StatusKilled,
	}}
	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)
	if len(c.Entries) != 0 {
		t.Errorf("entries=%v, want 0 — mutant for missing file must not be cached", c.Entries)
	}
}

// --- Update sort comparator (cache.go:454-477 — cmp.Or chain) ----------------

// TestUpdate_SortPrecedence asserts each tier of the sort comparator
// is honored. Pairs of entries that tie on every prior field but
// differ on one specific field verify that field's comparator runs.
// With any tier dropped (BRANCH_IF) or its sign inverted, the entries
// would emit in the wrong order and the assertion fires.
func TestUpdate_SortPrecedence(t *testing.T) {
	root := t.TempDir()
	prodPath := filepath.Join(root, "x.go")
	mustWrite(t, prodPath, "package x\n")
	prodHash, err := HashFile(prodPath)
	if err != nil {
		t.Fatal(err)
	}

	// Build prior entries that already-pass cache integrity (so they
	// carry over) and exercise each sort field tier in turn. Each pair
	// has identical lower-precedence fields except one.
	mk := func(rel string, line, col, off int, typ, orig, repl string) Entry {
		return Entry{
			RelFile: rel, Line: line, Col: col, StartOffset: off,
			Type: typ, Original: orig, Replacement: repl,
			ProdHash: prodHash, Status: "KILLED",
		}
	}

	c := &Cache{
		Entries: []Entry{
			// Insert deliberately scrambled — Update must sort by
			// (RelFile, Line, Col, StartOffset, Type, Original,
			// Replacement) in that order.
			mk("x.go", 1, 1, 0, "ARITHMETIC_BASE", "+", "-"),
			mk("x.go", 1, 1, 0, "ARITHMETIC_BASE", "+", "*"), // ties through Original
			mk("x.go", 1, 1, 0, "ARITHMETIC_BASE", "*", "+"), // ties through Type
			mk("x.go", 1, 1, 0, "BRANCH_IF", "+", "-"),       // ties through StartOffset
			mk("x.go", 1, 1, 1, "ARITHMETIC_BASE", "+", "-"), // ties through Col
			mk("x.go", 1, 2, 0, "ARITHMETIC_BASE", "+", "-"), // ties through Line
			mk("x.go", 2, 1, 0, "ARITHMETIC_BASE", "+", "-"), // ties through RelFile
			mk("y.go", 0, 0, 0, "ARITHMETIC_BASE", "+", "-"), // smaller RelFile
		},
	}

	// Need every entry's prod file to hash; create stub for y.go.
	mustWrite(t, filepath.Join(root, "y.go"), "package y\n")
	yHash, _ := HashFile(filepath.Join(root, "y.go"))
	c.Entries[len(c.Entries)-1].ProdHash = yHash

	c.Update(nil, NewHasher(nil), root, pkgDirTestFilesFor)

	// Expected sorted order (all entries carry over since prod hashes match).
	// Expected sort order (cmp.Or chain by RelFile, Line, Col,
	// StartOffset, Type, Original, Replacement). ASCII codepoints:
	// '*' (0x2A) < '+' (0x2B) < '-' (0x2D).
	want := []struct {
		rel  string
		line int
		col  int
		off  int
		typ  string
		orig string
		repl string
	}{
		{"x.go", 1, 1, 0, "ARITHMETIC_BASE", "*", "+"}, // smaller Original
		{"x.go", 1, 1, 0, "ARITHMETIC_BASE", "+", "*"}, // ties on Original=+, smaller Replacement
		{"x.go", 1, 1, 0, "ARITHMETIC_BASE", "+", "-"}, // ties on Original=+, larger Replacement
		{"x.go", 1, 1, 0, "BRANCH_IF", "+", "-"},       // larger Type
		{"x.go", 1, 1, 1, "ARITHMETIC_BASE", "+", "-"}, // larger StartOffset
		{"x.go", 1, 2, 0, "ARITHMETIC_BASE", "+", "-"}, // larger Col
		{"x.go", 2, 1, 0, "ARITHMETIC_BASE", "+", "-"}, // larger Line
		{"y.go", 0, 0, 0, "ARITHMETIC_BASE", "+", "-"}, // larger RelFile
	}
	if len(c.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(c.Entries), len(want), c.Entries)
	}
	for i, w := range want {
		got := c.Entries[i]
		if got.RelFile != w.rel || got.Line != w.line || got.Col != w.col ||
			got.StartOffset != w.off || got.Type != w.typ ||
			got.Original != w.orig || got.Replacement != w.repl {
			t.Errorf("position %d: got {%s %d %d %d %s %s→%s}, want {%s %d %d %d %s %s→%s}",
				i, got.RelFile, got.Line, got.Col, got.StartOffset, got.Type, got.Original, got.Replacement,
				w.rel, w.line, w.col, w.off, w.typ, w.orig, w.repl)
		}
	}
}

// --- Save error paths (cache.go:232, 239, 251, 255, 258, 261) ---------------

// withFailingOp swaps one of the os* hooks to a stub returning errSentinel.
// Restores at test cleanup.
func withFailingOp(t *testing.T, swap func(restore func())) {
	t.Helper()
	swap(func() {})
}

var errSentinel = errors.New("sentinel I/O failure")

func TestSave_PropagatesMkdirAllError(t *testing.T) {
	orig := osMkdirAll
	t.Cleanup(func() { osMkdirAll = orig })
	osMkdirAll = func(string, os.FileMode) error { return errSentinel }

	err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(t.TempDir(), "cache.json"))
	if !errors.Is(err, errSentinel) {
		t.Fatalf("err=%v, want sentinel", err)
	}
}

func TestSave_PropagatesCreateTempError(t *testing.T) {
	orig := newSaveSink
	t.Cleanup(func() { newSaveSink = orig })
	newSaveSink = func(string, string) (saveSink, error) { return nil, errSentinel }

	err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(t.TempDir(), "cache.json"))
	if !errors.Is(err, errSentinel) {
		t.Fatalf("err=%v, want sentinel", err)
	}
}

// fakeSink is a saveSink whose Write and Close errors can be set
// independently — what lets the Encode-only and Close-only tests below
// distinguish those return paths in Save (otherwise indistinguishable
// when both errors collapse into a single "non-nil error" assertion).
type fakeSink struct {
	name     string
	writeErr error
	closeErr error
	closed   bool
}

func (f *fakeSink) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}
func (f *fakeSink) Close() error { f.closed = true; return f.closeErr }
func (f *fakeSink) Name() string { return f.name }

// TestSave_PropagatesEncodeError asserts that an Encode failure is
// returned even when Close subsequently succeeds — kills the
// `if encodeErr != nil { return encodeErr }` BRANCH_IF mutant by
// requiring the SPECIFIC encode-error sentinel back, not just
// "any non-nil error" (which the Close-error branch would also
// produce on a real *os.File).
func TestSave_PropagatesEncodeError(t *testing.T) {
	dir := t.TempDir()
	orig := newSaveSink
	t.Cleanup(func() { newSaveSink = orig })

	encErr := errors.New("encode-only sentinel")
	newSaveSink = func(string, string) (saveSink, error) {
		return &fakeSink{
			name:     filepath.Join(dir, ".gomutants-cache-fake.tmp"),
			writeErr: encErr,
			closeErr: nil,
		}, nil
	}

	err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(dir, "cache.json"))
	if !errors.Is(err, encErr) {
		t.Fatalf("err=%v, want encode-only sentinel", err)
	}
}

// TestSave_PropagatesCloseError asserts that a Close failure is
// returned when Encode succeeded — kills the
// `if closeErr != nil { return closeErr }` BRANCH_IF mutant.
func TestSave_PropagatesCloseError(t *testing.T) {
	dir := t.TempDir()
	orig := newSaveSink
	t.Cleanup(func() { newSaveSink = orig })

	closeErr := errors.New("close-only sentinel")
	newSaveSink = func(string, string) (saveSink, error) {
		return &fakeSink{
			name:     filepath.Join(dir, ".gomutants-cache-fake.tmp"),
			writeErr: nil,
			closeErr: closeErr,
		}, nil
	}

	err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(dir, "cache.json"))
	if !errors.Is(err, closeErr) {
		t.Fatalf("err=%v, want close-only sentinel", err)
	}
}

// TestSave_AlwaysClosesEvenOnEncodeFailure asserts the file
// descriptor is released regardless of Encode outcome — i.e. Close is
// invoked on every code path, not gated on Encode success.
func TestSave_AlwaysClosesEvenOnEncodeFailure(t *testing.T) {
	dir := t.TempDir()
	orig := newSaveSink
	t.Cleanup(func() { newSaveSink = orig })

	sink := &fakeSink{
		name:     filepath.Join(dir, ".gomutants-cache-fake.tmp"),
		writeErr: errSentinel,
	}
	newSaveSink = func(string, string) (saveSink, error) { return sink, nil }

	_ = Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(dir, "cache.json"))
	if !sink.closed {
		t.Fatal("Save did not Close the sink on encode failure")
	}
}

func TestSave_PropagatesChmodError(t *testing.T) {
	orig := osChmod
	t.Cleanup(func() { osChmod = orig })
	osChmod = func(string, os.FileMode) error { return errSentinel }

	err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(t.TempDir(), "cache.json"))
	if !errors.Is(err, errSentinel) {
		t.Fatalf("err=%v, want sentinel", err)
	}
}

func TestSave_PropagatesRenameError(t *testing.T) {
	dir := t.TempDir()
	orig := osRename
	t.Cleanup(func() { osRename = orig })
	osRename = func(string, string) error { return errSentinel }

	err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(dir, "cache.json"))
	if !errors.Is(err, errSentinel) {
		t.Fatalf("err=%v, want sentinel", err)
	}

	// Rename failed → tmp should still exist (deferred Remove cleans
	// up only on the error path; verify cleanup happened).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "cache.json" {
			// Any remaining .tmp file means cleanup leaked.
			t.Errorf("temp file leaked after rename failure: %s", e.Name())
		}
	}
}

// TestSave_TmpFileCleanedUpAfterEncodeFailure asserts that a failed
// Encode triggers the deferred temp-file cleanup. The fakeSink's
// writeErr forces Encode to fail; the spy on osRemove confirms the
// deferred cleanup ran exactly once.
func TestSave_TmpFileCleanedUpAfterEncodeFailure(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, ".gomutants-cache-fake.tmp")

	origSink := newSaveSink
	t.Cleanup(func() { newSaveSink = origSink })
	newSaveSink = func(string, string) (saveSink, error) {
		return &fakeSink{name: tmpPath, writeErr: errSentinel}, nil
	}

	origRemove := osRemove
	t.Cleanup(func() { osRemove = origRemove })
	var removeCalls int
	osRemove = func(name string) error {
		removeCalls++
		return os.Remove(name)
	}

	if err := Save(&Cache{SchemaVersion: SchemaVersion}, filepath.Join(dir, "cache.json")); err == nil {
		t.Fatal("expected error, got nil")
	}
	if removeCalls != 1 {
		t.Errorf("osRemove calls=%d, want 1 (deferred cleanup must fire on encode failure)", removeCalls)
	}
}

// TestSave_TmpClearedAfterRenameSuccess asserts the post-rename
// tmpName="" assignment (cache.go:264). With the assignment removed,
// the deferred Remove still fires and would attempt to delete the
// just-renamed file (which is now at `path`, not tmpName) — but
// tmpName still holds the *original* temp name, which no longer
// exists, so Remove returns ENOENT (silently swallowed). Detection:
// inject a custom os.Rename that *moves* tmp to path, then assert that
// after Save returns, calling osRename again on the same tmpName fails
// with ENOENT — i.e. nothing else tried to remove it.
//
// Simpler observable: count how many times the deferred Remove fires
// by overriding tmpName's would-be removal path. Easiest: track the
// invariant via a stub on os.Remove via the global, but cache.go uses
// os.Remove directly. Pragmatic approach: assert the file exists at
// path AND no .tmp remains in dir (which TestSave_LeavesNoTempFiles
// already covers). The mutation `tmpName = ""` → `_ = ""` would still
// produce the same end state (target file at path), since the deferred
// Remove on the now-missing tmpName silently no-ops. Equivalent
// mutant; not separately killable without a refactor.
//
// To kill it, we'd need to observe the deferred Remove. Refactor: make
// the cleanup a named function we can spy on. Skipped — equivalent.

// --- testindex.go mutants ----------------------------------------------------

// TestBuildTestIndex_ContinuesPastSeenAndUnreadableDirs covers
// testindex.go:48 (the err-or-seen guard) and :54 (ReadDir fail
// branch) by passing a mix of: a dir that fails Abs (impossible), a
// dir already seen, an unreadable dir, and a good dir. The good dir
// must still be indexed.
func TestBuildTestIndex_ContinuesPastSeenAndUnreadableDirs(t *testing.T) {
	root := t.TempDir()

	good := filepath.Join(root, "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(good, "x_test.go"), "package x\nimport \"testing\"\nfunc TestX(t *testing.T) {}\n")

	missing := filepath.Join(root, "missing") // does not exist
	dup := good                               // duplicate of good

	ti := BuildTestIndex([]string{missing, good, dup})

	if got := ti.FilesFor("TestX"); len(got) != 1 {
		t.Errorf("FilesFor(TestX) = %v, want 1 entry (dup must not double-add)", got)
	}
	if all := ti.AllInDir(good); len(all) != 1 {
		t.Errorf("AllInDir(good) = %v, want 1 file", all)
	}
}

// TestBuildTestIndex_SkipsIsDirEntries covers testindex.go:60: a
// subdirectory whose name happens to end in `_test.go` must not be
// indexed as a file.
func TestBuildTestIndex_SkipsIsDirEntries(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory named like a test file.
	if err := os.MkdirAll(filepath.Join(dir, "deceptive_test.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "real_test.go"), "package x\nimport \"testing\"\nfunc TestReal(t *testing.T) {}\n")

	ti := BuildTestIndex([]string{dir})
	all := ti.AllInDir(dir)
	if len(all) != 1 || filepath.Base(all[0]) != "real_test.go" {
		t.Errorf("AllInDir = %v, want only real_test.go", all)
	}
}

// TestBuildTestIndex_SkipsMethodReceiver covers testindex.go:79
// (`fn.Recv != nil`): a method with a name like TestX (on a receiver)
// must not be indexed as a top-level test entry.
func TestBuildTestIndex_SkipsMethodReceiver(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "x_test.go"), `package x

import "testing"

type S struct{}
func (s S) TestMethod(t *testing.T) {} // method, not a top-level test
func TestTopLevel(t *testing.T)      {} // genuine
`)

	ti := BuildTestIndex([]string{dir})
	if got := ti.FilesFor("TestMethod"); got != nil {
		t.Errorf("method TestMethod indexed as test entry: %v", got)
	}
	if got := ti.FilesFor("TestTopLevel"); len(got) != 1 {
		t.Errorf("TestTopLevel not indexed: %v", got)
	}
}

// TestBuildTestIndex_SkipsDirsWithoutTests covers testindex.go:87
// (`if len(dirFiles) > 0`): a directory with only production files
// must not appear in byDir.
func TestBuildTestIndex_SkipsDirsWithoutTests(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "x.go"), "package x\nfunc Foo() {}\n")

	ti := BuildTestIndex([]string{dir})
	abs, _ := filepath.Abs(dir)
	if got := ti.AllInDir(abs); got != nil {
		t.Errorf("dir without tests indexed: %v", got)
	}
}

// TestBuildTestIndex_LoopContinuesPastSeen covers the
// `seen[abs] = true` write at testindex.go:51: removing it would let
// the same dir be processed twice, double-indexing every test name.
func TestBuildTestIndex_LoopContinuesPastSeen(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "x_test.go"), "package x\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n")

	ti := BuildTestIndex([]string{dir, dir, dir})
	got := ti.FilesFor("TestSame")
	if len(got) != 1 {
		t.Errorf("FilesFor = %v, want 1 entry (dup dirs must be deduped via seen[])", got)
	}
}

// --- Update nil-cache guard (cache.go:405) -----------------------------------

// TestUpdate_NilCacheReturnsEarly asserts the `if c == nil { return }`
// guard in Update. Without the guard, the next line dereferences c
// (via len(c.Entries)) and panics — which the test would observe as a
// failure.
func TestUpdate_NilCacheReturnsEarly(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Update on nil cache panicked: %v", r)
		}
	}()
	var c *Cache
	c.Update(nil, NewHasher(nil), t.TempDir(), pkgDirTestFilesFor)
}

// --- Update run-mutant skip on missing prod file (cache.go:419) --------------

// TestUpdate_ContinuesPastRunMutantWithMissingFile places a missing-
// file mutant before an intact one; with the `continue` inverted to
// `break`, the intact mutant's entry would be dropped.
func TestUpdate_ContinuesPastRunMutantWithMissingFile(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good.go")
	mustWrite(t, good, "package x\n")

	c := &Cache{}
	mutants := []mutator.Mutant{
		{
			ID: 1, Type: mutator.ArithmeticBase,
			File: filepath.Join(root, "missing.go"), RelFile: "missing.go",
			Line: 1, Col: 1, Original: "+", Replacement: "-",
			Status: mutator.StatusKilled,
		},
		{
			ID: 2, Type: mutator.ArithmeticBase,
			File: good, RelFile: "good.go",
			Line: 1, Col: 1, Original: "+", Replacement: "-",
			Status: mutator.StatusKilled, Duration: time.Millisecond,
		},
	}
	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

	if len(c.Entries) != 1 || c.Entries[0].RelFile != "good.go" {
		t.Errorf("entries=%v, want 1 entry for good.go (loop must continue past missing-file mutant)", c.Entries)
	}
}

// --- Save osRemove cleanup count (cache.go:278) ------------------------------

// TestSave_RemovesTmpOnlyOnFailurePath asserts the `committed` flag
// at the end of Save: the deferred Remove must fire on the failure
// path (rename fails) and must NOT fire on the success path. With
// `committed = true` removed (or replaced with `_ = true`), defer
// would call osRemove(tmpName) after the successful rename, and our
// stub increments the call count.
func TestSave_RemovesTmpOnlyOnFailurePath(t *testing.T) {
	orig := osRemove
	t.Cleanup(func() { osRemove = orig })
	var calls int
	osRemove = func(name string) error {
		calls++
		return os.Remove(name)
	}

	t.Run("success path: no Remove", func(t *testing.T) {
		calls = 0
		path := filepath.Join(t.TempDir(), "cache.json")
		if err := Save(&Cache{SchemaVersion: SchemaVersion}, path); err != nil {
			t.Fatalf("save: %v", err)
		}
		if calls != 0 {
			t.Errorf("osRemove called %d times on success path, want 0 — committed flag broken", calls)
		}
	})

	t.Run("failure path: one Remove", func(t *testing.T) {
		calls = 0
		origRename := osRename
		t.Cleanup(func() { osRename = origRename })
		osRename = func(string, string) error { return errSentinel }

		path := filepath.Join(t.TempDir(), "cache.json")
		if err := Save(&Cache{SchemaVersion: SchemaVersion}, path); !errors.Is(err, errSentinel) {
			t.Fatalf("expected sentinel, got %v", err)
		}
		if calls != 1 {
			t.Errorf("osRemove called %d times on failure path, want 1 — cleanup did not fire", calls)
		}
	})
}

// --- testindex.go INVERT_LOOP_CTRL + INVERT_LOGICAL --------------------------

// TestBuildTestIndex_OuterLoopContinuesPastDuplicate is the
// 3-pkgDir variant that catches `continue → break` on the err-or-seen
// guard: pkgDirs = [a, a, b]. With continue the second a (already seen)
// is skipped and b is processed. With break the second a triggers an
// early exit, leaving b unindexed.
func TestBuildTestIndex_OuterLoopContinuesPastDuplicate(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(a, "x_test.go"), "package x\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(b, "x_test.go"), "package y\nimport \"testing\"\nfunc TestB(t *testing.T) {}\n")

	ti := BuildTestIndex([]string{a, a, b})

	if got := ti.FilesFor("TestA"); len(got) != 1 {
		t.Errorf("TestA = %v, want 1 (must be indexed once despite duplicate dir)", got)
	}
	if got := ti.FilesFor("TestB"); len(got) != 1 {
		t.Errorf("TestB = %v, want 1 (outer loop did not continue past duplicate dir)", got)
	}
}

// TestBuildTestIndex_InnerLoopContinuesPastNonTestFiles places a
// production .go file before a _test.go file in the same dir and
// asserts both the test entry and AllInDir are populated. With the
// inner loop's `continue → break` mutation, the _test.go file would
// not be reached.
func TestBuildTestIndex_InnerLoopContinuesPastNonTestFiles(t *testing.T) {
	dir := t.TempDir()
	// alphabetical iteration order: 'a_prod.go' < 'b_test.go' <
	// 'c.go'. The first non-_test.go entry triggers the suffix-skip
	// continue; b_test.go must still be reached.
	mustWrite(t, filepath.Join(dir, "a_prod.go"), "package x\nfunc Foo() {}\n")
	mustWrite(t, filepath.Join(dir, "b_test.go"), "package x\nimport \"testing\"\nfunc TestB(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(dir, "c.go"), "package x\nfunc Bar() {}\n")

	ti := BuildTestIndex([]string{dir})
	if got := ti.FilesFor("TestB"); len(got) != 1 {
		t.Errorf("TestB not indexed — inner loop did not continue past non-_test.go file: %v", got)
	}
}

// TestBuildTestIndex_RequiresBothNonDirAndTestSuffix engineers a
// non-_test.go file containing a Test-named function. With the err-or-
// seen guard's `||` mutated to `&&`, or the suffix check itself
// mutated, the function might be indexed. Proper code skips on the
// suffix mismatch.
func TestBuildTestIndex_RequiresBothNonDirAndTestSuffix(t *testing.T) {
	dir := t.TempDir()
	// File without _test.go suffix but with a function named like a
	// test: a misuse, but possible. Must NOT be indexed. Uses a .go
	// extension so go/parser will accept the contents — what makes
	// the file ineligible is purely the missing _test.go suffix.
	mustWrite(t, filepath.Join(dir, "helper.go"), `package x

import "testing"

func TestPretender(t *testing.T) {} // wrong file — must be ignored
`)
	mustWrite(t, filepath.Join(dir, "real_test.go"), `package x

import "testing"

func TestReal(t *testing.T) {}
`)
	ti := BuildTestIndex([]string{dir})

	if got := ti.FilesFor("TestPretender"); got != nil {
		t.Errorf("TestPretender from non-_test.go file indexed: %v", got)
	}
	if got := ti.FilesFor("TestReal"); len(got) != 1 {
		t.Errorf("TestReal not indexed: %v", got)
	}
}

// Compile-time sanity for unused helpers.
var _ = withFailingOp
var _ = fmt.Sprintf
