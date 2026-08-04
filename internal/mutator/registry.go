package mutator

import (
	"go/ast"
	"go/token"
)

// Mutator discovers mutation candidates in a parsed Go source file.
type Mutator interface {
	Type() MutationType
	Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate
}

// Registry holds all registered mutators. typeSet mirrors mutators by
// MutationType so name-validation lookups (IsKnown / UnknownNames) and
// the directive parser share a single source of truth populated in
// NewRegistry.
type Registry struct {
	mutators []Mutator
	typeSet  map[string]struct{}
}

// NewRegistry creates a registry with all built-in mutators.
func NewRegistry() *Registry {
	mutators := []Mutator{
		&arithmeticBase{},
		&conditionalsBoundary{},
		&conditionalsNegation{},
		&incrementDecrement{},
		&invertNegatives{},
		&invertAssignments{},
		&invertBitwise{},
		&invertBitwiseAssignments{},
		&invertLogical{},
		&invertLoopCtrl{},
		&removeSelfAssignments{},
		&branchIf{},
		&branchElse{},
		&branchCase{},
		&expressionRemove{},
		&statementRemove{},
		&literalStep{typ: IntegerIncrement, kind: token.INT, delta: 1},
		&literalStep{typ: IntegerDecrement, kind: token.INT, delta: -1},
		&literalStep{typ: FloatIncrement, kind: token.FLOAT, delta: 1},
		&literalStep{typ: FloatDecrement, kind: token.FLOAT, delta: -1},
		&loopCondition{},
		&rangeBreak{},
		&returnValue{typ: ReturnErrorNil, owns: ownsError, replacement: fixed("nil")},
		&returnValue{typ: ReturnZero, owns: ownsOther, replacement: zeroValueExpr},
		&returnValue{typ: ReturnTrue, owns: ownsBool, replacement: fixed("true")},
		&returnValue{typ: ReturnFalse, owns: ownsBool, replacement: fixed("false")},
	}
	typeSet := make(map[string]struct{}, len(mutators))
	for _, m := range mutators {
		typeSet[string(m.Type())] = struct{}{}
	}
	return &Registry{mutators: mutators, typeSet: typeSet}
}

// Mutators returns all registered mutators.
func (r *Registry) Mutators() []Mutator {
	return r.mutators
}

// IsKnown reports whether name matches a registered mutator type.
func (r *Registry) IsKnown(name string) bool {
	_, ok := r.typeSet[name]
	return ok
}

// UnknownNames returns the subset of names that don't match any
// registered mutator type. Used by callers that accept user-supplied
// mutator lists (--only / --disable, config file) to surface typos
// before silently filtering them out.
func (r *Registry) UnknownNames(names []string) []string {
	var unknown []string
	for _, n := range names {
		if !r.IsKnown(n) {
			unknown = append(unknown, n)
		}
	}
	return unknown
}

// EnabledMutators returns mutators filtered by the given only/disable lists.
// If only is non-empty, only those types are included.
// Otherwise, disabled types are excluded.
func (r *Registry) EnabledMutators(only, disable []string) []Mutator {
	if len(only) > 0 {
		set := make(map[string]bool, len(only))
		for _, t := range only {
			set[t] = true
		}
		var out []Mutator
		for _, m := range r.mutators {
			if set[string(m.Type())] {
				out = append(out, m)
			}
		}
		return out
	}

	set := make(map[string]bool, len(disable))
	for _, t := range disable {
		set[t] = true
	}
	var out []Mutator
	for _, m := range r.mutators {
		if !set[string(m.Type())] {
			out = append(out, m)
		}
	}
	return out
}
