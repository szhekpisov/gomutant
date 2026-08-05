# Mutation Coverage

Status of gomutants's self-mutation test. "Efficacy" = `killed / (killed + lived)`;
`not_viable`, `not_covered`, and `timed_out` are excluded from the denominator.

## Summary (excluding `main`)

| Package  | Killed | Lived | Excluded | Efficacy |
|----------|-------:|------:|---------:|---------:|
| patch    | 24     | 0     | 3        | 100.00%  |
| mutator  | 455    | 7     | 57       | 98.48%   |
| cache    | 235    | 5     | 24       | 97.92%   |
| discover | 604    | 17    | 69       | 97.26%   |
| report   | 291    | 11    | 30       | 96.36%   |
| coverage | 336    | 17    | 34       | 95.18%   |
| tce      | 98     | 8     | 18       | 92.45%   |
| runner   | 279    | 23    | 26       | 92.38%   |
| config   | 156    | 15    | 3        | 91.23%   |
| **total**| **2478**| **103**| **264** | **96.01%** |

2845 mutants discovered, 27 further suppressed by inline directives. "Excluded"
is `not_viable` + `timed_out`.

Replicate with `gomutants -w 10 -o report.json ./internal/...`, or per package
with `gomutants -w 8 -o <pkg>.json ./internal/<pkg>/`.

## Survivors by mutator

| Mutator | Lived |
|---------|------:|
| INTEGER_INCREMENT | 41 |
| INTEGER_DECREMENT | 29 |
| RETURN_FALSE      | 11 |
| RETURN_ZERO       | 10 |
| RETURN_TRUE       | 9  |
| STATEMENT_REMOVE / FLOAT_INCREMENT / FLOAT_DECREMENT | 1 each |

Two classes account for 80 of the 103 survivors: numeric literals whose exact
value is not observable (72), and `ast.Inspect` visitors whose pruned subtree
holds nothing mutable (8). Both are described below.

## Why these mutants survive

The surviving mutants fall into a small set of patterns. Understanding the
pattern is more useful than chasing individual positions — future changes
should avoid *adding* mutants that hit the same dead zones.

### 1. Numeric literals whose exact value is not observable

72 survivors (`INTEGER_INCREMENT` 41, `INTEGER_DECREMENT` 29, plus the two float
cases). The literals cluster tightly:

| Literal | Count | What it is |
|---------|------:|------------|
| `0`      | 40 | loop starts, zero returns, index bases |
| `0o644`  | 12 | file mode on report/cache writes |
| `1`      | 6  | off-by-one steps and slice offsets |
| `1024`   | 4  | buffer sizes |
| `0o755`  | 4  | directory mode |
| `3.0`    | 2  | float test fixtures |
| `16`     | 2  | map pre-sizing |
| `64`     | 1  | a `strconv` bit size |

File modes are the clearest case: nothing in the suite reads back the mode, so
`0o644` → `0o645` is invisible. Buffer sizes and `strconv` bit sizes are
similar — the code behaves identically at any sane value, which is exactly why
`numeric_literal.go` and `return_value.go` carry `gomutants:disable-next-line`
directives for the handful that are provably equivalent. `strconv.ParseFloat`'s
bit size is the clearest: it only branches at 32 vs ≠32, so 63/64/65 all select
the same parser.

- `coverage/parse.go` (12 × `0`), `config/config.go` (8), `report/terminal.go`
  (5), `runner/worker.go` (4), `discover/directives.go` (3).
- Killing the mode literals means asserting `os.Stat().Mode()` after a write.
  The buffer sizes are better left documented than forced.

### 2. `ast.Inspect` visitors whose subtree holds nothing mutable

Every mutator's visitor ends in `return true`, and most carry interior guards
that also `return true` for a node of the right kind but the wrong sub-kind.
`RETURN_FALSE` flips these to `false`, which prunes that node's subtree.

This *was* the largest addressable class (38 survivors). It is now 8, all
equivalent, because `TestNestedConstructsAreTraversed` nests each mutator's
target under a node the same visitor reaches first — under a non-matching node
of the same kind for the sub-kind guards, and under a matching node for the
final `return true`. Pruning now changes the candidate count, and the test
asserts counts exactly, so it fails.

What remains cannot be killed, because the pruned subtree provably contains
nothing to find:

| Site | Why |
|------|-----|
| `invert_loop_ctrl.go:26, 32, 45` | a `BranchStmt`'s only child is its label |
| `numeric_literal.go:42, 53` | a `BasicLit` has no children at all |
| `invert_bitwise.go:41` | a constraint-union subtree is entirely type syntax, and every binary operator in it is already recorded as a constraint position |
| `statement_remove.go:25` | `len(stmt.Rhs) == 0` is reachable only under parser error recovery, which `Discover` never sees |
| `discover/excludecalls.go:192` | the pruned node is a call selector whose operands hold no further call to index |

Nesting fixtures for these would be theatre — the constructs cannot contain a
mutable instance of themselves.

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

## Where to invest if pushing past 96%

1. **Assert file modes** after report/cache writes. Kills ~16 — now the
   largest addressable win.
2. **Assert sort order** in the TCE report path. Kills ~2, and the ordering is
   user-visible so the assertion is worth having regardless.

The nesting work that used to head this list is done: `TestNestedConstructsAreTraversed`
killed 30 of the 38 traversal mutants, and the 8 that remain are equivalent
(class 2). Classes 4 and 5 are likewise equivalent-by-construction and are
better documented than forced — the first would require flattening
`(bool, error)` signatures, the second full type resolution.
