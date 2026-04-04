package mutator

import "time"

type MutantStatus int

const (
	StatusPending    MutantStatus = iota
	StatusKilled                  // Test failed — mutant detected.
	StatusLived                   // Tests passed — mutant survived.
	StatusNotCovered              // No test covers this code.
	StatusNotViable               // Mutant causes compile error.
	StatusTimedOut                // Test execution timed out.
)

func (s MutantStatus) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusKilled:
		return "KILLED"
	case StatusLived:
		return "LIVED"
	case StatusNotCovered:
		return "NOT COVERED"
	case StatusNotViable:
		return "NOT VIABLE"
	case StatusTimedOut:
		return "TIMED OUT"
	default:
		return "UNKNOWN"
	}
}

type MutationType string

const (
	ArithmeticBase       MutationType = "ARITHMETIC_BASE"
	ConditionalsBoundary MutationType = "CONDITIONALS_BOUNDARY"
	ConditionalsNegation MutationType = "CONDITIONALS_NEGATION"
	IncrementDecrement   MutationType = "INCREMENT_DECREMENT"
	InvertNegatives      MutationType = "INVERT_NEGATIVES"
	BranchIf             MutationType = "BRANCH_IF"
	BranchElse           MutationType = "BRANCH_ELSE"
	BranchCase           MutationType = "BRANCH_CASE"
	ExpressionRemove     MutationType = "EXPRESSION_REMOVE"
	StatementRemove      MutationType = "STATEMENT_REMOVE"
)

type MutantCandidate struct {
	Type        MutationType
	Pos         Position // For reporting (file, line, col).
	Original    string   // Display text before mutation.
	Replacement string   // Replacement source text.
	StartOffset int      // Byte offset of replacement start.
	EndOffset   int      // Byte offset of replacement end (exclusive).
}

// Position holds the location of a mutation for reporting purposes.
type Position struct {
	Filename string
	Line     int
	Column   int
	Offset   int
}

type Mutant struct {
	ID          int
	Type        MutationType
	File        string // Absolute path.
	RelFile     string // Relative to module root (for report).
	Line        int
	Col         int
	Original    string
	Replacement string
	StartOffset int // Byte offset in source file.
	EndOffset   int // Byte offset end (exclusive).
	Status      MutantStatus
	Duration    time.Duration
	Pkg         string // Package import path for go test.
}
