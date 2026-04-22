package discover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

// Package holds resolved package info from go list.
type Package struct {
	Dir        string   // Absolute directory path.
	ImportPath string   // e.g. "github.com/szhekpisov/diffyml/pkg/diffyml"
	GoFiles    []string // .go source files (base names).
	TestGoFiles []string // _test.go files (base names).
}

// goListJSON mirrors the fields we need from `go list -json`.
type goListJSON struct {
	Dir         string   `json:"Dir"`
	ImportPath  string   `json:"ImportPath"`
	GoFiles     []string `json:"GoFiles"`
	TestGoFiles []string `json:"TestGoFiles"`
	Error       *struct {
		Err string `json:"Err"`
	} `json:"Error"`
}

// ResolvePackages runs `go list -json` to resolve package patterns.
func ResolvePackages(ctx context.Context, dir string, patterns []string) ([]Package, error) {
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.String())
	}

	return decodeGoListJSON(&stdout)
}

// decodeGoListJSON parses JSON output from `go list -json`.
func decodeGoListJSON(r io.Reader) ([]Package, error) {
	var pkgs []Package
	dec := json.NewDecoder(r)
	for dec.More() {
		var p goListJSON
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("parsing go list output: %w", err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("go list error for %s: %s", p.ImportPath, p.Error.Err)
		}
		pkgs = append(pkgs, Package{
			Dir:         p.Dir,
			ImportPath:  p.ImportPath,
			GoFiles:     p.GoFiles,
			TestGoFiles: p.TestGoFiles,
		})
	}
	return pkgs, nil
}

// Discover walks the given packages, parses each source file, invokes all
// mutators, and returns a sorted list of Mutants with sequential IDs.
// moduleRoot is the absolute path to the project root (for computing absolute paths).
// goModule is the Go module name (for computing gremlins-compatible relative paths).
func Discover(fset *token.FileSet, pkgs []Package, mutators []mutator.Mutator, moduleRoot, goModule string) []mutator.Mutant {
	var allCandidates []mutator.MutantCandidate

	for _, pkg := range pkgs {
		for _, filename := range pkg.GoFiles {
			absPath := filepath.Join(pkg.Dir, filename)
			src, file, err := parseFile(fset, absPath)
			if err != nil {
				continue // Soft failure: skip unparseable files.
			}
			for _, m := range mutators {
				candidates := m.Discover(fset, file, src)
				allCandidates = append(allCandidates, candidates...)
			}
		}
	}

	// Sort by file, line, column, type for deterministic output.
	sort.Slice(allCandidates, func(i, j int) bool {
		return candidateLess(allCandidates[i], allCandidates[j])
	})

	// Build file→package lookup.
	filePkg := make(map[string]string)
	for _, p := range pkgs {
		for _, f := range p.GoFiles {
			filePkg[filepath.Join(p.Dir, f)] = p.ImportPath
		}
	}

	// Find common import path prefix for gremlins-compatible relative paths.
	// e.g. if all packages are under "github.com/foo/bar/pkg/diffyml",
	// then RelFile for "github.com/foo/bar/pkg/diffyml/cli/cli.go" is "cli/cli.go".
	commonPrefix := longestCommonPrefix(pkgs)

	// Convert candidates to mutants.
	mutants := make([]mutator.Mutant, len(allCandidates))
	for i, c := range allCandidates {
		absPath := c.Pos.Filename
		pkg := filePkg[absPath]

		// Compute gremlins-compatible relative path: strip common prefix from ImportPath.
		relPath := computeRelFile(commonPrefix, pkg, absPath, moduleRoot)

		// Coverage profile path: ImportPath/filename.
		coverageFile := pkg + "/" + filepath.Base(absPath)

		mutants[i] = mutator.Mutant{
			ID:           i + 1,
			Type:         c.Type,
			File:         absPath,
			RelFile:      relPath,
			Line:         c.Pos.Line,
			Col:          c.Pos.Column,
			Original:     c.Original,
			Replacement:  c.Replacement,
			StartOffset:  c.StartOffset,
			EndOffset:    c.EndOffset,
			CoverageFile: coverageFile,
			Status:       mutator.StatusPending,
			Pkg:          pkg,
		}
	}

	return mutants
}

func parseFile(fset *token.FileSet, path string) ([]byte, *ast.File, error) {
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil, err
	}
	// Re-read source bytes for byte-level patching.
	src, err := readFileBytes(path)
	if err != nil {
		return nil, nil, err
	}
	return src, file, nil
}

// candidateLess orders candidates by (file, line, column, type) so
// Discover produces deterministic output regardless of the AST-walk order.
func candidateLess(a, b mutator.MutantCandidate) bool {
	if a.Pos.Filename != b.Pos.Filename {
		return a.Pos.Filename < b.Pos.Filename
	}
	if a.Pos.Line != b.Pos.Line {
		return a.Pos.Line < b.Pos.Line
	}
	if a.Pos.Column != b.Pos.Column {
		return a.Pos.Column < b.Pos.Column
	}
	return a.Type < b.Type
}

// computeRelFile produces a gremlins-compatible module-relative path.
// If the candidate's package shares the common prefix, we strip it and
// append the file base; otherwise we fall back to filepath.Rel against
// moduleRoot. Factored out so the empty / no-prefix / non-matching-pkg
// cases can be unit-tested independently of Discover's wiring.
func computeRelFile(commonPrefix, pkg, absPath, moduleRoot string) string {
	if commonPrefix != "" && strings.HasPrefix(pkg, commonPrefix) {
		sub := strings.TrimPrefix(pkg, commonPrefix)
		sub = strings.TrimPrefix(sub, "/")
		if sub == "" {
			return filepath.Base(absPath)
		}
		return sub + "/" + filepath.Base(absPath)
	}
	rel, _ := filepath.Rel(moduleRoot, absPath)
	return rel
}

// longestCommonPrefix finds the longest common import path prefix across all packages.
func longestCommonPrefix(pkgs []Package) string {
	if len(pkgs) == 0 {
		return ""
	}
	prefix := pkgs[0].ImportPath
	for _, p := range pkgs[1:] {
		for !strings.HasPrefix(p.ImportPath, prefix) {
			slash := strings.LastIndex(prefix, "/")
			if slash < 0 {
				return ""
			}
			prefix = prefix[:slash]
		}
	}
	return prefix
}
