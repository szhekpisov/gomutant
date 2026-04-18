package discover

import "os"

// readFileBytesFunc is swappable for testing.
var readFileBytesFunc = os.ReadFile

func readFileBytes(path string) ([]byte, error) {
	return readFileBytesFunc(path)
}

// PreReadFiles reads all source files from the given packages into memory.
// Returns a map from absolute path to file contents.
func PreReadFiles(pkgs []Package) (map[string][]byte, error) {
	files := make(map[string][]byte)
	for _, pkg := range pkgs {
		for _, filename := range pkg.GoFiles {
			absPath := pkg.Dir + "/" + filename
			if _, ok := files[absPath]; ok {
				continue
			}
			data, err := os.ReadFile(absPath)
			if err != nil {
				return nil, err
			}
			files[absPath] = data
		}
	}
	return files, nil
}
