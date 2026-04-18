package coverage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFile(t *testing.T) {
	content := `mode: set
github.com/foo/bar/file.go:10.2,15.3 2 1
github.com/foo/bar/file.go:20.5,25.10 1 0
github.com/foo/bar/other.go:5.1,8.2 3 5
`
	dir := t.TempDir()
	path := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profile, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if len(profile.blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(profile.blocks))
	}

	b := profile.blocks[0]
	if b.File != "github.com/foo/bar/file.go" {
		t.Errorf("File=%q", b.File)
	}
	if b.StartLine != 10 || b.StartCol != 2 || b.EndLine != 15 || b.EndCol != 3 {
		t.Errorf("range=(%d.%d, %d.%d), want (10.2, 15.3)", b.StartLine, b.StartCol, b.EndLine, b.EndCol)
	}
	if b.NumStmt != 2 {
		t.Errorf("NumStmt=%d, want 2", b.NumStmt)
	}
	if b.Count != 1 {
		t.Errorf("Count=%d, want 1", b.Count)
	}
}

func TestParseFileNotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/coverage.out")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseFileMalformedLines(t *testing.T) {
	content := `mode: set
this is not valid
github.com/foo/bar/file.go:10.2,15.3 2 1
`
	dir := t.TempDir()
	path := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	profile, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v (should skip malformed)", err)
	}
	// Malformed line skipped, valid line parsed.
	if len(profile.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(profile.blocks))
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		line    string
		wantErr bool
		file    string
		count   int
	}{
		{"github.com/foo/bar/file.go:10.2,15.3 2 1", false, "github.com/foo/bar/file.go", 1},
		{"no-colon-here", true, "", 0},
		{"file.go:bad 1 1", true, "", 0},
		{"file.go:10.2,15.3 bad 1", true, "", 0},
		{"file.go:10.2,15.3 1 bad", true, "", 0},
		{"file.go:10.2 1 1", true, "", 0},      // no comma in range
		{"file.go:bad.2,15.3 1 1", true, "", 0}, // bad start line
		{"file.go:10.2,bad.3 1 1", true, "", 0}, // bad end line
		{"file.go:only-two-fields 1", true, "", 0},          // wrong field count (2)
		{"file.go:too many fields here 1 2 3", true, "", 0}, // wrong field count (5)
	}
	for _, tc := range tests {
		b, err := parseLine(tc.line)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseLine(%q): expected error", tc.line)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLine(%q): %v", tc.line, err)
			continue
		}
		if b.File != tc.file {
			t.Errorf("parseLine(%q).File=%q, want %q", tc.line, b.File, tc.file)
		}
		if b.Count != tc.count {
			t.Errorf("parseLine(%q).Count=%d, want %d", tc.line, b.Count, tc.count)
		}
	}
}

func TestParseLineCol(t *testing.T) {
	tests := []struct {
		input   string
		line    int
		col     int
		wantErr bool
	}{
		{"10.5", 10, 5, false},
		{"1.1", 1, 1, false},
		{"nodot", 0, 0, true},
		{"bad.5", 0, 0, true},
		{"10.bad", 0, 0, true},
	}
	for _, tc := range tests {
		line, col, err := parseLineCol(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseLineCol(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLineCol(%q): %v", tc.input, err)
			continue
		}
		if line != tc.line || col != tc.col {
			t.Errorf("parseLineCol(%q) = (%d, %d), want (%d, %d)", tc.input, line, col, tc.line, tc.col)
		}
	}
}

// errorReader returns data then an error on the next read.
type errorReader struct {
	data string
	read bool
}

func (r *errorReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, fmt.Errorf("injected read error")
}

func TestParseReaderScannerError(t *testing.T) {
	// Create a reader that returns data then errors mid-stream.
	// The data is valid enough to start scanning but the error triggers scanner.Err().
	r := &errorReader{data: "mode: set\n"}
	_, err := parseReader(r)
	if err == nil {
		t.Fatal("expected error from scanner")
	}
}

func TestParseReaderValid(t *testing.T) {
	r := strings.NewReader("mode: set\nfile.go:10.2,15.3 2 1\n")
	profile, err := parseReader(r)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if len(profile.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(profile.blocks))
	}
}

func TestParseReaderEmpty(t *testing.T) {
	r := strings.NewReader("")
	profile, err := parseReader(r)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if len(profile.blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(profile.blocks))
	}
}

// Ensure io import is used.
var _ io.Reader = (*errorReader)(nil)

func TestIsCovered(t *testing.T) {
	profile := &Profile{
		blocks: []Block{
			{File: "file.go", StartLine: 10, StartCol: 1, EndLine: 15, EndCol: 10, Count: 1},
			{File: "file.go", StartLine: 20, StartCol: 1, EndLine: 25, EndCol: 10, Count: 0},
			{File: "other.go", StartLine: 5, StartCol: 1, EndLine: 8, EndCol: 5, Count: 3},
		},
	}

	tests := []struct {
		file string
		line int
		col  int
		want bool
	}{
		// Inside covered block.
		{"file.go", 12, 5, true},
		// At start of covered block.
		{"file.go", 10, 1, true},
		// At end of covered block.
		{"file.go", 15, 10, true},
		// Before start col on start line.
		{"file.go", 10, 0, false},
		// After end col on end line.
		{"file.go", 15, 11, false},
		// Inside uncovered block (count=0).
		{"file.go", 22, 5, false},
		// Outside any block.
		{"file.go", 30, 1, false},
		// Wrong file.
		{"missing.go", 12, 5, false},
		// In other.go block.
		{"other.go", 6, 3, true},
		// Before block.
		{"file.go", 5, 1, false},
	}

	for _, tc := range tests {
		got := profile.IsCovered(tc.file, tc.line, tc.col)
		if got != tc.want {
			t.Errorf("IsCovered(%q, %d, %d) = %v, want %v", tc.file, tc.line, tc.col, got, tc.want)
		}
	}
}

func TestInBlock(t *testing.T) {
	b := Block{StartLine: 10, StartCol: 5, EndLine: 20, EndCol: 15}

	tests := []struct {
		line int
		col  int
		want bool
	}{
		{9, 10, false},   // before start line
		{21, 1, false},   // after end line
		{10, 4, false},   // start line, before start col
		{10, 5, true},    // start line, at start col
		{10, 10, true},   // start line, after start col
		{15, 1, true},    // middle line
		{20, 15, true},   // end line, at end col
		{20, 16, false},  // end line, after end col
	}

	for _, tc := range tests {
		got := inBlock(b, tc.line, tc.col)
		if got != tc.want {
			t.Errorf("inBlock(line=%d, col=%d) = %v, want %v", tc.line, tc.col, got, tc.want)
		}
	}
}
