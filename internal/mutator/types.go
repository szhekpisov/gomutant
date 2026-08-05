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
	StatusEquivalent              // Mutant provably equivalent to the original (TCE).
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
	case StatusEquivalent:
		return "EQUIVALENT"
	default:
		return "UNKNOWN"
	}
}

type MutationType string

const (
	ArithmeticBase           MutationType = "ARITHMETIC_BASE"
	ConditionalsBoundary     MutationType = "CONDITIONALS_BOUNDARY"
	ConditionalsNegation     MutationType = "CONDITIONALS_NEGATION"
	IncrementDecrement       MutationType = "INCREMENT_DECREMENT"
	InvertNegatives          MutationType = "INVERT_NEGATIVES"
	InvertAssignments        MutationType = "INVERT_ASSIGNMENTS"
	InvertBitwise            MutationType = "INVERT_BITWISE"
	InvertBitwiseAssignments MutationType = "INVERT_BITWISE_ASSIGNMENTS"
	InvertLogical            MutationType = "INVERT_LOGICAL"
	InvertLoopCtrl           MutationType = "INVERT_LOOP_CTRL"
	RemoveSelfAssignments    MutationType = "REMOVE_SELF_ASSIGNMENTS"
	BranchIf                 MutationType = "BRANCH_IF"
	BranchElse               MutationType = "BRANCH_ELSE"
	BranchCase               MutationType = "BRANCH_CASE"
	ExpressionRemove         MutationType = "EXPRESSION_REMOVE"
	StatementRemove          MutationType = "STATEMENT_REMOVE"
	IntegerIncrement         MutationType = "INTEGER_INCREMENT"
	IntegerDecrement         MutationType = "INTEGER_DECREMENT"
	FloatIncrement           MutationType = "FLOAT_INCREMENT"
	FloatDecrement           MutationType = "FLOAT_DECREMENT"
	LoopCondition            MutationType = "LOOP_CONDITION"
	RangeBreak               MutationType = "RANGE_BREAK"
	ReturnErrorNil           MutationType = "RETURN_ERROR_NIL"
	ReturnZero               MutationType = "RETURN_ZERO"
	ReturnTrue               MutationType = "RETURN_TRUE"
	ReturnFalse              MutationType = "RETURN_FALSE"
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
	ID           int
	Type         MutationType
	File         string // Absolute path.
	RelFile      string // Relative to module root (for report).
	Line         int
	Col          int
	Original     string
	Replacement  string
	StartOffset  int    // Byte offset in source file.
	EndOffset    int    // Byte offset end (exclusive).
	CoverageFile string // Coverage profile path (e.g. "module/pkg/file.go").
	Status       MutantStatus
	Duration     time.Duration
	Pkg          string // Package import path for go test.
	// FromCache marks results sourced from a prior-run cache entry
	// rather than this run's go-test invocation. In-memory only — not
	// serialized to the JSON report. Used by report.Generate to count
	// MutantsCached without changing the gremlins-compatible schema.
	FromCache bool
}
