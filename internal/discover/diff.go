package discover

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// diffMaxBufSize is both the initial allocation and the maximum line size
// for the unified-diff scanner. Keeping init == max means the scanner
// never grows, which removes the equivalent mutant produced when the
// initial size and the max are independently mutable.
const diffMaxBufSize = 16 * 1024 * 1024

// LineRange is a closed interval [Start, End] of 1-indexed line numbers.
type LineRange struct {
	Start int
	End   int
}

// Contains reports whether line is in [Start, End] inclusive.
func (r LineRange) Contains(line int) bool {
	return line >= r.Start && line <= r.End
}

// GitRoot returns the absolute path to the git working-tree root for dir.
func GitRoot(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w\n%s", err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// looksLikeBadRevision reports whether stderr from `git diff` indicates
// the caller passed a ref that git couldn't resolve. Split out so each
// branch of the OR is independently testable without relying on git's
// exact wording for a given ref-state.
func looksLikeBadRevision(stderr string) bool {
	return strings.Contains(stderr, "unknown revision") ||
		strings.Contains(stderr, "bad revision")
}

// resolveMergeBase returns the merge base of ref and HEAD — the commit the
// current branch forked from. Diffing against that instead of the ref's tip
// is what makes the range match the set of lines a pull request shows: on a
// branch that is behind its base, `git diff <ref>` also reports every line
// that landed on the base after the fork, attributing other people's work to
// this run.
//
// Falls back to ref itself on any non-zero exit — no merge base exists
// (unrelated histories, a repo with no commits), or ref doesn't resolve. In
// that last case the caller's `git diff` reproduces the failure and owns the
// diagnostic, so there is nothing to report from here.
func resolveMergeBase(ctx context.Context, dir, ref string) string {
	cmd := exec.CommandContext(ctx, "git", "merge-base", ref, "HEAD")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ref
	}
	// A zero exit means git printed exactly one commit id, so the trimmed
	// output is never empty and needs no separate guard.
	return strings.TrimRight(stdout.String(), "\n")
}

// RunGitDiff executes `git diff --unified=0 <merge-base(ref, HEAD)>` in dir
// and returns the changed line ranges per file (paths relative to the git
// root). Lines only deleted at a position (count=0) produce no range — there
// is nothing to mutate. Renames use the new (b/) path.
//
// Errors name the ref the caller passed, not the resolved merge base, which
// is an implementation detail they never typed.
func RunGitDiff(ctx context.Context, dir, ref string) (map[string][]LineRange, error) {
	base := resolveMergeBase(ctx, dir, ref)
	cmd := exec.CommandContext(ctx, "git", "diff", "--unified=0", "--no-color", "--no-ext-diff", base)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if looksLikeBadRevision(stderrStr) {
			return nil, fmt.Errorf("git diff %s: %w\n%s\nhint: %q is not a valid revision in this repo — if it's a remote branch, try `git fetch`", ref, err, stderrStr, ref)
		}
		return nil, fmt.Errorf("git diff %s: %w\n%s", ref, err, stderrStr)
	}
	return ParseUnifiedDiff(&stdout)
}

// ParseUnifiedDiff reads a unified diff (as produced by `git diff
// --unified=0`) and returns the changed line ranges per file. Paths are
// taken from the `+++ b/<path>` line; deleted files (`+++ /dev/null`)
// contribute no ranges. Hunk headers carry `+newstart[,newcount]`; we emit
// [newstart, newstart+newcount-1] for each newcount > 0.
func ParseUnifiedDiff(r io.Reader) (map[string][]LineRange, error) {
	ranges := make(map[string][]LineRange)
	var current string

	sc := bufio.NewScanner(r)
	// One fixed-size buffer: large enough for vendored/generated diff
	// lines (>1MB happens), small enough to bound pathological input.
	sc.Buffer(make([]byte, diffMaxBufSize), diffMaxBufSize)
	for sc.Scan() {
		line := sc.Text()
		// if/else (not switch): inside a switch case, `break` exits only
		// the switch — making `continue → break` mutations equivalent
		// here. Using if/else binds break to the surrounding for loop,
		// so mutations on the loop-control statements are detectable.
		if path, ok := strings.CutPrefix(line, "+++ "); ok {
			// Strip trailing tab-prefixed metadata (timestamps).
			if i := strings.IndexByte(path, '\t'); i >= 0 {
				path = path[:i]
			}
			if path == "/dev/null" {
				current = ""
				continue
			}
			current = stripDiffPrefix(path)
		} else if strings.HasPrefix(line, "@@") {
			if current == "" {
				continue
			}
			rng, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			ranges[current] = append(ranges[current], rng)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading diff: %w", err)
	}
	return ranges, nil
}

// stripDiffPrefix removes the standard "a/" or "b/" prefix that git adds
// to diff paths. If diff.noprefix is set the path is already plain — and
// in that pathological case a literal top-level "a/" or "b/" directory
// would be misinterpreted; not worth handling unless someone hits it.
func stripDiffPrefix(p string) string {
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// parseHunkHeader extracts the +start[,count] range from a unified-diff
// hunk header like "@@ -1,2 +3,4 @@ ctx". Returns ok=false if the header
// is malformed or the new-side count is zero (deletion-only hunk).
func parseHunkHeader(line string) (LineRange, bool) {
	// Locate the "+<start>[,<count>] " token. The first Cut may "succeed"
	// with after="" if there is no '+'; the second Cut catches that, so
	// we don't need a separate not-found check after the first.
	_, after, _ := strings.Cut(line, "+")
	tok, _, found := strings.Cut(after, " ")
	if !found {
		return LineRange{}, false
	}
	startStr, countStr, hasCount := strings.Cut(tok, ",")
	// Atoi returns (0, err) for invalid input — the start <= 0 / count <= 0
	// checks below catch parse failures and non-positive values uniformly,
	// so we drop the redundant explicit error checks.
	start, _ := strconv.Atoi(startStr)
	if start <= 0 {
		return LineRange{}, false
	}
	count := 1
	if hasCount {
		count, _ = strconv.Atoi(countStr)
	}
	if count <= 0 {
		return LineRange{}, false
	}
	return LineRange{Start: start, End: start + count - 1}, true
}

// FilterByDiff returns the subset of mutants whose (file, line) falls
// inside one of the changed ranges. Paths in ranges are relative to
// gitRoot; mutant File paths are absolute. The input slice is not
// modified; the returned slice is a fresh allocation.
func FilterByDiff(mutants []mutator.Mutant, ranges map[string][]LineRange, gitRoot string) []mutator.Mutant {
	out := make([]mutator.Mutant, 0, len(mutants))
	for _, m := range mutants {
		// filepath.Rel returns ("", err) on every error path, so the
		// rel == "" check below catches both genuine errors and the
		// (impossible-in-practice) empty-target case.
		r, _ := filepath.Rel(gitRoot, m.File)
		// filepath.Rel uses backslashes on Windows; git always uses forward slashes.
		rel := filepath.ToSlash(r)
		if rel == "" {
			continue
		}
		for _, h := range ranges[rel] {
			if h.Contains(m.Line) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}
