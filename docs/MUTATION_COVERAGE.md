# Mutation Coverage

Status of gomutants's self-mutation test. "Efficacy" = `killed / (killed + lived)`;
`not_viable`, `not_covered`, and `timed_out` are excluded from the denominator.

## Summary (excluding `main`)

| Package  | Killed | Lived | Excluded | Efficacy |
|----------|-------:|------:|---------:|---------:|
| patch    | 24     | 0     | 3        | 100.00%  |
| cache    | 235    | 5     | 24       | 97.92%   |
| discover | 604    | 17    | 69       | 97.26%   |
| report   | 291    | 11    | 30       | 96.36%   |
| coverage | 336    | 17    | 34       | 95.18%   |
| tce      | 98     | 8     | 18       | 92.45%   |
| runner   | 279    | 23    | 26       | 92.38%   |
| mutator  | 424    | 40    | 57       | 91.38%   |
| config   | 156    | 15    | 3        | 91.23%   |
| **total**| **2447**| **136**| **264** | **94.73%** |

2847 mutants discovered, 25 further suppressed by inline directives. "Excluded"
is `not_viable` + `timed_out`.

Replicate with `gomutants -w 10 -o report.json ./internal/...`, or per package
with `gomutants -w 8 -o <pkg>.json ./internal/<pkg>/`.

## Survivors by mutator

| Mutator | Lived |
|---------|------:|
| INTEGER_INCREMENT | 42 |
| RETURN_FALSE      | 41 |
| INTEGER_DECREMENT | 30 |
| RETURN_ZERO       | 10 |
| RETURN_TRUE       | 9  |
| STATEMENT_REMOVE / INVERT_LOOP_CTRL / FLOAT_INCREMENT / FLOAT_DECREMENT | 1 each |

Two classes account for 121 of the 136 survivors: numeric literals whose exact
value is not observable (72), and `return true` at the tail of an `ast.Inspect`
visitor (38). Both are described below.

## Why these mutants survive

The surviving mutants fall into a small set of patterns. Understanding the
pattern is more useful than chasing individual positions — future changes
should avoid *adding* mutants that hit the same dead zones.

### 1. `return true` at the tail of an `ast.Inspect` visitor

The single largest addressable class: 38 of the 41 `RETURN_FALSE` survivors,
almost all in `internal/mutator/*.go`.

```go
ast.Inspect(file, func(n ast.Node) bool {
    bin, ok := n.(*ast.BinaryExpr)
    if !ok {
        return true          // ← RETURN_FALSE mutates to `false`
    }
    ...
    return true              // ← and here
})
```

`false` tells `ast.Inspect` to stop descending into that node's children. The
mutation only changes the discovered candidate set when a mutator's target
construct is **nested inside another instance of itself** — a comparison inside
a comparison, an if inside an if, a range inside a range. The current fixtures
are mostly flat, so pruning the subtree finds the same candidates.

These are **killable**, not equivalent — this is a genuine fixture gap, and the
highest-value one on the list.

- `conditionals_negation.go:30, 43`, `conditionals_boundary.go:28, 41`,
  `branch_if.go:24`, `branch_else.go:24, 27, 41`, `branch_case.go:20, 39`,
  `invert_bitwise.go:41, 45, 58, 92`, `invert_loop_ctrl.go:26, 32, 45`,
  `statement_remove.go:20, 25, 31`, and the same shape across the remaining
  mutator files.
- To kill: add a nested instance of each mutator's target to the fixture — e.g.
  `(a < b) == (c < d)` for the comparison mutators, an `if` whose body holds
  another `if` for `BRANCH_IF`.

### 2. Numeric literals whose exact value is not observable

72 survivors (`INTEGER_INCREMENT` 42, `INTEGER_DECREMENT` 30, plus the two float
cases). The literals cluster tightly:

| Literal | Count | What it is |
|---------|------:|------------|
| `0`      | 40 | loop starts, zero returns, index bases |
| `0o644`  | 12 | file mode on report/cache writes |
| `1`      | 6  | off-by-one steps and slice offsets |
| `1024`   | 4  | buffer sizes |
| `0o755`  | 4  | directory mode |
| `64`     | 3  | `strconv` bit sizes |

File modes are the clearest case: nothing in the suite reads back the mode, so
`0o644` → `0o645` is invisible. Buffer sizes and `strconv` bit sizes are
similar — the code behaves identically at any sane value, which is exactly why
`numeric_literal.go` already carries `gomutants:disable-next-line` directives
for the handful that are provably equivalent.

- `coverage/parse.go` (12 × `0`), `config/config.go` (8), `report/terminal.go`
  (5), `runner/worker.go` (4), `discover/directives.go` (3).
- Killing the mode literals means asserting `os.Stat().Mode()` after a write.
  The buffer sizes are better left documented than forced.

### 3. Sort comparators forced to a constant

```go
sort.SliceStable(pending, func(a, b int) bool {
    return mutantLess(mutants[pending[a]], mutants[pending[b]])   // → true / false
})
```

Both `RETURN_TRUE` and `RETURN_FALSE` survive here. `SliceStable` with a
constant comparator leaves the input order untouched, and the assertions
downstream check *membership* and counts rather than order.

- `runner/pool.go:90` (scheduling order — a performance heuristic, not a
  correctness property), `tce/tce.go:233`.
- To kill: assert the resulting slice order explicitly, at least for
  `tce.go:233` where the order is user-visible in the report.

### 4. Boolean early-return guards on error paths

```go
original, ok := d.srcCache[m.File]
if !ok {
    return false, fmt.Errorf("tce: source not cached: %s", m.File)   // → true
}
```

`RETURN_TRUE` flips the bool while the non-nil error is still returned. Every
caller checks the error first and never reads the bool on that path, so the
mutation is unobservable — genuinely equivalent given the call sites.

- `tce/tce.go:118, 122, 126, 129, 133, 137`, `discover/excludecalls.go:199`.
- Not worth chasing: the fix would be to return a bare error, which the
  `(bool, error)` signature exists to avoid.

### 5. `RETURN_ZERO` on identifiers whose value syntax cannot resolve

10 survivors. The mutator skips returns that are *visibly* already zero
(`Block{}`, `0`, `""`), but an identifier's value is not knowable without type
and flow information:

```go
return d, nil            // d is a zero-valued `directive` on this path
return mutator.StatusPending   // a named constant that is itself 0
```

Where the variable already holds the zero value, `*new(T)` is exactly
equivalent and no test can kill it.

- `discover/directives.go:239, 244, 249` (`d`), `config/config.go:251, 255`
  (`cfg`), `coverage/testmap.go:307, 312` (`dur`), `cache/cache.go:597`
  (`mutator.StatusPending`), `runner/worker.go:234` (`m`),
  `discover/directives.go:82` (`mutants` → `nil`).
- Resolving these needs `go/types`, which the mutator deliberately does not
  use. Documented limitation, not a fixture gap.

## Where to invest if pushing past 95%

1. **Nest the fixtures in `internal/mutator/mutator_test.go`** — put each
   mutator's target construct inside another instance of itself. Kills up to 38
   mutants, the largest single win available, and cheap.
2. **Assert file modes** after report/cache writes. Kills ~16.
3. **Assert sort order** in the TCE report path. Kills ~2, and the ordering is
   user-visible so the assertion is worth having regardless.

Classes 4 and 5 are equivalent-by-construction and are better documented than
forced — the first would require flattening `(bool, error)` signatures, the
second full type resolution.
