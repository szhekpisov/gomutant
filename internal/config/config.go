package config

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type MutatorConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type Config struct {
	Workers            int                       `yaml:"workers"`
	TimeoutCoefficient int                       `yaml:"timeout-coefficient"`
	CoverPkg           string                    `yaml:"coverpkg"`
	Output             string                    `yaml:"output"`
	DryRun             bool                      `yaml:"dry-run"`
	Verbose            bool                      `yaml:"verbose"`
	Disable            []string                  `yaml:"disable"`
	Only               []string                  `yaml:"only"`
	Mutants            map[string]*MutatorConfig `yaml:"mutants"`
}

func Default() Config {
	return Config{
		Workers:            runtime.NumCPU(),
		TimeoutCoefficient: 10,
		Output:             "mutation-report.json",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Preserve defaults for zero-value fields.
	if cfg.Workers == 0 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.TimeoutCoefficient == 0 {
		cfg.TimeoutCoefficient = 10
	}
	if cfg.Output == "" {
		cfg.Output = "mutation-report.json"
	}

	return cfg, nil
}

func (c *Config) ApplyFlags(workers int, timeoutCoefficient int, coverPkg, output, disable, only string, dryRun, verbose bool) {
	if workers > 0 {
		c.Workers = workers
	}
	if timeoutCoefficient > 0 {
		c.TimeoutCoefficient = timeoutCoefficient
	}
	if coverPkg != "" {
		c.CoverPkg = coverPkg
	}
	if output != "" {
		c.Output = output
	}
	if disable != "" {
		c.Disable = splitAndTrim(disable)
	}
	if only != "" {
		c.Only = splitAndTrim(only)
	}
	if dryRun {
		c.DryRun = true
	}
	if verbose {
		c.Verbose = true
	}
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
