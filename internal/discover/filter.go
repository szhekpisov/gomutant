package discover

import (
	"path/filepath"

	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/mutator"
)

// FilterByCoverage marks mutants whose positions are not covered as StatusNotCovered.
// It builds a mapping from coverage-profile paths (module-relative) to absolute paths
// to bridge the two path formats.
func FilterByCoverage(mutants []mutator.Mutant, profile *coverage.Profile, pkgs []Package, goModule string) {
	// Build mapping: absolute path → coverage profile path.
	// Coverage uses "module/pkg/file.go", we need to map to "/abs/path/file.go".
	absToProfile := make(map[string]string)
	for _, pkg := range pkgs {
		for _, f := range pkg.GoFiles {
			absPath := filepath.Join(pkg.Dir, f)
			profilePath := pkg.ImportPath + "/" + f
			absToProfile[absPath] = profilePath
		}
	}

	for i := range mutants {
		if mutants[i].Status != mutator.StatusPending {
			continue
		}
		profilePath, ok := absToProfile[mutants[i].File]
		if !ok {
			mutants[i].Status = mutator.StatusNotCovered
			continue
		}
		if !profile.IsCovered(profilePath, mutants[i].Line, mutants[i].Col) {
			mutants[i].Status = mutator.StatusNotCovered
		}
	}
}
