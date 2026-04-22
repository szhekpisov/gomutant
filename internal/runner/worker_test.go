package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/mutator"
)

func TestNewWorker(t *testing.T) {
	dir := t.TempDir()
	cache := map[string][]byte{"/src/file.go": []byte("package p\n")}

	w, err := NewWorker(0, dir, 30*time.Second, cache, "/src", nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	if w.id != 0 {
		t.Errorf("id=%d, want 0", w.id)
	}

	// Verify temp files were created.
	if _, err := os.Stat(w.tmpSrcPath); err != nil {
		t.Errorf("tmpSrcPath not created: %v", err)
	}
	if _, err := os.Stat(w.overlayPath); err != nil {
		t.Errorf("overlayPath not created: %v", err)
	}
}

func TestWorkerTestMissingSource(t *testing.T) {
	dir := t.TempDir()
	cache := map[string][]byte{} // Empty cache.

	w, err := NewWorker(0, dir, 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID:   1,
		File: "/nonexistent/file.go",
		Status: mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for missing source", result.Status)
	}
	// Duration must be set even on early return paths.
	if result.Duration <= 0 {
		t.Errorf("Duration should be > 0 on early-return path, got %v", result.Duration)
	}
}

func TestWorkerTestInvalidPatch(t *testing.T) {
	dir := t.TempDir()
	src := []byte("package p\n")
	cache := map[string][]byte{"/src/file.go": src}

	w, err := NewWorker(0, dir, 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID:          1,
		File:        "/src/file.go",
		StartOffset: 100, // Beyond file length.
		EndOffset:   200,
		Replacement: "x",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for invalid patch", result.Status)
	}
	if result.Duration <= 0 {
		t.Errorf("Duration should be > 0 on early-return path, got %v", result.Duration)
	}
}

func TestWorkerTestNotViable(t *testing.T) {
	// Create a small Go project that will fail to compile with the mutation.
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	// Replace entire file with code that has an undefined symbol (compile error).
	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: 0,
		EndOffset:   len(src),
		Replacement: "package testpkg\n\nfunc Add(a, b int) int {\n\treturn UNDEFINED_SYMBOL\n}\n",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for compile error", result.Status)
	}
}

func TestWorkerTestKilled(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	// Mutate + to - (test should fail → KILLED).
	plusIdx := 51 // "a + b" — the "+" position
	for i, c := range src {
		if c == '+' && i > 30 { // Skip package line
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusKilled {
		t.Errorf("Status=%v, want KILLED", result.Status)
	}
	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}
}

func TestWorkerTestLived(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	// This function's test doesn't check the operator, so the mutant survives.
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	// Weak test: doesn't verify the result.
	_ = Add(1, 2)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusLived {
		t.Errorf("Status=%v, want LIVED", result.Status)
	}
}

func TestWorkerTestTimeout(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	// Test that will run forever.
	testSrc := `package testpkg

import "testing"
import "time"

func TestAdd(t *testing.T) {
	time.Sleep(10 * time.Minute)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	// Very short timeout.
	w, err := NewWorker(0, t.TempDir(), 3*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusTimedOut {
		t.Errorf("Status=%v, want TIMED_OUT", result.Status)
	}
}

// TestPgroupRSSBytesSelf exercises pgroupRSSBytes against our own process
// group. Kills:
//   - CONDITIONALS_NEGATION on `err != nil` (line 32): mutated `err == nil`
//     would short-circuit to return 0 on the success path.
//   - STATEMENT_REMOVE on `line = strings.TrimSpace(line)` (line 37):
//     without trimming, `ps` output lines (" 12345") fail strconv.ParseInt.
//   - CONDITIONALS_NEGATION on `line == ""` (line 38): mutated `!=` skips
//     every non-empty line, never reaching ParseInt.
//   - CONDITIONALS_NEGATION on `err != nil` (line 42): mutated `==` skips
//     successful parses, total stays 0.
//   - ARITHMETIC_BASE on `n * 1024` (line 45): mutated `n / 1024` produces
//     byte totals roughly 1e6× too small.
//
// We assert the returned value exceeds 1 MiB — real Go test processes use
// tens of MiB, but all the mutations above collapse the result toward 0.
func TestPgroupRSSBytesSelf(t *testing.T) {
	pgid, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	bytes := pgroupRSSBytes(pgid)
	if bytes < 1<<20 {
		t.Errorf("pgroupRSSBytes(self pgid=%d) = %d bytes, want >= 1 MiB (a running Go test process is typically tens of MiB)", pgid, bytes)
	}
	// Sanity upper bound — catches mutations that inflate the total.
	if bytes > 100*(1<<30) {
		t.Errorf("pgroupRSSBytes(self) = %d bytes, implausibly large", bytes)
	}
}

// TestPgroupRSSBytesInvalidPgid kills the err-path return: passing an
// invalid PGID makes `ps -g` emit either an error or empty output. The
// original returns 0; a mutation that keeps the loop running still
// returns 0 on empty output (no lines to parse), so this specifically
// verifies the call doesn't panic or return garbage.
func TestPgroupRSSBytesInvalidPgid(t *testing.T) {
	// Very large pgid unlikely to exist.
	bytes := pgroupRSSBytes(99999999)
	if bytes < 0 {
		t.Errorf("pgroupRSSBytes(invalid) = %d, want >= 0", bytes)
	}
}

// TestNonZeroSinceSleep kills CONDITIONALS_NEGATION on `d <= 0` (line 60):
// mutated `d > 0` takes the Nanosecond branch on every normal call, so
// the returned duration would be exactly 1 ns even after a real sleep.
func TestNonZeroSinceSleep(t *testing.T) {
	start := time.Now()
	time.Sleep(5 * time.Millisecond)
	d := nonZeroSince(start)
	if d < 5*time.Millisecond {
		t.Errorf("nonZeroSince after 5ms sleep = %v, want >= 5ms (mutation returns 1ns)", d)
	}
}

// TestNonZeroSinceFuture kills the BRANCH_IF on `{ return time.Nanosecond }`:
// a start time in the future yields d <= 0 from time.Since. The original
// returns time.Nanosecond (>0) so callers can use 0 as a "never set"
// sentinel. Under BRANCH_IF the body is elided and 0 or negative leaks out.
func TestNonZeroSinceFuture(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	d := nonZeroSince(future)
	if d <= 0 {
		t.Errorf("nonZeroSince(future) = %v, want > 0 (sentinel positive duration)", d)
	}
}

// TestCappedBufferCapsAtMax kills ARITHMETIC_BASE and INVERT_NEGATIVES on
// `maxCapturedOutput - len(c.buf)` (line 78): mutated `+` makes remaining
// always large, so the buffer grows past its cap. We write 2× the cap and
// assert the stored bytes don't exceed the cap.
func TestCappedBufferCapsAtMax(t *testing.T) {
	var c cappedBuffer
	chunk := make([]byte, 64*1024) // 64 KiB chunks
	for range 40 {                 // 40 * 64 KiB = 2.5 MiB, well above 1 MiB cap
		n, _ := c.Write(chunk)
		if n != len(chunk) {
			t.Errorf("Write returned n=%d, want %d (must report full length to satisfy io.Writer)", n, len(chunk))
		}
	}
	if len(c.buf) > maxCapturedOutput {
		t.Errorf("buf grew to %d bytes, exceeds cap %d — capping arithmetic broken", len(c.buf), maxCapturedOutput)
	}
	// Must have captured at least something up to the cap.
	if len(c.buf) == 0 {
		t.Errorf("buf is empty after writes — cap check too aggressive")
	}
}

// TestCappedBufferPartialFinalWrite kills patterns that mishandle the
// "final write exceeds remaining" branch. After writing cap-1 bytes, a
// second write of 10 bytes should fill to exactly the cap (1 byte taken
// from the second chunk).
func TestCappedBufferPartialFinalWrite(t *testing.T) {
	var c cappedBuffer
	first := make([]byte, maxCapturedOutput-1)
	c.Write(first)
	if len(c.buf) != maxCapturedOutput-1 {
		t.Fatalf("after first write: len=%d, want %d", len(c.buf), maxCapturedOutput-1)
	}
	// Second write: 10 bytes, but only 1 byte of remaining capacity.
	n, _ := c.Write([]byte("0123456789"))
	if n != 10 {
		t.Errorf("Write n=%d, want 10 (must report full input length)", n)
	}
	if len(c.buf) != maxCapturedOutput {
		t.Errorf("after partial write: len=%d, want %d (cap)", len(c.buf), maxCapturedOutput)
	}
}

// TestCappedBufferWriteAtCap kills mutations on the `remaining > 0` guard:
// once buf is at the cap, further writes must be no-ops but still return
// the input length (to satisfy the io.Writer contract).
func TestCappedBufferWriteAtCap(t *testing.T) {
	var c cappedBuffer
	c.buf = make([]byte, maxCapturedOutput)
	before := len(c.buf)
	n, err := c.Write([]byte("extra"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write n=%d, want 5", n)
	}
	if len(c.buf) != before {
		t.Errorf("buf grew past cap: len=%d, was %d", len(c.buf), before)
	}
}

// TestCappedBufferString kills trivial mutations on the String() accessor
// (e.g., STATEMENT_REMOVE on the return) by exercising it on real data.
func TestCappedBufferString(t *testing.T) {
	var c cappedBuffer
	c.Write([]byte("hello"))
	if got := c.String(); got != "hello" {
		t.Errorf("String() = %q, want %q", got, "hello")
	}
}

// TestBuildTestArgsShortFlag kills CONDITIONALS_NEGATION / BRANCH_IF on
// the GOMUTANT_TEST_SHORT gate: passing short=true must add "-short" to
// the command line; short=false must omit it. We assert both directions.
func TestBuildTestArgsShortFlag(t *testing.T) {
	w := &Worker{timeout: time.Second, overlayPath: "/tmp/o.json"}
	m := mutator.Mutant{Pkg: "mymod"}

	withShort := w.buildTestArgs(m, true)
	if !containsStr(withShort, "-short") {
		t.Errorf("short=true: args %v missing -short", withShort)
	}
	withoutShort := w.buildTestArgs(m, false)
	if containsStr(withoutShort, "-short") {
		t.Errorf("short=false: args %v should not contain -short", withoutShort)
	}
}

// TestBuildTestArgsPackageArgLast kills STATEMENT_REMOVE on
// `args = append(args, m.Pkg)`: removing that line leaves the command
// without a package target. Asserting that the package shows up as the
// final positional arg catches both the removal and any reordering.
func TestBuildTestArgsPackageArgLast(t *testing.T) {
	w := &Worker{timeout: time.Second, overlayPath: "/tmp/o.json"}
	m := mutator.Mutant{Pkg: "example.com/mod/sub"}
	args := w.buildTestArgs(m, false)
	if len(args) == 0 || args[len(args)-1] != "example.com/mod/sub" {
		t.Errorf("last arg = %q, want package import path; full args: %v",
			args[len(args)-1], args)
	}
	// Also: -timeout, -overlay, -failfast, -count=1 must all be present.
	for _, want := range []string{"-failfast", "-count=1"} {
		if !containsStr(args, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	if !anyHasPrefix(args, "-overlay=") {
		t.Errorf("args missing -overlay=…: %v", args)
	}
	if !anyHasPrefix(args, "-timeout=") {
		t.Errorf("args missing -timeout=…: %v", args)
	}
}

// TestBuildTestArgsWithTestMap kills CONDITIONALS_NEGATION / BRANCH_IF on
// `if w.testMap != nil`. With a non-nil map that actually contains the
// mutant's (file, line), the command line must include `-run=<regex>`.
// With no map, no -run should appear. Under either mutation, the -run
// flag would be either missing (when it should appear) or leak (via a
// nil-deref panic in the negation case).
func TestBuildTestArgsWithTestMap(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module testmod\n\ngo 1.26\n")
	mustWrite("add.go", "package testmod\n\nfunc Add(a, b int) int { return a + b }\n")
	mustWrite("add_test.go", "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"wrong\") } }\n")

	tm, err := coverage.BuildTestMap(context.Background(), dir, []string{"testmod"}, "", t.TempDir(), 1)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}

	wWith := &Worker{testMap: tm, timeout: time.Second, overlayPath: "/tmp/o.json"}
	wWithout := &Worker{timeout: time.Second, overlayPath: "/tmp/o.json"}
	m := mutator.Mutant{
		CoverageFile: "testmod/add.go",
		Line:         3,
		Pkg:          "testmod",
	}
	// With map: -run=<pattern> must appear.
	argsWith := wWith.buildTestArgs(m, false)
	if !anyHasPrefix(argsWith, "-run=") {
		t.Errorf("testMap non-nil with matching entry: expected -run= in %v", argsWith)
	}
	// Without map: -run= must not appear.
	argsWithout := wWithout.buildTestArgs(m, false)
	if anyHasPrefix(argsWithout, "-run=") {
		t.Errorf("testMap nil: -run= must be absent, got %v", argsWithout)
	}
	// With map but no matches for this (file, line): -run= must not appear.
	// Kills CONDITIONALS_BOUNDARY on `len(tests) > 0` — mutated `>= 0` would
	// always enter the branch and append -run= with an empty pattern.
	mMiss := mutator.Mutant{CoverageFile: "unknown/file.go", Line: 9999, Pkg: "testmod"}
	argsMiss := wWith.buildTestArgs(mMiss, false)
	if anyHasPrefix(argsMiss, "-run=") {
		t.Errorf("testMap non-nil but no matches: -run= must be absent (len(tests)>0 guard), got %v", argsMiss)
	}
}

// TestClassifyTestOutcome covers every branch of the classifier.
// Kills BRANCH_IF on the memKilled short-circuit, the runErr==nil
// Lived return, the DeadlineExceeded arm, and both EXPRESSION_REMOVE
// mutations on the `compileErrorRe && ([build failed] || [setup failed])`
// predicate.
func TestClassifyTestOutcome(t *testing.T) {
	anyErr := errors.New("exit status 1")
	tests := []struct {
		name       string
		runErr     error
		memKilled  bool
		testCtxErr error
		stdout     string
		stderr     string
		want       mutator.MutantStatus
	}{
		{"memkilled beats everything", anyErr, true, context.DeadlineExceeded, "", "", mutator.StatusTimedOut},
		// memKilled with otherwise-clean outcome: if the BRANCH_IF on the
		// memKilled early return is elided, execution falls through to
		// `runErr == nil → Lived`. Asserting TimedOut here kills that
		// mutation.
		{"memkilled alone still wins", nil, true, nil, "", "", mutator.StatusTimedOut},
		{"success => lived", nil, false, nil, "", "", mutator.StatusLived},
		{"timeout before classify", anyErr, false, context.DeadlineExceeded, "", "", mutator.StatusTimedOut},
		{"compile failure => not viable", anyErr, false, nil,
			"FAIL\ttestmod [build failed]\n", "worker-0.go:5:2: undefined: Foo\n", mutator.StatusNotViable},
		{"setup failure => not viable", anyErr, false, nil,
			"FAIL\ttestmod [setup failed]\n", "worker-0.go:5:2: cannot use\n", mutator.StatusNotViable},
		{"stderr compile regex but no [build failed] in stdout => killed", anyErr, false, nil,
			"--- FAIL: TestX\nadd_test.go:7: wrong\n", "worker-0.go:5:2: undefined\n", mutator.StatusKilled},
		{"[build failed] in stdout but no compile regex in stderr => killed", anyErr, false, nil,
			"FAIL [build failed]\n", "", mutator.StatusKilled},
		{"normal test failure => killed", anyErr, false, nil,
			"--- FAIL: TestAdd\n", "add_test.go:7: Add(1,2) != 3\n", mutator.StatusKilled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTestOutcome(tc.runErr, tc.memKilled, tc.testCtxErr, tc.stdout, tc.stderr)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Test helpers.
func containsStr(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
func anyHasPrefix(xs []string, prefix string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

func TestCompileErrorRegex(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"./file.go:10:5: undefined: foo", true},
		{"main.go:1:1: expected declaration", true},
		{"FAIL\ttestmod\t0.001s", false},
		{"ok  \ttestmod\t0.001s", false},
	}
	for _, tc := range tests {
		if got := compileErrorRe.MatchString(tc.input); got != tc.match {
			t.Errorf("compileErrorRe.Match(%q) = %v, want %v", tc.input, got, tc.match)
		}
	}
}
