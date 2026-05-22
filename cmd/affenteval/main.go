package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	fs := flag.NewFlagSet("affenteval", flag.ExitOnError)
	var (
		list        = fs.Bool("list", false, "list built-in scenarios and exit")
		scenarioCSV = fs.String("scenario", "", "comma-separated scenario names; empty runs all")
		repoRoot    = fs.String("repo-root", ".", "Affent repository root")
		workRoot    = fs.String("work-root", "", "directory for temporary scenario workspaces; default $TMPDIR/affent-eval")
		baseURL     = fs.String("base-url", os.Getenv("AFFENTCTL_BASE_URL"), "OpenAI-compatible endpoint")
		apiKey      = fs.String("api-key", os.Getenv("AFFENTCTL_API_KEY"), "API key")
		model       = fs.String("model", os.Getenv("AFFENTCTL_MODEL"), "model id")
		temperature = fs.String("temperature", "0", "sampling temperature forwarded to affentctl")
		timeout     = fs.Duration("timeout", 5*time.Minute, "per-scenario timeout")
	)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: affenteval [flags]

Runs deterministic local scenarios through affentctl and checks both task
success and trace-level process quality.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *list {
		for _, name := range agenteval.BatchScenarioNames() {
			fmt.Println(name)
		}
		return 0
	}
	names := splitCSV(*scenarioCSV)
	scenarios, err := agenteval.SelectBatchScenarios(names)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
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
