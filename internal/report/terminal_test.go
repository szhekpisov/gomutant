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

func TestHeader(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false)
	term.Header("0.1.0", "[./...]", 10, 5)

	out := buf.String()
	if !strings.Contains(out, "gomutant v0.1.0") {
		t.Errorf("Header missing version: %q", out)
	}
	if !strings.Contains(out, "Workers: 10") {
		t.Errorf("Header missing workers: %q", out)
	}
	if !strings.Contains(out, "Mutations: 5 types enabled") {
		t.Errorf("Header missing mutation count: %q", out)
	}
}

func TestPhase(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false)
	term.Phase("Collecting coverage...")
	term.PhaseDone("done (2.1s)")

	out := buf.String()
	if !strings.Contains(out, "Collecting coverage...") {
		t.Errorf("Phase missing: %q", out)
	}
	if !strings.Contains(out, "done (2.1s)") {
		t.Errorf("PhaseDone missing: %q", out)
	}
}

func TestOnResultVerbose(t *testing.T) {
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

	out := buf.String()
	if !strings.Contains(out, "[1/5]") {
		t.Errorf("OnResult missing counter: %q", out)
	}
	if !strings.Contains(out, "KILLED") {
		t.Errorf("OnResult missing status: %q", out)
	}
	if !strings.Contains(out, "file.go:10:5") {
		t.Errorf("OnResult missing location: %q", out)
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

func TestOnResultTTYProgressBar(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{
		w:     &buf,
		isTTY: true,
		total: 10,
		start: time.Now(),
	}

	m := mutator.Mutant{ID: 1, Status: mutator.StatusKilled, Duration: time.Millisecond}
	term.OnResult(m)

	out := buf.String()
	if !strings.Contains(out, "Testing mutants") {
		t.Errorf("OnResult TTY missing progress bar: %q", out)
	}
	if !strings.Contains(out, "1/10") {
		t.Errorf("OnResult TTY missing counter: %q", out)
	}
	if !strings.Contains(out, "\r") {
		t.Errorf("OnResult TTY missing carriage return: %q", out)
	}
}

func TestSummary(t *testing.T) {
	var buf bytes.Buffer
	term := NewTerminal(&buf, 0, false)

	r := &Report{
		MutantsKilled:     8,
		MutantsLived:      2,
		MutantsNotCovered: 3,
		MutantsNotViable:  1,
		MutantsTotal:      14,
		TestEfficacy:      80.0,
	}
	term.Summary(r)

	out := buf.String()
	if !strings.Contains(out, "Killed:") {
		t.Errorf("Summary missing Killed: %q", out)
	}
	if !strings.Contains(out, "Lived:") {
		t.Errorf("Summary missing Lived: %q", out)
	}
	if !strings.Contains(out, "Efficacy:") {
		t.Errorf("Summary missing Efficacy: %q", out)
	}
	if !strings.Contains(out, "80.00%") {
		t.Errorf("Summary missing efficacy value: %q", out)
	}
}

func TestSummaryTTYNewline(t *testing.T) {
	var buf bytes.Buffer
	term := &Terminal{
		w:     &buf,
		isTTY: true,
		total: 5,
		start: time.Now(),
	}

	r := &Report{
		MutantsKilled: 5,
		MutantsTotal:  5,
		TestEfficacy:  100,
	}
	term.Summary(r)

	out := buf.String()
	// TTY non-verbose should start with newline (after progress bar).
	if !strings.HasPrefix(out, "\n") {
		t.Errorf("Summary TTY should start with newline: %q", out)
	}
}

func TestSummaryTTYVerboseNoExtraNewline(t *testing.T) {
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

	out := buf.String()
	// TTY verbose: first char should be newline (from the empty Fprintln), not double newline from progress bar.
	if strings.HasPrefix(out, "\n\n") {
		t.Errorf("Summary TTY verbose should not have double newline: %q", out)
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

func TestNewTerminalWithFile(t *testing.T) {
	// Test with a real *os.File (pipe — not a TTY, but exercises the Stat path).
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	term := NewTerminal(w, 5, false)
	// Pipe is not a TTY.
	if term.isTTY {
		t.Error("pipe should not be detected as TTY")
	}
}

func TestWriteJSONError(t *testing.T) {
	r := &Report{GoModule: "test", MutatorStatistics: map[string]int{}}
	// Write to an invalid path (read-only directory).
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
