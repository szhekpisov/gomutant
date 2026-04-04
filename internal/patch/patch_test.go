package patch_test

import (
	"testing"

	"github.com/szhekpisov/gomutant/internal/patch"
)

func TestApplySameLength(t *testing.T) {
	original := []byte("a + b")
	got, err := patch.Apply(original, 2, 3, "-")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a - b" {
		t.Errorf("got %q, want %q", string(got), "a - b")
	}
}

func TestApplyShorter(t *testing.T) {
	original := []byte("a <= b")
	got, err := patch.Apply(original, 2, 4, "<")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a < b" {
		t.Errorf("got %q, want %q", string(got), "a < b")
	}
}

func TestApplyLonger(t *testing.T) {
	original := []byte("a < b")
	got, err := patch.Apply(original, 2, 3, "<=")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a <= b" {
		t.Errorf("got %q, want %q", string(got), "a <= b")
	}
}

func TestApplyBlockReplacement(t *testing.T) {
	original := []byte(`if x > 0 {
		return x
	}`)
	// Replace the block body "{\n\t\treturn x\n\t}" with "{ _ = 0 }"
	got, err := patch.Apply(original, 9, len(original), "{ _ = 0 }")
	if err != nil {
		t.Fatal(err)
	}
	want := "if x > 0 { _ = 0 }"
	if string(got) != want {
		t.Errorf("got %q, want %q", string(got), want)
	}
}

func TestApplyInvalidRange(t *testing.T) {
	original := []byte("hello")

	tests := []struct {
		name       string
		start, end int
	}{
		{"negative start", -1, 3},
		{"end beyond length", 0, 10},
		{"start > end", 3, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := patch.Apply(original, tt.start, tt.end, "x")
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestApplyDoesNotMutateOriginal(t *testing.T) {
	original := []byte("a + b")
	snapshot := string(original)
	_, err := patch.Apply(original, 2, 3, "-")
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != snapshot {
		t.Error("Apply mutated the original slice")
	}
}
