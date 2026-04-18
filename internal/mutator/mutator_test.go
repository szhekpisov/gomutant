package mutator_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

func parse(t *testing.T, src string) (*token.FileSet, *ast.File, []byte) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return fset, f, []byte(src)
}

// --- Token-level mutators ---

func TestArithmeticBase(t *testing.T) {
	src := `package p
func f() int { return 1 + 2 - 3 * 4 / 5 % 6 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ArithmeticBase)
	candidates := m.Discover(fset, file, srcBytes)

	// AST walk order depends on tree shape. Just check total count and all swaps are valid.
	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidates, got %d", len(candidates))
	}

	validSwaps := map[string]string{
		"+": "-", "-": "+", "*": "/", "/": "*", "%": "*",
	}
	for i, c := range candidates {
		want, ok := validSwaps[c.Original]
		if !ok {
			t.Errorf("candidate %d: unexpected original %q", i, c.Original)
		} else if c.Replacement != want {
			t.Errorf("candidate %d: %q→%q, want %q→%q", i, c.Original, c.Replacement, c.Original, want)
		}
	}

	for i, c := range candidates {
		if c.Type != mutator.ArithmeticBase {
			t.Errorf("candidate %d: type=%v, want %v", i, c.Type, mutator.ArithmeticBase)
		}
		if c.StartOffset >= c.EndOffset {
			t.Errorf("candidate %d: invalid offset range [%d:%d)", i, c.StartOffset, c.EndOffset)
		}
	}
}

func TestConditionalsBoundary(t *testing.T) {
	src := `package p
func f(a, b int) {
	_ = a < b
	_ = a <= b
	_ = a > b
	_ = a >= b
	_ = a == b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ConditionalsBoundary)
	candidates := m.Discover(fset, file, srcBytes)

	// <→<=, <=→<, >→>=, >=→> (== is not boundary)
	if len(candidates) != 4 {
		t.Fatalf("expected 4 candidates, got %d", len(candidates))
	}

	expected := []struct {
		original    string
		replacement string
	}{
		{"<", "<="},
		{"<=", "<"},
		{">", ">="},
		{">=", ">"},
	}
	for i, c := range candidates {
		if c.Original != expected[i].original || c.Replacement != expected[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, expected[i].original, expected[i].replacement)
		}
	}
}

func TestConditionalsNegation(t *testing.T) {
	src := `package p
func f(a, b int) {
	_ = a == b
	_ = a != b
	_ = a < b
	_ = a >= b
	_ = a > b
	_ = a <= b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ConditionalsNegation)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 6 {
		t.Fatalf("expected 6 candidates, got %d", len(candidates))
	}

	expected := []struct {
		original    string
		replacement string
	}{
		{"==", "!="},
		{"!=", "=="},
		{"<", ">="},
		{">=", "<"},
		{">", "<="},
		{"<=", ">"},
	}
	for i, c := range candidates {
		if c.Original != expected[i].original || c.Replacement != expected[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, expected[i].original, expected[i].replacement)
		}
	}
}

func TestIncrementDecrement(t *testing.T) {
	src := `package p
func f() {
	x := 0
	x++
	x--
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IncrementDecrement)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	if candidates[0].Original != "++" || candidates[0].Replacement != "--" {
		t.Errorf("candidate 0: got %q→%q, want \"++\"→\"--\"", candidates[0].Original, candidates[0].Replacement)
	}
	if candidates[1].Original != "--" || candidates[1].Replacement != "++" {
		t.Errorf("candidate 1: got %q→%q, want \"--\"→\"++\"", candidates[1].Original, candidates[1].Replacement)
	}
}

func TestInvertNegatives(t *testing.T) {
	src := `package p
func f() int { return -42 + -1 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertNegatives)
	candidates := m.Discover(fset, file, srcBytes)

	// Two unary negatives: -42 and -1.
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Original != "-" || c.Replacement != "+" {
			t.Errorf("candidate %d: got %q→%q, want \"-\"→\"+\"", i, c.Original, c.Replacement)
		}
		if c.EndOffset-c.StartOffset != 1 {
			t.Errorf("candidate %d: byte length=%d, want 1", i, c.EndOffset-c.StartOffset)
		}
	}
}

func TestInvertNegativesBinary(t *testing.T) {
	src := `package p
func f(a, b int) int { return a - b }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertNegatives)
	candidates := m.Discover(fset, file, srcBytes)

	// Binary subtraction also produces an INVERT_NEGATIVES candidate (matches gremlins).
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Original != "-" || candidates[0].Replacement != "+" {
		t.Errorf("got %q→%q, want \"-\"→\"+\"", candidates[0].Original, candidates[0].Replacement)
	}
}

// --- Block-level mutators ---

func TestBranchIf(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return x
	}
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchIf)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	c := candidates[0]
	if c.Replacement != "{ _ = 0 }" {
		t.Errorf("replacement=%q, want %q", c.Replacement, "{ _ = 0 }")
	}
	if c.Type != mutator.BranchIf {
		t.Errorf("type=%v, want %v", c.Type, mutator.BranchIf)
	}
}

func TestBranchIfElseIf(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchIf)
	candidates := m.Discover(fset, file, srcBytes)

	// Both the "if" body and the "else if" body should be candidates.
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestBranchElse(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return x
	} else {
		return 0
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchElse)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "{ _ = 0 }" {
		t.Errorf("replacement=%q, want %q", candidates[0].Replacement, "{ _ = 0 }")
	}
}

func TestBranchElseSkipsElseIf(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchElse)
	candidates := m.Discover(fset, file, srcBytes)

	// else-if is not a plain else block, so no candidates.
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates for else-if chain, got %d", len(candidates))
	}
}

func TestBranchCase(t *testing.T) {
	src := `package p
func f(x int) int {
	switch x {
	case 1:
		return 10
	case 2:
		return 20
	default:
		return 0
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchCase)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Replacement != "_ = 0" {
			t.Errorf("candidate %d: replacement=%q, want %q", i, c.Replacement, "_ = 0")
		}
	}
}

func TestExpressionRemove(t *testing.T) {
	src := `package p
func f(a, b bool) bool {
	return a && b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ExpressionRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// && produces 2 candidates: replace a with true, replace b with true.
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	if candidates[0].Replacement != "true" {
		t.Errorf("candidate 0: replacement=%q, want \"true\"", candidates[0].Replacement)
	}
	if candidates[1].Replacement != "true" {
		t.Errorf("candidate 1: replacement=%q, want \"true\"", candidates[1].Replacement)
	}
}

func TestExpressionRemoveOr(t *testing.T) {
	src := `package p
func f(a, b bool) bool {
	return a || b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ExpressionRemove)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Replacement != "false" {
			t.Errorf("candidate %d: replacement=%q, want \"false\"", i, c.Replacement)
		}
	}
}

func TestStatementRemoveAssign(t *testing.T) {
	src := `package p
func f() int {
	x := 0
	x = 42
	return x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// Only "x = 42" is a plain assign. ":=" is skipped.
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "_ = 42" {
		t.Errorf("replacement=%q, want \"_ = 42\"", candidates[0].Replacement)
	}
}

func TestStatementRemoveExprStmt(t *testing.T) {
	src := `package p
func f() {
	println("hello")
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "_ = 0" {
		t.Errorf("replacement=%q, want \"_ = 0\"", candidates[0].Replacement)
	}
}

func TestStatementRemoveIncDec(t *testing.T) {
	src := `package p
func f() int {
	x := 0
	x++
	return x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// x++ is also an IncDecStmt.
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "_ = x" {
		t.Errorf("replacement=%q, want \"_ = x\"", candidates[0].Replacement)
	}
}

func TestStatementRemoveSkipsShortDecl(t *testing.T) {
	src := `package p
func f() {
	x := 42
	_ = x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// ":=" is skipped, "_ = x" is an AssignStmt with Tok=ASSIGN.
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
}

// --- Registry ---

func TestRegistryEnabledMutators(t *testing.T) {
	reg := mutator.NewRegistry()

	all := reg.Mutators()
	if len(all) != 10 {
		t.Fatalf("expected 10 mutators, got %d", len(all))
	}

	only := reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil)
	if len(only) != 1 {
		t.Fatalf("expected 1 with --only, got %d", len(only))
	}

	disabled := reg.EnabledMutators(nil, []string{"ARITHMETIC_BASE", "BRANCH_IF"})
	if len(disabled) != 8 {
		t.Fatalf("expected 8 after disabling 2, got %d", len(disabled))
	}
}

// --- Offset sanity ---

func TestOffsetsMatchSource(t *testing.T) {
	src := `package p
func f() int { return 1 + 2 }
`
	fset, file, srcBytes := parse(t, src)
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		for _, c := range m.Discover(fset, file, srcBytes) {
			if c.StartOffset < 0 || c.EndOffset > len(srcBytes) || c.StartOffset > c.EndOffset {
				t.Errorf("%s: invalid offset [%d:%d) in %d-byte source", c.Type, c.StartOffset, c.EndOffset, len(srcBytes))
			}
			got := string(srcBytes[c.StartOffset:c.EndOffset])
			if got != c.Original {
				t.Errorf("%s at offset [%d:%d): source has %q, candidate says %q",
					c.Type, c.StartOffset, c.EndOffset, got, c.Original)
			}
		}
	}
}

// --- Status String ---

func TestMutantStatusString(t *testing.T) {
	tests := []struct {
		status mutator.MutantStatus
		want   string
	}{
		{mutator.StatusPending, "PENDING"},
		{mutator.StatusKilled, "KILLED"},
		{mutator.StatusLived, "LIVED"},
		{mutator.StatusNotCovered, "NOT COVERED"},
		{mutator.StatusNotViable, "NOT VIABLE"},
		{mutator.StatusTimedOut, "TIMED OUT"},
		{mutator.MutantStatus(99), "UNKNOWN"},
	}
	for _, tc := range tests {
		got := tc.status.String()
		if got != tc.want {
			t.Errorf("MutantStatus(%d).String() = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// --- No-op mutators on empty functions ---

func TestMutatorsOnEmptyFunc(t *testing.T) {
	src := `package p
func f() {}
`
	fset, file, srcBytes := parse(t, src)
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		// Should not panic on minimal source.
		_ = m.Discover(fset, file, srcBytes)
	}
}

// TestMutatorsNonMatchingNodes exercises the early-return paths in each mutator
// by providing AST nodes that don't match the mutator's target pattern.
func TestMutatorsNonMatchingNodes(t *testing.T) {
	// This source has diverse AST nodes but specifically avoids matching
	// certain mutator patterns, exercising the "no match" branches.
	src := `package p

import "fmt"

func f(x int) string {
	// Bitwise ops — not in arithmetic swap table.
	a := x & 0xff
	b := x | 0x0f
	c := x ^ 0x01
	d := x << 2
	e := x >> 1

	// String concatenation — ADD token but not numeric.
	s := "hello" + "world"

	// Comparison with == — not in ConditionalsBoundary.
	if a == b {
		fmt.Println(c, d, e, s)
	}

	// For loop (not if/switch).
	for i := 0; i < 10; i++ {
		_ = i
	}

	// Select statement (not switch).
	ch := make(chan int, 1)
	ch <- 1
	select {
	case v := <-ch:
		_ = v
	}

	// Type switch (case clause with no body beyond type assert).
	var iface interface{} = 42
	switch iface.(type) {
	case int:
	}

	// Return statement (not AssignStmt/ExprStmt/IncDecStmt for StatementRemove).
	return fmt.Sprintf("%d %d", a, b)
}
`
	fset, file, srcBytes := parse(t, src)
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		candidates := m.Discover(fset, file, srcBytes)
		// We just want these to run without panic and exercise all branches.
		_ = candidates
	}
}

// --- Edge cases for branch mutators ---

func TestBranchIfEmptyBody(t *testing.T) {
	src := `package p
func f(x int) {
	if x > 0 {
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchIf)
	candidates := m.Discover(fset, file, srcBytes)
	// Empty if body should be skipped.
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for empty if body, got %d", len(candidates))
	}
}

func TestBranchElseEmptyBody(t *testing.T) {
	src := `package p
func f(x int) {
	if x > 0 {
		_ = x
	} else {
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchElse)
	candidates := m.Discover(fset, file, srcBytes)
	// Empty else body should be skipped.
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for empty else body, got %d", len(candidates))
	}
}

// --- EnabledMutators with both only and disable ---

func TestRegistryEnabledMutatorsNoFilter(t *testing.T) {
	reg := mutator.NewRegistry()
	all := reg.EnabledMutators(nil, nil)
	if len(all) != 10 {
		t.Errorf("expected 10, got %d", len(all))
	}
}

// findMutator returns the mutator of the given type from the registry.
func findMutator(t *testing.T, typ mutator.MutationType) mutator.Mutator {
	t.Helper()
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		if m.Type() == typ {
			return m
		}
	}
	t.Fatalf("mutator %v not found in registry", typ)
	return nil
}
