// Package excludecalls is the fixture for --exclude-calls. Each function
// pins one edge of the feature: what the built-in set drops, what it
// deliberately leaves alone, and what only a user-supplied pattern
// reaches.
package excludecalls

import "log"

type logger struct{}

func (l logger) Debug(v ...any) {
	// Intentionally empty: the fixture needs a call site whose selector
	// renders as `logger.Debug`, not a working logger. A body would only
	// add mutants that say nothing about --exclude-calls.
}

// Ratio has the issue's motivating shape: the same arithmetic inside a
// log.Printf argument list, where no test can assert on it, and in the
// return value, where every test can.
func Ratio(done, total int) int {
	log.Printf("imported %d/%d (%d%%)", done, total, done*100/total)
	return done * 100 / total
}

// Fail pins what the built-in set leaves mutable: log.Fatalf terminates
// the process, so both its arguments and the call itself are real
// behaviour a test can catch.
func Fail(n int) {
	if n < 0 {
		log.Fatalf("negative: %d", n*2)
	}
}

// Trace pins the other omission: method-shaped logging calls are not
// covered out of the box, because `*.Debug` would reach domain methods
// too. A project opts in with --exclude-calls '*.Debug'.
func Trace(a, b int) int {
	var l logger
	l.Debug("sum", a+b)
	return a - b
}
