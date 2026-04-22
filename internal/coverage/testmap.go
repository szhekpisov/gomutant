package coverage

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Function variables for testing.
var (
	resolvePackagesFunc = resolvePackages
	listTestsFunc       = listTests
	parseFileFunc       = ParseFile
)

// TestMap maps (file, line) positions to the test functions that cover them.
type TestMap struct {
	// index maps "file:line" to a set of test function names.
	index map[string]map[string]bool
}

type testCoverage struct {
	testName string
	blocks   []Block
}

// compiledPkg holds a pre-compiled test binary for a package.
type compiledPkg struct {
	binPath    string // Path to compiled test binary.
	importPath string // Package import path.
	dir        string // Package directory (for running the binary).
}

// BuildTestMap enumerates tests in the given packages, compiles each package's
// test binary once, then runs each test function against the compiled binary
// with coverage. Uses parallel workers.
func BuildTestMap(ctx context.Context, projectDir string, packages []string, coverPkg string, tmpDir string, workers int) (*TestMap, error) {
	// 1. Enumerate all test function names.
	tests, err := listTestsFunc(ctx, projectDir, packages)
	if err != nil {
		return nil, fmt.Errorf("listing tests: %w", err)
	}

	// 2. Resolve package patterns to individual packages and compile test binaries.
	resolvedPkgs, err := resolvePackagesFunc(ctx, projectDir, packages)
	if err != nil {
		return nil, fmt.Errorf("resolving packages: %w", err)
	}

	pkgBins := make(map[string]*compiledPkg)
	for _, pkg := range resolvedPkgs {
		binPath := filepath.Join(tmpDir, "testbin-"+sanitize(pkg.importPath)+".test")
		args := []string{"test", "-c", "-o", binPath, "-cover"}
		if coverPkg != "" {
			args = append(args, "-coverpkg="+coverPkg)
		}
		args = append(args, pkg.importPath)

		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = projectDir
		cmd.Stdout = nil
		cmd.Stderr = nil

		if err := cmd.Run(); err != nil {
			// Package may have no tests; skip.
			continue
		}

		// Verify binary was created (packages with no tests produce no
		// binary). Stat is the right probe here — LookPath is for PATH
		// resolution, not file existence.
		if _, err := statFile(binPath); err != nil {
			continue
		}

		pkgBins[pkg.importPath] = &compiledPkg{
			binPath:    binPath,
			importPath: pkg.importPath,
			dir:        pkg.dir,
		}
	}

	// 3. Run tests in parallel using compiled binaries.
	work := make(chan testEntry, len(tests))
	results := make(chan testCoverage, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processWork(ctx, work, pkgBins, tmpDir, workerID, results)
		}(i)
	}

	// Feed work.
	go func() {
		feedWork(ctx, tests, work)
	}()

	// Close results when all workers are done.
	go func() {
		wg.Wait()
		close(results)
	}()

	// 4. Collect and index results.
	tm := &TestMap{index: make(map[string]map[string]bool)}
	for tc := range results {
		for _, b := range tc.blocks {
			if b.Count == 0 {
				continue
			}
			for line := b.StartLine; line <= b.EndLine; line++ {
				key := b.File + ":" + fmt.Sprint(line)
				if tm.index[key] == nil {
					tm.index[key] = make(map[string]bool)
				}
				tm.index[key][tc.testName] = true
			}
		}
	}

	return tm, nil
}

// processWork processes test entries from the work channel.
func processWork(ctx context.Context, work <-chan testEntry, pkgBins map[string]*compiledPkg, tmpDir string, workerID int, results chan<- testCoverage) {
	for test := range work {
		if ctx.Err() != nil {
			return
		}
		cp := pkgBins[test.pkg]
		if cp == nil {
			continue
		}
		profilePath := filepath.Join(tmpDir, fmt.Sprintf("testmap-%d.cov", workerID))
		blocks := runCompiledTest(ctx, cp, test.name, profilePath)
		if len(blocks) > 0 {
			results <- testCoverage{testName: test.name, blocks: blocks}
		}
	}
}

// feedWork sends test entries to the work channel, respecting context cancellation.
func feedWork(ctx context.Context, tests []testEntry, work chan<- testEntry) {
	for _, t := range tests {
		select {
		case work <- t:
		case <-ctx.Done():
			close(work)
			return
		}
	}
	close(work)
}

// runCompiledTest runs a pre-compiled test binary for a single test with coverage.
func runCompiledTest(ctx context.Context, cp *compiledPkg, testName, profilePath string) []Block {
	args := []string{
		fmt.Sprintf("-test.run=^%s$", regexp.QuoteMeta(testName)),
		"-test.coverprofile=" + profilePath,
	}

	cmd := exec.CommandContext(ctx, cp.binPath, args...)
	cmd.Dir = cp.dir
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil
	}

	profile, err := parseFileFunc(profilePath)
	if err != nil {
		return nil
	}
	return profile.blocks
}

// TestsFor returns the test function names that cover the given position.
// Returns nil if no mapping exists (caller should run all tests).
func (tm *TestMap) TestsFor(file string, line int) []string {
	if tm == nil {
		return nil
	}
	key := file + ":" + fmt.Sprint(line)
	testSet := tm.index[key]
	if len(testSet) == 0 {
		return nil
	}
	tests := make([]string, 0, len(testSet))
	for t := range testSet {
		tests = append(tests, t)
	}
	return tests
}

// RunPattern returns a -run regex pattern that matches exactly the given tests.
func RunPattern(tests []string) string {
	if len(tests) == 0 {
		return ""
	}
	escaped := make([]string, len(tests))
	for i, t := range tests {
		escaped[i] = regexp.QuoteMeta(t)
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

type testEntry struct {
	name string
	pkg  string
}

func listTests(ctx context.Context, projectDir string, packages []string) ([]testEntry, error) {
	var allTests []testEntry

	for _, pkg := range packages {
		args := []string{"test", "-list", ".", pkg}
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = projectDir

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("go test -list for %s: %w\n%s", pkg, err, stderr.String())
		}

		scanner := bufio.NewScanner(&stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "ok") {
				continue
			}
			allTests = append(allTests, testEntry{name: line, pkg: pkg})
		}
	}

	return allTests, nil
}

type resolvedPkg struct {
	importPath string
	dir        string
}

func resolvePackages(ctx context.Context, projectDir string, patterns []string) ([]resolvedPkg, error) {
	args := append([]string{"list", "-f", "{{.ImportPath}}\t{{.Dir}}"}, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = projectDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.String())
	}

	var pkgs []resolvedPkg
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		if len(parts) == 2 {
			pkgs = append(pkgs, resolvedPkg{importPath: parts[0], dir: parts[1]})
		}
	}
	return pkgs, nil
}

func statFile(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// sanitize makes a test name safe for use as a filename.
func sanitize(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", "\\", "_", ".", "_").Replace(s)
}
