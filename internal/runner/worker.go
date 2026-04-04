package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/szhekpisov/gomutant/internal/mutator"
	"github.com/szhekpisov/gomutant/internal/patch"
)

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
}

// NewWorker creates a worker with stable temp file paths.
func NewWorker(id int, tmpDir string, timeout time.Duration, sourceCache map[string][]byte, projectDir string) (*Worker, error) {
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
		m.Pkg,
	}
	cmd := exec.CommandContext(testCtx, "go", args...)
	cmd.Dir = w.projectDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	m.Duration = time.Since(start)

	// 6. Classify result.
	if err == nil {
		m.Status = mutator.StatusLived
		return m
	}

	if testCtx.Err() == context.DeadlineExceeded {
		m.Status = mutator.StatusTimedOut
		return m
	}

	// Non-zero exit: distinguish KILLED from NOT_VIABLE.
	combined := stdout.String() + stderr.String()
	if compileErrorRe.MatchString(stderr.String()) && !strings.Contains(combined, "FAIL\t") {
		m.Status = mutator.StatusNotViable
		return m
	}

	m.Status = mutator.StatusKilled
	return m
}
