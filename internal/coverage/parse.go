package coverage

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Block represents a single coverage block from a Go coverage profile.
type Block struct {
	File      string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	NumStmt   int
	Count     int
}

// Profile holds parsed coverage data indexed for fast lookup.
type Profile struct {
	blocks []Block
}

// ParseFile parses a Go coverage profile file.
// Format: mode line, then "file:startLine.startCol,endLine.endCol numStmt count"
func ParseFile(path string) (*Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening coverage profile: %w", err)
	}
	defer f.Close()

	var blocks []Block
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		b, err := parseLine(line)
		if err != nil {
			continue // Skip malformed lines.
		}
		blocks = append(blocks, b)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading coverage profile: %w", err)
	}

	return &Profile{blocks: blocks}, nil
}

// IsCovered returns true if the given position (file, line, col) falls
// within a coverage block with count > 0.
func (p *Profile) IsCovered(file string, line, col int) bool {
	for _, b := range p.blocks {
		if b.File != file {
			continue
		}
		if b.Count == 0 {
			continue
		}
		if !inBlock(b, line, col) {
			continue
		}
		return true
	}
	return false
}

func inBlock(b Block, line, col int) bool {
	// Check if (line, col) is within [startLine.startCol, endLine.endCol].
	if line < b.StartLine || line > b.EndLine {
		return false
	}
	if line == b.StartLine && col < b.StartCol {
		return false
	}
	if line == b.EndLine && col > b.EndCol {
		return false
	}
	return true
}

// parseLine parses a single coverage profile line.
// Format: "file:startLine.startCol,endLine.endCol numStmt count"
func parseLine(line string) (Block, error) {
	// Split "file:rest" at the last colon (file paths on Windows have colons).
	lastColon := strings.LastIndex(line, ":")
	if lastColon < 0 {
		return Block{}, fmt.Errorf("no colon in line")
	}
	file := line[:lastColon]
	rest := line[lastColon+1:]

	// rest = "startLine.startCol,endLine.endCol numStmt count"
	parts := strings.Fields(rest)
	if len(parts) != 3 {
		return Block{}, fmt.Errorf("expected 3 fields, got %d", len(parts))
	}

	rangeStr := parts[0] // "startLine.startCol,endLine.endCol"
	numStmt, err := strconv.Atoi(parts[1])
	if err != nil {
		return Block{}, err
	}
	count, err := strconv.Atoi(parts[2])
	if err != nil {
		return Block{}, err
	}

	// Parse "startLine.startCol,endLine.endCol"
	comma := strings.Index(rangeStr, ",")
	if comma < 0 {
		return Block{}, fmt.Errorf("no comma in range")
	}
	startStr := rangeStr[:comma]
	endStr := rangeStr[comma+1:]

	startLine, startCol, err := parseLineCol(startStr)
	if err != nil {
		return Block{}, err
	}
	endLine, endCol, err := parseLineCol(endStr)
	if err != nil {
		return Block{}, err
	}

	return Block{
		File:      file,
		StartLine: startLine,
		StartCol:  startCol,
		EndLine:   endLine,
		EndCol:    endCol,
		NumStmt:   numStmt,
		Count:     count,
	}, nil
}

func parseLineCol(s string) (int, int, error) {
	dot := strings.Index(s, ".")
	if dot < 0 {
		return 0, 0, fmt.Errorf("no dot in %q", s)
	}
	line, err := strconv.Atoi(s[:dot])
	if err != nil {
		return 0, 0, err
	}
	col, err := strconv.Atoi(s[dot+1:])
	if err != nil {
		return 0, 0, err
	}
	return line, col, nil
}
