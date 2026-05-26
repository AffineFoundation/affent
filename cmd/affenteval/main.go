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

const (
	evalJSONLSchemaVersion      = 1
	batchSummaryExamplesPerKind = 2
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
		list              = fs.Bool("list", false, "list built-in scenarios and exit")
		listSuites        = fs.Bool("list-suites", false, "list built-in scenario suites and exit")
		suite             = fs.String("suite", "", "scenario suite to run/list (e.g. small-model-tools)")
		scenarioCSV       = fs.String("scenario", "", "comma-separated scenario names; empty runs all")
		prompt            = fs.String("prompt", "", "run one ad-hoc prompt; use '-' for stdin")
		promptFile        = fs.String("prompt-file", "", "run one ad-hoc prompt read from file")
		adHocName         = fs.String("name", "adhoc", "scenario name for --prompt/--prompt-file debug runs")
		adHocSessionID    = fs.String("session-id", "", "session id forwarded to affentctl for --prompt/--prompt-file debug runs")
		adHocMaxTurns     = fs.Int("max-turns", agenteval.DefaultBatchMaxTurnSteps, "max assistant/tool loop steps for --prompt/--prompt-file debug runs")
		adHocVerify       = fs.String("verify-command", "", "optional verifier command for --prompt/--prompt-file debug runs")
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
		runtimeEvalMode   = fs.Bool("runtime-eval-mode", false, "pass affentctl --eval-mode to disable tools by default during scenario runs")
		runtimeTools      = fs.String("runtime-tools", "", "comma-separated affentctl --eval-tools allowlist for --runtime-eval-mode, e.g. readonly_workspace,web or read_file,shell")
		runtimeAllTools   = fs.Bool("runtime-all-tools", false, "pass affentctl --eval-all-tools to enable the full tool surface under --runtime-eval-mode")
		runtimeMemory     = fs.Bool("runtime-memory", false, "pass affentctl --memory=true during scenario runs; useful with --runtime-eval-mode for memory-only opt-in")
		runtimeWeb        = fs.Bool("runtime-web", false, "pass affentctl --web --web-search during scenario runs for external retrieval/debug evals")
		runtimeBrowser    = fs.Bool("runtime-browser", false, "pass affentctl --browser during scenario runs for rendered-page/browser debug evals")
		runtimeMCPConfig  = fs.String("runtime-mcp-config", "", "pass affentctl --mcp-config PATH during scenario runs; useful with --runtime-eval-mode to opt into MCP only")
		traceDeltas       = fs.Bool("trace-deltas", false, "retain streaming message delta events in trace JSONL for deep debugging; default skips deltas to keep traces compact")
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
	scenarios, err := selectedEvalScenarios(*suite, *scenarioCSV, *prompt, *promptFile, *adHocName, *adHocSessionID, *adHocMaxTurns, *adHocVerify)
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
		RuntimeTools:             *runtimeTools,
		RuntimeAllTools:          *runtimeAllTools,
		RuntimeMemory:            *runtimeMemory,
		RuntimeWeb:               *runtimeWeb,
		RuntimeBrowser:           *runtimeBrowser,
		RuntimeMCPConfig:         *runtimeMCPConfig,
		TraceDeltas:              *traceDeltas,
		Timeout:                  *timeout,
		VerifierOutputCapBytes:   *verifierOutputCap,
		CleanupPassingWorkspaces: !*keepWorkspaces,
	}
	jsonlMeta := evalJSONLMetadataFromConfig(*suite, *model, *providerLabel, *executor, *temperature, *topP, *maxTokens, *seed, *runtimeEvalMode, *runtimeTools, *runtimeAllTools, *runtimeMemory, *runtimeWeb, *runtimeBrowser, *traceDeltas, *runtimeMCPConfig, *timeout)
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
	ToolFailureExamples        map[string][]agenteval.ToolFailureExample
	RuntimeErrorByKind         map[string]int
	RuntimeErrorExamples       map[string][]agenteval.RuntimeErrorExample
	RuntimeSurfaceScenarios    int
	RuntimeSurfaceTools        map[string]int
	RuntimeSurfaceCapabilities map[string]int
	LoopDecisions              int
	LoopDecisionByKind         map[string]int
	LoopDecisionByDecision     map[string]int
	LoopDecisionExamples       []agenteval.LoopDecision
	ContextCompactions         int
	ContextCompactionsReactive int
	ContextCompactionRemoved   int
	ContextCompactionSummary   int
	LoopGuardInterventions     int
	ForcedNoTools              int
	SourceAccessResults        int
	SourceAccessVerified       int
	SourceAccessDiscoveryOnly  int
	SourceAccessNetwork        int
	SourceAccessDynamicPartial int
	MemoryUpdates              int
	MemoryUpdateAdd            int
	MemoryUpdateReplace        int
	MemoryUpdateRemove         int
	ToolDurationMS             int64
	ToolContextTruncated       int
	ToolContextOmittedBytes    int
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
	TraceEvents                int
	TraceEventTypes            map[string]int
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
	mergeExampleMap(&s.ToolFailureExamples, res.ToolFailureExamples, batchSummaryExamplesPerKind)
	for k, v := range res.RuntimeErrorByKind {
		if s.RuntimeErrorByKind == nil {
			s.RuntimeErrorByKind = map[string]int{}
		}
		s.RuntimeErrorByKind[k] += v
	}
	mergeExampleMap(&s.RuntimeErrorExamples, res.RuntimeErrorExamples, batchSummaryExamplesPerKind)
	if res.RuntimeSurface != nil {
		s.RuntimeSurfaceScenarios++
		if s.RuntimeSurfaceTools == nil {
			s.RuntimeSurfaceTools = map[string]int{}
		}
		for _, tool := range runtimeSurfaceToolNames(res.RuntimeSurface) {
			s.RuntimeSurfaceTools[tool]++
		}
		if s.RuntimeSurfaceCapabilities == nil {
			s.RuntimeSurfaceCapabilities = map[string]int{}
		}
		for _, cap := range runtimeSurfaceCapabilityNames(res.RuntimeSurface.Capabilities) {
			s.RuntimeSurfaceCapabilities[cap]++
		}
	}
	s.LoopDecisions += res.LoopDecisionStats.Count
	for k, v := range res.LoopDecisionStats.ByKind {
		if s.LoopDecisionByKind == nil {
			s.LoopDecisionByKind = map[string]int{}
		}
		s.LoopDecisionByKind[k] += v
	}
	for k, v := range res.LoopDecisionStats.ByDecision {
		if s.LoopDecisionByDecision == nil {
			s.LoopDecisionByDecision = map[string]int{}
		}
		s.LoopDecisionByDecision[k] += v
	}
	s.LoopDecisionExamples = appendLoopDecisionExamples(s.LoopDecisionExamples, res.LoopDecisionStats.Examples, batchSummaryExamplesPerKind)
	s.ContextCompactions += res.ContextCompactions.Count
	s.ContextCompactionsReactive += res.ContextCompactions.Reactive
	s.ContextCompactionRemoved += res.ContextCompactions.RemovedMessages
	s.ContextCompactionSummary += res.ContextCompactions.SummaryBytes
	s.LoopGuardInterventions += res.ToolStats.LoopGuardInterventions
	s.ForcedNoTools += res.ToolStats.ForcedNoTools
	s.SourceAccessResults += res.ToolStats.SourceAccessResults
	s.SourceAccessVerified += res.ToolStats.SourceAccessVerified
	s.SourceAccessDiscoveryOnly += res.ToolStats.SourceAccessDiscoveryOnly
	s.SourceAccessNetwork += res.ToolStats.SourceAccessNetwork
	s.SourceAccessDynamicPartial += res.ToolStats.SourceAccessDynamicPartial
	s.MemoryUpdates += res.ToolStats.MemoryUpdates
	s.MemoryUpdateAdd += res.ToolStats.MemoryUpdateAdd
	s.MemoryUpdateReplace += res.ToolStats.MemoryUpdateReplace
	s.MemoryUpdateRemove += res.ToolStats.MemoryUpdateRemove
	s.ToolDurationMS += res.ToolStats.ToolDurationMS
	s.ToolContextTruncated += res.ToolStats.ToolContextTruncated
	s.ToolContextOmittedBytes += res.ToolStats.ToolContextOmittedBytes
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
	s.TraceEvents += res.TraceEvents
	for k, v := range res.TraceEventTypes {
		if s.TraceEventTypes == nil {
			s.TraceEventTypes = map[string]int{}
		}
		s.TraceEventTypes[k] += v
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
	if hasBatchSourceAccessStats(s) {
		fmt.Fprintf(w, " source_access=results:%d,verified:%d,discovery:%d,network:%d,dynamic_partial:%d",
			s.SourceAccessResults,
			s.SourceAccessVerified,
			s.SourceAccessDiscoveryOnly,
			s.SourceAccessNetwork,
			s.SourceAccessDynamicPartial,
		)
	}
	if hasBatchMemoryUpdateStats(s) {
		fmt.Fprintf(w, " memory_updates=%d(add:%d,replace:%d,remove:%d)",
			s.MemoryUpdates,
			s.MemoryUpdateAdd,
			s.MemoryUpdateReplace,
			s.MemoryUpdateRemove,
		)
	}
	if len(s.RuntimeErrorByKind) > 0 {
		fmt.Fprintf(w, " runtime_error_kinds=%s", formatStringIntCounts(s.RuntimeErrorByKind))
	}
	if s.RuntimeSurfaceScenarios > 0 {
		fmt.Fprintf(w, " runtime_surface=scenarios:%d", s.RuntimeSurfaceScenarios)
		if len(s.RuntimeSurfaceCapabilities) > 0 {
			fmt.Fprintf(w, " runtime_capabilities=%s", formatStringIntCounts(s.RuntimeSurfaceCapabilities))
		}
		if len(s.RuntimeSurfaceTools) > 0 {
			fmt.Fprintf(w, " runtime_tools=%s", formatStringIntCounts(s.RuntimeSurfaceTools))
		}
	}
	if s.LoopDecisions > 0 {
		fmt.Fprintf(w, " loop_decisions=%d", s.LoopDecisions)
		if len(s.LoopDecisionByKind) > 0 {
			fmt.Fprintf(w, " loop_decision_kinds=%s", formatStringIntCounts(s.LoopDecisionByKind))
		}
		if len(s.LoopDecisionByDecision) > 0 {
			fmt.Fprintf(w, " loop_decision_results=%s", formatStringIntCounts(s.LoopDecisionByDecision))
		}
	}
	if s.ContextCompactions > 0 {
		fmt.Fprintf(w, " compactions=%d,reactive=%d,removed=%d,summary_bytes=%d",
			s.ContextCompactions,
			s.ContextCompactionsReactive,
			s.ContextCompactionRemoved,
			s.ContextCompactionSummary,
		)
	}
	if s.TraceEvents > 0 {
		fmt.Fprintf(w, " trace_events=%d", s.TraceEvents)
		if len(s.TraceEventTypes) > 0 {
			fmt.Fprintf(w, " trace_event_types=%s", formatStringIntCounts(s.TraceEventTypes))
		}
	}
	if hasBatchToolContextTruncation(s) {
		fmt.Fprintf(w, " ctx_trunc=%d,omitted=%d", s.ToolContextTruncated, s.ToolContextOmittedBytes)
	}
	printDelegationRollup(w, s.FocusedTaskCalls, s.FocusedTaskByType, s.FocusedTaskErrors, s.SubagentCalls, s.SubagentByMode, s.SubagentErrors)
	printPlanRollup(w, s.PlanCalls, s.PlanByAction, s.PlanErrors)
	fmt.Fprintln(w)
	printFailureHintLines(w, s.FailureKinds, "")
	printToolFailureHintLines(w, s.ToolFailureByKind, "")
	printToolFailureExampleLines(w, s.ToolFailureExamples, "")
	printFailureHintLines(w, s.RuntimeErrorByKind, "")
	printRuntimeErrorExampleLines(w, s.RuntimeErrorExamples, "")
	printLoopDecisionExampleLines(w, s.LoopDecisionExamples, "")
}

func mergeExampleMap[T any](dst *map[string][]T, src map[string][]T, maxPerKind int) {
	if dst == nil || maxPerKind <= 0 {
		return
	}
	for kind, values := range src {
		for _, ex := range values {
			if len((*dst)[kind]) >= maxPerKind {
				break
			}
			if *dst == nil {
				*dst = map[string][]T{}
			}
			(*dst)[kind] = append((*dst)[kind], ex)
		}
	}
}

func hasBatchRepairStats(s batchSummary) bool {
	return s.ToolRepairCalls > 0 ||
		s.ToolRepairSucceeded > 0 ||
		s.ToolRepairFailed > 0 ||
		s.ToolRepairNotes > 0 ||
		len(s.ToolRepairByKind) > 0
}

func hasBatchToolContextTruncation(s batchSummary) bool {
	return s.ToolContextTruncated > 0 || s.ToolContextOmittedBytes > 0
}

func hasBatchSourceAccessStats(s batchSummary) bool {
	return s.SourceAccessResults > 0 ||
		s.SourceAccessVerified > 0 ||
		s.SourceAccessDiscoveryOnly > 0 ||
		s.SourceAccessNetwork > 0 ||
		s.SourceAccessDynamicPartial > 0
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

func printToolFailureExampleLines(w io.Writer, examples map[string][]agenteval.ToolFailureExample, indent string) {
	if len(examples) == 0 {
		return
	}
	kinds := make([]string, 0, len(examples))
	for kind := range examples {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		for _, ex := range examples[kind] {
			fmt.Fprintf(w, "%stool_failure_example[%s]: tool=%s", indent, kind, ex.Tool)
			if ex.ArgsSummary != "" {
				fmt.Fprintf(w, " args=%s", ex.ArgsSummary)
			}
			fmt.Fprintf(w, " exit=%d", ex.ExitCode)
			if ex.ResultSummary != "" {
				fmt.Fprintf(w, " result=%s", ex.ResultSummary)
			}
			fmt.Fprintln(w)
		}
	}
}

func printRuntimeErrorExampleLines(w io.Writer, examples map[string][]agenteval.RuntimeErrorExample, indent string) {
	if len(examples) == 0 {
		return
	}
	kinds := make([]string, 0, len(examples))
	for kind := range examples {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		for _, ex := range examples[kind] {
			fmt.Fprintf(w, "%sruntime_error_example[%s]: %s\n", indent, kind, ex.Message)
		}
	}
}

func printLoopDecisionExampleLines(w io.Writer, examples []agenteval.LoopDecision, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sloop_decision_example[%s]: decision=%s", indent, ex.Kind, ex.Decision)
		if ex.Trigger != "" {
			fmt.Fprintf(w, " trigger=%s", ex.Trigger)
		}
		if ex.Confidence != "" {
			fmt.Fprintf(w, " confidence=%s", ex.Confidence)
		}
		if ex.Reason != "" {
			fmt.Fprintf(w, " reason=%s", ex.Reason)
		}
		if ex.RequiredAction != "" {
			fmt.Fprintf(w, " action=%s", ex.RequiredAction)
		}
		fmt.Fprintln(w)
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
	case "context_overflow":
		return "upstream rejected the request because the prompt/context window was too large; compaction, shorter history, or smaller tool context is needed"
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
	case "dynamic_shell":
		return "web_fetch received a client-rendered loading/app shell rather than source evidence; use any discovery preview/links only to choose a canonical API/text endpoint or rendered page tooling when configured"
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
		return "loop guard saw consecutive tool failures; this is a soft warning, so read the latest Failure/Next guidance, switch source or approach, and continue with useful fallback tools"
	case "loop_guard_halted_tool":
		return "loop guard halted a tool after repeated failures this turn; stop using that tool and continue with another source or the verified evidence"
	case "loop_guard_call_cap":
		return "loop guard blocked an excessive number of workflow-tool calls in one turn; continue from current plan/delegation results"
	case "loop_guard_direct_reader_warning":
		return "loop guard blocked direct web_fetch to a URL already marked by web_search as a direct-reader trap; use the snippet as weak evidence or switch to an API/text/source URL"
	case "loop_guard_no_new_evidence":
		return "loop guard stopped repeated tool calls that were not adding evidence; switch source/tool, use a narrower evidence path, or answer with a clearly marked gap"
	case "tool_policy_first_tool":
		return "runtime policy blocked an early tool because a required first tool was skipped; call the required planner/delegation tool first"
	case "tool_policy_repeat":
		return "runtime policy blocked a repeated workflow/delegation tool call; use the prior result instead of spawning another child"
	case "tool_policy_active":
		return "runtime policy blocked parent-side exploration after a successful workflow result; answer from the prior structured evidence"
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
	RuntimeTools    string `json:"runtime_tools,omitempty"`
	RuntimeAllTools bool   `json:"runtime_all_tools,omitempty"`
	RuntimeMemory   bool   `json:"runtime_memory,omitempty"`
	RuntimeWeb      bool   `json:"runtime_web,omitempty"`
	RuntimeBrowser  bool   `json:"runtime_browser,omitempty"`
	TraceDeltas     bool   `json:"trace_deltas,omitempty"`
	RuntimeMCP      bool   `json:"runtime_mcp,omitempty"`
	TimeoutMS       int64  `json:"timeout_ms"`
}

func evalJSONLMetadataFromConfig(suite, model, providerLabel, executor, temperature, topP, maxTokens, seed string, runtimeEvalMode bool, runtimeTools string, runtimeAllTools, runtimeMemory, runtimeWeb, runtimeBrowser, traceDeltas bool, runtimeMCPConfig string, timeout time.Duration) evalJSONLMetadata {
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
		RuntimeTools:    strings.TrimSpace(runtimeTools),
		RuntimeAllTools: runtimeAllTools,
		RuntimeMemory:   runtimeMemory,
		RuntimeWeb:      runtimeWeb,
		RuntimeBrowser:  runtimeBrowser,
		TraceDeltas:     traceDeltas,
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
	Type                       string                                     `json:"type"`
	Scenario                   string                                     `json:"scenario"`
	OK                         bool                                       `json:"ok"`
	RunExitCode                int                                        `json:"run_exit_code"`
	DurationMS                 int64                                      `json:"duration_ms"`
	Workspace                  string                                     `json:"workspace"`
	TracePath                  string                                     `json:"trace_path"`
	DebugManifestPath          string                                     `json:"debug_manifest_path,omitempty"`
	TimelinePath               string                                     `json:"timeline_path,omitempty"`
	FinalTextPath              string                                     `json:"final_text_path,omitempty"`
	StdoutPath                 string                                     `json:"stdout_path,omitempty"`
	StderrPath                 string                                     `json:"stderr_path,omitempty"`
	AffentctlCommand           []string                                   `json:"affentctl_command,omitempty"`
	TraceSchemaVersion         int                                        `json:"trace_schema_version,omitempty"`
	TurnEndReason              string                                     `json:"turn_end_reason,omitempty"`
	ToolCalls                  int                                        `json:"tool_calls"`
	ToolErrors                 int                                        `json:"tool_errors"`
	ToolRepaired               int                                        `json:"tool_repaired"`
	ToolNameCanonicalized      int                                        `json:"tool_name_canonicalized"`
	ToolRepairCalls            int                                        `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded        int                                        `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed           int                                        `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes            int                                        `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind           map[string]int                             `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind          map[string]int                             `json:"tool_failure_by_kind,omitempty"`
	ToolFailureExamples        map[string][]agenteval.ToolFailureExample  `json:"tool_failure_examples,omitempty"`
	RuntimeErrorByKind         map[string]int                             `json:"runtime_error_by_kind,omitempty"`
	RuntimeErrorExamples       map[string][]agenteval.RuntimeErrorExample `json:"runtime_error_examples,omitempty"`
	RuntimeSurface             *runtimeSurfaceSummary                     `json:"runtime_surface,omitempty"`
	RuntimeSurfaceScenarios    int                                        `json:"runtime_surface_scenarios,omitempty"`
	RuntimeSurfaceTools        map[string]int                             `json:"runtime_surface_tools,omitempty"`
	RuntimeSurfaceCapabilities map[string]int                             `json:"runtime_surface_capabilities,omitempty"`
	LoopDecisions              int                                        `json:"loop_decisions,omitempty"`
	LoopDecisionByKind         map[string]int                             `json:"loop_decision_by_kind,omitempty"`
	LoopDecisionByDecision     map[string]int                             `json:"loop_decision_by_decision,omitempty"`
	LoopDecisionExamples       []agenteval.LoopDecision                   `json:"loop_decision_examples,omitempty"`
	ContextCompactions         int                                        `json:"context_compactions,omitempty"`
	ContextCompactionsReactive int                                        `json:"context_compactions_reactive,omitempty"`
	ContextCompactionRemoved   int                                        `json:"context_compaction_removed_messages,omitempty"`
	ContextCompactionSummary   int                                        `json:"context_compaction_summary_bytes,omitempty"`
	LoopGuardInterventions     int                                        `json:"loop_guard_interventions"`
	ForcedNoTools              int                                        `json:"forced_no_tools"`
	SourceAccessResults        int                                        `json:"source_access_results"`
	SourceAccessVerified       int                                        `json:"source_access_verified"`
	SourceAccessDiscoveryOnly  int                                        `json:"source_access_discovery_only"`
	SourceAccessNetwork        int                                        `json:"source_access_network"`
	SourceAccessDynamicPartial int                                        `json:"source_access_dynamic_partial"`
	MemoryUpdates              int                                        `json:"memory_updates"`
	MemoryUpdateAdd            int                                        `json:"memory_update_add"`
	MemoryUpdateReplace        int                                        `json:"memory_update_replace"`
	MemoryUpdateRemove         int                                        `json:"memory_update_remove"`
	ToolDurationMS             int64                                      `json:"tool_duration_ms"`
	ToolContextTruncated       int                                        `json:"tool_context_truncated"`
	ToolContextOmittedBytes    int                                        `json:"tool_context_omitted_bytes"`
	ToolArgsTruncated          int                                        `json:"tool_args_truncated"`
	ToolArgsOmittedBytes       int                                        `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated       int                                        `json:"tool_results_truncated"`
	ToolResultsOmittedBytes    int                                        `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts        int                                        `json:"tool_result_artifacts"`
	VerifierCommand            string                                     `json:"verifier_command,omitempty"`
	VerifierRan                bool                                       `json:"verifier_ran"`
	VerifierOK                 bool                                       `json:"verifier_ok"`
	VerifierExitCode           int                                        `json:"verifier_exit_code"`
	VerifierDurationMS         int64                                      `json:"verifier_duration_ms"`
	VerifierOutputBytes        int                                        `json:"verifier_output_bytes"`
	VerifierOutputTruncated    bool                                       `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes int                                        `json:"verifier_output_omitted_bytes"`
	VerifierOutputCapBytes     int                                        `json:"verifier_output_cap_bytes"`
	TraceEvents                int                                        `json:"trace_events,omitempty"`
	TraceEventTypes            map[string]int                             `json:"trace_event_types,omitempty"`
	InputTokens                int                                        `json:"input_tokens"`
	OutputTokens               int                                        `json:"output_tokens"`
	WorkspaceRemoved           bool                                       `json:"workspace_removed,omitempty"`
	CleanupError               string                                     `json:"cleanup_error,omitempty"`
	Failures                   []string                                   `json:"failures,omitempty"`
	FailureKinds               map[string]int                             `json:"failure_kinds,omitempty"`
	FailureHints               failureHintMap                             `json:"failure_hints,omitempty"`
	ToolFailureHints           failureHintMap                             `json:"tool_failure_hints,omitempty"`
	RuntimeErrorHints          failureHintMap                             `json:"runtime_error_hints,omitempty"`

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
	Type                       string                                     `json:"type"`
	Scenarios                  int                                        `json:"scenarios"`
	Passed                     int                                        `json:"passed"`
	Failed                     int                                        `json:"failed"`
	DurationMS                 int64                                      `json:"duration_ms"`
	ToolCalls                  int                                        `json:"tool_calls"`
	ToolErrors                 int                                        `json:"tool_errors"`
	ToolRepaired               int                                        `json:"tool_repaired"`
	ToolNameCanonicalized      int                                        `json:"tool_name_canonicalized"`
	ToolRepairCalls            int                                        `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded        int                                        `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed           int                                        `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes            int                                        `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind           map[string]int                             `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind          map[string]int                             `json:"tool_failure_by_kind,omitempty"`
	ToolFailureExamples        map[string][]agenteval.ToolFailureExample  `json:"tool_failure_examples,omitempty"`
	RuntimeErrorByKind         map[string]int                             `json:"runtime_error_by_kind,omitempty"`
	RuntimeErrorExamples       map[string][]agenteval.RuntimeErrorExample `json:"runtime_error_examples,omitempty"`
	RuntimeSurfaceScenarios    int                                        `json:"runtime_surface_scenarios,omitempty"`
	RuntimeSurfaceTools        map[string]int                             `json:"runtime_surface_tools,omitempty"`
	RuntimeSurfaceCapabilities map[string]int                             `json:"runtime_surface_capabilities,omitempty"`
	LoopDecisions              int                                        `json:"loop_decisions,omitempty"`
	LoopDecisionByKind         map[string]int                             `json:"loop_decision_by_kind,omitempty"`
	LoopDecisionByDecision     map[string]int                             `json:"loop_decision_by_decision,omitempty"`
	LoopDecisionExamples       []agenteval.LoopDecision                   `json:"loop_decision_examples,omitempty"`
	ContextCompactions         int                                        `json:"context_compactions,omitempty"`
	ContextCompactionsReactive int                                        `json:"context_compactions_reactive,omitempty"`
	ContextCompactionRemoved   int                                        `json:"context_compaction_removed_messages,omitempty"`
	ContextCompactionSummary   int                                        `json:"context_compaction_summary_bytes,omitempty"`
	LoopGuardInterventions     int                                        `json:"loop_guard_interventions"`
	ForcedNoTools              int                                        `json:"forced_no_tools"`
	SourceAccessResults        int                                        `json:"source_access_results"`
	SourceAccessVerified       int                                        `json:"source_access_verified"`
	SourceAccessDiscoveryOnly  int                                        `json:"source_access_discovery_only"`
	SourceAccessNetwork        int                                        `json:"source_access_network"`
	SourceAccessDynamicPartial int                                        `json:"source_access_dynamic_partial"`
	MemoryUpdates              int                                        `json:"memory_updates"`
	MemoryUpdateAdd            int                                        `json:"memory_update_add"`
	MemoryUpdateReplace        int                                        `json:"memory_update_replace"`
	MemoryUpdateRemove         int                                        `json:"memory_update_remove"`
	ToolDurationMS             int64                                      `json:"tool_duration_ms"`
	ToolContextTruncated       int                                        `json:"tool_context_truncated"`
	ToolContextOmittedBytes    int                                        `json:"tool_context_omitted_bytes"`
	ToolArgsTruncated          int                                        `json:"tool_args_truncated"`
	ToolArgsOmittedBytes       int                                        `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated       int                                        `json:"tool_results_truncated"`
	ToolResultsOmittedBytes    int                                        `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts        int                                        `json:"tool_result_artifacts"`
	VerifierRuns               int                                        `json:"verifier_runs"`
	VerifierPassed             int                                        `json:"verifier_passed"`
	VerifierFailed             int                                        `json:"verifier_failed"`
	VerifierOutputTruncated    int                                        `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes int                                        `json:"verifier_output_omitted_bytes"`
	TraceSchemaVersions        map[int]int                                `json:"trace_schema_versions,omitempty"`
	TraceEvents                int                                        `json:"trace_events,omitempty"`
	TraceEventTypes            map[string]int                             `json:"trace_event_types,omitempty"`
	InputTokens                int                                        `json:"input_tokens"`
	OutputTokens               int                                        `json:"output_tokens"`
	EndCompleted               int                                        `json:"end_completed"`
	EndMaxTurns                int                                        `json:"end_max_turns"`
	EndErrors                  int                                        `json:"end_errors"`
	EndCancelled               int                                        `json:"end_cancelled"`
	EndUnknown                 int                                        `json:"end_unknown"`
	FailureKinds               map[string]int                             `json:"failure_kinds,omitempty"`
	FailureHints               failureHintMap                             `json:"failure_hints,omitempty"`
	ToolFailureHints           failureHintMap                             `json:"tool_failure_hints,omitempty"`
	RuntimeErrorHints          failureHintMap                             `json:"runtime_error_hints,omitempty"`
	RemovedWorkspaces          int                                        `json:"removed_workspaces"`
	CleanupErrors              int                                        `json:"cleanup_errors"`

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

type runtimeSurfaceSummary struct {
	ToolCount                    int                      `json:"tool_count"`
	Tools                        []string                 `json:"tools,omitempty"`
	Capabilities                 *sse.RuntimeCapabilities `json:"capabilities,omitempty"`
	MaxTurnSteps                 int                      `json:"max_turn_steps,omitempty"`
	MaxToolCalls                 int                      `json:"max_tool_calls,omitempty"`
	ToolResultEventCapBytes      int                      `json:"tool_result_event_cap_bytes,omitempty"`
	ToolResultContextMaxBytes    int                      `json:"tool_result_context_max_bytes,omitempty"`
	ToolResultContextBudgetBytes int                      `json:"tool_result_context_budget_bytes,omitempty"`
	ToolResultArtifactPrefix     string                   `json:"tool_result_artifact_prefix,omitempty"`
	TurnToolOverride             bool                     `json:"turn_tool_override,omitempty"`
}

func printBatchResultJSONL(w io.Writer, meta evalJSONLMetadata, res agenteval.BatchResult) {
	failureKinds := failureKindsForResult(res.Failures)
	writeJSONLine(w, batchResultRecord{
		evalJSONLMetadata:          meta,
		Type:                       "scenario",
		Scenario:                   res.BatchScenario,
		OK:                         res.OK,
		RunExitCode:                res.RunExitCode,
		DurationMS:                 res.Duration.Milliseconds(),
		Workspace:                  res.Workspace,
		TracePath:                  res.TracePath,
		DebugManifestPath:          retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved),
		TimelinePath:               retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved),
		FinalTextPath:              retainedDebugPath(res.FinalTextPath, res.WorkspaceRemoved),
		StdoutPath:                 retainedDebugPath(res.StdoutPath, res.WorkspaceRemoved),
		StderrPath:                 retainedDebugPath(res.StderrPath, res.WorkspaceRemoved),
		AffentctlCommand:           append([]string(nil), res.AffentctlCommand...),
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
		ToolFailureExamples:        cloneToolFailureExamples(res.ToolFailureExamples),
		RuntimeErrorByKind:         cloneStringIntMap(res.RuntimeErrorByKind),
		RuntimeErrorExamples:       cloneRuntimeErrorExamples(res.RuntimeErrorExamples),
		RuntimeSurface:             runtimeSurfaceSummaryForJSONL(res.RuntimeSurface),
		LoopDecisions:              res.LoopDecisionStats.Count,
		LoopDecisionByKind:         cloneStringIntMap(res.LoopDecisionStats.ByKind),
		LoopDecisionByDecision:     cloneStringIntMap(res.LoopDecisionStats.ByDecision),
		LoopDecisionExamples:       cloneLoopDecisionExamples(res.LoopDecisionStats.Examples),
		ContextCompactions:         res.ContextCompactions.Count,
		ContextCompactionsReactive: res.ContextCompactions.Reactive,
		ContextCompactionRemoved:   res.ContextCompactions.RemovedMessages,
		ContextCompactionSummary:   res.ContextCompactions.SummaryBytes,
		LoopGuardInterventions:     res.ToolStats.LoopGuardInterventions,
		ForcedNoTools:              res.ToolStats.ForcedNoTools,
		SourceAccessResults:        res.ToolStats.SourceAccessResults,
		SourceAccessVerified:       res.ToolStats.SourceAccessVerified,
		SourceAccessDiscoveryOnly:  res.ToolStats.SourceAccessDiscoveryOnly,
		SourceAccessNetwork:        res.ToolStats.SourceAccessNetwork,
		SourceAccessDynamicPartial: res.ToolStats.SourceAccessDynamicPartial,
		MemoryUpdates:              res.ToolStats.MemoryUpdates,
		MemoryUpdateAdd:            res.ToolStats.MemoryUpdateAdd,
		MemoryUpdateReplace:        res.ToolStats.MemoryUpdateReplace,
		MemoryUpdateRemove:         res.ToolStats.MemoryUpdateRemove,
		ToolDurationMS:             res.ToolStats.ToolDurationMS,
		ToolContextTruncated:       res.ToolStats.ToolContextTruncated,
		ToolContextOmittedBytes:    res.ToolStats.ToolContextOmittedBytes,
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
		TraceEvents:                res.TraceEvents,
		TraceEventTypes:            cloneStringIntMap(res.TraceEventTypes),
		InputTokens:                res.Usage.InputTokens,
		OutputTokens:               res.Usage.OutputTokens,
		WorkspaceRemoved:           res.WorkspaceRemoved,
		CleanupError:               res.CleanupError,
		Failures:                   res.Failures,
		FailureKinds:               failureKinds,
		FailureHints:               failureHintsForKinds(failureKinds),
		ToolFailureHints:           toolFailureHintsForKinds(res.ToolStats.ToolFailureByKind),
		RuntimeErrorHints:          failureHintsForKinds(res.RuntimeErrorByKind),
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

func runtimeSurfaceSummaryForJSONL(surface *sse.RuntimeSurfacePayload) *runtimeSurfaceSummary {
	if surface == nil {
		return nil
	}
	tools := runtimeSurfaceToolNames(surface)
	caps := surface.Capabilities
	return &runtimeSurfaceSummary{
		ToolCount:                    surface.ToolCount,
		Tools:                        tools,
		Capabilities:                 &caps,
		MaxTurnSteps:                 surface.MaxTurnSteps,
		MaxToolCalls:                 surface.MaxToolCalls,
		ToolResultEventCapBytes:      surface.ToolResultEventCapBytes,
		ToolResultContextMaxBytes:    surface.ToolResultContextMaxBytes,
		ToolResultContextBudgetBytes: surface.ToolResultContextBudgetBytes,
		ToolResultArtifactPrefix:     surface.ToolResultArtifactPrefix,
		TurnToolOverride:             surface.TurnToolOverride,
	}
}

func runtimeSurfaceToolNames(surface *sse.RuntimeSurfacePayload) []string {
	if surface == nil {
		return nil
	}
	tools := make([]string, 0, len(surface.Tools))
	seen := map[string]bool{}
	for _, tool := range surface.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, name)
	}
	sort.Strings(tools)
	return tools
}

func runtimeSurfaceCapabilityNames(c sse.RuntimeCapabilities) []string {
	var out []string
	if c.Builtins {
		out = append(out, "builtins")
	} else if len(c.WorkspaceTools) > 0 {
		out = append(out, "workspace_partial")
	}
	if c.Memory {
		out = append(out, "memory")
	}
	if c.Plan {
		out = append(out, "plan")
	}
	if c.SessionSearch {
		out = append(out, "session_search")
	}
	if c.WebFetch {
		out = append(out, "web_fetch")
	}
	if c.WebSearch {
		out = append(out, "web_search")
	}
	if c.Browser {
		out = append(out, "browser")
	}
	if c.Subagent {
		out = append(out, "subagent")
	}
	if c.FocusedTasks {
		out = append(out, "focused_tasks")
	}
	if c.Skill {
		out = append(out, "skill")
	}
	if c.MCP {
		out = append(out, "mcp")
	}
	return out
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
		ToolFailureExamples:        cloneToolFailureExamples(s.ToolFailureExamples),
		RuntimeErrorByKind:         cloneStringIntMap(s.RuntimeErrorByKind),
		RuntimeErrorExamples:       cloneRuntimeErrorExamples(s.RuntimeErrorExamples),
		RuntimeSurfaceScenarios:    s.RuntimeSurfaceScenarios,
		RuntimeSurfaceTools:        cloneStringIntMap(s.RuntimeSurfaceTools),
		RuntimeSurfaceCapabilities: cloneStringIntMap(s.RuntimeSurfaceCapabilities),
		LoopDecisions:              s.LoopDecisions,
		LoopDecisionByKind:         cloneStringIntMap(s.LoopDecisionByKind),
		LoopDecisionByDecision:     cloneStringIntMap(s.LoopDecisionByDecision),
		LoopDecisionExamples:       cloneLoopDecisionExamples(s.LoopDecisionExamples),
		ContextCompactions:         s.ContextCompactions,
		ContextCompactionsReactive: s.ContextCompactionsReactive,
		ContextCompactionRemoved:   s.ContextCompactionRemoved,
		ContextCompactionSummary:   s.ContextCompactionSummary,
		LoopGuardInterventions:     s.LoopGuardInterventions,
		ForcedNoTools:              s.ForcedNoTools,
		SourceAccessResults:        s.SourceAccessResults,
		SourceAccessVerified:       s.SourceAccessVerified,
		SourceAccessDiscoveryOnly:  s.SourceAccessDiscoveryOnly,
		SourceAccessNetwork:        s.SourceAccessNetwork,
		SourceAccessDynamicPartial: s.SourceAccessDynamicPartial,
		MemoryUpdates:              s.MemoryUpdates,
		MemoryUpdateAdd:            s.MemoryUpdateAdd,
		MemoryUpdateReplace:        s.MemoryUpdateReplace,
		MemoryUpdateRemove:         s.MemoryUpdateRemove,
		ToolDurationMS:             s.ToolDurationMS,
		ToolContextTruncated:       s.ToolContextTruncated,
		ToolContextOmittedBytes:    s.ToolContextOmittedBytes,
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
		TraceEvents:                s.TraceEvents,
		TraceEventTypes:            cloneStringIntMap(s.TraceEventTypes),
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
		RuntimeErrorHints:          failureHintsForKinds(s.RuntimeErrorByKind),
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

func cloneToolFailureExamples(in map[string][]agenteval.ToolFailureExample) map[string][]agenteval.ToolFailureExample {
	return cloneExampleMap(in)
}

func cloneRuntimeErrorExamples(in map[string][]agenteval.RuntimeErrorExample) map[string][]agenteval.RuntimeErrorExample {
	return cloneExampleMap(in)
}

func cloneLoopDecisionExamples(in []agenteval.LoopDecision) []agenteval.LoopDecision {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.LoopDecision(nil), in...)
}

func appendLoopDecisionExamples(dst, src []agenteval.LoopDecision, limit int) []agenteval.LoopDecision {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		dst = append(dst, ex)
	}
	return dst
}

func cloneExampleMap[T any](in map[string][]T) map[string][]T {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]T, len(in))
	for kind, examples := range in {
		if len(examples) == 0 {
			continue
		}
		out[kind] = append([]T(nil), examples...)
	}
	if len(out) == 0 {
		return nil
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
	if res.TraceDeltas {
		fmt.Fprintln(w, "  trace_deltas: true")
	}
	if res.TraceEvents > 0 {
		fmt.Fprintf(w, "  trace_events: %d", res.TraceEvents)
		if len(res.TraceEventTypes) > 0 {
			fmt.Fprintf(w, " (%s)", formatStringIntCounts(res.TraceEventTypes))
		}
		fmt.Fprintln(w)
	}
	if path := retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved); path != "" {
		fmt.Fprintf(w, "  debug: %s\n", path)
	}
	if path := retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved); path != "" {
		fmt.Fprintf(w, "  timeline: %s\n", path)
	}
	if path := retainedDebugPath(res.FinalTextPath, res.WorkspaceRemoved); path != "" {
		fmt.Fprintf(w, "  final: %s\n", path)
	}
	if path := retainedDebugPath(res.StdoutPath, res.WorkspaceRemoved); path != "" {
		fmt.Fprintf(w, "  stdout: %s\n", path)
	}
	if path := retainedDebugPath(res.StderrPath, res.WorkspaceRemoved); path != "" {
		fmt.Fprintf(w, "  stderr: %s\n", path)
	}
	if len(res.AffentctlCommand) > 0 {
		fmt.Fprintf(w, "  command: %s\n", strings.Join(res.AffentctlCommand, " "))
	}
	if res.RunExitCode != 0 {
		fmt.Fprintf(w, "  run_exit: %d\n", res.RunExitCode)
	}
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
	if hasToolContextTruncation(res.ToolStats) {
		fmt.Fprintf(w, " ctx_trunc=%d,omitted=%d", res.ToolStats.ToolContextTruncated, res.ToolStats.ToolContextOmittedBytes)
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
	if hasSourceAccessStats(res.ToolStats) {
		fmt.Fprintf(w, " source_access=results:%d,verified:%d,discovery:%d,network:%d,dynamic_partial:%d",
			res.ToolStats.SourceAccessResults,
			res.ToolStats.SourceAccessVerified,
			res.ToolStats.SourceAccessDiscoveryOnly,
			res.ToolStats.SourceAccessNetwork,
			res.ToolStats.SourceAccessDynamicPartial,
		)
	}
	if hasMemoryUpdateStats(res.ToolStats) {
		fmt.Fprintf(w, " memory_updates=%d(add:%d,replace:%d,remove:%d)",
			res.ToolStats.MemoryUpdates,
			res.ToolStats.MemoryUpdateAdd,
			res.ToolStats.MemoryUpdateReplace,
			res.ToolStats.MemoryUpdateRemove,
		)
	}
	if len(res.RuntimeErrorByKind) > 0 {
		fmt.Fprintf(w, " runtime_error_kinds=%s", formatStringIntCounts(res.RuntimeErrorByKind))
	}
	if res.LoopDecisionStats.Count > 0 {
		fmt.Fprintf(w, " loop_decisions=%d", res.LoopDecisionStats.Count)
		if len(res.LoopDecisionStats.ByKind) > 0 {
			fmt.Fprintf(w, " loop_decision_kinds=%s", formatStringIntCounts(res.LoopDecisionStats.ByKind))
		}
		if len(res.LoopDecisionStats.ByDecision) > 0 {
			fmt.Fprintf(w, " loop_decision_results=%s", formatStringIntCounts(res.LoopDecisionStats.ByDecision))
		}
	}
	if res.ContextCompactions.Count > 0 {
		fmt.Fprintf(w, " compactions=%d,reactive=%d,removed=%d,summary_bytes=%d",
			res.ContextCompactions.Count,
			res.ContextCompactions.Reactive,
			res.ContextCompactions.RemovedMessages,
			res.ContextCompactions.SummaryBytes,
		)
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
	printToolFailureExampleLines(w, res.ToolFailureExamples, "  ")
	printFailureHintLines(w, res.RuntimeErrorByKind, "  ")
	printRuntimeErrorExampleLines(w, res.RuntimeErrorExamples, "  ")
	printLoopDecisionExampleLines(w, res.LoopDecisionStats.Examples, "  ")
}

func retainedDebugPath(path string, workspaceRemoved bool) string {
	path = strings.TrimSpace(path)
	if workspaceRemoved {
		return ""
	}
	return path
}

func hasToolTruncation(stats agenteval.ToolTruncationStats) bool {
	return stats.ArgsTruncated > 0 ||
		stats.ArgsOmittedBytes > 0 ||
		stats.ResultsTruncated > 0 ||
		stats.ResultsOmittedBytes > 0 ||
		stats.ResultArtifacts > 0
}

func hasToolContextTruncation(stats agenteval.ToolRuntimeStats) bool {
	return stats.ToolContextTruncated > 0 || stats.ToolContextOmittedBytes > 0
}

func hasSourceAccessStats(stats agenteval.ToolRuntimeStats) bool {
	return stats.SourceAccessResults > 0 ||
		stats.SourceAccessVerified > 0 ||
		stats.SourceAccessDiscoveryOnly > 0 ||
		stats.SourceAccessNetwork > 0 ||
		stats.SourceAccessDynamicPartial > 0
}

func hasMemoryUpdateStats(stats agenteval.ToolRuntimeStats) bool {
	return stats.MemoryUpdates > 0 ||
		stats.MemoryUpdateAdd > 0 ||
		stats.MemoryUpdateReplace > 0 ||
		stats.MemoryUpdateRemove > 0
}

func hasBatchMemoryUpdateStats(stats batchSummary) bool {
	return stats.MemoryUpdates > 0 ||
		stats.MemoryUpdateAdd > 0 ||
		stats.MemoryUpdateReplace > 0 ||
		stats.MemoryUpdateRemove > 0
}

func failureKind(failure string) string {
	failure = strings.TrimSpace(failure)
	lower := strings.ToLower(failure)
	if kind := agenteval.RuntimeErrorKind(failure); kind != "" {
		return kind
	}
	switch {
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

func selectedEvalScenarios(suite, scenarioCSV, prompt, promptFile, name, sessionID string, maxTurns int, verifyCommand string) ([]agenteval.BatchScenario, error) {
	if strings.TrimSpace(prompt) != "" || strings.TrimSpace(promptFile) != "" {
		if strings.TrimSpace(suite) != "" || strings.TrimSpace(scenarioCSV) != "" {
			return nil, fmt.Errorf("--prompt/--prompt-file cannot be combined with --suite or --scenario")
		}
		if maxTurns <= 0 {
			return nil, fmt.Errorf("--max-turns must be positive")
		}
		body, err := readAdHocPrompt(prompt, promptFile)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("ad-hoc prompt is empty")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "adhoc"
		}
		return []agenteval.BatchScenario{{
			Name:          name,
			Prompt:        body,
			SessionID:     strings.TrimSpace(sessionID),
			MaxTurns:      maxTurns,
			VerifyCommand: strings.TrimSpace(verifyCommand),
		}}, nil
	}
	names := splitCSV(scenarioCSV)
	return agenteval.SelectBatchScenariosForSuite(suite, names)
}

func readAdHocPrompt(prompt, promptFile string) (string, error) {
	promptFile = strings.TrimSpace(promptFile)
	if strings.TrimSpace(prompt) != "" && promptFile != "" {
		return "", fmt.Errorf("--prompt and --prompt-file cannot be used together")
	}
	if promptFile != "" {
		raw, err := os.ReadFile(promptFile)
		if err != nil {
			return "", fmt.Errorf("--prompt-file: %w", err)
		}
		return string(raw), nil
	}
	if prompt == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("--prompt=-: %w", err)
		}
		return string(raw), nil
	}
	return prompt, nil
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
