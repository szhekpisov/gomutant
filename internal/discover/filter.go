package discover

import (
	"path/filepath"
	"strings"

	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/mutator"
)

// FilterByCoverage marks mutants whose positions are not covered as StatusNotCovered.
// It builds a mapping from coverage-profile paths (module-relative) to absolute paths
// to bridge the two path formats.
func FilterByCoverage(mutants []mutator.Mutant, profile *coverage.Profile, pkgs []Package, goModule string) {
	// Build mapping: coverage profile path → absolute path.
	// Coverage uses "module/pkg/file.go", we need to map to "/abs/path/file.go".
	absToProfile := make(map[string]string)
	for _, pkg := range pkgs {
		for _, f := range pkg.GoFiles {
			absPath := filepath.Join(pkg.Dir, f)
			// Coverage profile path: <module>/<relative-from-module-root>
			// We can derive it from ImportPath + filename.
			profilePath := pkg.ImportPath + "/" + f
			absToProfile[absPath] = profilePath
		}
	}

	// Also build reverse: for mutants that use the cli subpackage path.
	// Coverage may use paths like "github.com/foo/bar/pkg/diffyml/cli/cli.go"
	// which is ImportPath + "/" + filename.

	for i := range mutants {
		if mutants[i].Status != mutator.StatusPending {
			continue
		}
		profilePath, ok := absToProfile[mutants[i].File]
		if !ok {
			// Try matching by suffix as fallback.
			profilePath = findProfilePath(mutants[i].File, absToProfile)
			if profilePath == "" {
				mutants[i].Status = mutator.StatusNotCovered
				continue
			}
		}
		if !profile.IsCovered(profilePath, mutants[i].Line, mutants[i].Col) {
			mutants[i].Status = mutator.StatusNotCovered
		}
	}
}

func findProfilePath(absPath string, mapping map[string]string) string {
	for abs, prof := range mapping {
		if strings.HasSuffix(absPath, abs[len(abs)-len(filepath.Base(abs)):]) && filepath.Base(abs) == filepath.Base(absPath) {
			// Check if the full paths match by suffix.
			if abs == absPath {
				return prof
			}
		}
	}
	return ""
}
