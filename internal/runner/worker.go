package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/mutator"
	"github.com/szhekpisov/gomutant/internal/patch"
)

// maxSubprocRSSBytes caps per-mutant subprocess group memory. A mutation that
// flips a loop termination or allocation bound can make go test (or its test
// binary) balloon to tens of GB within seconds. We SIGKILL the whole process
// group at this cap; a killed mutant is classified as TimedOut.
const maxSubprocRSSBytes int64 = 2 * 1024 * 1024 * 1024 // 2 GiB

// pgroupRSSBytes returns the summed RSS of all processes in the given PGID.
// Uses `ps -g` (BSD/macOS flag) which is also supported on Linux GNU ps.
func pgroupRSSBytes(pgid int) int64 {
	out, err := exec.Command("ps", "-o", "rss=", "-g", strconv.Itoa(pgid)).Output()
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		total += n * 1024 // ps rss is KB
	}
	return total
}

// killPgroup sends SIGKILL to the entire process group.
func killPgroup(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// maxCapturedOutput caps per-stream subprocess capture. A misbehaving mutant
// (panic-loop, infinite logging) can otherwise balloon RSS by gigabytes before
// the timeout fires.
const maxCapturedOutput = 1 << 20 // 1 MiB

// cappedBuffer accumulates writes up to cap bytes and silently drops the rest.
// Compile-error detection only needs early output; later noise is useless.
type cappedBuffer struct {
	buf []byte
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := maxCapturedOutput - len(c.buf); remaining > 0 {
		if len(p) <= remaining {
			c.buf = append(c.buf, p...)
		} else {
			c.buf = append(c.buf, p[:remaining]...)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string { return string(c.buf) }

var compileErrorRe = regexp.MustCompile(`\.go:\d+:\d+:`)

// overlay is the JSON structure for `go test -overlay`.
type overlay struct {
	Replace map[string]string `json:"Replace"`
}

// Worker tests a single mutant using go test with overlay.
type Worker struct {
	id          int
	tmpSrcPath  string // Stable temp source file for this worker.
	overlayPath string // Stable overlay JSON file for this worker.
	timeout     time.Duration
	sourceCache map[string][]byte // Read-only, shared across workers.
	projectDir  string            // Working directory for go test.
	testMap     *coverage.TestMap  // Per-test coverage map (may be nil).
}

// NewWorker creates a worker with stable temp file paths.
func NewWorker(id int, tmpDir string, timeout time.Duration, sourceCache map[string][]byte, projectDir string, testMap *coverage.TestMap) (*Worker, error) {
	tmpSrc := filepath.Join(tmpDir, fmt.Sprintf("worker-%d.go", id))
	overlayFile := filepath.Join(tmpDir, fmt.Sprintf("overlay-%d.json", id))

	// Create empty files so paths exist.
	if err := os.WriteFile(tmpSrc, nil, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(overlayFile, nil, 0o644); err != nil {
		return nil, err
	}

	return &Worker{
		id:          id,
		tmpSrcPath:  tmpSrc,
		overlayPath: overlayFile,
		timeout:     timeout,
		sourceCache: sourceCache,
		projectDir:  projectDir,
		testMap:     testMap,
	}, nil
}

// Test applies a mutation and runs go test, returning the updated mutant.
func (w *Worker) Test(ctx context.Context, m mutator.Mutant) mutator.Mutant {
	start := time.Now()

	// 1. Get original source.
	original, ok := w.sourceCache[m.File]
	if !ok {
		m.Status = mutator.StatusNotViable
		m.Duration = time.Since(start)
		return m
	}

	// 2. Apply patch.
	patched, err := patch.Apply(original, m.StartOffset, m.EndOffset, m.Replacement)
	if err != nil {
		m.Status = mutator.StatusNotViable
		m.Duration = time.Since(start)
		return m
	}

	// 3. Write patched source to worker's temp file.
	if err := os.WriteFile(w.tmpSrcPath, patched, 0o644); err != nil {
		m.Status = mutator.StatusNotViable
		m.Duration = time.Since(start)
		return m
	}

	// 4. Write overlay JSON (absolute paths required).
	ov := overlay{Replace: map[string]string{m.File: w.tmpSrcPath}}
	ovBytes, _ := json.Marshal(ov)
	if err := os.WriteFile(w.overlayPath, ovBytes, 0o644); err != nil {
		m.Status = mutator.StatusNotViable
		m.Duration = time.Since(start)
		return m
	}

	// 5. Run go test.
	testCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	args := []string{"test", "-count=1", "-failfast",
		fmt.Sprintf("-timeout=%s", w.timeout),
		fmt.Sprintf("-overlay=%s", w.overlayPath),
	}

	// GOMUTANT_TEST_SHORT=1 propagates -short to inner go test, letting the
	// target suite skip heavy integration tests. Used for gomutant self-testing
	// to avoid recursive worker-pool fanout.
	if os.Getenv("GOMUTANT_TEST_SHORT") == "1" {
		args = append(args, "-short")
	}

	// Use per-test coverage map to run only relevant tests.
	if w.testMap != nil {
		if tests := w.testMap.TestsFor(m.CoverageFile, m.Line); len(tests) > 0 {
			args = append(args, fmt.Sprintf("-run=%s", coverage.RunPattern(tests)))
		}
	}

	args = append(args, m.Pkg)
	cmd := exec.CommandContext(testCtx, "go", args...)
	cmd.Dir = w.projectDir
	// Put go test + its compiler + test-binary descendants in their own
	// process group so we can kill the whole tree if RSS runs away.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr cappedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		m.Status = mutator.StatusNotViable
		m.Duration = time.Since(start)
		return m
	}

	var memKilled atomic.Bool
	monitorDone := make(chan struct{})
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		pgid := cmd.Process.Pid
		for {
			select {
			case <-monitorDone:
				return
			case <-t.C:
				if pgroupRSSBytes(pgid) > maxSubprocRSSBytes {
					memKilled.Store(true)
					killPgroup(pgid)
					return
				}
			}
		}
	}()

	err = cmd.Wait()
	close(monitorDone)
	m.Duration = time.Since(start)

	// 6. Classify result.
	if memKilled.Load() {
		m.Status = mutator.StatusTimedOut
		return m
	}

	if err == nil {
		m.Status = mutator.StatusLived
		return m
	}

	if testCtx.Err() == context.DeadlineExceeded {
		m.Status = mutator.StatusTimedOut
		return m
	}

	// Non-zero exit: distinguish KILLED from NOT_VIABLE.
	// Build failures show "[build failed]" or "[setup failed]" in stdout.
	stdoutStr := stdout.String()
	if compileErrorRe.MatchString(stderr.String()) &&
		(strings.Contains(stdoutStr, "[build failed]") || strings.Contains(stdoutStr, "[setup failed]")) {
		m.Status = mutator.StatusNotViable
		return m
	}

	m.Status = mutator.StatusKilled
	return m
}
