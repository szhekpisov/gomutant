package main

import (
	"context"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/szhekpisov/gomutant/internal/config"
	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/discover"
	"github.com/szhekpisov/gomutant/internal/mutator"
	"github.com/szhekpisov/gomutant/internal/report"
	"github.com/szhekpisov/gomutant/internal/runner"
)

const version = "0.1.0"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "gomutant: %v\n", err)
		os.Exit(1)
	}
}

// stdout is the writer for user-facing output. Tests swap this to capture output.
var stdout io.Writer = os.Stdout

// stderr is the writer for warnings. Tests swap this to capture output.
var stderr io.Writer = os.Stderr

func run(ctx context.Context, args []string) error {
	// Strip "unleash" for gremlins CLI compat.
	if len(args) > 0 && args[0] == "unleash" {
		args = args[1:]
	}

	fs := flag.NewFlagSet("gomutant", flag.ContinueOnError)

	var (
		workers            int
		timeoutCoefficient int
		coverPkg           string
		output             string
		configPath         string
		disable            string
		only               string
		dryRun             bool
		verbose            bool
		showVersion        bool
	)

	fs.IntVar(&workers, "workers", 0, "parallel workers (default: NumCPU)")
	fs.IntVar(&workers, "w", 0, "parallel workers (shorthand)")
	fs.IntVar(&timeoutCoefficient, "timeout-coefficient", 0, "multiply baseline test time (default: 10)")
	fs.StringVar(&coverPkg, "coverpkg", "", "coverage package pattern")
	fs.StringVar(&output, "output", "", "JSON report path")
	fs.StringVar(&output, "o", "", "JSON report path (shorthand)")
	fs.StringVar(&configPath, "config", ".gomutant.yml", "config file path")
	fs.StringVar(&disable, "disable", "", "comma-separated mutator types to disable")
	fs.StringVar(&only, "only", "", "comma-separated mutator types to run (disables all others)")
	fs.BoolVar(&dryRun, "dry-run", false, "list mutants without testing")
	fs.BoolVar(&verbose, "verbose", false, "show each mutant as tested")
	fs.BoolVar(&verbose, "v", false, "verbose (shorthand)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if showVersion {
		fmt.Fprintf(stdout, "gomutant v%s\n", version)
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.ApplyFlags(workers, timeoutCoefficient, coverPkg, output, disable, only, dryRun, verbose)

	packages := fs.Args()
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	// Determine project directory (current working directory).
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Read go module name from go.mod.
	goModule, err := readModuleName(projectDir)
	if err != nil {
		return err
	}

	// Get enabled mutators.
	reg := mutator.NewRegistry()
	enabledMutators := reg.EnabledMutators(cfg.Only, cfg.Disable)

	term := report.NewTerminal(stdout, 0, cfg.Verbose)
	term.Header(version, fmt.Sprintf("%v", packages), cfg.Workers, len(enabledMutators))

	// 1. Resolve packages.
	term.Phase("Resolving packages...")
	pkgs, err := discover.ResolvePackages(ctx, projectDir, packages)
	if err != nil {
		return err
	}
	term.PhaseDone(fmt.Sprintf("done (%d packages)", len(pkgs)))

	// 2. Create temp directory.
	tmpDir, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 3. Collect coverage.
	term.Phase("Collecting coverage...")
	coverStart := time.Now()
	profilePath, err := runner.RunCoverage(ctx, projectDir, packages, cfg.CoverPkg, tmpDir)
	if err != nil {
		return err
	}
	profile, err := coverage.ParseFile(profilePath)
	if err != nil {
		return err
	}
	term.PhaseDone(fmt.Sprintf("done (%s)", time.Since(coverStart).Round(100*time.Millisecond)))

	// 4. Measure baseline test duration.
	term.Phase("Measuring baseline...")
	baseline, err := runner.MeasureBaseline(ctx, projectDir, packages)
	if err != nil {
		return err
	}
	testTimeout := baseline * time.Duration(cfg.TimeoutCoefficient)
	term.PhaseDone(fmt.Sprintf("done (%s, timeout: %s)", baseline.Round(100*time.Millisecond), testTimeout.Round(time.Second)))

	// 5. Discover mutants.
	term.Phase("Discovering mutants...")
	fset := token.NewFileSet()
	mutants := discover.Discover(fset, pkgs, enabledMutators, projectDir, goModule)
	discover.FilterByCoverage(mutants, profile, pkgs, goModule)

	pendingCount := 0
	notCoveredCount := 0
	for _, m := range mutants {
		if m.Status == mutator.StatusPending {
			pendingCount++
		} else if m.Status == mutator.StatusNotCovered {
			notCoveredCount++
		}
	}
	term.PhaseDone(fmt.Sprintf("%d found (%d not covered, %d to test)", len(mutants), notCoveredCount, pendingCount))

	if cfg.DryRun {
		for _, m := range mutants {
			fmt.Fprintf(stdout, "[%s] %s:%d:%d  %s → %s  (%s)\n",
				m.Status.String(), m.RelFile, m.Line, m.Col,
				m.Original, m.Replacement, m.Type)
		}
		return nil
	}

	// 6. Pre-read source files.
	srcCache, err := discover.PreReadFiles(pkgs)
	if err != nil {
		return fmt.Errorf("pre-reading source files: %w", err)
	}

	// 7. Build per-test coverage map.
	term.Phase("Building per-test coverage map...")
	testMap, err := coverage.BuildTestMap(ctx, projectDir, packages, cfg.CoverPkg, tmpDir, cfg.Workers)
	if err != nil {
		// Non-fatal: fall back to running all tests per mutant.
		fmt.Fprintf(stderr, "warning: per-test coverage map failed: %v\n", err)
		testMap = nil
		term.PhaseDone("skipped (will run all tests per mutant)")
	} else {
		term.PhaseDone("done")
	}

	// 8. Run mutation testing.
	runStart := time.Now()
	term2 := report.NewTerminal(stdout, pendingCount, cfg.Verbose)
	pool := runner.NewPool(cfg.Workers, testTimeout, tmpDir, srcCache, projectDir, testMap)
	mutants = pool.Run(ctx, mutants, term2.OnResult)
	elapsed := time.Since(runStart)

	// 8. Generate report.
	totalElapsed := time.Since(coverStart)
	r := report.Generate(mutants, goModule, totalElapsed)

	term2.Summary(r)

	if err := report.WriteJSON(r, cfg.Output); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	fmt.Fprintf(stdout, "Report: %s\n", cfg.Output)
	_ = elapsed

	return nil
}

func readModuleName(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range splitLines(data) {
		if len(line) > 7 && string(line[:7]) == "module " {
			return string(line[7:]), nil
		}
	}
	return "", fmt.Errorf("module name not found in go.mod")
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := 0
		for i < len(data) && data[i] != '\n' {
			i++
		}
		lines = append(lines, data[:i])
		if i < len(data) {
			i++ // skip \n
		}
		data = data[i:]
	}
	return lines
}
