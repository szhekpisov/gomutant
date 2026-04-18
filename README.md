# gomutant

A fast mutation testing tool for Go. Drop-in replacement for [go-gremlins](https://github.com/go-gremlins/gremlins) with more mutators, generics support, and ~42% faster execution.

## Features

- **10 mutation types** — token-level and block-level mutations (see below)
- **Fast** — parallel workers with `go test -overlay` (no source tree copies)
- **Generics support** — byte-level patching preserves all Go syntax
- **Gremlins-compatible** — JSON report format for easy migration
- **Per-test coverage mapping** — compiles test binaries once, runs only relevant tests per mutant
- **Configurable** — YAML config file + CLI flags

## Installation

```bash
go install github.com/szhekpisov/gomutant@latest
```

## Usage

```bash
# Run on all packages
gomutant ./...

# Run on specific packages
gomutant ./pkg/mypackage/...

# Dry run — list mutants without testing
gomutant --dry-run ./...

# Verbose output — show each mutant as tested
gomutant -v ./...

# Run only specific mutator types
gomutant --only ARITHMETIC_BASE,CONDITIONALS_NEGATION ./...

# Disable specific mutator types
gomutant --disable BRANCH_IF,BRANCH_ELSE ./...

# Custom worker count and timeout
gomutant -w 8 --timeout-coefficient 15 ./...

# Specify coverage package pattern
gomutant --coverpkg ./pkg/mypackage/... ./...

# Custom output path
gomutant -o report.json ./...
```

### Gremlins compatibility

The `unleash` subcommand is accepted and ignored for CLI compatibility:

```bash
gomutant unleash ./...
```

## CLI Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--workers` | `-w` | NumCPU | Parallel workers |
| `--timeout-coefficient` | | 10 | Multiply baseline test time for per-mutant timeout |
| `--coverpkg` | | | Coverage package pattern (passed to `go test -coverpkg`) |
| `--output` | `-o` | `mutation-report.json` | JSON report path |
| `--config` | | `.gomutant.yml` | Config file path |
| `--disable` | | | Comma-separated mutator types to disable |
| `--only` | | | Comma-separated mutator types to run (disables all others) |
| `--dry-run` | | false | List mutants without testing |
| `--verbose` | `-v` | false | Show each mutant as tested |
| `--version` | | | Print version and exit |

## Configuration

Create a `.gomutant.yml` in your project root:

```yaml
workers: 10
timeout-coefficient: 10
coverpkg: "./pkg/mypackage/..."
output: mutation-report.json
```

Priority: defaults < config file < CLI flags.

## Mutators

### Token-level

| Type | Description | Example |
|------|-------------|---------|
| `ARITHMETIC_BASE` | Swap arithmetic operators | `+` <-> `-`, `*` <-> `/`, `%` <-> `*` |
| `CONDITIONALS_BOUNDARY` | Relax/tighten boundaries | `<` <-> `<=`, `>` <-> `>=` |
| `CONDITIONALS_NEGATION` | Negate comparisons | `==` <-> `!=`, `<` <-> `>=`, `>` <-> `<=` |
| `INCREMENT_DECREMENT` | Swap increment/decrement | `++` <-> `--` |
| `INVERT_NEGATIVES` | Invert negation | `-x` -> `+x`, `a - b` -> `a + b` |

### Block-level

| Type | Description | Example |
|------|-------------|---------|
| `BRANCH_IF` | Empty if/else-if body | `if x { doStuff() }` -> `if x { _ = 0 }` |
| `BRANCH_ELSE` | Empty else body | `else { doStuff() }` -> `else { _ = 0 }` |
| `BRANCH_CASE` | Empty case body | `case 1: doStuff()` -> `case 1: _ = 0` |
| `EXPRESSION_REMOVE` | Remove boolean operand | `a && b` -> `true && b` / `a && true` |
| `STATEMENT_REMOVE` | Remove statement effect | `x = expr` -> `_ = expr`, `f()` -> `_ = 0` |

## JSON Report

Output is compatible with the gremlins JSON format:

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
  "elapsed_time": 159.84,
  "files": [...]
}
```

## Mutant Statuses

| Status | Meaning |
|--------|---------|
| KILLED | Test failed — mutant detected |
| LIVED | Tests passed — mutant survived (test gap) |
| NOT COVERED | No test covers this code |
| NOT VIABLE | Mutant causes compile error |
| TIMED OUT | Test execution exceeded timeout |

## How It Works

1. **Resolve packages** — `go list -json` to find source files
2. **Collect coverage** — `go test -coverprofile` to identify covered code
3. **Measure baseline** — run tests once to establish timeout threshold
4. **Discover mutants** — parse AST, apply mutators, filter by coverage
5. **Build test map** — compile test binaries once, map tests to covered lines
6. **Test mutants** — parallel workers apply mutations via `go test -overlay`

Each worker owns a stable temp file. Mutations are applied as byte-level patches on the original source, written to the temp file, and injected via Go's overlay mechanism. The original source tree is never modified.

## Benchmarks

Tested on diffyml (792 mutants, 10 workers, darwin/arm64):

| Tool | Time | Mutants |
|------|------|---------|
| gremlins | ~276s | 779 |
| gomutant | ~160s | 792 |

~42% faster with more mutations discovered.

## License

[MIT](LICENSE)
