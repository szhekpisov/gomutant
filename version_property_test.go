package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	ldflagsTestCommit    = "abc123def4567890"
	ldflagsTestBuildDate = "2026-05-10T12:00:00Z"
)

// TestVersionFlagWithLdflags builds the binary with -X main.version /
// main.commit / main.buildDate and asserts each value reaches the
// --version output. This is the only safety net for the build-system
// contract: in-process tests can't catch a regression where someone
// renames the package-level vars and silently breaks ldflags injection.
func TestVersionFlagWithLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ldflags property test in short mode")
	}

	// Release candidates reach main.version through the same -X path as
	// stable tags (goreleaser's {{.Version}} is the tag minus the leading
	// "v", prerelease suffix intact), so --version must render "1.2.3-rc0"
	// verbatim — nothing may parse or truncate at the "-".
	tests := []struct {
		name    string
		version string
	}{
		{"stable", "1.2.3"},
		{"release candidate", "1.2.3-rc0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertLdflagsReachVersionOutput(t, tc.version)
		})
	}
}

// assertLdflagsReachVersionOutput builds the binary with version injected
// via -ldflags and checks that --version renders it verbatim alongside the
// commit and build date.
func assertLdflagsReachVersionOutput(t *testing.T, version string) {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "gomutants_ldflags")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	ldflags := strings.Join([]string{
		"-X main.version=" + version,
		"-X main.commit=" + ldflagsTestCommit,
		"-X main.buildDate=" + ldflagsTestBuildDate,
	}, " ")

	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(binPath, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	got := string(out)

	for _, want := range []string{version, ldflagsTestCommit, ldflagsTestBuildDate} {
		if !strings.Contains(got, want) {
			t.Errorf("--version output missing %q\nfull output: %s", want, got)
		}
	}
	if !strings.HasPrefix(got, "gomutants v"+version+" (commit: "+ldflagsTestCommit+", built: "+ldflagsTestBuildDate+")") {
		t.Errorf("--version format mismatch\ngot:  %s", got)
	}
}
