[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/szhekpisov/gomutants/badge)](https://scorecard.dev/viewer/?uri=github.com/szhekpisov/gomutants)
[![Go Report Card](https://goreportcard.com/badge/github.com/szhekpisov/gomutants)](https://goreportcard.com/report/github.com/szhekpisov/gomutants)
[![codecov](https://codecov.io/gh/szhekpisov/gomutants/graph/badge.svg?token=XNXMEJDGV2)](https://codecov.io/gh/szhekpisov/gomutants)
[![Mutation testing badge](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fbadge-api.stryker-mutator.io%2Fgithub.com%2Fszhekpisov%2Fgomutants%2Fmain)](https://dashboard.stryker-mutator.io/reports/github.com/szhekpisov/gomutants/main)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=szhekpisov_gomutants&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=szhekpisov_gomutants)
[![Security & Static Analysis](https://github.com/szhekpisov/gomutants/actions/workflows/security.yml/badge.svg?branch=main)](https://github.com/szhekpisov/gomutants/actions/workflows/security.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/szhekpisov/gomutants.svg)](https://pkg.go.dev/github.com/szhekpisov/gomutants)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

# gomutants

  A fast mutation tester for Go for those who love flexibility and hate to wait.

  - Best for CI — test only mutants on lines changed vs the parent branch.
  - Best for local testing — incremental cache that makes warm reruns ~120× faster.
  - Fully configurable — specify mutators, packages, and tests you want to run.
  - Built with performance in mind — adaptive timeouts, OOM safety net, and bounded per-worker concurrency that keeps parallel mutants from oversubscribing CPU.

## Table of Contents

- [Why gomutants?](#why-gomutants)
- [Where gomutants isn't the fit?](#where-gomutants-isnt-the-fit)
- [How It Compares](#how-it-compares)
- [Installation](#installation)
  - [Go Install](#go-install)
  - [GitHub Action](#github-action)
  - [Direct binary download](#direct-binary-download)
  - [From Source](#from-source)
  - [Verifying Releases](#verifying-releases)
- [Quick Start](#quick-start)
- [Features](#features)
- [Usage](#usage)
  - [PR-Scoped Mode](#pr-scoped-mode)
  - [Cross-Package Mode](#cross-package-mode)
  - [Stryker-format Reports](#stryker-format-reports)
  - [HTML Reports](#html-reports)
  - [Exit Codes & CI Integration](#exit-codes--ci-integration)
  - [Claude Code Plugin](#claude-code-plugin)
  - [Inline Ignore Directives](#inline-ignore-directives)
  - [Call-Site Exclusion](#call-site-exclusion)
  - [Configuration File](#configuration-file)
  - [Mutators](#mutators)
  - [All Flags](#all-flags)
- [How It Works](#how-it-works)
- [Self-efficacy (gomutants on itself)](#self-efficacy-gomutants-on-itself)
- [Security & Code Quality](#security--code-quality)
- [Contributing](#contributing)
- [License](#license)

## Why gomutants?

* **Built for PR gates.** `--changed-since <ref>` runs mutants on lines added or modified since the given git ref — fast enough to gate every pull request without re-running the full mutation suite on untouched code.

* **Fastest at scale.** On full-module runs with many mutants, gomutants is ~20% faster wall-clock and ~1.7× faster per tested mutant than the nearest Go mutation tester — and warm reruns with the incremental cache enabled finish 120–150× faster than cold runs (e.g. a 46-minute `prometheus/tsdb` cold run becomes 19s warm). See [`docs/performance.md`](docs/performance.md) for methodology and external-target benchmarks.

* **Gets mutation testing right.** Per-test coverage routing runs each mutant only against the tests whose coverage touches the mutated line, not the whole suite. Adaptive per-mutant timeouts kill infinite-loop mutants in seconds, not minutes. Byte-level patches via `go test -overlay` preserve generics and never modify the source tree. 22 mutators including block-level operators (`BRANCH_IF`, `BRANCH_ELSE`, `BRANCH_CASE`, `EXPRESSION_REMOVE`, `STATEMENT_REMOVE`, `LOOP_CONDITION`, `RANGE_BREAK`) surface weak-assertion test gaps that token-level mutation misses.

## Where gomutants isn't the fit?

One-off manual runs or thin test suites (<70% line coverage) — the one-time setup cost (coverage collection, baseline measurement, per-test coverage map build) only pays off when many mutants share it.

## How It Compares

| Feature | gomutants | [gremlins](https://github.com/go-gremlins/gremlins) | [go-mutesting](https://github.com/zimmski/go-mutesting) |
|---|---|---|---|
| Mutators (default set) | 16 | 5 | 6 |
| Block-level mutators | yes | no | no |
| Generics support | yes (byte-patching) | partial[^1] | no |
| `--changed-since <ref>` | first-class | no | no |
| Per-test coverage routing | yes | no | no |
| Incremental cache | yes (on by default) | no | no |
| `NOT_VIABLE` classification | yes | no[^2] | partial |
| Equivalent-mutant detection (TCE) | yes (opt-in) | no | no |
| OOM-safe subprocess control | 2 GiB RSS cap, process group | no | no |
| gremlins-compatible JSON | yes | (native) | no |
| Stryker dashboard format | yes | no | no |
| Self-contained HTML report | yes | no | no |
| Per-mutant timeout | yes (adaptive) | yes (fixed) | yes (fixed) |
| Active maintenance | yes | yes | minimal |

[^1]: gremlins uses AST rewriting; some generic constructs round-trip incorrectly.
[^2]: Compile-failing mutants are silently dropped, so they don't appear in the report at all — they neither contribute to the kill count nor surface as a separate category.

### Mutator-set equivalence

gomutants is a strict superset of [ooze](https://github.com/gtramontina/ooze) v0.2.0 and [gremlins](https://github.com/go-gremlins/gremlins) v0.6.0: more mutators overall, and on the mutators they share it generates the same positions (or more, for ooze). Position-level reports: ooze on [uuid](docs/equivalence/ooze/uuid.md); gremlins on [uuid](docs/equivalence/gremlins/uuid.md) and [cobra](docs/equivalence/gremlins/cobra.md).

### Benchmark snapshot

Four real-world Go projects on Apple M1 Pro 10-core, gomutants v0.2.2 vs gremlins v0.6.0, matched 5-operator set (gremlins' defaults), `workers=10`, `--cache=off`, `GOTOOLCHAIN=go1.25.7` (gremlins is broken on Go 1.26.x). Engine and gremlins rows are 3-run medians; cold-OOB rows on the larger targets are single-run.

**Engine wall-clock (cold cache, like-for-like operators):**

| Target | gremlins | gomutants | Speedup |
|---|---:|---:|---:|
| google/uuid (~2.3k LOC, 1 pkg) | 27.5 s | 29.7 s | 0.93× |
| spf13/cobra (~6k LOC, 1 pkg) | 129 s | **73 s** | **1.78×** |
| prometheus/model/labels (~4k LOC, 1 pkg) | 139 s | **90 s** | **1.55×** |
| prometheus tsdb-4 (~24k LOC, 4 pkgs) | 951 s¹ | 855 s | 1.11× |

¹ gremlins's `unleash` accepts only one target argument, so its tsdb-4 row sums 4 per-subpackage invocations; gomutants's row is a single multi-package run.

**Warm-cache rerun (full out-of-the-box workload, cache on)** — the inner edit/test loop where gomutants short-circuits unchanged mutants via the content-addressed cache. gremlins has no equivalent.

| Target | Cold OOB | Warm rerun | Speedup |
|---|---:|---:|---:|
| google/uuid | 77 s | 3.2 s | ~24× |
| spf13/cobra | 410 s | **2.7 s** | **~150×** |
| prometheus/model/labels | 342 s | **2.8 s** | **~120×** |
| prometheus tsdb-4 | 2768 s (~46 min) | **19 s** | **~145×** |

**Reading the numbers:**

- **Engine ordering depends on package size.** Roughly tied on uuid (~120 mutants), 1.5–1.8× faster on medium single-package targets where gomutants's pre-built test binary amortizes across many mutants, tied again on the 4-package multi-target where one-shot setup balances against gremlins's per-subpackage setup paid 4×.
- **Adaptive per-mutant timeouts win on contended runs.** Gremlins ran 26% of uuid mutants into its `--timeout-coefficient=20` ceiling under worker contention; gomutants ran 2.5%. Same pattern on tsdb-4 (196 vs 45 timeouts).

See [`docs/performance.md`](docs/performance.md) for full per-target tables, NOT_COVERED interpretation differences, Go 1.26 compatibility notes, and reproduction commands. The in-repo [`benchmarks/results.md`](benchmarks/results.md) covers `./testdata/simple/` and other in-repo targets.

## Installation

### Go Install

```bash
go install github.com/szhekpisov/gomutants@5741a097e347d75afdd7894464e8c2f612281dd4 # v0.5.0
```

Make sure `$GOPATH/bin` is in your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

The minimum supported version of Go for gomutants is **1.26**, both for building gomutants itself and for the project under test (gomutants shells out to `go test` in your project's toolchain). macOS and Linux on amd64/arm64 are tested in CI; Windows works wherever `go` does, though it isn't covered by automated tests.

### GitHub Action

gomutants is published as a composite action:

```yaml
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
  with:
    fetch-depth: 0  # required so --changed-since can reach the base ref
- uses: szhekpisov/gomutants@5741a097e347d75afdd7894464e8c2f612281dd4 # v0.5.0
  with:
    args: --changed-since origin/${{ github.base_ref }} ./...
```

Each LIVED mutant on a changed line is emitted as a `::warning file=...,line=...::` workflow command, which GitHub renders inline on the "Files changed" view. The action fails on any LIVED mutant by default (`threshold-efficacy: 100`); set `threshold-efficacy: ""` to surface annotations without failing the job.

| Input | Default | Description |
|---|---|---|
| `args` | _required_ | Arguments forwarded to `gomutants`. The action appends `--annotations=github` automatically. |
| `version` | `latest` | gomutants version to install. With `version: latest` the action keeps a pre-installed binary on PATH; with any pinned tag/branch/SHA it always re-installs so what runs matches what was requested. |
| `threshold-efficacy` | `100` | Minimum test efficacy `%` (`KILLED/(KILLED+LIVED)`). Below threshold → exit 10. Default `100` fails the step on any LIVED mutant; set to `""` to disable. |
| `threshold-mcover` | _empty_ | Minimum mutant coverage `%` (`(KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED)`). Below threshold → exit 11. Empty disables. |
| `working-directory` | `.` | Directory containing `go.mod`. |
| `cache` | `.gomutants-cache.json` | Path to the incremental-analysis cache file. Set to `off` to disable. Pair with [`actions/cache`](https://github.com/actions/cache) to persist across CI runs. |

See [`action.yml`](action.yml) for the full composite definition.

### Direct binary download

Binaries for Linux and macOS (amd64 and arm64) are attached to every [release](https://github.com/szhekpisov/gomutants/releases):

```bash
VERSION=0.2.2  # check the releases page for the latest
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fL "https://github.com/szhekpisov/gomutants/releases/download/v${VERSION}/gomutants_${VERSION}_${OS}_${ARCH}.tar.gz" \
  | tar -xz
sudo mv gomutants /usr/local/bin/
```

See [Verifying Releases](#verifying-releases) below to check signatures and provenance before installing.

### From Source

```bash
git clone https://github.com/szhekpisov/gomutants.git
cd gomutants
go build ./...
./gomutants --version
```

### Verifying Releases

Published release artifacts are append-only and signed. Every release includes:

- **Checksums** (`checksums.txt`) — SHA256 hashes for all archives
- **Cosign signature** (`checksums.txt.sigstore.json`) — keyless Sigstore signature
- **SBOMs** (`*.spdx.json`) — SPDX Software Bill of Materials for each archive
- **SLSA provenance** — Level 3 provenance attestation

<details>
<summary>Verification commands</summary>

**Verify the checksums signature:**

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/szhekpisov/gomutants/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

# Linux
sha256sum --check checksums.txt --ignore-missing
# macOS
shasum -a 256 --check checksums.txt --ignore-missing
```

**Verify SLSA provenance:**

```bash
gh attestation verify gomutants_<VERSION>_linux_amd64.tar.gz \
  --repo szhekpisov/gomutants
```

</details>

## Quick Start

```bash
# Run on the whole module.
gomutants ./...

# Run only on lines this PR changes.
gomutants --changed-since origin/main ./...

# Near drop-in for gremlins users:
gomutants unleash ./...

# Use in CI — exit code 10 if efficacy falls below the threshold:
gomutants --threshold-efficacy 80 ./...
```

## Features

- **`--changed-since <ref>`** — scope mutation testing to lines changed vs a git ref. Fast enough to gate every PR.
- **Per-test coverage routing** — each mutant runs only the tests whose coverage touches the mutated line, not the whole suite.
- **Incremental cache** — content-addressed; warm reruns skip mutants whose source bytes and tests are byte-identical to the previous run (120–150× speedup on warm reruns).
- **Resumable runs** — the cache is checkpointed mid-run, so a run killed by an OOM, a CI timeout, or a double Ctrl-C resumes from the last checkpoint instead of starting over.
- **Adaptive per-mutant timeouts** — deadlines sized from recorded per-test durations × margin, so fast tests don't wait out a multi-minute global ceiling.
- **Byte-level patching via `go test -overlay`** — generics and all Go syntax survive intact; source tree never modified.
- **22 mutators including block-level** — `BRANCH_IF`, `BRANCH_ELSE`, `BRANCH_CASE`, `EXPRESSION_REMOVE`, `STATEMENT_REMOVE`, `LOOP_CONDITION`, `RANGE_BREAK` on top of 15 token-level operators (arithmetic, bitwise, comparison, logical, loop control, literal increment/decrement).
- **OOM-safe** — each `go test` child runs in its own process group with a 2 GiB RSS cap; output capped at 1 MiB per stream.
- **Multiple report formats** — gremlins-compatible JSON (default), [Stryker `mutation-testing-elements` v2](https://github.com/stryker-mutator/mutation-testing-elements) JSON, and a self-contained interactive HTML report.
- **Conservative discovery** — compile-failing mutants surface as `NOT_VIABLE` and don't inflate efficacy.
- **Equivalent-mutant detection (opt-in)** — `--detect-equivalent` recompiles each survivor with `-gcflags=-S` and reclassifies it as `EQUIVALENT` when the generated assembly matches the original (Trivial Compiler Equivalence). Such mutants can't be killed by any test, so they drop out of the efficacy denominator instead of failing the gate. Sound: a killable mutant is never marked equivalent.
- **Cross-package routing (opt-in)** — `--integration` extends per-test routing across package boundaries so a mutant is killed by a covering test in *any* importing package (cross-package/E2E tests), not just its own. See [Cross-Package Mode](#cross-package-mode).
- **Inline ignore directives** — `// gomutants:disable*` comments suppress specific mutants by line, function, or regex.
- **Call-site exclusion** — `--exclude-calls` drops mutants inside calls matching a selector glob, so operators in logging arguments stop counting as test gaps. Covers Go's standard-library logging out of the box; the Go analogue of PITest's `avoidCallsTo`. See [Call-Site Exclusion](#call-site-exclusion).
- **GitHub Action** — surfaces surviving mutants as inline annotations on the PR diff.
- **Claude Code plugin** — `/gomutants:mutants` slash command runs gomutants on changed code and proposes concrete `*_test.go` cases that would kill each surviving mutant.

## Usage

```bash
gomutants [flags] <package patterns>
```

### PR-Scoped Mode

`--changed-since <ref>` scopes a run to mutants on lines added or modified since the given git ref:

```bash
# CI: gate every PR on changed lines only
gomutants --changed-since origin/main ./...

# Local: see what changed since the last commit
gomutants --changed-since HEAD~1 ./...
```

The flag runs `git diff --unified=0 <ref>` and keeps only mutants on added/modified lines. Combine with `--threshold-efficacy 100` to fail on any LIVED mutant on changed lines. A typical setup runs `--changed-since` per PR and the full tree post-merge; see [`.github/workflows/mutation.yml`](.github/workflows/mutation.yml) for an example.

### Cross-Package Mode

By default each mutant runs only against the tests in **its own package** whose coverage touches the mutated line. That's the fast path, and usually the right one: a mutant that only a downstream test kills means the mutated package's own tests are weak, and the `LIVED` report is the nudge to strengthen them.

But some architectures legitimately assert behavior across package boundaries — thin wiring/glue packages, generated code, or a black-box/E2E suite that tests through the public API. There, a mutant's only killer lives in an *importing* package, and per-package routing reports it `LIVED` (a false survivor) or `NOT COVERED`.

`--integration` closes that gap:

```bash
gomutants --integration ./...
```

It routes each mutant to the covering tests in **any** package that imports it. Concretely it:

- computes the reverse-dependency closure of the target packages (every package whose imports *or test imports* reach a target) and widens coverage collection and the per-test build to that set;
- pins `-coverpkg` to the target packages so importing tests record coverage on the mutated code (passing `--coverpkg` as well is an error);
- runs each covering package's tests in its own `go test` invocation, short-circuiting on the first kill.

Trade-offs:

- **Slower.** The per-test coverage build expands to the reverse-dependency closure, and `-coverpkg` instrumentation adds overhead. The closure keeps this bounded to packages that can actually reach a target, but on large modules it's a real cost.
- **Scores aren't comparable** to a non-integration run: mutants flip `LIVED`/`NOT COVERED` → `KILLED`, raising efficacy and mutant coverage. Pick one mode per gate.
- Default (per-package) routing remains the recommended path; reach for `--integration` only when your suite deliberately asserts behavior across package boundaries.

### Stryker-format Reports

```bash
gomutants --stryker-output stryker-report.json ./...
```

Writes a [mutation-testing-elements v2](https://github.com/stryker-mutator/mutation-testing-elements) report alongside the gremlins-format JSON. The same file feeds:

- The [`<mutation-test-report-app>`](https://www.npmjs.com/package/mutation-testing-elements) web component, which renders an interactive HTML view when embedded in a page with `src="stryker-report.json"`.
- The [Stryker Dashboard](https://stryker-mutator.io/docs/General/dashboard/), which hosts the report and serves a mutation-score badge:

```bash
curl -X PUT \
  -H "X-Api-Key: $STRYKER_DASHBOARD_KEY" \
  -H "Content-Type: application/json" \
  --data @stryker-report.json \
  "https://dashboard.stryker-mutator.io/api/reports/github.com/<org>/<repo>/<branch-or-sha>"
```

Once registered on `dashboard.stryker-mutator.io`, your project gets a `mutationScoreBadge` URL you can drop in this README — the same surface PIT, Stryker (JS/.NET/Scala), and Infection PHP plug into.

### HTML Reports

```bash
gomutants --html-output mutation-report.html ./...
```

Writes a single self-contained HTML file. Open it in any browser — no web server, no network access, no companion JSON file. The page bundles the [`<mutation-test-report-app>`](https://www.npmjs.com/package/mutation-testing-elements) web component and the report data into one document, so it works as a CI artifact you can upload from a job and link to from a PR check.

Inside, you get a per-file efficacy sidebar and click-through annotated source: each mutated line is highlighted with the mutator name, status (KILLED / SURVIVED / NO_COVERAGE / TIMEOUT / COMPILE_ERROR), and the original-vs-replacement diff.

If you already publish to the [Stryker Dashboard](https://stryker-mutator.io/docs/General/dashboard/) you don't need this flag — the dashboard renders the same report with history and a hosted badge. `--html-output` is for local viewing and CI artifacts, especially in air-gapped environments where uploading to a third-party dashboard isn't an option.

### Exit Codes & CI Integration

| Exit code | Meaning |
|-----------|---------|
| `0` | Success |
| `1` | Runtime, build, or target error |
| `2` | Usage or configuration error (unknown/invalid flags, conflicting options, invalid config) |
| `10` | Below `--threshold-efficacy` (gremlins-compat) |
| `11` | Below `--threshold-mcover` (gremlins-compat) |

```bash
gomutants --threshold-efficacy 80 --threshold-mcover 90 ./...
```

`test_efficacy = killed / (killed + lived)` — excludes `not_viable`, `not_covered`, and `timed_out`.
`mutations_coverage = (killed + lived) / (killed + lived + not_covered)`.

### Claude Code Plugin

This repo ships a [Claude Code](https://claude.com/claude-code) plugin that exposes a `/gomutants:mutants` slash command. It runs gomutants on changed code, parses the JSON report, and proposes concrete `*_test.go` cases that would kill each surviving mutant — without editing any files. It also writes a self-contained interactive HTML report (the same one `--html-output` produces) to `/tmp/gomutants-report.html` for click-through inspection.

Install:

```text
/plugin marketplace add szhekpisov/gomutants
/plugin install gomutants@gomutants
```

Use:

```text
/gomutants:mutants                    # default: --changed-since main ./...
/gomutants:mutants ./internal/foo     # scope to a package
/gomutants:mutants --since HEAD~1     # scope by git ref
```

The plugin assumes `gomutants` is on `PATH` (`go install github.com/szhekpisov/gomutants@latest`), and falls back to `go run github.com/szhekpisov/gomutants@latest` otherwise. Plugin sources live under [`plugin/`](plugin/); the marketplace manifest is at [`.claude-plugin/marketplace.json`](.claude-plugin/marketplace.json).

### Inline Ignore Directives

Annotate Go source with `// gomutants:disable*` comments to silence specific mutants. Suppressed mutants are dropped from the run entirely — they don't appear in any status bucket and don't affect `test_efficacy` or `mutations_coverage`. The aggregate count surfaces as `mutants_suppressed` in the JSON report and on the terminal summary.

For a project-wide policy rather than a per-site annotation — "never mutate inside logging calls" — see [Call-Site Exclusion](#call-site-exclusion).

Four forms:

```go
// Same line — suppress every (or one) mutator on the line of the directive.
return a + b // gomutants:disable
return a + b // gomutants:disable ARITHMETIC_BASE reason="commutative"
return a + b // gomutants:disable ARITHMETIC_BASE,INVERT_NEGATIVES

// Next line — suppress mutators on the first non-blank, non-comment line that follows.
// gomutants:disable-next-line CONDITIONALS_NEGATION reason="branch always taken in prod"
if debugMode { ... }

// Function — when placed as the doc-comment of a func, suppresses every mutant in the body.
// gomutants:disable-func reason="generated code"
func gen() { ... }

// Regexp — anywhere in the file; suppresses mutants on lines whose source text matches.
// gomutants:disable-regexp ^\s*log\. reason="logging is not behaviour"
```

<details>
<summary>Grammar and edge cases</summary>

```text
DIRECTIVE  = "// gomutants:" KIND [ WS PATTERN ] [ WS MUTATORS ] [ WS "reason=" QUOTED ]
KIND       = "disable" | "disable-next-line" | "disable-func" | "disable-regexp"
PATTERN    = present only for "disable-regexp"; first whitespace-delimited token after the kind, RE2 syntax
MUTATORS   = ( MUTATOR ("," MUTATOR)* ) | "*"   // upper-case mutator type names; "*" = all
QUOTED     = any Go-quoted string ("...", `...`, or 'c') with standard escape handling
```

- Omitting `MUTATORS` (or supplying `*`) suppresses every mutator at the directive's target.
- `reason="..."` is optional; recommended for self-documentation. Reasons surface to stderr under `--verbose`.
- Unknown mutator name → warning to stderr, that name is dropped, the rest of the directive still applies. If *every* named mutator is unknown, the directive is dropped entirely with a summary warning — a typo like `TYPP_O` must not silently disable every mutator on the line. Forward-compatible across mutator renames (rename one mutator at a time; stale names are individually skipped).
- `disable-func` placed on a non-function comment → warning, directive ignored.
- `disable-regexp` with an invalid pattern → warning, directive ignored.
- `disable-next-line` on the last line of a file (or with only blanks/comments after it) → warning, directive ignored.
- Patterns with whitespace are not supported in v1; use `\s` instead.
- Multiple `// gomutants:` directives on a single physical line are not supported (Go treats them as one comment). Combine with a comma list (`disable A,B`) or `*` instead.

</details>

### Call-Site Exclusion

Some mutants are real code changes that no reasonable test can catch. The canonical case is arithmetic inside a logging argument:

```go
log.Printf("imported %d/%d rows (%.1f%%)", done, total, float64(done)/float64(total)*100)
```

Every operator there gets an `ARITHMETIC_BASE` mutant. No test asserts the log text, so they LIVE forever, dragging `test_efficacy` down and failing gates for code whose only observable effect is a log line. They aren't equivalent either — the emitted text genuinely differs — so `--detect-equivalent` correctly leaves them in the denominator.

`--exclude-calls` closes the gap between file-level exclusion and per-line directives: mutants inside a matching call are dropped before any test runs, and counted in `mutants_suppressed`.

**This is on by default for Go's standard-library logging** — no setup needed for the common case:

```
log.Print*   log.Output
slog.Debug*  slog.Info*  slog.Warn*  slog.Error*  slog.Log*
```

Add your own logger or telemetry wrappers; your list *extends* the built-ins:

```yaml
# .gomutants.yml
exclude-calls:
  - "*.Debug"          # any receiver's Debug method
  - "metrics.*"        # a whole package
  - "tracer.Record"    # one exact call
```

```bash
gomutants --exclude-calls 'log.Print*,*.Debug' ./...

# Narrow or replace the built-ins instead of extending them:
gomutants --exclude-calls 'mylog.*' --exclude-calls-defaults=false ./...
```

Patterns are **globs, not regexps** — every character is literal except `*`, which matches any run of characters. Matching is anchored, so `log.Print` matches only `log.Print`, and `log.Print*` is what also catches `log.Printf` / `log.Println`.

<details>
<summary>What a pattern is matched against, and what gets suppressed</summary>

The pattern is matched against the call's selector as written in the source:

| Call | Selector | Matched by |
|------|----------|------------|
| `log.Printf(…)` | `log.Printf` | `log.Print*`, `log.*` |
| `logger.Debug(…)` | `logger.Debug` | `*.Debug` |
| `s.log.Errorf(…)` | `s.log.Errorf` | `*.Errorf` |
| `getLogger().Info(…)` | `_.Info` | `*.Info` (the receiver has no name to match) |
| `println(…)` | `println` | `println` |

- **The whole call expression is suppressed, not just the argument list.** That includes `STATEMENT_REMOVE` of a bare `log.Printf(...)` statement — deleting a log line is as unkillable as mutating what it prints — and anything nested inside the arguments.
- **`log.Fatal*` and `log.Panic*` are deliberately not in the default set.** They exit or panic, so deleting one is a real behavioural change your tests can and should catch. Same for `slog.New` / `slog.SetDefault` / `slog.With`, which configure rather than emit.
- **Method-shaped patterns like `*.Info` are not defaults either** — they would reach `err.Error()` and any domain method sharing the name. Opt in per project.
- **Matching is syntactic, with no type resolution.** An aliased import (`import stdlog "log"`) renders under its alias, so add `stdlog.*` if you use one. A local variable named `log` would also match `log.*`.
- Suppressed mutants leave every count and both denominators, exactly like directive-suppressed ones. The terminal summary and `mutants_suppressed_by_calls` in the JSON report break out how many came from here rather than from `// gomutants:disable*`.
- A pattern of only asterisks is rejected: it would suppress nearly every mutant in the module, which is never intended and would otherwise pass silently.

</details>

### Configuration File

`.gomutants.yml` in the project root:

```yaml
workers: 10
test-cpu: 0             # 0 = let go test use GOMAXPROCS
timeout-coefficient: 10
adaptive-timeout: true  # per-test adaptive sizing; set false for single global timeout
timeout-margin: 3.0     # multiplier on per-test sums (only when adaptive)
timeout-min: 2s         # floor on per-mutant adaptive timeout
coverpkg: "./pkg/mypackage/..."
tags: ""                # build tags forwarded to the inner go list/go test (e.g. "integration,debug")
test-flags: ""          # flags forwarded to the inner go test only (e.g. "-short")
output: mutation-report.json
changed-since: ""       # set to e.g. "main" to scope runs by default
integration: false      # cross-package routing; manages -coverpkg itself (don't also set coverpkg)
cache: ""               # path to incremental-analysis cache; "" = .gomutants-cache.json, "off" = disabled
checkpoint-interval: 10s # how often to flush the cache mid-run; 0s disables (final flush still runs)
disable: []
only: []
exclude-calls: []            # selector globs; extends the built-in stdlib-logging set
exclude-calls-defaults: true # false = drop the built-ins and use only the list above
```

Priority: built-in defaults < config file < CLI flags. See [`.gomutants.yml.example`](.gomutants.yml.example) for a complete reference.

### Mutators

**Token-level:**

| Type | Description | Example |
|------|-------------|---------|
| `ARITHMETIC_BASE` | Swap arithmetic operators | `+` <-> `-`, `*` <-> `/`, `%` <-> `*` |
| `CONDITIONALS_BOUNDARY` | Relax/tighten boundaries | `<` <-> `<=`, `>` <-> `>=` |
| `CONDITIONALS_NEGATION` | Negate comparisons | `==` <-> `!=`, `<` <-> `>=`, `>` <-> `<=` |
| `INCREMENT_DECREMENT` | Swap increment/decrement | `++` <-> `--` |
| `INVERT_NEGATIVES` | Invert negation | `-x` -> `+x`, `a - b` -> `a + b` |
| `INVERT_ASSIGNMENTS` | Swap arithmetic compound assignments | `+=` <-> `-=`, `*=` <-> `/=`, `%=` -> `*=` |
| `INVERT_BITWISE` | Swap bitwise binary operators | `&` <-> `\|`, `^` -> `&`, `<<` <-> `>>` |
| `INVERT_BITWISE_ASSIGNMENTS` | Swap bitwise compound assignments | `&=` <-> `\|=`, `^=` -> `&=`, `<<=` <-> `>>=` |
| `INVERT_LOGICAL` | Swap logical operators | `&&` <-> `\|\|` |
| `INVERT_LOOP_CTRL` | Swap loop control | `break` <-> `continue` |
| `REMOVE_SELF_ASSIGNMENTS` | Drop op from compound assignment | `x += y` -> `x = y` |
| `INTEGER_INCREMENT` | Increment integer literal | `42` -> `43`, `0xFF` -> `256` |
| `INTEGER_DECREMENT` | Decrement integer literal | `42` -> `41`, `0` -> `-1` |
| `FLOAT_INCREMENT` | Increment float literal | `1.5` -> `2.5`, `0.0` -> `1.0` |
| `FLOAT_DECREMENT` | Decrement float literal | `1.5` -> `0.5`, `1e2` -> `99.0` |

**Block-level:**

| Type | Description | Example |
|------|-------------|---------|
| `BRANCH_IF` | Empty if/else-if body | `if x { doStuff() }` -> `if x { _ = 0 }` |
| `BRANCH_ELSE` | Empty else body | `else { doStuff() }` -> `else { _ = 0 }` |
| `BRANCH_CASE` | Empty case body | `case 1: doStuff()` -> `case 1: _ = 0` |
| `EXPRESSION_REMOVE` | Remove boolean operand | `a && b` -> `true && b` / `a && true` |
| `STATEMENT_REMOVE` | Remove statement effect | `x = expr` -> `_ = expr`, `f()` -> `_ = 0` |
| `LOOP_CONDITION` | Force for-loop condition to false | `for i := 0; i < n; i++ {}` -> `for i := 0; false; i++ {}` |
| `RANGE_BREAK` | Insert early break in for…range body | `for _, v := range xs { f(v) }` -> `for _, v := range xs { break; f(v) }` |

**Mutant statuses:**

| Status | Meaning |
|--------|---------|
| KILLED | Test failed — mutant detected |
| LIVED | Tests passed — **test gap** |
| NOT COVERED | No test covers the mutated line |
| NOT VIABLE | Mutation causes a compile error (filtered, not counted as a kill) |
| TIMED OUT | Test execution exceeded the per-mutant timeout |

### All Flags

<details>
<summary>Complete flag reference</summary>

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--workers` | `-w` | NumCPU | Parallel workers |
| `--test-cpu` | | 0 (omit) | Value passed to inner `go test -cpu` per mutant; 0 lets go test use `GOMAXPROCS` |
| `--timeout-coefficient` | | 10 | Multiplier applied to baseline test time for the **global timeout ceiling** (also the per-mutant timeout when `--adaptive-timeout=false`) |
| `--adaptive-timeout` | | true | Use the per-test durations recorded during the coverage build to size each mutant's timeout. Pass `=false` to fall back to the single global ceiling. |
| `--timeout-margin` | | 3.0 | When adaptive: `per-mutant timeout = sum(selected test durations) × this`, clamped to `[--timeout-min, --timeout-coefficient × baseline]` |
| `--timeout-min` | | 2s | Floor for the per-mutant adaptive timeout. Absorbs cold-start, child fork, and GC pause overhead that doesn't scale with the underlying test work. |
| `--coverpkg` | | | Coverage package pattern (forwarded to `go test -coverpkg`) |
| `--tags` | | | Comma-separated build tags forwarded as `-tags` to every inner `go list` / `go test` (including `go test -c`/`-list`), so mutation testing reaches code behind `//go:build` constraints (gremlins-compat) |
| `--test-flags` | | | Flags forwarded verbatim to the inner `go test` runs and to nothing else. Whitespace-separated and repeatable. Part of the cache identity, so changing the value discards cached verdicts rather than replaying them. Setting it also stands adaptive timeouts down to the global ceiling, since the per-test timings are measured without these flags. Use it to trade mutation fidelity for speed on property-based suites — see [Speeding up property-based suites](#speeding-up-property-based-suites) |
| `--output` | `-o` | `mutation-report.json` | JSON report path |
| `--config` | | `.gomutants.yml` | Config file path |
| `--disable` | | | Comma-separated mutator types to disable |
| `--only` | | | Comma-separated mutator types to run (disables all others) |
| `--exclude-files` | | | Comma-separated regexps; skip mutating production files whose module-relative path matches any. Unanchored (e.g. `vendor/` hits anywhere). Excluded files produce no mutants and are never parsed. |
| `--exclude-calls` | | | Comma-separated selector globs; suppress mutants inside calls whose selector matches any (e.g. `log.Print*,*.Debug`). Anchored; `*` is the only metacharacter. Extends the built-in stdlib-logging set. Suppressed mutants are dropped before any test runs. See [Call-Site Exclusion](#call-site-exclusion). |
| `--exclude-calls-defaults` | | true | Apply the built-in `--exclude-calls` set for Go's standard-library logging (`log.Print*`, `slog.Info*`, …). Pass `=false` to narrow or fully replace it. |
| `--changed-since` | | | Only test mutants on lines changed vs git ref (e.g. `main`, `HEAD~1`); requires a git repo |
| `--cache` | | `.gomutants-cache.json` | Path to incremental-analysis cache file. Skips mutants whose source and tests are byte-identical to the cached run. Pass `--cache=off` to disable. |
| `--checkpoint-interval` | | 10s | How often to flush completed mutant outcomes to the cache mid-run, so a hard kill (OOM, CI timeout, SIGKILL) loses at most this much progress and the next run resumes from the last checkpoint. `0` disables periodic checkpointing (the cache is then written only once, at the end). Ignored when `--cache=off`. |
| `--detect-equivalent` | | false | After testing, recompile each surviving mutant with package-scoped `-gcflags=-S` and reclassify it as `EQUIVALENT` when the generated assembly is identical to the original (Trivial Compiler Equivalence). Equivalent mutants can't be killed by any test, so they're dropped from the efficacy denominator. Adds one package compile per survivor. |
| `--integration` | | false | Route each mutant to covering tests in *any* package that imports it, not just its own. Widens coverage and the per-test build to the reverse-dependency closure of the target packages and manages `-coverpkg` itself (passing `--coverpkg` too is an error). Lets a mutant be killed by a cross-package/E2E test. See [Cross-Package Mode](#cross-package-mode). |
| `--annotations` | | | Emit annotations for LIVED mutants. Supported: `github` (workflow-command warnings on stdout). |
| `--stryker-output` | | | Also write a [Stryker mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) report at this path (for the HTML viewer and Stryker Dashboard). |
| `--html-output` | | | Also write a self-contained interactive HTML mutation report at this path (Stryker mutation-testing-elements viewer bundled inline; no network access required to open). |
| `--threshold-efficacy` | | 0 | Minimum test efficacy (KILLED/(KILLED+LIVED)). Below threshold → exit 10 (gremlins-compat). 0 disables. |
| `--threshold-mcover` | | 0 | Minimum mutant coverage ((KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED)). Below threshold → exit 11 (gremlins-compat). 0 disables. |
| `--dry-run` | | false | List mutants without testing |
| `--verbose` | `-v` | false | Stream each mutant as tested |
| `--quiet` | `-q` | false | Suppress header, phase lines, and per-mutant progress; only the final summary lands on stdout (warnings still go to stderr). Mutually exclusive with `--verbose`. |
| `--version` | | | Print version and exit |

</details>

Common invocations:

```bash
# Default: run on all packages with NumCPU workers.
gomutants ./...

# Faster CI: only mutants on lines this PR changes.
gomutants --changed-since origin/main ./...

# Local exploration: see what would be tested without running.
gomutants --dry-run ./...

# Verbose stream of every mutant as it completes.
gomutants -v ./...

# Quiet for CI: only the final summary on stdout (exit code still gates).
gomutants -q --threshold-efficacy 80 ./...

# Limit to specific mutators (or exclude some).
gomutants --only ARITHMETIC_BASE,CONDITIONALS_NEGATION ./...
gomutants --disable BRANCH_IF,BRANCH_ELSE ./...

# Skip generated code and vendored deps.
gomutants --exclude-files 'vendor/,_gen\.go$' ./...

# Teach it about your own logger on top of the built-in stdlib set.
gomutants --exclude-calls '*.Debug,*.Infof' ./...

# Tune for memory-tight runners.
gomutants --workers=2 ./...

# Give each go test more CPU lanes (paired with low --workers).
gomutants --workers=1 --test-cpu=8 ./...
```

`gomutants unleash ./...` is accepted unchanged for gremlins-compat scripts.

### Speeding up property-based suites

Property-based tests are the worst case for mutation testing: every mutant on
a covered line re-runs the whole property. At the iteration counts these
frameworks default to — commonly 100 — the run becomes dominated by iteration
rather than by mutants.

`--test-flags` forwards flags to the inner `go test`, so you can dial that
down explicitly, with `-short` or with whatever flag your property framework
exposes for its iteration count:

```bash
# Cheaper per-mutant runs; composes with --changed-since for a pre-push gate.
gomutants --changed-since main --test-flags '-short' ./...
```

Four things to know:

- **`go test` only.** The flags reach the per-mutant runs, the coverage run,
  and the baseline run — never `go list` or the build steps. This is why
  `GOFLAGS` is not a workaround. Go applies a GOFLAGS entry only "when the
  given flag is known by the current command" (`go help environment`), so
  you cannot say which invocations a flag reaches: `-short` is silently
  ignored by `go list` and `go test -c`, while `-race` is honored by them.
  GOFLAGS also bypasses the managed-flag check below — `GOFLAGS=-run=TestFoo`
  narrows the coverage run and every per-mutant run with no diagnostic,
  which `--test-flags -run=TestFoo` rejects outright.
- **`-short` narrows coverage too, deliberately.** The coverage run sees the
  same flags, so a test that `-short` skips is not recorded as covering. Its
  lines report as `NOT_COVERED` rather than as false survivors — an honest
  gap instead of a misleading one.
- **Changing the value invalidates the cache.** `--test-flags` is part of the
  cache's identity, so alternating between a cheap gate run and a full scoring
  run does not replay one's verdicts as the other's. The cost is that each
  value keeps its own cache generation: switching back and forth re-runs from
  cold rather than resuming. That is the intended trade — a mutant that
  survived 20 property iterations says nothing about whether it survives 100.
  Whitespace-only differences don't count as a change. Flag order does:
  arbitrary test-binary flags can interact while they are parsed, so the
  cache conservatively keeps `-race -short` and `-short -race` separate.
- **The per-test timing phase does not see the flags,** so adaptive timeouts
  stand down. That phase compiles with `go test -c` and runs the binary
  directly with `-test.*`-namespaced flags, where `-short` would need
  translating. Its durations therefore describe a run your flags have
  changed, which is unusable as a deadline in either direction — under
  `-race` a deadline measured without it would fire early, turning survivors
  into `TIMED_OUT` and quietly dropping them out of the efficacy
  denominator. With `--test-flags` set, every mutant gets the global
  `baseline × --timeout-coefficient` ceiling instead (the baseline *is*
  measured with your flags), exactly as under `--adaptive-timeout=false`.
  This also caps the speedup: the timing phase runs each test once at full
  cost regardless, so a suite dominated by it improves less than the
  per-mutant arithmetic suggests.

Your flags are placed **after** the package argument, which is what lets
flags belonging to the *test binary* rather than to `go test` work at all.
`go test` goes on claiming its own flags past one it does not recognise,
but that first unrecognised flag marks the package list as already seen —
so a package named after it is forwarded to the test binary as a
positional argument and the package list falls back to `.`. With
`-rapid.checks=100` ahead of the package, the working directory would be
tested instead. Your order is preserved both on argv and in the cache
identity. Go reads the last occurrence of a repeated flag wherever the
package sits, and distinct custom flags can also share state or otherwise
interact while they are parsed.

Before an `-args` boundary, flags gomutants manages itself (`-overlay`,
`-run`, `-timeout`, `-coverprofile`, `-coverpkg`, `-c`, `-o`, `-exec`) are
rejected rather than silently honored. These conflicts can fail quietly; for
example, replacing `-overlay` means no mutant is ever applied and every one
"survives". The `-test.`-prefixed spellings are rejected too — `go test` hands `-test.run`
straight to the test binary, where it beats the `-run` filter gomutants
computed.

`-args` is supported for the rarer case where a custom test-binary flag has
the same name as a flag owned by the Go command. Put Go-owned flags before it
and raw test-binary flags after it, for example
`--test-flags '-race -args -x'`. Gomutants' package and managed flags are
already ahead of this boundary, so `-args` cannot swallow them. A managed
`-test.*` spelling after `-args` is still rejected because it would override
the corresponding flag in the test binary itself.

One wrinkle, and it only bites if you pass a *managed* name after the
boundary. `go test -bench -args …` binds `-args` to `-bench` as its value,
so the fields after it are read by the Go command after all — and an
`-overlay` among them would silently replace the mutation overlay. Rather
than track which Go flags take values, gomutants declines to treat `-args`
or `--` as a boundary when the field before it is a flag with no inline
`=` value. Write that value inline (`-bench=. -args -run=custom`) if you
need the relaxation there. Names gomutants does not manage are unaffected,
so `--test-flags '-race -args -x'` works either way.

## How It Works

1. **Resolve packages** via `go list -json`.
2. **Collect coverage** with `go test -coverprofile`. Mutants on uncovered lines are filtered upfront as `NOT_COVERED`.
3. **Measure baseline test time** to set the global timeout ceiling (`baseline × --timeout-coefficient`). With `--adaptive-timeout=false` this also becomes every mutant's deadline.
4. **Discover mutants** by walking the AST and emitting byte-level patches. Address-of `&` is recognised and skipped; unary `-` is emitted by exactly one mutator.
5. **Build per-test coverage map.** Test binaries are compiled once; each test runs in isolation with `-test.run=<one>` to record the lines it covers — and its wall-time, used for adaptive per-mutant timeouts.
6. **Test mutants** in parallel:
   - Each worker owns a stable temp source file + overlay JSON.
   - Mutations are applied as byte-level patches; the original tree is never written to.
   - The mutant's covered tests are looked up; only those run via `go test -overlay -run=<regex>`.
   - Each `go test` child runs in its own process group with a 2 GiB RSS cap; output is capped at 1 MiB per stream.
7. **Detect equivalent mutants** (only with `--detect-equivalent`). Each surviving mutant is recompiled with package-scoped `-gcflags=-S` under the same overlay mechanism; the reference (original) package is compiled once per package. When the normalized assembly matches the original, the mutation was folded away by the compiler and is reclassified `EQUIVALENT` — provably unkillable, so it leaves the efficacy denominator rather than failing the gate. The comparison is one-sided: any real difference in generated code diverges the hash, so a killable mutant is never marked equivalent.

Performance optimizations layered on top:

- **Per-mutant adaptive timeout.** Each mutant's deadline is `clamp(sum(selected test durations) × --timeout-margin, --timeout-min, global ceiling)`. A 50ms unit test gets a 2s floor instead of waiting out a multi-minute whole-suite ceiling, so infinite-loop mutants on fast packages trip in seconds rather than minutes. Falls back to the per-package sum when no per-test set is known, then to the global ceiling. Disable with `--adaptive-timeout=false`.
- **`GOMAXPROCS=NumCPU/workers` per child.** Without this, `--workers=10` on a 10-core box would have each child also assume 10 cores, oversubscribing 100×. With it, each child compiles + tests within its share.
- **Sort pending mutants by `(Pkg, File, Offset)` before dispatch.** The first mutant in a package pays the cold compile; subsequent ones reuse the build cache for deps and stdlib. This sort alone was a 17% wall-clock reduction.
- **`-vet=off` on the inner `go test`.** Vet runs in the user's CI on clean source; re-running it for every mutant is wasted work. Measured 17–39% per-mutant wall-clock reduction on representative packages.
- **Incremental cache.** Mutants whose source byte range and the surrounding tests are byte-identical to a prior run are skipped and their previous classifications reused. CI runs that touch one file pay for that file only.

### JSON report

Compatible with the gremlins JSON format:

```json
{
  "go_module": "github.com/example/project",
  "test_efficacy": 100,
  "mutations_coverage": 97.16,
  "mutants_total": 792,
  "mutants_killed": 772,
  "mutants_lived": 0,
  "mutants_not_viable": 0,
  "mutants_not_covered": 20,
  "mutants_suppressed": 5,
  "elapsed_time": 159.84,
  "files": [...]
}
```

`mutants_suppressed` is omitted when zero; it counts mutants dropped by `// gomutants:disable*` directives or by [`--exclude-calls`](#call-site-exclusion), and is excluded from every other count. `mutants_suppressed_by_calls` (also omitted when zero) breaks out the `--exclude-calls` share of that total rather than adding to it.

`mutants_equivalent` is omitted when zero; it counts surviving mutants proven equivalent by `--detect-equivalent`. They stay in `mutants_total` but count as neither killed nor lived, so they drop out of the `test_efficacy` denominator.

## Self-efficacy (gomutants on itself)

gomutants kills **100%** of mutants in its `./internal/...` library code (every package at 100% efficacy). Statement coverage is also 100%. The CI gate fails on any surviving mutant on changed lines per PR, and on the full `./internal/...` tree post-merge — drift surfaces on the merge that introduces it.

The `main` package is excluded from mutation testing. Its mutants exercise the integration test suite (which forks gomutants subprocesses to test mutated overlays), each taking minutes; running them in CI under the same gate isn't tractable, and most surviving mutants are output-formatting drift the integration tests intentionally don't pin.

## Security & Code Quality

**Supply chain.** Releases are signed with [cosign](https://docs.sigstore.dev/) (keyless Sigstore), ship [SPDX](https://spdx.dev/) SBOMs for every artifact, and carry [SLSA Level 3](https://slsa.dev/spec/v1.0/levels#build-l3) build provenance. Published tags are immutable. See [Verifying Releases](#verifying-releases) for verification commands. The repo is tracked by [OpenSSF Scorecard](https://scorecard.dev/viewer/?uri=github.com/szhekpisov/gomutants) (badge above).

**Continuous checks.** Every push and PR is scanned by:

- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) — known vulnerability detection
- [zizmor](https://github.com/zizmorcore/zizmor) — GitHub Actions workflow security scanning
- [golangci-lint](https://golangci-lint.run/) — multi-linter static analysis

**Test quality.** Unit + integration test suite (the integration suite forks gomutants subprocesses to test mutated overlays end-to-end). Mutation testing gated per-PR (no LIVED mutant on changed lines) with 100% post-merge efficacy on `./internal/...` library code — the tool is dogfooded on itself, gated by its own CI gate.

**Reporting vulnerabilities.** Open a [private GitHub Security Advisory](https://github.com/szhekpisov/gomutants/security/advisories/new).

## Contributing

Found a bug or have a feature request? [Open an issue](https://github.com/szhekpisov/gomutants/issues/new).

<details>
<summary>Development setup</summary>

**Prerequisites:** Go 1.26+

```bash
git clone https://github.com/szhekpisov/gomutants.git
cd gomutants
go build ./...
```

**Useful commands:**

```bash
go test ./...                    # full test suite (unit + integration)
go test -race ./...              # race detector
./gomutants ./internal/<pkg>/    # mutation-test one package locally
```

**CI pipelines** (run on every push and PR):
- **Tests** — unit + integration tests with coverage
- **Security & Static Analysis** — govulncheck + zizmor + golangci-lint
- **OpenSSF Scorecard** — supply-chain best-practices scoring
- **Mutation Testing** — gomutants on itself, gated per-PR on changed lines

</details>

## License

[MIT](LICENSE).

---

If you find this project useful, please consider giving it a ⭐ — it helps others discover it.
