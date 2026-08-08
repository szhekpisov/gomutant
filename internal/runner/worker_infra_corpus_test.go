package runner

// Regression corpus for infrastructure-error classification.
//
// Real `go test` output for host failures, paired with the ground truth a
// human would assign. It exists because the classifier reads a stream with
// two authors — the Go toolchain and the code under test — and every bug
// found in it so far was a case of trusting the wrong one. A signature list
// change that looks harmless in isolation shows up here as a flipped verdict.
//
// The host-failure cases and the "a test printed it itself" cases both matter:
// missing a host failure resurrects the false KILLED that issue #79 reported,
// and matching a test's own output silently drops a genuine kill out of the
// efficacy denominator. Several cases are taken from the patch proposed on
// that issue (chrisophus/gomutants, feat/infra-error-classification), whose
// wider signature list is what surfaced the build-phase gap.

import (
	"errors"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

func TestInfraClassificationCorpus(t *testing.T) {
	anyErr := errors.New("exit status 1")
	cases := []struct {
		name           string
		stdout, stderr string
		want           mutator.MutantStatus
	}{
		// --- Host failures: must not be scored as a kill. ---
		{"ENOSPC while the toolchain writes an object file",
			"FAIL\ttestmod [build failed]\n",
			"write /tmp/go-build123/b001/x.o: no space left on device\n",
			mutator.StatusInfraError},
		{"toolchain cannot fork the compiler",
			"FAIL\ttestmod [build failed]\n",
			"go: fork/exec /usr/local/go/pkg/tool/darwin_arm64/compile: resource temporarily unavailable\n",
			mutator.StatusInfraError},
		{"linker runs out of memory",
			"FAIL\ttestmod [build failed]\n",
			"/usr/bin/ld: out of memory allocating 8388608 bytes\n",
			mutator.StatusInfraError},
		{"Go runtime OOM in the test binary",
			"fatal error: runtime: out of memory\n", "",
			mutator.StatusInfraError},
		{"OS thread exhaustion",
			"runtime: failed to create new OS thread (have 8193 already; errno=11)\nfatal error: newosproc\n", "",
			mutator.StatusInfraError},
		{"file descriptor exhaustion",
			"", "open /tmp/x: too many open files\n",
			mutator.StatusInfraError},
		{"read-only file system",
			"", "open /tmp/go-build/x: read-only file system\n",
			mutator.StatusInfraError},
		{"signatures match case-insensitively",
			"", "Write /VAR/TMP/X: NO SPACE LEFT ON DEVICE\n",
			mutator.StatusInfraError},

		// --- The tested code printed the phrase: still a kill. ---
		{"test asserts on an ENOSPC message",
			"--- FAIL: TestDiskFull\n    disk_test.go:9: got \"no space left on device\", want nil\n", "",
			mutator.StatusKilled},
		{"test asserts on an EAGAIN message",
			"--- FAIL: TestRetry\n    retry_test.go:14: err = resource temporarily unavailable\n", "",
			mutator.StatusKilled},
		{"test logs an out-of-memory message",
			"--- FAIL: TestAlloc\n    alloc_test.go:7: cache rejected entry: out of memory\n", "",
			mutator.StatusKilled},
		{"test asserts on an EIO message",
			"--- FAIL: TestRead\n    io_test.go:3: want input/output error\n", "",
			mutator.StatusKilled},
		{"generic wording from a running test binary",
			"", "resource temporarily unavailable\n",
			mutator.StatusKilled},
		// A suite that processes `go test` output prints the build markers as
		// fixture data. That must not promote the surrounding test-authored
		// text to the toolchain-only tier — gomutants' own corpus here is
		// exactly such a suite, and got misclassified by an earlier draft.
		{"test echoes a build marker as fixture data",
			"--- FAIL: TestCorpus\n    x_test.go:9: stdout: \"FAIL\\tm [build failed]\\n\" stderr: \"out of memory\"\n", "",
			mutator.StatusKilled},

		// --- Neither: the existing classifications still win. ---
		{"compile error beats an infra-looking stderr",
			"FAIL\ttestmod [build failed]\n",
			"worker-0.go:5:2: undefined: Foo: no space left on device\n",
			mutator.StatusNotViable},
		{"ordinary test failure",
			"--- FAIL: TestAdd\n", "add_test.go:7: Add(1,2) != 3\n",
			mutator.StatusKilled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTestOutcome(anyErr, false, nil, tc.stdout, tc.stderr); got != tc.want {
				t.Errorf("classified %v, want %v\nstdout: %q\nstderr: %q", got, tc.want, tc.stdout, tc.stderr)
			}
		})
	}
}

// The RSS monitor and the per-mutant deadline decide before any signature is
// read: a host failure gomutants itself caused is a TIMED OUT, not an
// environment problem to be excused.
func TestInfraClassificationCorpusPrecedence(t *testing.T) {
	oom := "fatal error: out of memory\n"
	if got := classifyTestOutcome(errors.New("signal: killed"), true, nil, oom, ""); got != mutator.StatusTimedOut {
		t.Errorf("memKilled with an OOM signature = %v, want TimedOut", got)
	}
	if got := classifyTestOutcome(nil, false, nil, oom, ""); got != mutator.StatusLived {
		t.Errorf("passing tests with an OOM signature = %v, want Lived", got)
	}
}
