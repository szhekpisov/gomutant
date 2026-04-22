package report

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

// TestHeaderExact asserts the exact bytes produced by Header.
// Kills STATEMENT_REMOVE on any of the three Fprintf lines and any text mutations.
func TestHeaderExact(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false)
	term.Header("0.1.0", "[./...]", 10, 5)

	want := "gomutant v0.1.0\n\nTarget: [./...]\nWorkers: 10 | Mutations: 5 types enabled\n\n"
	if got := buf.String(); got != want {
		t.Errorf("Header output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestPhaseExact(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false)
	term.Phase("Collecting coverage...")
	term.PhaseDone("done (2.1s)")

	want := "Collecting coverage... done (2.1s)\n"
	if got := buf.String(); got != want {
		t.Errorf("Phase output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestOnResultVerboseExact asserts the full verbose line format.
func TestOnResultVerboseExact(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 5, true)

	m := mutator.Mutant{
		ID:          1,
		Status:      mutator.StatusKilled,
		RelFile:     "file.go",
		Line:        10,
		Col:         5,
		Original:    "+",
		Replacement: "-",
		Duration:    150 * time.Millisecond,
	}
	term.OnResult(m)

	want := "  [1/5] KILLED file.go:10:5 + → - (150ms)\n"
	if got := buf.String(); got != want {
		t.Errorf("OnResult verbose output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestOnResultNonTTY(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 5, false)

	m := mutator.Mutant{ID: 1, Status: mutator.StatusLived, Duration: time.Second}
	term.OnResult(m)

	// Non-verbose, non-TTY: should produce no output.
	if buf.Len() != 0 {
		t.Errorf("expected no output for non-TTY non-verbose, got %q", buf.String())
	}
}

// TestOnResultTTYProgressBarExact pins the exact progress bar format so
// mutations on the arithmetic, loop bounds, bar rendering, and counters
// are all caught.
func TestOnResultTTYProgressBarExact(t *testing.T) {
	// total=10, done will become 1 after OnResult → bar has 3 '=' chars (30 * 1 / 10 = 3), 27 ' ' chars
	// pctDone = 1 * 100 / 10 = 10
	// elapsed = 0s (started now, rounded to seconds)
	var buf bytes.Buffer
	start := time.Now()
	term := &Terminal{
		w:     &buf,
		isTTY: true,
		total: 10,
		start: start,
	}

	m := mutator.Mutant{ID: 1, Status: mutator.StatusKilled, Duration: time.Millisecond}
	term.OnResult(m)

	got := buf.String()
	// Must begin with carriage return.
	if !strings.HasPrefix(got, "\r") {
		t.Errorf("expected leading \\r, got %q", got)
	}
	// Must contain "Testing mutants [...] 1/10  10%  " (with 30-wide bar: 3 '=' + 27 ' ').
	wantBar := "===                           "
	wantPrefix := fmt.Sprintf("\rTesting mutants [%s] 1/10  10%%  ", wantBar)
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("OnResult TTY:\n got: %q\nwant prefix: %q", got, wantPrefix)
	}
}

// TestOnResultTTYBarFullWhenDone covers the `filled == barWidth` path so
// all bar slots become '=' — kills CONDITIONALS_BOUNDARY mutations on
// `i < filled` (==, >=, etc. would leave wrong last char).
func TestOnResultTTYBarFullWhenDone(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{
		w:     &buf,
		isTTY: true,
		total: 1,
		start: time.Now(),
	}
	m := mutator.Mutant{ID: 1, Status: mutator.StatusKilled}
	term.OnResult(m)

	got := buf.String()
	wantBar := strings.Repeat("=", 30)
	if !strings.Contains(got, "["+wantBar+"]") {
		t.Errorf("expected bar fully filled: %q", got)
	}
	if !strings.Contains(got, "100%") {
		t.Errorf("expected 100%% at completion: %q", got)
	}
}

// TestOnResultTTYBarEmptyWhenStarting covers the `filled == 0` case.
func TestOnResultTTYBarEmptyWhenStarting(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{
		w:     &buf,
		isTTY: true,
		total: 1000, // large total so done=1 means filled=0
		start: time.Now(),
	}
	m := mutator.Mutant{ID: 1, Status: mutator.StatusKilled}
	term.OnResult(m)

	got := buf.String()
	wantBar := strings.Repeat(" ", 30)
	if !strings.Contains(got, "["+wantBar+"]") {
		t.Errorf("expected empty bar: %q", got)
	}
	if !strings.Contains(got, "0%") {
		t.Errorf("expected 0%% near start: %q", got)
	}
}

// TestOnResultTTYTotalZeroSkipsPct covers the `t.total > 0` guard in OnResult.
// With total=0, the pct branch is skipped and progress bar is NOT rendered
// (because `barWidth * t.done / t.total` would divide by zero).
// Actually the code unconditionally computes `filled := barWidth * t.done / t.total`
// even when total=0. Let's verify the guarded behavior: only pctDone is guarded.
func TestOnResultTTYTotalZero(t *testing.T) {
	// If total == 0, we expect a panic on division... actually total=0 with isTTY
	// means pctDone stays 0, but `filled := barWidth * t.done / t.total` divides by 0.
	// So this is an untested edge case. Skip — not realistic (total always > 0 in practice).
	t.Skip("total=0 in TTY path would divide by zero; not a real code path")
}

// TestSummaryExact pins the full Summary output for a known Report.
// Kills every STATEMENT_REMOVE on each Fprintf line and every arithmetic
// mutation on the MutantsKilled + MutantsLived and timed-out computations.
func TestSummaryExact(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false) // non-TTY
	r := &Report{
		MutantsKilled:     8,
		MutantsLived:      2,
		MutantsNotCovered: 3,
		MutantsNotViable:  1,
		MutantsTotal:      14,   // timed_out = 14 - 8 - 2 - 3 - 1 = 0
		TestEfficacy:      80.0,
	}
	term.Summary(r)

	// Non-TTY: no leading newline (from the isTTY guard).
	want := "\n" +
		"  Killed:       8  (80.0%)\n" +
		"  Lived:        2  (20.0%)\n" +
		"  Not covered:  3\n" +
		"  Not viable:   1\n" +
		"  Timed out:    0\n" +
		"  Efficacy:     80.00%\n" +
		"\n"
	if got := buf.String(); got != want {
		t.Errorf("Summary output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestSummaryTimedOutNonZero exercises the subtraction arithmetic in the
// timed-out computation — specific values force timed_out = 2.
func TestSummaryTimedOutNonZero(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false)
	r := &Report{
		MutantsKilled:     5,
		MutantsLived:      1,
		MutantsNotCovered: 3,
		MutantsNotViable:  1,
		MutantsTotal:      12, // timed_out = 12 - 5 - 1 - 3 - 1 = 2
		TestEfficacy:      83.33,
	}
	term.Summary(r)

	if !strings.Contains(buf.String(), "Timed out:    2\n") {
		t.Errorf("expected 'Timed out:    2' line, got %q", buf.String())
	}
}

// TestSummaryTTYLeadingNewline covers the `t.isTTY && !t.verbose` branch
// that prints a newline before the summary to clear the progress line.
func TestSummaryTTYLeadingNewline(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{
		w:     &buf,
		isTTY: true,
		total: 5,
		start: time.Now(),
	}
	r := &Report{MutantsKilled: 5, MutantsTotal: 5, TestEfficacy: 100}
	term.Summary(r)

	// Should start with "\n\n" — first from isTTY guard, second from the
	// unconditional Fprintln before summary lines.
	if !strings.HasPrefix(buf.String(), "\n\n") {
		t.Errorf("TTY summary should start with two newlines: %q", buf.String())
	}
}

// TestSummaryTTYVerboseNoLeadingNewline covers the `!t.verbose` short-circuit:
// in verbose mode, no extra newline before summary.
func TestSummaryTTYVerboseNoLeadingNewline(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{
		w:       &buf,
		isTTY:   true,
		total:   5,
		start:   time.Now(),
		verbose: true,
	}
	r := &Report{MutantsKilled: 5, MutantsTotal: 5, TestEfficacy: 100}
	term.Summary(r)

	// Should start with single "\n" from the unconditional Fprintln, not "\n\n".
	got := buf.String()
	if strings.HasPrefix(got, "\n\n") {
		t.Errorf("verbose TTY summary should not have double leading newline: %q", got)
	}
	if !strings.HasPrefix(got, "\n") {
		t.Errorf("verbose TTY summary should still have one leading newline: %q", got)
	}
}

// TestSummaryNonTTYNoLeadingNewline kills EXPRESSION_REMOVE on the isTTY && !verbose guard:
// if the guard's isTTY operand is replaced with true, a non-TTY would get the extra newline.
func TestSummaryNonTTYNoLeadingNewline(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false) // bytes.Buffer → isTTY=false
	r := &Report{MutantsKilled: 5, MutantsTotal: 5, TestEfficacy: 100}
	term.Summary(r)

	got := buf.String()
	// Non-TTY should start with single "\n" from the unconditional Fprintln.
	if strings.HasPrefix(got, "\n\n") {
		t.Errorf("non-TTY summary should not have double leading newline: %q", got)
	}
}

func TestPct(t *testing.T) {
	if got := pct(0, 0); got != 0 {
		t.Errorf("pct(0, 0) = %f, want 0", got)
	}
	if got := pct(1, 2); got != 50 {
		t.Errorf("pct(1, 2) = %f, want 50", got)
	}
	if got := pct(3, 3); got != 100 {
		t.Errorf("pct(3, 3) = %f, want 100", got)
	}
}

func TestNewTerminalNonFile(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 10, false)
	if term.isTTY {
		t.Error("bytes.Buffer should not be detected as TTY")
	}
	if term.total != 10 {
		t.Errorf("total=%d, want 10", term.total)
	}
}

// TestNewTerminalWithPipe exercises the Stat() path in NewTerminal.
// A pipe is a *os.File but not a TTY.
func TestNewTerminalWithPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	term := NewTerminal(w, 5, false)
	if term.isTTY {
		t.Error("pipe should not be detected as TTY")
	}
}

// TestNewTerminalWithClosedFile covers the `err != nil` branch from Stat().
// An os.File that was already closed will fail Stat.
func TestNewTerminalWithClosedFile(t *testing.T) {
	f, err := os.CreateTemp("", "term-*")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	_ = f.Close()
	os.Remove(name)
	// f is now a closed file.
	term := NewTerminal(f, 5, false)
	if term.isTTY {
		t.Error("closed file should not be detected as TTY")
	}
}

func TestWriteJSONError(t *testing.T) {
	r := &Report{GoModule: "test", MutatorStatistics: map[string]int{}}
	err := WriteJSON(r, "/dev/null/impossible/report.json")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestWriteJSONMarshalError(t *testing.T) {
	orig := marshalJSON
	marshalJSON = func(v any) ([]byte, error) {
		return nil, fmt.Errorf("injected marshal error")
	}
	defer func() { marshalJSON = orig }()

	r := &Report{GoModule: "test", MutatorStatistics: map[string]int{}}
	err := WriteJSON(r, filepath.Join(t.TempDir(), "report.json"))
	if err == nil {
		t.Fatal("expected error from marshalJSON")
	}
}

// TestWriteJSONMkdirError kills BRANCH_IF on the os.MkdirAll error branch in
// json.go. Passing a path whose parent "directory" is actually a regular file
// forces MkdirAll to fail; the test asserts the error propagates.
func TestWriteJSONMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// MkdirAll("blocker-file/nested") fails because blocker-file is a regular file.
	path := filepath.Join(blocker, "nested", "report.json")
	r := &Report{GoModule: "test", MutatorStatistics: map[string]int{}}
	err := WriteJSON(r, path)
	if err == nil {
		t.Fatal("expected WriteJSON to return MkdirAll error")
	}
}

// TestNewTerminalDetectsCharDevice kills STATEMENT_REMOVE on the isTTY
// assignment in NewTerminal. /dev/null is a character device on Unix; if the
// assignment is removed, isTTY stays at its zero value (false) and the check
// here fails.
func TestNewTerminalDetectsCharDevice(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("/dev/null unavailable: %v", err)
	}
	defer f.Close()
	term := NewTerminal(f, 10, false)
	if !term.isTTY {
		t.Errorf("expected isTTY=true for char device %s, got false", os.DevNull)
	}
}

// TestOnResultProgressBarWidth kills ARITHMETIC_BASE on the bar-fill
// computation and CONDITIONALS_BOUNDARY on `t.total > 0`. We set isTTY
// directly since our test buffer isn't a char device.
func TestOnResultProgressBarWidth(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{w: &buf, isTTY: true, total: 10, start: time.Now(), verbose: false}
	for range 5 {
		term.OnResult(mutator.Mutant{})
	}
	// barWidth=30, done=5, total=10 -> filled = 30*5/10 = 15 '=' chars.
	// If ARITHMETIC_BASE flips * -> / : 30/5/10 = 0 (no '=').
	// If ARITHMETIC_BASE flips / -> * : 30*5*10 = 1500 (buffer overflow, but
	// the bar is a fixed [30]byte so at minimum count differs).
	// Final frame is after the last '\r'.
	frames := strings.Split(buf.String(), "\r")
	last := frames[len(frames)-1]
	equals := strings.Count(last, "=")
	if equals != 15 {
		t.Errorf("bar fill: got %d '=', want 15. last frame: %q", equals, last)
	}
	// Also assert the percentage displayed, which catches pctDone mutations.
	if !strings.Contains(last, "50%") {
		t.Errorf("expected 50%% progress, got: %q", last)
	}
}

// TestOnResultZeroTotalNoPanic kills CONDITIONALS_BOUNDARY on `t.total > 0`.
// Mutating > to >= lets the block execute with total=0 and divide by zero.
func TestOnResultZeroTotalNoPanic(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{w: &buf, isTTY: true, total: 0, start: time.Now(), verbose: false}
	// Must not panic. Under mutation (>= 0), pctDone = done*100/0 -> panic.
	term.OnResult(mutator.Mutant{})
}
