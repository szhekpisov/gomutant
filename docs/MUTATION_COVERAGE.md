# Mutation Coverage

Status of gomutant's self-mutation test. "Efficacy" = `killed / (killed + lived)`;
`not_viable`, `not_covered`, and `timed_out` are excluded from the denominator.

## Summary (excluding `main`)

| Package  | Killed | Lived | Efficacy |
|----------|-------:|------:|---------:|
| config   | 42     | 0     | 100.00%  |
| patch    | 13     | 0     | 100.00%  |
| mutator  | 78     | 0     | 100.00%  |
| report   | 95     | 0     | 100.00%  |
| coverage | 113    | 9     | 92.62%   |
| discover | 50     | 4     | 92.59%   |
| runner   | 80     | 20    | 80.00%   |
| **total**| **471**| **33**| **93.45%** |

Run the no-main self-test with `scripts/` (external) or replicate with
`gomutant -w 8 -o <pkg>.json ./internal/<pkg>/` per package.

## Why these mutants survive

The surviving mutants fall into a small set of patterns. Understanding the
pattern is more useful than chasing individual positions — future changes
should avoid *adding* mutants that hit the same dead zones.

### 1. `<` vs `<=` inside a `!=` guard

```go
if a != b {
    return a < b    // ← CONDITIONALS_BOUNDARY mutates to `<=`
}
```

Given `a != b`, `a < b` and `a <= b` are identical — mutation has no
observable effect. Appears in every sort comparator.

- `discover.go:103, 106, 109` (filename, line, column comparators)
- Any refactor to kill these requires extracting the comparator and unit
  testing it with hand-constructed inputs that violate the `!=` precondition.

### 2. Tiebreaker that is never tied in practice

```go
return a.Type < b.Type   // CONDITIONALS_BOUNDARY, reached only when file, line, col all equal
```

Two mutator candidates with identical `(file, line, col, type)` don't
happen — each mutator type emits at most one candidate per position.
Unreachable without synthetic input.

- `discover.go:111`

### 3. Mutation's "wrong" branch falls through to the same end state

```go
if !ok {
    mutants[i].Status = StatusNotCovered
    continue       // ← BRANCH_IF elides body; but the loop body below also assigns NotCovered
}
if !profile.IsCovered(profilePath, ...) {
    mutants[i].Status = StatusNotCovered
}
```

When the guard fires, `profilePath` is the zero value `""`, and
`profile.IsCovered("", …)` is always false — so the later branch assigns
NotCovered anyway. Terminal status matches.

- `filter.go:30`
- `discover.go:192` — `slash < 0` vs `<= 0`: when `slash == 0`, the loop's
  continuation path assigns `prefix = ""` and then exits because
  `HasPrefix(p, "")` is always true; both paths `return ""`.
- `coverage/testmap.go:71, 77, 78` — build-failure / LookPath / statFile
  guards whose body is `continue`. Whether the body runs or is elided,
  the next loop iteration reaches the same `pkgBins` state because the
  binary-absent signal propagates via missing-key lookups downstream.

### 4. Mutation requires a code path only taken on rare OS errors

Example: `if err := cmd.Run(); err != nil { return err }` — to distinguish
the return from a fall-through, the subsequent code must differ when
`err == nil` (success path). For command runners where success is the
only tested path, the mutation is unreachable without a subprocess mock.

- `coverage/testmap.go:121 (line <= b.EndLine` boundary)`, `:146` —
  coverage-block iteration details that only diverge on block shapes the
  real `go test -cover` output doesn't produce.
- `coverage/testmap.go:137, 173, 177, 242` — context cancellation, cwd
  setting, error-path returns that observe the same final result as the
  success path in integration tests.

### 5. Pool / worker control-flow with ctx-gated or panic-safe fall-through

```go
for i := range p.workers {
    w, err := NewWorker(...)
    if err != nil {
        continue     // BRANCH_IF: skipping this continues into wg.Add and spawns a goroutine with nil w
    }
    ...
}
```

Killing this requires forcing `NewWorker` to fail mid-pool — currently
`NewWorker` only fails on `os.WriteFile`, which is hard to make fail for
one worker but not another.

- `runner/pool.go:51, 63, 70`: early-return guards, NewWorker-error skip,
  ctx.Err() early-out in the worker goroutine loop.
- `runner/pool.go:139`: `cmd.Stdout = os.Stderr` — a UX-only sink; dropping
  it changes what the user sees but not what the function returns.

### 6. Subprocess memory-monitor paths

The RSS-kill monitor goroutine in `runner/worker.go` (lines 209–226) runs
concurrently with `cmd.Wait()`, ticks every 200 ms, and only fires when a
mutant's subprocess tree blows past 2 GiB. Mutations in this goroutine
(BRANCH_IF on `memKilled`, CONDITIONALS_BOUNDARY on the `>
maxSubprocRSSBytes` compare) need a mutant that *actually* allocates >2 GiB
within the test timeout. Hard to stage reliably.

- `runner/worker.go:219 (>, memKilled branch)`, `:233 (memKilled.Load())`.

### 7. `Worker.Test` file-write early-returns

```go
if err := os.WriteFile(w.tmpSrcPath, patched, 0o644); err != nil {
    m.Status = StatusNotViable
    m.Duration = nonZeroSince(start)
    return m
}
```

Both `WriteFile` calls land inside `Worker.Test`. Failing only one
requires making one of two writable paths break mid-test — feasible with
a filesystem fault injector but heavy lift.

- `runner/worker.go:114, 117` (in `NewWorker`), `:153, 162` (in `Test`).

### 8. `compileErrorRe` classifier with coupled predicates

```go
if compileErrorRe.MatchString(stderr.String()) &&
    (strings.Contains(stdoutStr, "[build failed]") ||
     strings.Contains(stdoutStr, "[setup failed]")) {
    m.Status = StatusNotViable
    return m
}
```

EXPRESSION_REMOVE on each side lives because the observed pairs are
either `(true, true)` or `(false, false)`. A `[setup failed]` with no
`.go:N:N:` stderr marker would kill the left-side mutation, but the
existing tests don't produce that shape.

- `runner/worker.go:251, 252`.

### 9. Mutations swallowed by downstream error handling

When the mutation changes behavior but the downstream caller *also*
validates and produces an equivalent error, the final observed status
doesn't change.

- `runner/worker.go:180` (`GOMUTANT_TEST_SHORT == "1"` condition): adding
  or dropping `-short` changes which inner tests run. In the tests
  exercising `Worker.Test`, the target test passes under both modes.
- `runner/worker.go:185`: `if w.testMap != nil` — the test-filter is a
  *performance* optimization. Dropping the filter runs all tests in the
  package; final KILLED/LIVED status is identical, only wall time differs.
- `runner/worker.go:191, 196, 202, 229`: arg-append, sysprocattr,
  cmd.Start error path, close(monitorDone). Each is either preparatory
  state or a side-effect whose absence still yields the same classified
  status for the tested mutants.

### 10. Helper that only changes stderr-wrapping

```go
return 0, fmt.Errorf("go list: %w\n%s", err, stderr.String())
```

A mutation on the `strings.TrimSpace` / `line == ""` guards in
`pgroupRSSBytes` live because `ps -o rss= -g <pgid>` on an invalid pgid
returns empty output — the current failure-path assertion cannot
distinguish "no data" from "data I couldn't parse." Same-state classes.

- `coverage/testmap.go:233`, `coverage/testmap.go:61–64` (CONDITIONALS on
  `coverPkg != ""` — kills require inspecting arg order, not just
  exit status, and the existing profile-content check only catches three
  of the five surrounding mutants).

## Where to invest if pushing past 90%

1. **Extract comparators** in `discover.Discover` as package-level funcs
   and unit-test with hand-built slices. Kills ~5 mutants cleanly.
2. **Inject `exec.Command` indirection** in `runner/pool.go` and
   `coverage/testmap.go` to simulate partial failures. Kills ~10 mutants.
3. **Drop the GOMUTANT_TEST_SHORT branch** if no longer needed —
   removing code removes its mutants. (Confirm it's still load-bearing
   for self-testing first.)

The remaining mutants in the runner's memory-monitor path are best left
documented rather than forced — a test that allocates >2 GiB to trigger
the RSS kill path would make the self-test both slow and flaky.
