package excludecalls

import "testing"

func TestRatio(t *testing.T) {
	// Ratio(1, 1) is the case that pins the literal: 100 → 101 survives
	// the other inputs, where integer division truncates the difference
	// away.
	if Ratio(1, 1) != 100 || Ratio(1, 2) != 50 || Ratio(3, 4) != 75 {
		t.Fatal("Ratio")
	}
}

func TestFail(t *testing.T) {
	// Never call Fail with a negative: log.Fatalf would exit the test
	// binary. The mutants inside that branch are the point of the
	// fixture, not the branch itself.
	Fail(1)
	Fail(0)
}

func TestTrace(t *testing.T) {
	if Trace(5, 3) != 2 || Trace(0, 0) != 0 || Trace(-1, 1) != -2 {
		t.Fatal("Trace")
	}
}
