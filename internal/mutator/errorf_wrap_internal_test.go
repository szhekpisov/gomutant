package mutator

import (
	"reflect"
	"testing"
)

// TestWrapVerbOffsets exercises the scanner directly rather than through a
// parsed literal. Going through go/ast would always prefix the input with an
// opening quote, leaving the loop's start index and its bounds arithmetic
// unreachable — a verb can never sit at index 0 of a literal's source text.
func TestWrapVerbOffsets(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []int
	}{
		{"verb at index zero", "%w", []int{0}},
		{"verb after text", "load: %w", []int{6}},
		{"two verbs", "%w and %w", []int{0, 7}},
		{"adjacent verbs", "%w%w", []int{0, 2}},
		{"escaped percent only", "%%w", nil},
		{"escape then verb", "%%%w", []int{2}},
		{"escape then text then verb", "100%% done: %w", []int{12}},
		{"other verbs ignored", "%s %d %v", nil},
		{"trailing percent", "oops %", nil},
		{"lone percent", "%", nil},
		{"empty", "", nil},
		{"no verbs", "plain text", nil},
		{"w without percent", "wrap", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapVerbOffsets(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("wrapVerbOffsets(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for _, off := range got {
				if off < 0 || off+2 > len(tt.in) {
					t.Fatalf("offset %d out of range for %d-byte input", off, len(tt.in))
				}
				if tt.in[off:off+2] != "%w" {
					t.Errorf("offset %d points at %q, want %q", off, tt.in[off:off+2], "%w")
				}
			}
		})
	}
}
