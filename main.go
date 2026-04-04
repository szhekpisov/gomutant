package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/szhekpisov/gomutant/internal/config"
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
		fmt.Printf("gomutant v%s\n", version)
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

	_ = ctx
	_ = cfg
	_ = packages

	fmt.Printf("gomutant v%s\n", version)
	fmt.Printf("Packages: %v\n", packages)
	fmt.Printf("Workers: %d | Timeout coefficient: %d\n", cfg.Workers, cfg.TimeoutCoefficient)

	return nil
}
