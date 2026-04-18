package simple

import "testing"

func TestAdd(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 3},
		{0, 0, 0},
		{-1, 1, 0},
		{-3, -4, -7},
	}
	for _, tc := range tests {
		if got := Add(tc.a, tc.b); got != tc.want {
			t.Errorf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsPositive(t *testing.T) {
	if !IsPositive(1) {
		t.Error("IsPositive(1) should be true")
	}
	if IsPositive(0) {
		t.Error("IsPositive(0) should be false")
	}
	if IsPositive(-1) {
		t.Error("IsPositive(-1) should be false")
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		n, want int
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
	}
	for _, tc := range tests {
		if got := Abs(tc.n); got != tc.want {
			t.Errorf("Abs(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		v, lo, hi, want int
	}{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
	}
	for _, tc := range tests {
		if got := Clamp(tc.v, tc.lo, tc.hi); got != tc.want {
			t.Errorf("Clamp(%d, %d, %d) = %d, want %d", tc.v, tc.lo, tc.hi, got, tc.want)
		}
	}
}

func TestCountPositives(t *testing.T) {
	tests := []struct {
		nums []int
		want int
	}{
		{[]int{1, 2, 3}, 3},
		{[]int{-1, 0, 1}, 1},
		{[]int{-1, -2}, 0},
		{nil, 0},
	}
	for _, tc := range tests {
		if got := CountPositives(tc.nums); got != tc.want {
			t.Errorf("CountPositives(%v) = %d, want %d", tc.nums, got, tc.want)
		}
	}
}

func TestGrade(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{95, "A"},
		{90, "A"},
		{85, "B"},
		{80, "B"},
		{75, "C"},
		{70, "C"},
		{65, "F"},
		{0, "F"},
	}
	for _, tc := range tests {
		if got := Grade(tc.score); got != tc.want {
			t.Errorf("Grade(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestBoth(t *testing.T) {
	if !Both(true, true) {
		t.Error("Both(true, true) should be true")
	}
	if Both(true, false) {
		t.Error("Both(true, false) should be false")
	}
	if Both(false, true) {
		t.Error("Both(false, true) should be false")
	}
	if Both(false, false) {
		t.Error("Both(false, false) should be false")
	}
}

func TestEither(t *testing.T) {
	if !Either(true, true) {
		t.Error("Either(true, true) should be true")
	}
	if !Either(true, false) {
		t.Error("Either(true, false) should be true")
	}
	if !Either(false, true) {
		t.Error("Either(false, true) should be true")
	}
	if Either(false, false) {
		t.Error("Either(false, false) should be false")
	}
}
