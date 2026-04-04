package patch

import "fmt"

// Apply returns a copy of original with bytes [start:end) replaced by replacement.
func Apply(original []byte, start, end int, replacement string) ([]byte, error) {
	if start < 0 || end > len(original) || start > end {
		return nil, fmt.Errorf("patch: invalid range [%d:%d) in %d-byte file", start, end, len(original))
	}
	out := make([]byte, 0, len(original)-(end-start)+len(replacement))
	out = append(out, original[:start]...)
	out = append(out, replacement...)
	out = append(out, original[end:]...)
	return out, nil
}
