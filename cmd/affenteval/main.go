package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
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
		jsonl       = fs.Bool("jsonl", false, "emit machine-readable JSONL records instead of text")
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
	var summary batchSummary
	for _, scenario := range scenarios {
		res := runner.Run(ctx, scenario)
		summary.add(res)
		if *jsonl {
			printBatchResultJSONL(os.Stdout, res)
		} else {
			printBatchResult(os.Stdout, res)
		}
	}
	if *jsonl {
		printBatchSummaryJSONL(os.Stdout, summary)
	} else {
		printBatchSummary(os.Stdout, summary)
	}
	if summary.Failed > 0 {
		return 1
	}
	return 0
}

type batchSummary struct {
	Total          int
	Passed         int
	Failed         int
	Duration       time.Duration
	ToolCalls      int
	ToolErrors     int
	ToolRepaired   int
	ToolDurationMS int64
	InputTokens    int
	OutputTokens   int
}

func (s *batchSummary) add(res agenteval.BatchResult) {
	s.Total++
	if res.OK {
		s.Passed++
	} else {
		s.Failed++
	}
	s.Duration += res.Duration
	s.ToolCalls += res.ToolCalls
	s.ToolErrors += res.ToolStats.ToolErrors
	s.ToolRepaired += res.ToolStats.ToolArgsRepaired
	s.ToolDurationMS += res.ToolStats.ToolDurationMS
	s.InputTokens += res.Usage.InputTokens
	s.OutputTokens += res.Usage.OutputTokens
}

func printBatchSummary(w io.Writer, s batchSummary) {
	fmt.Fprintf(w, "SUMMARY scenarios=%d passed=%d failed=%d duration=%s tools=%d errors=%d repaired=%d tool_ms=%d tokens=%d/%d\n",
		s.Total,
		s.Passed,
		s.Failed,
		s.Duration.Round(time.Millisecond),
		s.ToolCalls,
		s.ToolErrors,
		s.ToolRepaired,
		s.ToolDurationMS,
		s.InputTokens,
		s.OutputTokens,
	)
}

type batchResultRecord struct {
	Type           string   `json:"type"`
	Scenario       string   `json:"scenario"`
	OK             bool     `json:"ok"`
	DurationMS     int64    `json:"duration_ms"`
	Workspace      string   `json:"workspace"`
	TracePath      string   `json:"trace_path"`
	TurnEndReason  string   `json:"turn_end_reason,omitempty"`
	ToolCalls      int      `json:"tool_calls"`
	ToolErrors     int      `json:"tool_errors"`
	ToolRepaired   int      `json:"tool_repaired"`
	ToolDurationMS int64    `json:"tool_duration_ms"`
	InputTokens    int      `json:"input_tokens"`
	OutputTokens   int      `json:"output_tokens"`
	Failures       []string `json:"failures,omitempty"`
}

type batchSummaryRecord struct {
	Type           string `json:"type"`
	Scenarios      int    `json:"scenarios"`
	Passed         int    `json:"passed"`
	Failed         int    `json:"failed"`
	DurationMS     int64  `json:"duration_ms"`
	ToolCalls      int    `json:"tool_calls"`
	ToolErrors     int    `json:"tool_errors"`
	ToolRepaired   int    `json:"tool_repaired"`
	ToolDurationMS int64  `json:"tool_duration_ms"`
	InputTokens    int    `json:"input_tokens"`
	OutputTokens   int    `json:"output_tokens"`
}

func printBatchResultJSONL(w io.Writer, res agenteval.BatchResult) {
	writeJSONLine(w, batchResultRecord{
		Type:           "scenario",
		Scenario:       res.BatchScenario,
		OK:             res.OK,
		DurationMS:     res.Duration.Milliseconds(),
		Workspace:      res.Workspace,
		TracePath:      res.TracePath,
		TurnEndReason:  res.TurnEndReason,
		ToolCalls:      res.ToolCalls,
		ToolErrors:     res.ToolStats.ToolErrors,
		ToolRepaired:   res.ToolStats.ToolArgsRepaired,
		ToolDurationMS: res.ToolStats.ToolDurationMS,
		InputTokens:    res.Usage.InputTokens,
		OutputTokens:   res.Usage.OutputTokens,
		Failures:       res.Failures,
	})
}

func printBatchSummaryJSONL(w io.Writer, s batchSummary) {
	writeJSONLine(w, batchSummaryRecord{
		Type:           "summary",
		Scenarios:      s.Total,
		Passed:         s.Passed,
		Failed:         s.Failed,
		DurationMS:     s.Duration.Milliseconds(),
		ToolCalls:      s.ToolCalls,
		ToolErrors:     s.ToolErrors,
		ToolRepaired:   s.ToolRepaired,
		ToolDurationMS: s.ToolDurationMS,
		InputTokens:    s.InputTokens,
		OutputTokens:   s.OutputTokens,
	})
}

func writeJSONLine(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func printBatchResult(w io.Writer, res agenteval.BatchResult) {
	status := "PASS"
	if !res.OK {
		status = "FAIL"
	}
	fmt.Fprintf(w, "%s %s (%s)\n", status, res.BatchScenario, res.Duration.Round(time.Millisecond))
	fmt.Fprintf(w, "  workspace: %s\n", res.Workspace)
	fmt.Fprintf(w, "  trace: %s\n", res.TracePath)
	fmt.Fprintf(w, "  metrics: tools=%d errors=%d repaired=%d tool_ms=%d tokens=%d/%d",
		res.ToolCalls,
		res.ToolStats.ToolErrors,
		res.ToolStats.ToolArgsRepaired,
		res.ToolStats.ToolDurationMS,
		res.Usage.InputTokens,
		res.Usage.OutputTokens,
	)
	if res.TurnEndReason != "" {
		fmt.Fprintf(w, " end=%s", res.TurnEndReason)
	}
	fmt.Fprintln(w)
	for _, failure := range res.Failures {
		fmt.Fprintf(w, "  - %s\n", failure)
	}
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
	sampling := agent.SamplingDefaults{Temperature: &t}
	if err := sampling.Validate(); err != nil {
		return fmt.Errorf("--%s", err.Error())
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
