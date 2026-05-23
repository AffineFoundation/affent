package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/agenteval"
)

func main() {
	if err := loadDotEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "affenteval: load .env: %v\n", err)
		os.Exit(64)
	}
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("affenteval", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		list        = fs.Bool("list", false, "list built-in scenarios and exit")
		listSuites  = fs.Bool("list-suites", false, "list built-in scenario suites and exit")
		suite       = fs.String("suite", "", "scenario suite to run/list (e.g. small-model-tools)")
		scenarioCSV = fs.String("scenario", "", "comma-separated scenario names; empty runs all")
		repoRoot    = fs.String("repo-root", ".", "Affent repository root")
		workRoot    = fs.String("work-root", "", "directory for temporary scenario workspaces; default $TMPDIR/affent-eval")
		baseURL     = fs.String("base-url", "", "OpenAI-compatible endpoint (env: AFFENTCTL_BASE_URL)")
		apiKey      = fs.String("api-key", "", "API key (env: AFFENTCTL_API_KEY)")
		model       = fs.String("model", "", "model id (env: AFFENTCTL_MODEL)")
		temperature = fs.String("temperature", "0", "sampling temperature forwarded to affentctl")
		timeout     = fs.Duration("timeout", 5*time.Minute, "per-scenario timeout")
	)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `usage: affenteval [flags]

Runs deterministic local scenarios through affentctl and checks both task
success and trace-level process quality.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 64
	}
	if *listSuites {
		for _, name := range agenteval.BatchSuiteNames() {
			fmt.Println(name)
		}
		return 0
	}
	if *list {
		if *suite == "" {
			for _, name := range agenteval.BatchScenarioNames() {
				fmt.Println(name)
			}
		} else {
			scenarios, err := agenteval.SelectBatchScenariosForSuite(*suite, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "suite: %v\n", err)
				return 64
			}
			for _, scenario := range scenarios {
				fmt.Println(scenario.Name)
			}
		}
		return 0
	}
	names := splitCSV(*scenarioCSV)
	scenarios, err := agenteval.SelectBatchScenariosForSuite(*suite, names)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		return 64
	}
	if err := validateRunConfig(*temperature, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 64
	}
	runner := agenteval.BatchRunner{
		RepoRoot:    *repoRoot,
		WorkRoot:    *workRoot,
		BaseURL:     *baseURL,
		APIKey:      *apiKey,
		Model:       *model,
		Temperature: *temperature,
		Timeout:     *timeout,
	}
	ctx := context.Background()
	failures := 0
	for _, scenario := range scenarios {
		res := runner.Run(ctx, scenario)
		status := "PASS"
		if !res.OK {
			status = "FAIL"
			failures++
		}
		fmt.Printf("%s %s (%s)\n", status, res.BatchScenario, res.Duration.Round(time.Millisecond))
		fmt.Printf("  workspace: %s\n", res.Workspace)
		fmt.Printf("  trace: %s\n", res.TracePath)
		for _, failure := range res.Failures {
			fmt.Printf("  - %s\n", failure)
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func validateRunConfig(temperature string, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be a positive duration")
	}
	if strings.TrimSpace(temperature) == "" {
		return nil
	}
	t, err := strconv.ParseFloat(temperature, 64)
	if err != nil {
		return fmt.Errorf("--temperature: %w", err)
	}
	if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 2 {
		return fmt.Errorf("--temperature must be between 0 and 2")
	}
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
