package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/mutator"
	"github.com/szhekpisov/gomutants/internal/patch"
)

// maxSubprocRSSBytes caps per-mutant subprocess group memory. A mutation that
// flips a loop termination or allocation bound can make go test (or its test
// binary) balloon to tens of GB within seconds. We SIGKILL the whole process
// group at this cap; a killed mutant is classified as TimedOut.
//
// var (not const) so tests can lower it to a tiny value and force the
// monitor goroutine's kill path on a normal-sized test process.
//
// On Windows pgroupRSSBytes is a no-op (returns 0) so this cap never trips;
// the per-mutant context timeout is the backstop there.
var maxSubprocRSSBytes int64 = 2 * 1024 * 1024 * 1024 // 2 GiB

// monitorPollInterval is the cadence at which the RSS monitor probes
// `ps -g`. var so tests can shrink it to make the kill path fire quickly.
var monitorPollInterval = 1 * time.Second

// nonZeroSince returns time.Since(start) but guarantees a strictly positive
// result, so callers can use Duration==0 as a "never set" sentinel. Without
// this, rapid early-return paths can yield a zero Duration on some clocks.
func nonZeroSince(start time.Time) time.Duration {
	return clampPositive(time.Since(start))
}

// clampPositive returns d if it is strictly positive, otherwise the smallest
// positive Duration. Extracted from nonZeroSince so the d == 0 boundary can
// be tested directly — driving nonZeroSince is racy because time.Since on a
// just-captured `time.Now()` returns a tiny but nonzero positive duration
// on real clocks, hiding the `<` vs `<=` mutation.
func clampPositive(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Nanosecond
	}
	return d
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
	// Compute take with min/max so neither comparison is an AST `<`/`<=`
	// operator the boundary mutator can target. The previous if-else form
	// produced equivalent boundary mutants because both branches collapsed
	// to the same observable result on the equality cases.
	take := min(len(p), max(0, maxCapturedOutput-len(c.buf)))
	c.buf = append(c.buf, p[:take]...)
	return len(p), nil
}

func (c *cappedBuffer) String() string { return string(c.buf) }

var compileErrorRe = regexp.MustCompile(`\.go:\d+:\d+:`)

// infrastructureOutputSignatures are operating-system/resource failures that
// prevent gomutants from obtaining a meaningful verdict for a mutant, as they
// appear in a subprocess's *output*.
//
// Keep matching deliberately conservative. This list is scanned against `go
// test` stdout, which also carries the tested code's own output, so any
// phrase a failing test could plausibly print itself would launder a genuine
// kill into a non-result. Generic wordings are therefore qualified with the
// runtime prefix only the real host failure produces ("out of memory" alone
// is a common application error message; "fatal error: out of memory" is the
// Go runtime dying). EAGAIN's "resource temporarily unavailable" is left out
// entirely for the same reason — it stays in infrastructureErrnos, where the
// error is typed and cannot be forged by test output. An unexplained signal
// (including "signal: killed") is likewise excluded: a mutated test can cause
// that on its own, and a real OOM kill already surfaces as TIMED OUT via the
// RSS monitor.
var infrastructureOutputSignatures = []string{
	"no space left on device",
	"cannot allocate memory",
	"fatal error: out of memory",
	"runtime: out of memory",
	"runtime: failed to create new os thread",
	"too many open files",
	"read-only file system",
	"input/output error",
	"text file busy",
}

// infrastructureErrnos are the same class of failures as typed syscall
// errors, for the paths where gomutants itself holds an error value (its
// per-mutant temp-file writes, cmd.Start) instead of a subprocess's text.
// errors.Is on the errno is exact, survives wrapping that rewrites the
// message, and cannot be spoofed by the tested code, so it carries the
// signatures that are too generic to match as free text.
var infrastructureErrnos = []error{
	syscall.ENOSPC,  // no space left on device
	syscall.ENOMEM,  // cannot allocate memory
	syscall.EMFILE,  // too many open files (per-process limit)
	syscall.ENFILE,  // too many open files (system-wide limit)
	syscall.EROFS,   // read-only file system
	syscall.EIO,     // input/output error
	syscall.EAGAIN,  // fork/thread exhaustion
	syscall.ETXTBSY, // text file busy
}

// testFailureMarker is the per-test line `go test` prints when a test
// function itself reported a failure. Its presence means a test ran and
// detected the mutation, so an infrastructure signature elsewhere in the same
// output is the test's own text rather than the host failing — the mutant
// stays KILLED. A genuine host failure aborts the binary with a runtime
// `fatal error:` or a toolchain message and no per-test failure line.
const testFailureMarker = "--- FAIL: "

// hasInfrastructureError reports whether any input contains a recognized
// infrastructure signature. Subprocess output and OS errors vary in
// capitalization, so matching is case-insensitive.
func hasInfrastructureError(values ...string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, signature := range infrastructureOutputSignatures {
			if strings.Contains(lower, signature) {
				return true
			}
		}
	}
	return false
}

// isInfrastructureErr reports whether err is, or wraps, a recognized host
// resource failure. The errno comparison is authoritative; the message scan
// is a fallback for errors that lost their errno on the way up (a wrapper
// that reformatted the text rather than chaining it).
func isInfrastructureErr(err error) bool {
	for _, errno := range infrastructureErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	return hasInfrastructureError(err.Error())
}

// setupErrorStatus classifies a failure in one of gomutants' own per-mutant
// setup steps (staging the patched source, the overlay, or starting the test
// process). A recognized host failure becomes InfraError — the mutation was
// never actually tested, so it must not be cached or scored. Anything else
// keeps the historical NotViable: the mutant could not be staged either way,
// and NotViable already means "no verdict, excluded from efficacy".
func setupErrorStatus(err error) mutator.MutantStatus {
	if isInfrastructureErr(err) {
		return mutator.StatusInfraError
	}
	return mutator.StatusNotViable
}

// writeFileFunc, execCommandContext, and startCommandFunc are package-level
// indirections around process/file operations. Swapping them in tests lets us
// hit the unhappy paths in NewWorker / Worker.Test (write failure, fork/exec
// failure) without contriving filesystem, PATH, or host resource state.
var (
	writeFileFunc      = os.WriteFile
	execCommandContext = exec.CommandContext
	startCommandFunc   = func(cmd *exec.Cmd) error { return cmd.Start() }
)

// shortFlagFromEnv reports whether the inner `go test` should be invoked
// with -short. Extracted from Worker.Test so the env-string equality check
// is reachable without spinning up a subprocess.
func shortFlagFromEnv() bool {
	return os.Getenv("GOMUTANTS_TEST_SHORT") == "1"
}

// overlay is the JSON structure for `go test -overlay`.
type overlay struct {
	Replace map[string]string `json:"Replace"`
}

// Worker tests a single mutant using go test with overlay.
type Worker struct {
	id          int
	tmpSrcPath  string // Stable temp source file for this worker.
	overlayPath string // Stable overlay JSON file for this worker.
	policy      TimeoutPolicy
	sourceCache map[string][]byte // Read-only, shared across workers.
	projectDir  string            // Working directory for go test.
	testMap     *coverage.TestMap // Per-test coverage map (may be nil).

	// childGOMAXPROCS, if > 0, caps the GOMAXPROCS of each `go test` child.
	// Limits compile + test runtime parallelism per child so N parallel workers
	// don't oversubscribe a NumCPU-core host. Zero means inherit from parent.
	childGOMAXPROCS int

	// testCPU, if > 0, is forwarded to the inner `go test` as `-cpu=N`.
	// Zero omits the flag so go test defaults to GOMAXPROCS. Note: when
	// childGOMAXPROCS > 0, go test silently caps -cpu at that value, so
	// the intended pairing is --workers=1 --test-cpu=N (or any combo
	// where --test-cpu <= NumCPU/--workers).
	testCPU int

	// tags, if non-empty, is forwarded to the inner `go test` as
	// `-tags=<value>` so mutants in build-tag-gated files compile and run.
	// Set by the pool after construction, mirroring testCPU.
	tags string

	// testFlags are the user's --test-flags, appended verbatim to every
	// inner `go test` argv. Empty appends nothing. They land last of all —
	// after the flags we set ourselves *and* after the package argument, so
	// where both spell the same flag the user's value wins (Go's flag
	// parsing takes the last occurrence) and a flag `go test` does not
	// recognize cannot demote the package we meant to test into a
	// positional argument for the test binary.
	//
	// That "last one wins" rule is argv-level only, and it is not a
	// general override guarantee: -timeout is also enforced out-of-band by
	// the context deadline in Worker.Test, so lengthening it via argv
	// alone would not work. Flags in that class, along with the ones
	// gomutants depends on (-overlay, -run, …), are rejected at the
	// CLI boundary and cannot reach here. Set by the pool after
	// construction, mirroring tags.
	testFlags []string
}

// NewWorker creates a worker with stable temp file paths.
func NewWorker(id int, tmpDir string, policy TimeoutPolicy, sourceCache map[string][]byte, projectDir string, testMap *coverage.TestMap) (*Worker, error) {
	tmpSrc := filepath.Join(tmpDir, fmt.Sprintf("worker-%d.go", id))
	overlayFile := filepath.Join(tmpDir, fmt.Sprintf("overlay-%d.json", id))

	// Create empty files so paths exist.
	if err := writeFileFunc(tmpSrc, nil, 0o644); err != nil {
		return nil, err
	}
	if err := writeFileFunc(overlayFile, nil, 0o644); err != nil {
		return nil, err
	}

	return &Worker{
		id:          id,
		tmpSrcPath:  tmpSrc,
		overlayPath: overlayFile,
		policy:      policy,
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
		m.Duration = nonZeroSince(start)
		return m
	}

	// 2. Apply patch.
	patched, err := patch.Apply(original, m.StartOffset, m.EndOffset, m.Replacement)
	if err != nil {
		m.Status = mutator.StatusNotViable
		m.Duration = nonZeroSince(start)
		return m
	}

	// 3. Write patched source to worker's temp file.
	if err := writeFileFunc(w.tmpSrcPath, patched, 0o644); err != nil {
		m.Status = setupErrorStatus(err)
		m.Duration = nonZeroSince(start)
		return m
	}

	// 4. Write overlay JSON (absolute paths required).
	ov := overlay{Replace: map[string]string{m.File: w.tmpSrcPath}}
	ovBytes, _ := json.Marshal(ov)
	if err := writeFileFunc(w.overlayPath, ovBytes, 0o644); err != nil {
		m.Status = setupErrorStatus(err)
		m.Duration = nonZeroSince(start)
		return m
	}

	// 5. Compute the per-mutant timeout once and reuse it for both the
	// outer context deadline (which feeds exec.CommandContext's
	// SIGKILL-on-expiry) and the inner -timeout flag (which lets `go test`
	// exit cleanly with its own timeout error). Computing once means an
	// odd-shaped TimeoutPolicy can't desync the two. The single deadline is
	// shared across every per-package invocation: computeTimeout already
	// sizes it from the sum of all covering tests' durations (across
	// packages), so the whole cross-package run is bounded as one unit.
	timeout := w.computeTimeout(m)
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 6. Run each covering package's invocation in turn, short-circuiting on
	// the first non-Lived outcome (a kill, timeout, or compile failure). A
	// mutant is Lived only if every covering package's tests pass. A
	// cmd.Start failure surfaces as NotViable or InfraError from
	// runMutantTest, which the non-Lived check below returns just like any
	// other terminal outcome.
	for _, args := range w.testInvocations(m, shortFlagFromEnv(), timeout) {
		status := w.runMutantTest(testCtx, args)
		// Parent-context cancel (Ctrl-C, upstream deadline) propagates via
		// exec.CommandContext as a non-nil cmd.Wait error that is neither
		// memKilled nor the test's own timeout, which classifyTestOutcome
		// would mistake for StatusKilled — silently marking cancelled mutants
		// as tested and inflating efficacy. Detect parent cancel here and
		// preserve the incoming Status + zero Duration so the pool surfaces
		// the mutant as Pending (not tested), keeping Pending ⇒ Duration==0.
		if ctx.Err() != nil {
			return m
		}
		if status != mutator.StatusLived {
			m.Duration = time.Since(start)
			m.Status = status
			return m
		}
	}

	m.Duration = time.Since(start)
	m.Status = mutator.StatusLived
	return m
}

// runMutantTest runs one `go test` invocation under the RSS monitor and
// returns its classified status. A cmd.Start failure (an infrastructure
// problem — exec/fork failure, PATH misconfig, rlimit — not a mutant-viability
// signal) is reported as InfraError when it carries a recognized signature,
// or NotViable otherwise. The caller treats either as a terminal outcome.
//
// Extracted from Worker.Test so the integration path can drive it once per
// covering package while sharing a single per-mutant deadline.
func (w *Worker) runMutantTest(testCtx context.Context, args []string) mutator.MutantStatus {
	cmd, stdout, stderr := w.makeTestCmd(testCtx, args)

	if err := startCommandFunc(cmd); err != nil {
		status := setupErrorStatus(err)
		fmt.Fprintf(os.Stderr, "gomutants: worker %d: cmd.Start failed, treating as %s: %v\n", w.id, status, err)
		return status
	}

	// Resolve the process-group "handle" we'll later kill if RSS runs away.
	// On Unix this is the child's pgid (with Setpgid:true the parent has
	// already issued setpgid before Start returns on Linux; on macOS it
	// happens in the child post-fork, leaving a brief window where Getpgid
	// may transiently differ from the leader's pid — processGroup falls
	// back to cmd.Process.Pid then). On Windows there is no pgid concept
	// and processGroup returns pid unchanged.
	pgid := processGroup(cmd.Process.Pid)

	var memKilled atomic.Bool
	monitorDone := make(chan struct{})
	monitorExited := make(chan struct{})
	go func() {
		// 1s cadence: 5 workers × 1 poll/s = 5 ps/sec aggregate (was 25 at
		// 200ms). The 2 GiB cap is loose enough that a 800 ms-later kill is
		// still safe — even on M-series RAM bandwidth a runaway alloc takes
		// ≥1s to add 2 GiB resident. testTimeout (10× baseline) is the
		// outer backstop.
		defer close(monitorExited)
		t := time.NewTicker(monitorPollInterval)
		defer t.Stop()
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

	err := cmd.Wait()
	close(monitorDone)
	// Wait for the monitor goroutine to fully exit before returning. Without
	// this barrier its still-pending reads of psOutputFunc / syscallKillFunc
	// race with the next test's swap of those package-level vars (caught by
	// `go test -race`).
	<-monitorExited

	return classifyTestOutcome(err, memKilled.Load(), testCtx.Err(), stdout.String(), stderr.String())
}

// makeTestCmd builds the *exec.Cmd that runs the mutated `go test` plus
// its capped stdout/stderr buffers. Extracted from Worker.Test so each
// piece of cmd configuration (process group, GOMAXPROCS env, capped
// buffers) can be asserted on directly. Without extraction the cmd is
// local to Test and the SysProcAttr / Env mutations are invisible to tests.
func (w *Worker) makeTestCmd(ctx context.Context, args []string) (*exec.Cmd, *cappedBuffer, *cappedBuffer) {
	cmd := execCommandContext(ctx, "go", args...)
	cmd.Dir = w.projectDir
	// Put go test + its compiler + test-binary descendants in their own
	// process group so we can kill the whole tree if RSS runs away.
	// applyProcessGroup is platform-specific (Setpgid on Unix,
	// CREATE_NEW_PROCESS_GROUP on Windows).
	applyProcessGroup(cmd)
	if w.childGOMAXPROCS > 0 {
		// exec auto-sets PWD=cmd.Dir only when cmd.Env is nil (see Go's
		// exec.go ~L1220). When we set Env explicitly the child inherits the
		// parent's stale PWD, which breaks module-relative paths. Mirror the
		// auto-PWD behavior plus our GOMAXPROCS cap.
		cmd.Env = append(os.Environ(),
			"PWD="+cmd.Dir,
			fmt.Sprintf("GOMAXPROCS=%d", w.childGOMAXPROCS),
		)
	}
	stdout := &cappedBuffer{}
	stderr := &cappedBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd, stdout, stderr
}

// baseTestArgs builds the flag-only prefix shared by every `go test`
// invocation for a mutant — everything except the `-run` filter, the
// package argument, and the user's --test-flags that follow it. Split out
// so both the single-package builder and the per-package integration
// builder share one source of truth for the flag wiring (and so each flag
// stays unit-testable).
//
// `timeout` is the resolved per-mutant deadline (computed by the caller
// from TimeoutPolicy), threaded into both the outer context and the
// `-timeout=` flag.
func (w *Worker) baseTestArgs(short bool, timeout time.Duration) []string {
	// -vet=off: vet runs in the user's CI on clean source; re-running it
	// per mutant is pure overhead. Measured ~17–39% per-mutant wall-clock
	// reduction on representative packages.
	args := []string{"test", "-count=1", "-failfast", "-vet=off",
		fmt.Sprintf("-timeout=%s", timeout),
		fmt.Sprintf("-overlay=%s", w.overlayPath),
	}
	if w.testCPU > 0 {
		args = append(args, fmt.Sprintf("-cpu=%d", w.testCPU))
	}
	if w.tags != "" {
		args = append(args, "-tags="+w.tags)
	}
	// GOMUTANTS_TEST_SHORT=1 propagates -short to inner go test, letting the
	// target suite skip heavy integration tests. Used for gomutants self-testing
	// to avoid recursive worker-pool fanout.
	if short {
		args = append(args, "-short")
	}
	return args
}

// buildTestArgs constructs the `go test` argv for the single-package case:
// the mutant's own package, optionally filtered to its covering tests. Used
// when no cross-package routing applies (the common path). Kept as a
// distinct builder so callers can verify the -short, -run, and package arg
// wiring without spinning up a subprocess.
//
// The user's --test-flags go last, after the package. `go test` goes on
// parsing its own flags past one it does not recognize, but that first
// unrecognized flag marks the package list as already seen, so a package
// name after it is forwarded to the test binary as a positional argument
// and `go test` falls back to `.`. A test-binary flag ahead of the
// package therefore leaves the working directory to be tested and every
// mutant reported LIVED. Trailing placement also preserves the override
// rule — Go takes the last occurrence of a repeated flag, so a user value
// still beats ours.
func (w *Worker) buildTestArgs(m mutator.Mutant, short bool, timeout time.Duration) []string {
	args := w.baseTestArgs(short, timeout)
	// Use per-test coverage map to run only relevant tests.
	if w.testMap != nil {
		if tests := w.testMap.TestsFor(m.CoverageFile, m.Line); len(tests) > 0 {
			args = append(args, fmt.Sprintf("-run=%s", coverage.RunPattern(tests)))
		}
	}
	args = append(args, m.Pkg)
	return append(args, w.testFlags...)
}

// testInvocations returns the ordered set of `go test` argv lists to run for
// one mutant. In the common case this is a single invocation against the
// mutant's own package (identical to buildTestArgs). When the per-test
// coverage map routes the mutant to covering tests in *other* packages
// (integration mode), it returns one invocation per covering package, each
// filtered to that package's covering tests.
//
// Per-package invocations are required because `go test -run` applies its
// regex independently per package and `-failfast` does not short-circuit
// across packages; a single multi-package invocation would mis-route
// same-named tests and run every package even after one already killed the
// mutant. The mutant's own package is ordered first so the cheapest, most
// likely killer runs before any cross-package suite.
func (w *Worker) testInvocations(m mutator.Mutant, short bool, timeout time.Duration) [][]string {
	groups := map[string][]string{}
	if w.testMap != nil {
		for _, ref := range w.testMap.TestRefsFor(m.CoverageFile, m.Line) {
			groups[ref.Pkg] = append(groups[ref.Pkg], ref.Name)
		}
	}

	// No routing info: run the whole of the mutant's own package. (When the
	// only covering package is the mutant's own, the general loop below
	// produces the same single invocation, so no special-case is needed.)
	if len(groups) == 0 {
		return [][]string{w.buildTestArgs(m, short, timeout)}
	}

	base := w.baseTestArgs(short, timeout)
	invs := make([][]string, 0, len(groups))
	for _, pkg := range orderRoutePackages(groups, m.Pkg) {
		args := append(slices.Clone(base),
			fmt.Sprintf("-run=%s", coverage.RunPattern(groups[pkg])), pkg)
		// User flags trail the package here for the same reason as in
		// buildTestArgs.
		args = append(args, w.testFlags...)
		invs = append(invs, args)
	}
	return invs
}

// orderRoutePackages returns the covering packages with the mutant's own
// package first (when present), then the rest in deterministic sorted order.
// Same-package tests are the cheapest and likeliest to kill, so running them
// first minimizes wasted cross-package work before a short-circuit.
func orderRoutePackages(groups map[string][]string, ownPkg string) []string {
	rest := make([]string, 0, len(groups))
	for pkg := range groups {
		if pkg != ownPkg {
			rest = append(rest, pkg)
		}
	}
	slices.Sort(rest)
	if groups[ownPkg] != nil {
		return append([]string{ownPkg}, rest...)
	}
	return rest
}

// computeTimeout resolves the per-mutant deadline for `m` from the
// worker's policy and testMap. Extracted from Worker.Test so the wiring
// — that the policy's TestMap argument is in fact w.testMap, not nil —
// is unit-testable without spinning up a real subprocess. A regression
// where a refactor passed nil here would silently downgrade every
// mutant to the global ceiling and pass the existing tests.
func (w *Worker) computeTimeout(m mutator.Mutant) time.Duration {
	return w.policy.For(w.testMap, m)
}

// classifyTestOutcome decides a mutant's terminal status from the raw
// subprocess outcome. Pure function so the branching can be unit-tested
// without staging actual test failures.
//
// Priority order:
//  1. memKilled → TimedOut (RSS monitor SIGKILL'd the tree).
//  2. runErr == nil → Lived (tests all passed with the mutant applied).
//  3. testCtxErr == DeadlineExceeded → TimedOut.
//  4. stderr carries a `file.go:N:N:` compile error AND stdout shows
//     `[build failed]` / `[setup failed]` → NotViable.
//  5. stdout or stderr carries a recognized infrastructure signature and no
//     test reported a failure of its own → InfraError.
//  6. Otherwise → Killed.
func classifyTestOutcome(runErr error, memKilled bool, testCtxErr error, stdout, stderr string) mutator.MutantStatus {
	if memKilled {
		return mutator.StatusTimedOut
	}
	if runErr == nil {
		return mutator.StatusLived
	}
	if testCtxErr == context.DeadlineExceeded {
		return mutator.StatusTimedOut
	}
	if compileErrorRe.MatchString(stderr) &&
		(strings.Contains(stdout, "[build failed]") || strings.Contains(stdout, "[setup failed]")) {
		return mutator.StatusNotViable
	}
	// A test that reported its own failure detected the mutation, so the
	// signature belongs to that test's output, not to a dying host. Ordering
	// the marker check first also keeps the common killed path from paying
	// for the case-insensitive scan of up to 2 MiB of captured output.
	if !strings.Contains(stdout, testFailureMarker) && hasInfrastructureError(stdout, stderr) {
		return mutator.StatusInfraError
	}
	return mutator.StatusKilled
}
