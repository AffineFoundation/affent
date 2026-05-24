package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/agenteval"
	"github.com/affinefoundation/affent/internal/sse"
)

const evalJSONLSchemaVersion = 1

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
		list              = fs.Bool("list", false, "list built-in scenarios and exit")
		listSuites        = fs.Bool("list-suites", false, "list built-in scenario suites and exit")
		suite             = fs.String("suite", "", "scenario suite to run/list (e.g. small-model-tools)")
		scenarioCSV       = fs.String("scenario", "", "comma-separated scenario names; empty runs all")
		repoRoot          = fs.String("repo-root", ".", "Affent repository root")
		workRoot          = fs.String("work-root", "", "directory for temporary scenario workspaces; default $TMPDIR/affent-eval")
		baseURL           = fs.String("base-url", "", "OpenAI-compatible endpoint (env: AFFENTCTL_BASE_URL)")
		apiKey            = fs.String("api-key", "", "API key (env: AFFENTCTL_API_KEY)")
		model             = fs.String("model", "", "model id (env: AFFENTCTL_MODEL)")
		providerLabel     = fs.String("provider-label", "", "provider label written to JSONL for comparisons (env: AFFENTEVAL_PROVIDER_LABEL)")
		temperature       = fs.String("temperature", "0", "sampling temperature forwarded to affentctl")
		topP              = fs.String("top-p", "", "top-p sampling forwarded to affentctl; empty keeps provider default")
		maxTokens         = fs.String("max-tokens", "", "max output tokens forwarded to affentctl; empty keeps provider default")
		seed              = fs.String("seed", "", "deterministic-sampling seed forwarded to affentctl; empty keeps provider default")
		executor          = fs.String("executor", "local", "affentctl tool executor for scenario runs: local, sandbox, or docker:<container>")
		runtimeEvalMode   = fs.Bool("runtime-eval-mode", false, "pass affentctl --eval-mode to keep only the basic benchmark tool surface during scenario runs")
		runtimeMemory     = fs.Bool("runtime-memory", false, "pass affentctl --memory=true during scenario runs; useful with --runtime-eval-mode for memory-only opt-in")
		runtimeMCPConfig  = fs.String("runtime-mcp-config", "", "pass affentctl --mcp-config PATH during scenario runs; useful with --runtime-eval-mode to opt into MCP only")
		timeout           = fs.Duration("timeout", 5*time.Minute, "per-scenario timeout")
		verifierOutputCap = fs.Int("verifier-output-cap", agenteval.DefaultVerifierOutputCapBytes, "maximum verifier output bytes buffered per scenario")
		jsonl             = fs.Bool("jsonl", false, "emit machine-readable JSONL records instead of text")
		keepWorkspaces    = fs.Bool("keep-workspaces", false, "keep passing scenario workspaces; failing scenario workspaces are always kept")
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
	if err := validateRunConfig(*temperature, *topP, *maxTokens, *seed, *timeout, *executor, len(scenarios), *workRoot, flagWasSet(fs, "work-root"), *verifierOutputCap); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 64
	}
	runner := agenteval.BatchRunner{
		RepoRoot:                 *repoRoot,
		WorkRoot:                 *workRoot,
		BaseURL:                  *baseURL,
		APIKey:                   *apiKey,
		Model:                    *model,
		Temperature:              *temperature,
		TopP:                     *topP,
		MaxTokens:                *maxTokens,
		Seed:                     *seed,
		Executor:                 *executor,
		RuntimeEvalMode:          *runtimeEvalMode,
		RuntimeMemory:            *runtimeMemory,
		RuntimeMCPConfig:         *runtimeMCPConfig,
		Timeout:                  *timeout,
		VerifierOutputCapBytes:   *verifierOutputCap,
		CleanupPassingWorkspaces: !*keepWorkspaces,
	}
	jsonlMeta := evalJSONLMetadataFromConfig(*suite, *model, *providerLabel, *executor, *temperature, *topP, *maxTokens, *seed, *runtimeEvalMode, *runtimeMemory, *runtimeMCPConfig, *timeout)
	ctx := context.Background()
	var summary batchSummary
	for _, scenario := range scenarios {
		res := runner.Run(ctx, scenario)
		summary.add(res)
		if *jsonl {
			printBatchResultJSONL(os.Stdout, jsonlMeta, res)
		} else {
			printBatchResult(os.Stdout, res)
		}
	}
	if *jsonl {
		printBatchSummaryJSONL(os.Stdout, jsonlMeta, summary)
	} else {
		printBatchSummary(os.Stdout, summary)
	}
	if summary.Failed > 0 {
		return 1
	}
	return 0
}

type batchSummary struct {
	Total                      int
	Passed                     int
	Failed                     int
	Duration                   time.Duration
	ToolCalls                  int
	ToolErrors                 int
	ToolRepaired               int
	ToolNameCanonicalized      int
	ToolRepairCalls            int
	ToolRepairSucceeded        int
	ToolRepairFailed           int
	ToolRepairNotes            int
	ToolRepairByKind           map[string]int
	ToolFailureByKind          map[string]int
	LoopGuardInterventions     int
	ForcedNoTools              int
	ToolDurationMS             int64
	ToolArgsTruncated          int
	ToolArgsOmittedBytes       int
	ToolResultsTruncated       int
	ToolResultsOmittedBytes    int
	ToolResultArtifacts        int
	VerifierRuns               int
	VerifierPassed             int
	VerifierFailed             int
	VerifierOutputTruncated    int
	VerifierOutputOmittedBytes int
	TraceSchemaVersions        map[int]int
	InputTokens                int
	OutputTokens               int
	EndCompleted               int
	EndMaxTurns                int
	EndErrors                  int
	EndCancelled               int
	EndUnknown                 int
	FailureKinds               map[string]int
	RemovedWorkspaces          int
	CleanupErrors              int

	// Delegation aggregates focused-task / subagent usage across all
	// scenarios in the batch. Zero-valued when no scenario used a
	// delegation tool — the JSONL emitter omits empty sub-maps so a
	// batch with no delegation activity produces a clean record.
	FocusedTaskCalls  int
	FocusedTaskByType map[string]int
	FocusedTaskErrors int
	SubagentCalls     int
	SubagentByMode    map[string]int
	SubagentErrors    int

	// Plan aggregates persisted-plan tool usage across scenarios.
	// Zero-valued when no scenario used the plan tool.
	PlanCalls    int
	PlanByAction map[string]int
	PlanErrors   int
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
	s.ToolNameCanonicalized += res.ToolStats.ToolNameCanonicalized
	s.ToolRepairCalls += res.Repair.Calls
	s.ToolRepairSucceeded += res.Repair.SucceededCalls
	s.ToolRepairFailed += res.Repair.FailedCalls
	s.ToolRepairNotes += res.Repair.Notes
	for k, v := range res.Repair.ByKind {
		if s.ToolRepairByKind == nil {
			s.ToolRepairByKind = map[string]int{}
		}
		s.ToolRepairByKind[k] += v
	}
	for k, v := range res.ToolStats.ToolFailureByKind {
		if s.ToolFailureByKind == nil {
			s.ToolFailureByKind = map[string]int{}
		}
		s.ToolFailureByKind[k] += v
	}
	s.LoopGuardInterventions += res.ToolStats.LoopGuardInterventions
	s.ForcedNoTools += res.ToolStats.ForcedNoTools
	s.ToolDurationMS += res.ToolStats.ToolDurationMS
	s.ToolArgsTruncated += res.ToolTruncation.ArgsTruncated
	s.ToolArgsOmittedBytes += res.ToolTruncation.ArgsOmittedBytes
	s.ToolResultsTruncated += res.ToolTruncation.ResultsTruncated
	s.ToolResultsOmittedBytes += res.ToolTruncation.ResultsOmittedBytes
	s.ToolResultArtifacts += res.ToolTruncation.ResultArtifacts
	if res.Verifier.Ran {
		s.VerifierRuns++
		if res.Verifier.OK {
			s.VerifierPassed++
		} else {
			s.VerifierFailed++
		}
		if res.Verifier.OutputTruncated {
			s.VerifierOutputTruncated++
		}
		s.VerifierOutputOmittedBytes += res.Verifier.OutputOmittedBytes
	}
	if res.TraceSchemaVersion > 0 {
		if s.TraceSchemaVersions == nil {
			s.TraceSchemaVersions = map[int]int{}
		}
		s.TraceSchemaVersions[res.TraceSchemaVersion]++
	}
	s.InputTokens += res.Usage.InputTokens
	s.OutputTokens += res.Usage.OutputTokens
	switch res.TurnEndReason {
	case sse.TurnEndCompleted:
		s.EndCompleted++
	case sse.TurnEndMaxTurns:
		s.EndMaxTurns++
	case sse.TurnEndError:
		s.EndErrors++
	case sse.TurnEndCancelled:
		s.EndCancelled++
	default:
		s.EndUnknown++
	}
	if res.WorkspaceRemoved {
		s.RemovedWorkspaces++
	}
	if res.CleanupError != "" {
		s.CleanupErrors++
	}
	// Roll up delegation usage. Per-kind sub-maps are merged
	// key-by-key so a model that used recall in three scenarios and
	// explore in one produces {"recall":3,"explore":1} in the batch
	// summary, with the same shape per-scenario records carry.
	d := res.Delegation
	s.FocusedTaskCalls += d.FocusedTaskCalls
	s.FocusedTaskErrors += d.FocusedTaskErrors
	for k, v := range d.FocusedTaskByType {
		if s.FocusedTaskByType == nil {
			s.FocusedTaskByType = map[string]int{}
		}
		s.FocusedTaskByType[k] += v
	}
	s.SubagentCalls += d.SubagentCalls
	s.SubagentErrors += d.SubagentErrors
	for k, v := range d.SubagentByMode {
		if s.SubagentByMode == nil {
			s.SubagentByMode = map[string]int{}
		}
		s.SubagentByMode[k] += v
	}
	p := res.Plan
	s.PlanCalls += p.Calls
	s.PlanErrors += p.Errors
	for k, v := range p.ByAction {
		if s.PlanByAction == nil {
			s.PlanByAction = map[string]int{}
		}
		s.PlanByAction[k] += v
	}
	for _, failure := range res.Failures {
		if s.FailureKinds == nil {
			s.FailureKinds = map[string]int{}
		}
		s.FailureKinds[failureKind(failure)]++
	}
}

func printBatchSummary(w io.Writer, s batchSummary) {
	fmt.Fprintf(w, "SUMMARY scenarios=%d passed=%d failed=%d duration=%s tools=%d errors=%d repaired=%d canonicalized=%d loop_guard=%d forced_no_tools=%d tool_ms=%d trunc=args:%d,results:%d,artifacts:%d omitted=%d/%d verifier=run:%d,passed:%d,failed:%d,truncated:%d,omitted:%d tokens=%d/%d ends=completed:%d,max_turns:%d,error:%d,cancelled:%d,unknown:%d failure_kinds=%s removed_workspaces=%d cleanup_errors=%d",
		s.Total,
		s.Passed,
		s.Failed,
		s.Duration.Round(time.Millisecond),
		s.ToolCalls,
		s.ToolErrors,
		s.ToolRepaired,
		s.ToolNameCanonicalized,
		s.LoopGuardInterventions,
		s.ForcedNoTools,
		s.ToolDurationMS,
		s.ToolArgsTruncated,
		s.ToolResultsTruncated,
		s.ToolResultArtifacts,
		s.ToolArgsOmittedBytes,
		s.ToolResultsOmittedBytes,
		s.VerifierRuns,
		s.VerifierPassed,
		s.VerifierFailed,
		s.VerifierOutputTruncated,
		s.VerifierOutputOmittedBytes,
		s.InputTokens,
		s.OutputTokens,
		s.EndCompleted,
		s.EndMaxTurns,
		s.EndErrors,
		s.EndCancelled,
		s.EndUnknown,
		formatFailureKinds(s.FailureKinds),
		s.RemovedWorkspaces,
		s.CleanupErrors,
	)
	if hasBatchRepairStats(s) {
		fmt.Fprintf(w, " repair_calls=%d,ok=%d,failed=%d", s.ToolRepairCalls, s.ToolRepairSucceeded, s.ToolRepairFailed)
	}
	if len(s.ToolRepairByKind) > 0 {
		fmt.Fprintf(w, " repair_kinds=%s", formatStringIntCounts(s.ToolRepairByKind))
	}
	if len(s.ToolFailureByKind) > 0 {
		fmt.Fprintf(w, " tool_failure_kinds=%s", formatStringIntCounts(s.ToolFailureByKind))
	}
	printDelegationRollup(w, s.FocusedTaskCalls, s.FocusedTaskByType, s.FocusedTaskErrors, s.SubagentCalls, s.SubagentByMode, s.SubagentErrors)
	printPlanRollup(w, s.PlanCalls, s.PlanByAction, s.PlanErrors)
	fmt.Fprintln(w)
	printFailureHintLines(w, s.FailureKinds, "")
	printToolFailureHintLines(w, s.ToolFailureByKind, "")
}

func hasBatchRepairStats(s batchSummary) bool {
	return s.ToolRepairCalls > 0 ||
		s.ToolRepairSucceeded > 0 ||
		s.ToolRepairFailed > 0 ||
		s.ToolRepairNotes > 0 ||
		len(s.ToolRepairByKind) > 0
}

func printDelegationRollup(w io.Writer, focusedTaskCalls int, focusedTaskByType map[string]int, focusedTaskErrors int, subagentCalls int, subagentByMode map[string]int, subagentErrors int) {
	if focusedTaskCalls == 0 && subagentCalls == 0 {
		return
	}
	fmt.Fprintf(w, " delegation=focused_tasks:%d,subagents:%d", focusedTaskCalls, subagentCalls)
	if focusedTaskErrors > 0 || subagentErrors > 0 {
		fmt.Fprintf(w, " delegation_errors=focused_tasks:%d,subagents:%d", focusedTaskErrors, subagentErrors)
	}
	if len(focusedTaskByType) > 0 {
		fmt.Fprintf(w, " focused_task_by_type=%s", formatStringIntCounts(focusedTaskByType))
	}
	if len(subagentByMode) > 0 {
		fmt.Fprintf(w, " subagent_by_mode=%s", formatStringIntCounts(subagentByMode))
	}
}

func printPlanRollup(w io.Writer, calls int, byAction map[string]int, errors int) {
	if calls == 0 {
		return
	}
	fmt.Fprintf(w, " plan=calls:%d,errors:%d", calls, errors)
	if len(byAction) > 0 {
		fmt.Fprintf(w, " plan_by_action=%s", formatStringIntCounts(byAction))
	}
}

func formatFailureKinds(counts map[string]int) string {
	return formatStringIntCounts(counts)
}

func printFailureHintLines(w io.Writer, counts map[string]int, indent string) {
	hints := failureHintsForKinds(counts)
	if len(hints) == 0 {
		return
	}
	keys := make([]string, 0, len(hints))
	for key := range hints {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "%shint[%s]: %s\n", indent, key, hints[key])
	}
}

func printToolFailureHintLines(w io.Writer, counts map[string]int, indent string) {
	hints := toolFailureHintsForKinds(counts)
	if len(hints) == 0 {
		return
	}
	keys := make([]string, 0, len(hints))
	for key := range hints {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "%stool_failure_hint[%s]: %s\n", indent, key, hints[key])
	}
}

func failureHintsForKinds(counts map[string]int) failureHintMap {
	if len(counts) == 0 {
		return nil
	}
	out := make(failureHintMap)
	for kind, count := range counts {
		if count <= 0 {
			continue
		}
		if hint := failureKindHint(kind); hint != "" {
			out[kind] = hint
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toolFailureHintsForKinds(counts map[string]int) failureHintMap {
	if len(counts) == 0 {
		return nil
	}
	out := make(failureHintMap)
	for kind, count := range counts {
		if count <= 0 {
			continue
		}
		if hint := toolFailureKindHint(kind); hint != "" {
			out[kind] = hint
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func failureKindHint(kind string) string {
	switch kind {
	case "llm_timeout":
		return "upstream LLM streaming stalled past the per-call timeout; inspect provider queue/TTFT/chunk gaps or raise the runtime/eval timeout for slow reasoning models"
	case "llm_incomplete_stream":
		return "upstream closed the SSE stream before finish_reason; inspect model server, proxy, KV-cache, crash, or OOM logs rather than treating this as a verifier failure"
	default:
		return ""
	}
}

func toolFailureKindHint(kind string) string {
	switch kind {
	case "blocked":
		return "direct web_fetch was refused by the source; use a canonical/alternate source, browser tools when available, or mark the source as unverified"
	case "empty_response":
		return "web_fetch received an empty successful response; do not treat it as evidence, switch source or use a rendered/API/text endpoint"
	case "non_text":
		return "web_fetch received non-readable content; fetch an HTML/API/text variant or use browser/screenshot tooling when configured"
	case "timeout":
		return "tool request timed out; retry once only if the source is important, then switch source or report the gap"
	case "rate_limited":
		return "the source rate-limited requests; avoid repeated retries and use cached snippets or another authoritative source"
	case "server_error":
		return "the source returned a server-side failure; retry later once or use another authoritative mirror/source"
	case "not_found":
		return "the URL is stale or gone; rediscover the canonical URL before retrying"
	case "no_results":
		return "web_search returned no usable evidence; refine with distinctive entities or official domains, then switch source or report the gap"
	case "search_error":
		return "the configured web_search backend failed; inspect provider credentials/limits/logs and avoid treating search as available evidence"
	case "stale_ref":
		return "a browser ref no longer matched the current page; call browser_snapshot and retry with a fresh visible ref instead of repeating the old one"
	case "not_interactable":
		return "a browser ref was present but hidden, disabled, or covered; inspect with browser_snapshot, scroll or close the covering element, then retry a visible ref"
	case "private_network_blocked":
		return "SSRF guard blocked a private or local network URL; use public sources or explicitly configure trusted local access"
	case "invalid_args":
		return "the model called a tool with invalid arguments; inspect tool repair and prompt pressure if this is frequent"
	case "loop_guard_repeated_failed_input":
		return "loop guard blocked a repeat of the same failed URL/query; change the source/query instead of retrying the identical input"
	case "loop_guard_repeated_call":
		return "loop guard blocked repeated identical tool arguments; change arguments, use another tool, or answer from gathered evidence"
	case "loop_guard_repeated_failures":
		return "loop guard saw consecutive tool failures; read the latest Failure/Next guidance and switch approach before retrying"
	case "loop_guard_halted_tool":
		return "loop guard halted a tool after repeated failures this turn; stop using that tool and continue with another source or the verified evidence"
	case "loop_guard_call_cap":
		return "loop guard blocked an excessive number of workflow-tool calls in one turn; continue from current plan/delegation results"
	case "http_error", "network_error":
		return "web_fetch hit a transport or HTTP failure; inspect status/detail in the tool result and switch sources if it repeats"
	default:
		return ""
	}
}

type failureHintMap map[string]string

func formatStringIntCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
}

type evalJSONLMetadata struct {
	SchemaVersion   int    `json:"schema_version"`
	Suite           string `json:"suite,omitempty"`
	Model           string `json:"model,omitempty"`
	ProviderLabel   string `json:"provider_label,omitempty"`
	Executor        string `json:"executor"`
	Temperature     string `json:"temperature,omitempty"`
	TopP            string `json:"top_p,omitempty"`
	MaxTokens       string `json:"max_tokens,omitempty"`
	Seed            string `json:"seed,omitempty"`
	RuntimeEvalMode bool   `json:"runtime_eval_mode,omitempty"`
	RuntimeMemory   bool   `json:"runtime_memory,omitempty"`
	RuntimeMCP      bool   `json:"runtime_mcp,omitempty"`
	TimeoutMS       int64  `json:"timeout_ms"`
}

func evalJSONLMetadataFromConfig(suite, model, providerLabel, executor, temperature, topP, maxTokens, seed string, runtimeEvalMode, runtimeMemory bool, runtimeMCPConfig string, timeout time.Duration) evalJSONLMetadata {
	model = strings.TrimSpace(model)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("AFFENTCTL_MODEL"))
	}
	providerLabel = strings.TrimSpace(providerLabel)
	if providerLabel == "" {
		providerLabel = strings.TrimSpace(os.Getenv("AFFENTEVAL_PROVIDER_LABEL"))
	}
	return evalJSONLMetadata{
		SchemaVersion:   evalJSONLSchemaVersion,
		Suite:           strings.TrimSpace(suite),
		Model:           model,
		ProviderLabel:   providerLabel,
		Executor:        normalizedEvalExecutor(executor),
		Temperature:     strings.TrimSpace(temperature),
		TopP:            strings.TrimSpace(topP),
		MaxTokens:       strings.TrimSpace(maxTokens),
		Seed:            strings.TrimSpace(seed),
		RuntimeEvalMode: runtimeEvalMode,
		RuntimeMemory:   runtimeMemory,
		RuntimeMCP:      strings.TrimSpace(runtimeMCPConfig) != "",
		TimeoutMS:       timeout.Milliseconds(),
	}
}

func normalizedEvalExecutor(executor string) string {
	executor = strings.TrimSpace(executor)
	if executor == "" {
		return "local"
	}
	return executor
}

type batchResultRecord struct {
	evalJSONLMetadata
	Type                       string         `json:"type"`
	Scenario                   string         `json:"scenario"`
	OK                         bool           `json:"ok"`
	DurationMS                 int64          `json:"duration_ms"`
	Workspace                  string         `json:"workspace"`
	TracePath                  string         `json:"trace_path"`
	TraceSchemaVersion         int            `json:"trace_schema_version,omitempty"`
	TurnEndReason              string         `json:"turn_end_reason,omitempty"`
	ToolCalls                  int            `json:"tool_calls"`
	ToolErrors                 int            `json:"tool_errors"`
	ToolRepaired               int            `json:"tool_repaired"`
	ToolNameCanonicalized      int            `json:"tool_name_canonicalized"`
	ToolRepairCalls            int            `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded        int            `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed           int            `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes            int            `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind           map[string]int `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind          map[string]int `json:"tool_failure_by_kind,omitempty"`
	LoopGuardInterventions     int            `json:"loop_guard_interventions"`
	ForcedNoTools              int            `json:"forced_no_tools"`
	ToolDurationMS             int64          `json:"tool_duration_ms"`
	ToolArgsTruncated          int            `json:"tool_args_truncated"`
	ToolArgsOmittedBytes       int            `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated       int            `json:"tool_results_truncated"`
	ToolResultsOmittedBytes    int            `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts        int            `json:"tool_result_artifacts"`
	VerifierCommand            string         `json:"verifier_command,omitempty"`
	VerifierRan                bool           `json:"verifier_ran"`
	VerifierOK                 bool           `json:"verifier_ok"`
	VerifierExitCode           int            `json:"verifier_exit_code"`
	VerifierDurationMS         int64          `json:"verifier_duration_ms"`
	VerifierOutputBytes        int            `json:"verifier_output_bytes"`
	VerifierOutputTruncated    bool           `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes int            `json:"verifier_output_omitted_bytes"`
	VerifierOutputCapBytes     int            `json:"verifier_output_cap_bytes"`
	InputTokens                int            `json:"input_tokens"`
	OutputTokens               int            `json:"output_tokens"`
	WorkspaceRemoved           bool           `json:"workspace_removed,omitempty"`
	CleanupError               string         `json:"cleanup_error,omitempty"`
	Failures                   []string       `json:"failures,omitempty"`
	FailureKinds               map[string]int `json:"failure_kinds,omitempty"`
	FailureHints               failureHintMap `json:"failure_hints,omitempty"`
	ToolFailureHints           failureHintMap `json:"tool_failure_hints,omitempty"`

	// Per-scenario delegation breakdown. Fields are omitted from the
	// JSONL when the scenario used no delegation tools, so older
	// records and delegation-free runs stay compact and noise-free.
	FocusedTaskCalls  int            `json:"focused_task_calls,omitempty"`
	FocusedTaskByType map[string]int `json:"focused_task_by_type,omitempty"`
	FocusedTaskErrors int            `json:"focused_task_errors,omitempty"`
	SubagentCalls     int            `json:"subagent_calls,omitempty"`
	SubagentByMode    map[string]int `json:"subagent_by_mode,omitempty"`
	SubagentErrors    int            `json:"subagent_errors,omitempty"`

	// Per-scenario plan-tool breakdown. Fields are omitted from the
	// JSONL when the scenario did not call the plan tool.
	PlanCalls    int            `json:"plan_calls,omitempty"`
	PlanByAction map[string]int `json:"plan_by_action,omitempty"`
	PlanErrors   int            `json:"plan_errors,omitempty"`
}

type batchSummaryRecord struct {
	evalJSONLMetadata
	Type                       string         `json:"type"`
	Scenarios                  int            `json:"scenarios"`
	Passed                     int            `json:"passed"`
	Failed                     int            `json:"failed"`
	DurationMS                 int64          `json:"duration_ms"`
	ToolCalls                  int            `json:"tool_calls"`
	ToolErrors                 int            `json:"tool_errors"`
	ToolRepaired               int            `json:"tool_repaired"`
	ToolNameCanonicalized      int            `json:"tool_name_canonicalized"`
	ToolRepairCalls            int            `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded        int            `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed           int            `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes            int            `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind           map[string]int `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind          map[string]int `json:"tool_failure_by_kind,omitempty"`
	LoopGuardInterventions     int            `json:"loop_guard_interventions"`
	ForcedNoTools              int            `json:"forced_no_tools"`
	ToolDurationMS             int64          `json:"tool_duration_ms"`
	ToolArgsTruncated          int            `json:"tool_args_truncated"`
	ToolArgsOmittedBytes       int            `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated       int            `json:"tool_results_truncated"`
	ToolResultsOmittedBytes    int            `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts        int            `json:"tool_result_artifacts"`
	VerifierRuns               int            `json:"verifier_runs"`
	VerifierPassed             int            `json:"verifier_passed"`
	VerifierFailed             int            `json:"verifier_failed"`
	VerifierOutputTruncated    int            `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes int            `json:"verifier_output_omitted_bytes"`
	TraceSchemaVersions        map[int]int    `json:"trace_schema_versions,omitempty"`
	InputTokens                int            `json:"input_tokens"`
	OutputTokens               int            `json:"output_tokens"`
	EndCompleted               int            `json:"end_completed"`
	EndMaxTurns                int            `json:"end_max_turns"`
	EndErrors                  int            `json:"end_errors"`
	EndCancelled               int            `json:"end_cancelled"`
	EndUnknown                 int            `json:"end_unknown"`
	FailureKinds               map[string]int `json:"failure_kinds,omitempty"`
	FailureHints               failureHintMap `json:"failure_hints,omitempty"`
	ToolFailureHints           failureHintMap `json:"tool_failure_hints,omitempty"`
	RemovedWorkspaces          int            `json:"removed_workspaces"`
	CleanupErrors              int            `json:"cleanup_errors"`

	// Per-batch delegation aggregates. Same omitempty discipline as
	// the per-scenario record so a batch with zero delegation usage
	// emits a record without any focused_task_* / subagent_* fields.
	FocusedTaskCalls  int            `json:"focused_task_calls,omitempty"`
	FocusedTaskByType map[string]int `json:"focused_task_by_type,omitempty"`
	FocusedTaskErrors int            `json:"focused_task_errors,omitempty"`
	SubagentCalls     int            `json:"subagent_calls,omitempty"`
	SubagentByMode    map[string]int `json:"subagent_by_mode,omitempty"`
	SubagentErrors    int            `json:"subagent_errors,omitempty"`

	// Per-batch plan-tool aggregates. Omitted when no scenario used plan.
	PlanCalls    int            `json:"plan_calls,omitempty"`
	PlanByAction map[string]int `json:"plan_by_action,omitempty"`
	PlanErrors   int            `json:"plan_errors,omitempty"`
}

func printBatchResultJSONL(w io.Writer, meta evalJSONLMetadata, res agenteval.BatchResult) {
	failureKinds := failureKindsForResult(res.Failures)
	writeJSONLine(w, batchResultRecord{
		evalJSONLMetadata:          meta,
		Type:                       "scenario",
		Scenario:                   res.BatchScenario,
		OK:                         res.OK,
		DurationMS:                 res.Duration.Milliseconds(),
		Workspace:                  res.Workspace,
		TracePath:                  res.TracePath,
		TraceSchemaVersion:         res.TraceSchemaVersion,
		TurnEndReason:              res.TurnEndReason,
		ToolCalls:                  res.ToolCalls,
		ToolErrors:                 res.ToolStats.ToolErrors,
		ToolRepaired:               res.ToolStats.ToolArgsRepaired,
		ToolNameCanonicalized:      res.ToolStats.ToolNameCanonicalized,
		ToolRepairCalls:            res.Repair.Calls,
		ToolRepairSucceeded:        res.Repair.SucceededCalls,
		ToolRepairFailed:           res.Repair.FailedCalls,
		ToolRepairNotes:            res.Repair.Notes,
		ToolRepairByKind:           cloneStringIntMap(res.Repair.ByKind),
		ToolFailureByKind:          cloneStringIntMap(res.ToolStats.ToolFailureByKind),
		LoopGuardInterventions:     res.ToolStats.LoopGuardInterventions,
		ForcedNoTools:              res.ToolStats.ForcedNoTools,
		ToolDurationMS:             res.ToolStats.ToolDurationMS,
		ToolArgsTruncated:          res.ToolTruncation.ArgsTruncated,
		ToolArgsOmittedBytes:       res.ToolTruncation.ArgsOmittedBytes,
		ToolResultsTruncated:       res.ToolTruncation.ResultsTruncated,
		ToolResultsOmittedBytes:    res.ToolTruncation.ResultsOmittedBytes,
		ToolResultArtifacts:        res.ToolTruncation.ResultArtifacts,
		VerifierCommand:            res.Verifier.Command,
		VerifierRan:                res.Verifier.Ran,
		VerifierOK:                 res.Verifier.OK,
		VerifierExitCode:           res.Verifier.ExitCode,
		VerifierDurationMS:         res.Verifier.Duration.Milliseconds(),
		VerifierOutputBytes:        res.Verifier.OutputBytes,
		VerifierOutputTruncated:    res.Verifier.OutputTruncated,
		VerifierOutputOmittedBytes: res.Verifier.OutputOmittedBytes,
		VerifierOutputCapBytes:     res.Verifier.OutputCapBytes,
		InputTokens:                res.Usage.InputTokens,
		OutputTokens:               res.Usage.OutputTokens,
		WorkspaceRemoved:           res.WorkspaceRemoved,
		CleanupError:               res.CleanupError,
		Failures:                   res.Failures,
		FailureKinds:               failureKinds,
		FailureHints:               failureHintsForKinds(failureKinds),
		ToolFailureHints:           toolFailureHintsForKinds(res.ToolStats.ToolFailureByKind),
		FocusedTaskCalls:           res.Delegation.FocusedTaskCalls,
		FocusedTaskByType:          res.Delegation.FocusedTaskByType,
		FocusedTaskErrors:          res.Delegation.FocusedTaskErrors,
		SubagentCalls:              res.Delegation.SubagentCalls,
		SubagentByMode:             res.Delegation.SubagentByMode,
		SubagentErrors:             res.Delegation.SubagentErrors,
		PlanCalls:                  res.Plan.Calls,
		PlanByAction:               cloneStringIntMap(res.Plan.ByAction),
		PlanErrors:                 res.Plan.Errors,
	})
}

func printBatchSummaryJSONL(w io.Writer, meta evalJSONLMetadata, s batchSummary) {
	writeJSONLine(w, batchSummaryRecord{
		evalJSONLMetadata:          meta,
		Type:                       "summary",
		Scenarios:                  s.Total,
		Passed:                     s.Passed,
		Failed:                     s.Failed,
		DurationMS:                 s.Duration.Milliseconds(),
		ToolCalls:                  s.ToolCalls,
		ToolErrors:                 s.ToolErrors,
		ToolRepaired:               s.ToolRepaired,
		ToolNameCanonicalized:      s.ToolNameCanonicalized,
		ToolRepairCalls:            s.ToolRepairCalls,
		ToolRepairSucceeded:        s.ToolRepairSucceeded,
		ToolRepairFailed:           s.ToolRepairFailed,
		ToolRepairNotes:            s.ToolRepairNotes,
		ToolRepairByKind:           cloneStringIntMap(s.ToolRepairByKind),
		ToolFailureByKind:          cloneStringIntMap(s.ToolFailureByKind),
		LoopGuardInterventions:     s.LoopGuardInterventions,
		ForcedNoTools:              s.ForcedNoTools,
		ToolDurationMS:             s.ToolDurationMS,
		ToolArgsTruncated:          s.ToolArgsTruncated,
		ToolArgsOmittedBytes:       s.ToolArgsOmittedBytes,
		ToolResultsTruncated:       s.ToolResultsTruncated,
		ToolResultsOmittedBytes:    s.ToolResultsOmittedBytes,
		ToolResultArtifacts:        s.ToolResultArtifacts,
		VerifierRuns:               s.VerifierRuns,
		VerifierPassed:             s.VerifierPassed,
		VerifierFailed:             s.VerifierFailed,
		VerifierOutputTruncated:    s.VerifierOutputTruncated,
		VerifierOutputOmittedBytes: s.VerifierOutputOmittedBytes,
		TraceSchemaVersions:        cloneTraceSchemaVersions(s.TraceSchemaVersions),
		InputTokens:                s.InputTokens,
		OutputTokens:               s.OutputTokens,
		EndCompleted:               s.EndCompleted,
		EndMaxTurns:                s.EndMaxTurns,
		EndErrors:                  s.EndErrors,
		EndCancelled:               s.EndCancelled,
		EndUnknown:                 s.EndUnknown,
		FailureKinds:               cloneFailureKinds(s.FailureKinds),
		FailureHints:               failureHintsForKinds(s.FailureKinds),
		ToolFailureHints:           toolFailureHintsForKinds(s.ToolFailureByKind),
		RemovedWorkspaces:          s.RemovedWorkspaces,
		CleanupErrors:              s.CleanupErrors,
		FocusedTaskCalls:           s.FocusedTaskCalls,
		FocusedTaskByType:          cloneStringIntMap(s.FocusedTaskByType),
		FocusedTaskErrors:          s.FocusedTaskErrors,
		SubagentCalls:              s.SubagentCalls,
		SubagentByMode:             cloneStringIntMap(s.SubagentByMode),
		SubagentErrors:             s.SubagentErrors,
		PlanCalls:                  s.PlanCalls,
		PlanByAction:               cloneStringIntMap(s.PlanByAction),
		PlanErrors:                 s.PlanErrors,
	})
}

// cloneStringIntMap returns a copy of in or nil if in is empty. Used
// by the JSONL emitter to avoid sharing internal counters with
// serialized records and to keep empty maps off the wire.
func cloneStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneFailureKinds(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTraceSchemaVersions(in map[int]int) map[int]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func failureKindsForResult(failures []string) map[string]int {
	if len(failures) == 0 {
		return nil
	}
	out := make(map[string]int, len(failures))
	for _, failure := range failures {
		out[failureKind(failure)]++
	}
	return out
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
	fmt.Fprintf(w, "  workspace: %s", res.Workspace)
	if res.WorkspaceRemoved {
		fmt.Fprint(w, " (removed)")
	}
	if res.CleanupError != "" {
		fmt.Fprintf(w, " (cleanup_error=%s)", res.CleanupError)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  trace: %s\n", res.TracePath)
	fmt.Fprintf(w, "  metrics: tools=%d errors=%d repaired=%d canonicalized=%d loop_guard=%d forced_no_tools=%d tool_ms=%d tokens=%d/%d",
		res.ToolCalls,
		res.ToolStats.ToolErrors,
		res.ToolStats.ToolArgsRepaired,
		res.ToolStats.ToolNameCanonicalized,
		res.ToolStats.LoopGuardInterventions,
		res.ToolStats.ForcedNoTools,
		res.ToolStats.ToolDurationMS,
		res.Usage.InputTokens,
		res.Usage.OutputTokens,
	)
	if hasToolTruncation(res.ToolTruncation) {
		fmt.Fprintf(w, " trunc=args:%d,results:%d,artifacts:%d omitted=%d/%d",
			res.ToolTruncation.ArgsTruncated,
			res.ToolTruncation.ResultsTruncated,
			res.ToolTruncation.ResultArtifacts,
			res.ToolTruncation.ArgsOmittedBytes,
			res.ToolTruncation.ResultsOmittedBytes,
		)
	}
	if res.Repair.HasAny() {
		fmt.Fprintf(w, " repair_calls=%d,ok=%d,failed=%d", res.Repair.Calls, res.Repair.SucceededCalls, res.Repair.FailedCalls)
	}
	if len(res.Repair.ByKind) > 0 {
		fmt.Fprintf(w, " repair_kinds=%s", formatStringIntCounts(res.Repair.ByKind))
	}
	if len(res.ToolStats.ToolFailureByKind) > 0 {
		fmt.Fprintf(w, " tool_failure_kinds=%s", formatStringIntCounts(res.ToolStats.ToolFailureByKind))
	}
	printDelegationRollup(w, res.Delegation.FocusedTaskCalls, res.Delegation.FocusedTaskByType, res.Delegation.FocusedTaskErrors, res.Delegation.SubagentCalls, res.Delegation.SubagentByMode, res.Delegation.SubagentErrors)
	printPlanRollup(w, res.Plan.Calls, res.Plan.ByAction, res.Plan.Errors)
	if res.TurnEndReason != "" {
		fmt.Fprintf(w, " end=%s", res.TurnEndReason)
	}
	fmt.Fprintln(w)
	if res.Verifier.Ran {
		status := "pass"
		if !res.Verifier.OK {
			status = "fail"
		}
		fmt.Fprintf(w, "  verifier: %s exit=%d duration=%s output=%d",
			status,
			res.Verifier.ExitCode,
			res.Verifier.Duration.Round(time.Millisecond),
			res.Verifier.OutputBytes,
		)
		if res.Verifier.OutputTruncated {
			fmt.Fprintf(w, " truncated omitted=%d cap=%d",
				res.Verifier.OutputOmittedBytes,
				res.Verifier.OutputCapBytes,
			)
		}
		fmt.Fprintf(w, " command=%q\n", res.Verifier.Command)
	}
	for _, failure := range res.Failures {
		fmt.Fprintf(w, "  - %s\n", failure)
	}
	printFailureHintLines(w, failureKindsForResult(res.Failures), "  ")
	printToolFailureHintLines(w, res.ToolStats.ToolFailureByKind, "  ")
}

func hasToolTruncation(stats agenteval.ToolTruncationStats) bool {
	return stats.ArgsTruncated > 0 ||
		stats.ArgsOmittedBytes > 0 ||
		stats.ResultsTruncated > 0 ||
		stats.ResultsOmittedBytes > 0 ||
		stats.ResultArtifacts > 0
}

func failureKind(failure string) string {
	failure = strings.TrimSpace(failure)
	lower := strings.ToLower(failure)
	switch {
	case isLLMTimeoutFailure(lower):
		return "llm_timeout"
	case isLLMIncompleteStreamFailure(lower):
		return "llm_incomplete_stream"
	case strings.HasPrefix(lower, "affentctl run failed:"):
		return "affentctl_run"
	case strings.HasPrefix(lower, "verify command failed:"):
		return "verify_command"
	case strings.HasPrefix(lower, "parse trace:"):
		return "parse_trace"
	case strings.Contains(lower, "turn ended with reason"):
		return "turn_end"
	case strings.Contains(lower, "missing required command match"):
		return "missing_command"
	case strings.Contains(lower, "forbidden command substring") || strings.Contains(lower, "used forbidden"):
		return "forbidden_command"
	case strings.Contains(lower, "protected file") || strings.Contains(lower, "modified protected file"):
		return "protected_file"
	case strings.Contains(lower, "forbidden content"):
		return "forbidden_content"
	case strings.Contains(lower, "final text did not contain"):
		return "final_text_missing"
	case strings.Contains(lower, "final text contained forbidden") || strings.Contains(lower, "final text leaked"):
		return "final_text_forbidden"
	case strings.Contains(lower, "expected at least one") && strings.Contains(lower, "invocation"):
		return "missing_tool"
	case strings.Contains(lower, "found forbidden") && strings.Contains(lower, "call"):
		return "forbidden_tool"
	case strings.Contains(lower, "direct install cannot use a remote source url"):
		return "skill_install_guard"
	case strings.Contains(lower, "result to contain"):
		return "tool_result_missing"
	case strings.Contains(lower, "focused_task_errors=") || strings.Contains(lower, "subagent_errors="):
		return "delegation_error"
	case strings.Contains(lower, "focused_tasks="):
		return "missing_focused_task"
	case strings.Contains(lower, "subagents="):
		return "missing_subagent"
	default:
		return "other"
	}
}

func isLLMTimeoutFailure(lower string) bool {
	return (strings.Contains(lower, "llm ") && strings.Contains(lower, "timed out")) ||
		strings.Contains(lower, "stream idle timeout") ||
		(strings.Contains(lower, "context deadline exceeded") &&
			(strings.Contains(lower, "max-call-timeout") ||
				strings.Contains(lower, "per-call-timeout") ||
				strings.Contains(lower, "waiting for chat completion")))
}

func isLLMIncompleteStreamFailure(lower string) bool {
	return strings.Contains(lower, "incomplete sse stream") ||
		strings.Contains(lower, "stream ended without finish") ||
		(strings.Contains(lower, "finish_reason") &&
			strings.Contains(lower, "closed the connection"))
}

func validateRunConfig(temperature, topP, maxTokens, seed string, timeout time.Duration, executor string, scenarioCount int, workRoot string, workRootSet bool, verifierOutputCap int) error {
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be a positive duration")
	}
	if verifierOutputCap <= 0 {
		return fmt.Errorf("--verifier-output-cap must be positive")
	}
	if err := validateEvalExecutor(executor, scenarioCount, workRoot, workRootSet); err != nil {
		return err
	}
	sampling, err := parseEvalSampling(temperature, topP, maxTokens, seed)
	if err != nil {
		return err
	}
	if err := sampling.Validate(); err != nil {
		return evalSamplingFlagError(err)
	}
	return nil
}

func parseEvalSampling(temperature, topP, maxTokens, seed string) (agent.SamplingDefaults, error) {
	var sampling agent.SamplingDefaults
	if strings.TrimSpace(temperature) != "" {
		t, err := strconv.ParseFloat(strings.TrimSpace(temperature), 64)
		if err != nil {
			return sampling, fmt.Errorf("--temperature: %w", err)
		}
		sampling.Temperature = &t
	}
	if strings.TrimSpace(topP) != "" {
		t, err := strconv.ParseFloat(strings.TrimSpace(topP), 64)
		if err != nil {
			return sampling, fmt.Errorf("--top-p: %w", err)
		}
		sampling.TopP = &t
	}
	if strings.TrimSpace(maxTokens) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(maxTokens))
		if err != nil {
			return sampling, fmt.Errorf("--max-tokens: %w", err)
		}
		sampling.MaxTokens = &n
	}
	if strings.TrimSpace(seed) != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(seed), 10, 64)
		if err != nil {
			return sampling, fmt.Errorf("--seed: %w", err)
		}
		sampling.Seed = &n
	}
	return sampling, nil
}

func evalSamplingFlagError(err error) error {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "temperature "):
		return fmt.Errorf("--temperature %s", strings.TrimPrefix(msg, "temperature "))
	case strings.HasPrefix(msg, "top_p "):
		return fmt.Errorf("--top-p %s", strings.TrimPrefix(msg, "top_p "))
	case strings.HasPrefix(msg, "max_tokens "):
		return fmt.Errorf("--max-tokens %s", strings.TrimPrefix(msg, "max_tokens "))
	default:
		return fmt.Errorf("--sampling: %w", err)
	}
}

func validateEvalExecutor(executor string, scenarioCount int, workRoot string, workRootSet bool) error {
	executor = strings.TrimSpace(executor)
	switch {
	case executor == "", executor == "local":
		return nil
	case executor == "sandbox":
		if scenarioCount != 1 {
			return fmt.Errorf("--executor sandbox is only supported for one selected scenario because affentctl auto-starts a fixed-name sandbox for that scenario workspace; use --scenario for one run, or pre-start a sandbox over --work-root and pass --executor docker:<container>")
		}
		return nil
	case strings.HasPrefix(executor, "docker:"):
		name := strings.TrimSpace(strings.TrimPrefix(executor, "docker:"))
		if name == "" {
			return fmt.Errorf("--executor docker: requires a container name")
		}
		if strings.ContainsAny(name, " \t\r\n") {
			return fmt.Errorf("--executor docker:<container> must not contain whitespace")
		}
		if !workRootSet || strings.TrimSpace(workRoot) == "" {
			return fmt.Errorf("--executor docker:<container> requires explicit --work-root mounted at the same absolute path inside the container")
		}
		if !filepath.IsAbs(workRoot) {
			return fmt.Errorf("--work-root must be an absolute path when using --executor docker:<container>")
		}
		return nil
	default:
		return fmt.Errorf("unknown --executor %q (valid: local, sandbox, docker:<container>)", executor)
	}
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
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
