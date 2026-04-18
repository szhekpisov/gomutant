package untested

import "testing"

// Intentionally weak tests — some mutants should survive.

func TestMax(t *testing.T) {
	// Only tests one direction — mutants on the comparison will survive.
	if Max(3, 1) != 3 {
		t.Error("wrong")
	}
}

func TestDouble(t *testing.T) {
	// Tests with 0 — multiplication mutations survive (0*x == 0+x == 0).
	if Double(0) != 0 {
		t.Error("wrong")
	}
}

// IsEven has no test at all — all its mutants should be NOT COVERED.
