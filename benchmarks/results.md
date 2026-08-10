# Benchmark Results: gomutants vs Gremlins and Mutago

_Generated: 2026-08-10_

| | |
|---|---|
| Host | Darwin arm64 |
| CPU | Apple M1 Pro |
| Target Go | go1.25.7 darwin/arm64 |
| gomutants | gomutants v0.6.0-rc1+dirty (commit: f77ee074632fd2352f6ef92878a209930b1ce938, built: 2026-08-10T00:09:54Z) |
| Gremlins | github.com/go-gremlins/gremlins v0.6.0 |
| Mutago | github.com/quality-gates/mutago/v2 v2.8.1 |
| workers | 10 |
| timeout coefficient | 50 |
| hyperfine runs per scenario | 3 |

Raw hyperfine output, warm-up logs, and final tool reports are in `benchmarks/out/` (gitignored).

### small-defaults — ./testdata/simple with each tool's default mutator catalog

| Metric | gomutants | Gremlins | Mutago |
|---|---:|---:|---:|
| Wall-clock mean ± σ (s) | 11.01 ± 0.06 | 4.27 ± 0.15 | 11.33 ± 1.90 |
| Mutants reported | 68 | 20 | 63 |
| Killed | 55 | 11 | 54 |
| Lived / escaped | 9 | 3 | 9 |
| Not covered | 0 | 6 | 0 |
| Not viable | 4 | 0 | n/a |
| Timed out | 0 | 0 | n/a |
| Other errors | 0 | 0 | 0¹ |
| Skipped | 0 | 0 | 0 |
| Tested mutants (killed + lived) | 64 | 14 | 63 |
| Time per tested mutant (ms) | 172 | 305 | 180 |

**Pairwise wall clock (default catalogs; workloads differ):** Gremlins was 2.58× faster than gomutants; gomutants was 1.03× faster than Mutago.

### small-matched — ./testdata/simple with the matched 4-operator set

| Metric | gomutants | Gremlins | Mutago |
|---|---:|---:|---:|
| Wall-clock mean ± σ (s) | 4.25 ± 0.06 | 3.95 ± 0.03 | 4.06 ± 0.04 |
| Mutants reported | 18 | 19 | 18 |
| Killed | 15 | 10 | 15 |
| Lived / escaped | 3 | 3 | 3 |
| Not covered | 0 | 6 | 0 |
| Not viable | 0 | 0 | n/a |
| Timed out | 0 | 0 | n/a |
| Other errors | 0 | 0 | 0¹ |
| Skipped | 0 | 0 | 0 |
| Tested mutants (killed + lived) | 18 | 13 | 18 |
| Time per tested mutant (ms) | 236 | 304 | 226 |

**Pairwise wall clock (matched operator semantics):** Gremlins was 1.08× faster than gomutants; Mutago was 1.05× faster than gomutants.

### mutator-matched — ./internal/mutator with the matched 4-operator set

| Metric | gomutants | Gremlins | Mutago |
|---|---:|---:|---:|
| Wall-clock mean ± σ (s) | 14.22 ± 0.13 | 23.16 ± 0.25 | 22.22 ± 7.94 |
| Mutants reported | 84 | 89 | 71 |
| Killed | 82 | 87 | 69 |
| Lived / escaped | 0 | 2 | 2 |
| Not covered | 0 | 0 | 0 |
| Not viable | 2 | 0 | n/a |
| Timed out | 0 | 0 | n/a |
| Other errors | 0 | 0 | 0¹ |
| Skipped | 0 | 0 | 0 |
| Tested mutants (killed + lived) | 82 | 89 | 71 |
| Time per tested mutant (ms) | 173 | 260 | 313 |

**Pairwise wall clock (matched operator semantics):** gomutants was 1.63× faster than Gremlins; gomutants was 1.56× faster than Mutago.

¹ Mutago combines compile failures, execution errors, and timeouts in one
`errored` outcome; its timeout count cannot be separated from the summary.

## Reading the results

- `small-defaults` measures the product-facing default mutator catalogs, not
  equal work. Mutago coverage and per-test routing are enabled because they are
  its strongest native execution mode; gomutants uses its default routing and
  Gremlins always starts with package coverage.
- The matched scenarios use the four operator semantics shared exactly by all
  three tools: arithmetic replacement, conditional boundary, conditional
  negation, and unary-negative inversion. `INCREMENT_DECREMENT` is excluded
  because Mutago does not implement the same `++`/`--` swap.
- The medium matched scenario is the cleanest engine comparison. Total counts
  should still be read alongside wall time: discovery and viability filters can
  differ even when operator transformations are the same.
- Time per tested mutant normalizes wall time by KILLED + LIVED/escaped only.
  It excludes uncovered, non-viable, timed-out, errored, and skipped mutants
  because no completed test verdict exists for them.

## Caveats

- The harness copies the two source targets into an isolated temporary module
  with `go 1.25.7`. Pinned Gremlins v0.6.0 panics while executing these targets
  under Go 1.26.x; running every tool through the pinned compatible toolchain
  keeps the comparison usable.
- Gomutants's persistent cache is disabled. Gremlins and Mutago do not provide
  an equivalent mutant-verdict cache, so this page compares cold execution.
- Gomutants performs one-time coverage, baseline, and per-test-map setup. That
  fixed cost is visible on the tiny fixture and amortizes as mutant count grows.
- The coefficient is 50 because lower Gremlins deadlines misclassify mutants on
  fast packages under worker contention. All tools receive the same value.
- Gooze v1.0.1 is excluded: the audited tag does not compile because its source
  refers to missing coverage-index definitions, so it cannot produce a valid
  end-to-end timing.
- Wall-clock results are sensitive to CPU load, thermal state, and Go build
  cache state. Re-run under quiet conditions before quoting the numbers.
