package report

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

// Terminal handles progress output to the terminal.
type Terminal struct {
	w       io.Writer
	isTTY   bool
	mu      sync.Mutex
	total   int
	done    int
	start   time.Time
	verbose bool
}

// NewTerminal creates a terminal progress reporter.
func NewTerminal(w io.Writer, total int, verbose bool) *Terminal {
	isTTY := false
	if f, ok := w.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			isTTY = info.Mode()&os.ModeCharDevice != 0
		}
	}
	return &Terminal{
		w:       w,
		isTTY:   isTTY,
		total:   total,
		start:   time.Now(),
		verbose: verbose,
	}
}

// Header prints the initial info banner.
func (t *Terminal) Header(version string, target string, workers int, mutatorCount int) {
	fmt.Fprintf(t.w, "gomutant v%s\n\n", version)
	fmt.Fprintf(t.w, "Target: %s\n", target)
	fmt.Fprintf(t.w, "Workers: %d | Mutations: %d types enabled\n\n", workers, mutatorCount)
}

// Phase prints a phase status line.
func (t *Terminal) Phase(msg string) {
	fmt.Fprintf(t.w, "%s", msg)
}

// PhaseDone completes a phase line.
func (t *Terminal) PhaseDone(msg string) {
	fmt.Fprintf(t.w, " %s\n", msg)
}

// OnResult is the callback for each completed mutant.
func (t *Terminal) OnResult(m mutator.Mutant) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done++

	if t.verbose {
		fmt.Fprintf(t.w, "  [%d/%d] %s %s:%d:%d %s → %s (%s)\n",
			t.done, t.total,
			m.Status.String(),
			m.RelFile, m.Line, m.Col,
			m.Original, m.Replacement,
			m.Duration.Round(time.Millisecond))
		return
	}

	if t.isTTY {
		elapsed := time.Since(t.start).Round(time.Second)
		pctDone := 0
		if t.total > 0 {
			pctDone = t.done * 100 / t.total
		}
		const barWidth = 30
		filled := barWidth * t.done / t.total
		var bar [barWidth]byte
		for i := range barWidth {
			if i < filled {
				bar[i] = '='
			} else {
				bar[i] = ' '
			}
		}
		fmt.Fprintf(t.w, "\rTesting mutants [%s] %d/%d  %d%%  %s", bar[:], t.done, t.total, pctDone, elapsed)
	}
}

// Summary prints the final summary.
func (t *Terminal) Summary(r *Report) {
	if t.isTTY && !t.verbose {
		fmt.Fprintln(t.w) // Newline after progress bar.
	}

	fmt.Fprintln(t.w)
	fmt.Fprintf(t.w, "  Killed:       %d  (%.1f%%)\n", r.MutantsKilled, pct(r.MutantsKilled, r.MutantsKilled+r.MutantsLived))
	fmt.Fprintf(t.w, "  Lived:        %d  (%.1f%%)\n", r.MutantsLived, pct(r.MutantsLived, r.MutantsKilled+r.MutantsLived))
	fmt.Fprintf(t.w, "  Not covered:  %d\n", r.MutantsNotCovered)
	fmt.Fprintf(t.w, "  Not viable:   %d\n", r.MutantsNotViable)
	fmt.Fprintf(t.w, "  Timed out:    %d\n", r.MutantsTotal-r.MutantsKilled-r.MutantsLived-r.MutantsNotCovered-r.MutantsNotViable)
	fmt.Fprintf(t.w, "  Efficacy:     %.2f%%\n", r.TestEfficacy)
	fmt.Fprintln(t.w)
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
