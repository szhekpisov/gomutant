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
	p, err := ParseFile("/nonexistent/coverage.out")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if p != nil {
		t.Errorf("expected nil profile on open error, got %v", p)
	}
	// Error must come from the open-failure branch, not a fall-through to
	// parseReader on a nil *os.File (which would wrap ErrInvalid with
	// "reading coverage profile"). Kills BRANCH_IF that elides the return.
	if !strings.Contains(err.Error(), "opening coverage profile") {
		t.Errorf("error should wrap open failure, got: %v", err)
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
		name string
		line int
		col  int
		want bool
	}{
		{"before start line", 9, 10, false},
		{"after end line", 21, 1, false},
		{"start line, before start col", 10, 4, false},
		{"start line, at start col", 10, 5, true},
		{"start line, after start col", 10, 10, true},
		{"middle line", 15, 1, true},
		{"end line, at end col", 20, 15, true},
		{"end line, after end col", 20, 16, false},
		// Critical for EXPRESSION_REMOVE on `line == b.EndLine && col > b.EndCol`:
		// col > EndCol but NOT on end line → must still return true (inside the block).
		{"mid-line col exceeds EndCol", 15, 99, true},
		// Critical for EXPRESSION_REMOVE right operand → true: end line with col <= EndCol must return true.
		{"end line at start col", 20, 1, true},
		{"end line middle col", 20, 7, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inBlock(b, tc.line, tc.col)
			if got != tc.want {
				t.Errorf("inBlock(line=%d, col=%d) = %v, want %v", tc.line, tc.col, got, tc.want)
			}
		})
	}
}

// TestParseReaderSkipsModePrefixedLineThatWouldParse kills BRANCH_IF on the
// `strings.HasPrefix(line, "mode:")` body. Under mutation the "continue" is
// elided, so a line that begins with "mode:" but is otherwise well-formed
// ("mode: file.go:10.1,15.2 2 1" — three fields after the last colon, valid
// range/counts) is parsed as a coverage block instead of being skipped.
// Using a mode-prefixed line that would *parse* forces the two paths to
// diverge: original → 1 block, mutated → 2 blocks.
func TestParseReaderSkipsModePrefixedLineThatWouldParse(t *testing.T) {
	input := "mode: file.go:10.1,15.2 2 1\nfile.go:20.1,25.2 1 1\n"
	profile, err := parseReader(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if len(profile.blocks) != 1 {
		t.Fatalf("want 1 block (mode-prefixed line skipped), got %d: %+v", len(profile.blocks), profile.blocks)
	}
	if strings.HasPrefix(profile.blocks[0].File, "mode:") {
		t.Errorf("mode-prefixed line was parsed as a block: %+v", profile.blocks[0])
	}
}

// TestParseLineColonAtStart covers CONDITIONALS_BOUNDARY on `lastColon < 0`:
// with mutation `<= 0`, a line starting with ":" (lastColon = 0) would incorrectly error.
func TestParseLineColonAtStart(t *testing.T) {
	// Line with colon at position 0 → lastColon = 0, not -1.
	// Format ":<rest>" → file = "", rest = "<rest>".
	// But "<rest>" needs 3 fields to be valid.
	// Use ":10.2,15.3 2 1" → file="", valid range and numStmt/count.
	b, err := parseLine(":10.2,15.3 2 1")
	if err != nil {
		t.Errorf("parseLine with colon at start should succeed, got error: %v", err)
	}
	if b.File != "" {
		t.Errorf("File=%q, want empty", b.File)
	}
}

// TestParseLineCommaAtStart covers CONDITIONALS_BOUNDARY on `comma < 0`:
// with mutation `<= 0`, a line with comma at position 0 (empty start) would error.
func TestParseLineCommaAtStart(t *testing.T) {
	// Comma at position 0 within the range string → comma = 0.
	// parseLineCol("") fails (no dot) → error.
	_, err := parseLine("file.go:,10.5 2 1")
	if err == nil {
		t.Error("parseLine with empty start of range should error (no dot in startStr)")
	}
	// The error should come from parseLineCol, not from comma-not-found.
	// The `comma < 0` guard with mutation `<= 0` would cause comma=0 to be treated as -1 (error).
	// But with original `< 0`, comma=0 proceeds to parseLineCol which errors with "no dot".
	// Either way errors, but error message differs.
	if !strings.Contains(err.Error(), "no dot") {
		t.Errorf("expected 'no dot' error (from parseLineCol), got: %v", err)
	}
}

// TestParseLineColDotAtStart covers CONDITIONALS_BOUNDARY on `dot < 0` in parseLineCol:
// position input ".5" has dot at 0.
func TestParseLineColDotAtStart(t *testing.T) {
	// ".5,10.3" → startStr = ".5" → dot = 0.
	// Original `< 0` false → proceed. Atoi("") errors → return error (numeric parse).
	// Mutation `<= 0` true → return "no dot" error.
	_, err := parseLine("file.go:.5,10.3 1 1")
	if err == nil {
		t.Error("parseLine with empty start line should error")
	}
	// Error should NOT be "no dot" — it should be a numeric parse error, because original
	// `dot < 0` is false (dot is 0), so we proceed to Atoi which fails.
	if strings.Contains(err.Error(), "no dot") {
		t.Errorf("unexpected 'no dot' error (should be numeric): %v", err)
	}
}
