package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
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
		list                = fs.Bool("list", false, "list built-in scenarios and exit")
		listSuites          = fs.Bool("list-suites", false, "list built-in scenario suites and exit")
		listQualityProfiles = fs.Bool("list-quality-profiles", false, "list built-in quality gate profiles and exit")
		suite               = fs.String("suite", "", "scenario suite to run/list (e.g. small-model-tools)")
		scenarioCSV         = fs.String("scenario", "", "comma-separated scenario names; empty runs all")
		prompt              = fs.String("prompt", "", "run one ad-hoc prompt; use '-' for stdin")
		promptFile          = fs.String("prompt-file", "", "run one ad-hoc prompt read from file")
		adHocName           = fs.String("name", "adhoc", "scenario name for --prompt/--prompt-file debug runs")
		adHocSessionID      = fs.String("session-id", "", "session id forwarded to affentctl for --prompt/--prompt-file debug runs")
		adHocMaxTurns       = fs.Int("max-turns", agenteval.DefaultBatchMaxTurnSteps, "max assistant/tool loop steps for --prompt/--prompt-file debug runs")
		adHocVerify         = fs.String("verify-command", "", "optional verifier command for --prompt/--prompt-file debug runs")
		repoRoot            = fs.String("repo-root", ".", "Affent repository root")
		workRoot            = fs.String("work-root", "", "directory for temporary scenario workspaces; default $TMPDIR/affent-eval")
		baseURL             = fs.String("base-url", "", "OpenAI-compatible endpoint (env: AFFENTCTL_BASE_URL)")
		apiKey              = fs.String("api-key", "", "API key (env: AFFENTCTL_API_KEY)")
		model               = fs.String("model", "", "model id (env: AFFENTCTL_MODEL)")
		providerLabel       = fs.String("provider-label", "", "provider label written to JSONL for comparisons (env: AFFENTEVAL_PROVIDER_LABEL)")
		temperature         = fs.String("temperature", "0", "sampling temperature forwarded to affentctl")
		topP                = fs.String("top-p", "", "top-p sampling forwarded to affentctl; empty keeps provider default")
		maxTokens           = fs.String("max-tokens", "", "max output tokens forwarded to affentctl; empty keeps provider default")
		seed                = fs.String("seed", "", "deterministic-sampling seed forwarded to affentctl; empty keeps provider default")
		executor            = fs.String("executor", "local", "affentctl tool executor for scenario runs: local, sandbox, or docker:<container>")
		runtimeEvalMode     = fs.Bool("runtime-eval-mode", true, "pass affentctl --eval-mode during scenario runs; default true so evals start with no tools")
		runtimeTools        = fs.String("runtime-tools", "", "comma-separated affentctl --eval-tools allowlist, e.g. readonly_workspace,web,recall or read_file,shell")
		runtimeAllTools     = fs.Bool("runtime-all-tools", false, "pass affentctl --eval-all-tools to enable the full tool surface under runtime eval mode")
		runtimeMemory       = fs.Bool("runtime-memory", false, "pass affentctl --memory=true during scenario runs; useful for memory-only opt-in")
		runtimeWeb          = fs.Bool("runtime-web", false, "pass affentctl --web --web-search during scenario runs for external retrieval/debug evals")
		runtimeBrowser      = fs.Bool("runtime-browser", false, "pass affentctl --browser during scenario runs for rendered-page/browser debug evals")
		runtimeMCPConfig    = fs.String("runtime-mcp-config", "", "pass affentctl --mcp-config PATH during scenario runs; useful to opt into MCP only")
		traceDeltas         = fs.Bool("trace-deltas", false, "retain streaming message delta events in trace JSONL for deep debugging; default skips deltas to keep traces compact")
		timeout             = fs.Duration("timeout", 5*time.Minute, "per-scenario timeout")
		verifierOutputCap   = fs.Int("verifier-output-cap", agenteval.DefaultVerifierOutputCapBytes, "maximum verifier output bytes buffered per scenario")
		jsonl               = fs.Bool("jsonl", false, "emit machine-readable JSONL records instead of text")
		keepWorkspaces      = fs.Bool("keep-workspaces", false, "keep passing scenario workspaces; failing scenario workspaces are always kept")
		qualityProfile      = fs.String("quality-profile", "", "predefined quality gate profile: longrun or web-evidence; explicit gate flags override profile thresholds")
		gates               = qualityGateConfig{
			MinPassRate:                      fs.Float64("min-pass-rate", -1, "optional quality gate: minimum batch pass rate, 0..1"),
			MinCompletionRate:                fs.Float64("min-completion-rate", -1, "optional quality gate: minimum completed-turn rate, 0..1"),
			MinMemoryUpdateRate:              fs.Float64("min-memory-update-rate", -1, "optional quality gate: minimum confirmed memory updates per scenario, 0..1"),
			MinRuntimeSurfaceRate:            fs.Float64("min-runtime-surface-rate", -1, "optional quality gate: minimum scenario rate with recorded runtime surface, 0..1"),
			MinSourceNetworkRate:             fs.Float64("min-source-network-rate", -1, "optional quality gate: minimum network/API source access rate, 0..1"),
			MinSourceAccessVerifiedRate:      fs.Float64("min-source-access-verified-rate", -1, "optional quality gate: minimum verified SourceAccess rate, 0..1"),
			MinExpectationCapabilityPassRate: fs.Float64("min-expectation-capability-pass-rate", -1, "optional quality gate: minimum pass rate across declared expectation capability instances, 0..1"),
			MinSessionSearchContextHitRate:   fs.Float64("min-session-search-context-hit-rate", -1, "optional quality gate: minimum session_search context-hit rate, 0..1"),
			MinToolRepairSuccessRate:         fs.Float64("min-tool-repair-success-rate", -1, "optional quality gate: minimum successful tool-call repair rate, 0..1"),
			MinVerifierPassRate:              fs.Float64("min-verifier-pass-rate", -1, "optional quality gate: minimum verifier pass rate, 0..1"),
			MaxFocusedTaskErrorRate:          fs.Float64("max-focused-task-error-rate", -1, "optional quality gate: maximum focused-task error rate per focused-task call, 0..1"),
			MaxForcedNoToolsRate:             fs.Float64("max-forced-no-tools-rate", -1, "optional quality gate: maximum forced no-tool follow-up rate per tool call, 0..1"),
			MaxLoopGuardInterventionRate:     fs.Float64("max-loop-guard-intervention-rate", -1, "optional quality gate: maximum loop guard intervention rate per tool call, 0..1"),
			MaxPlanErrorRate:                 fs.Float64("max-plan-error-rate", -1, "optional quality gate: maximum plan tool error rate per plan call, 0..1"),
			MaxSourceDiscoveryOnlyRate:       fs.Float64("max-source-discovery-only-rate", -1, "optional quality gate: maximum discovery-only source access rate, 0..1"),
			MaxSourceDynamicPartialRate:      fs.Float64("max-source-dynamic-partial-rate", -1, "optional quality gate: maximum dynamic-partial source access rate, 0..1"),
			MaxSubagentErrorRate:             fs.Float64("max-subagent-error-rate", -1, "optional quality gate: maximum subagent error rate per subagent call, 0..1"),
			MaxToolErrorRate:                 fs.Float64("max-tool-error-rate", -1, "optional quality gate: maximum tool error rate, 0..1"),
			MaxToolContextTruncationRate:     fs.Float64("max-tool-context-truncation-rate", -1, "optional quality gate: maximum tool-context truncation rate, 0..1"),
			MaxToolResultTruncationRate:      fs.Float64("max-tool-result-truncation-rate", -1, "optional quality gate: maximum tool-result event truncation rate, 0..1"),
			MaxAvgRuntimeErrors:              fs.Float64("max-avg-runtime-errors", -1, "optional quality gate: maximum average runtime error events per scenario"),
			MaxAvgContextCompactions:         fs.Float64("max-avg-context-compactions", -1, "optional quality gate: maximum average context compactions per scenario"),
			MaxAvgReactiveCompactions:        fs.Float64("max-avg-reactive-context-compactions", -1, "optional quality gate: maximum average reactive context compactions per scenario"),
			MaxAvgTotalTokens:                fs.Float64("max-avg-total-tokens", -1, "optional quality gate: maximum average total tokens per scenario"),
		}
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
	if *listQualityProfiles {
		printQualityGateProfiles(os.Stdout)
		return 0
	}
	if err := applyQualityGateProfile(&gates, *qualityProfile, func(name string) bool {
		return flagWasSet(fs, name)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 64
	}
	if err := validateQualityGateConfig(gates); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
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
	if err := validateRunConfig(
		*temperature,
		*topP,
		*maxTokens,
		*seed,
		*timeout,
		*executor,
		scenarios,
		*workRoot,
		flagWasSet(fs, "work-root"),
		*verifierOutputCap,
		*runtimeEvalMode,
		*runtimeTools,
		*runtimeAllTools,
		*runtimeMemory,
		*runtimeWeb,
		*runtimeBrowser,
		*runtimeMCPConfig,
	); err != nil {
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
	jsonlMeta := evalJSONLMetadataFromConfig(*suite, *model, *providerLabel, *executor, *temperature, *topP, *maxTokens, *seed, *runtimeEvalMode, *runtimeTools, *runtimeAllTools, *runtimeMemory, *runtimeWeb, *runtimeBrowser, *traceDeltas, *runtimeMCPConfig, *timeout, *qualityProfile, gates)
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
	gateFailures := qualityGateFailures(summary, gates)
	if *jsonl {
		printBatchSummaryJSONL(os.Stdout, jsonlMeta, summary, gateFailures)
	} else {
		printBatchSummary(os.Stdout, summary)
		printBatchQualityGates(os.Stdout, jsonlMeta, gateFailures)
	}
	if len(gateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "quality gates failed:")
		for _, failure := range gateFailures {
			fmt.Fprintf(os.Stderr, "  - %s\n", failure)
		}
	}
	if summary.Failed > 0 {
		return 1
	}
	if len(gateFailures) > 0 {
		return 1
	}
	return 0
}

type qualityGateConfig struct {
	MinPassRate                      *float64
	MinCompletionRate                *float64
	MinMemoryUpdateRate              *float64
	MinRuntimeSurfaceRate            *float64
	MinSourceNetworkRate             *float64
	MinSourceAccessVerifiedRate      *float64
	MinExpectationCapabilityPassRate *float64
	MinSessionSearchContextHitRate   *float64
	MinToolRepairSuccessRate         *float64
	MinVerifierPassRate              *float64
	MaxFocusedTaskErrorRate          *float64
	MaxForcedNoToolsRate             *float64
	MaxLoopGuardInterventionRate     *float64
	MaxPlanErrorRate                 *float64
	MaxSourceDiscoveryOnlyRate       *float64
	MaxSourceDynamicPartialRate      *float64
	MaxSubagentErrorRate             *float64
	MaxToolErrorRate                 *float64
	MaxToolContextTruncationRate     *float64
	MaxToolResultTruncationRate      *float64
	MaxAvgRuntimeErrors              *float64
	MaxAvgContextCompactions         *float64
	MaxAvgReactiveCompactions        *float64
	MaxAvgTotalTokens                *float64
}

type qualityGateProfileDefinition struct {
	Name        string
	Description string
	Gates       qualityGateConfig
}

func qualityGateProfileDefinitions() []qualityGateProfileDefinition {
	return []qualityGateProfileDefinition{
		{
			Name:        "longrun",
			Description: "general long-run stability gates for task completion, tool recovery, delegation/plan errors, truncation, runtime errors, and token cost",
			Gates: qualityGateConfig{
				MinPassRate:                      float64Ptr(0.80),
				MinCompletionRate:                float64Ptr(0.90),
				MinExpectationCapabilityPassRate: float64Ptr(0.80),
				MinRuntimeSurfaceRate:            float64Ptr(0.90),
				MaxFocusedTaskErrorRate:          float64Ptr(0.10),
				MaxForcedNoToolsRate:             float64Ptr(0.10),
				MaxLoopGuardInterventionRate:     float64Ptr(0.20),
				MaxPlanErrorRate:                 float64Ptr(0.05),
				MaxSubagentErrorRate:             float64Ptr(0.10),
				MaxToolErrorRate:                 float64Ptr(0.08),
				MaxToolContextTruncationRate:     float64Ptr(0.30),
				MaxToolResultTruncationRate:      float64Ptr(0.20),
				MaxAvgRuntimeErrors:              float64Ptr(0.20),
				MaxAvgReactiveCompactions:        float64Ptr(0.50),
				MaxAvgTotalTokens:                float64Ptr(120000),
			},
		},
		{
			Name:        "web-evidence",
			Description: "web and browser evidence gates for current-fact tasks, emphasizing verified SourceAccess, network/API evidence, low discovery-only output, and bounded cost",
			Gates: qualityGateConfig{
				MinPassRate:                      float64Ptr(0.80),
				MinCompletionRate:                float64Ptr(0.90),
				MinExpectationCapabilityPassRate: float64Ptr(0.80),
				MinRuntimeSurfaceRate:            float64Ptr(0.90),
				MinSourceNetworkRate:             float64Ptr(0.50),
				MinSourceAccessVerifiedRate:      float64Ptr(0.90),
				MaxForcedNoToolsRate:             float64Ptr(0.10),
				MaxLoopGuardInterventionRate:     float64Ptr(0.25),
				MaxSourceDiscoveryOnlyRate:       float64Ptr(0.15),
				MaxSourceDynamicPartialRate:      float64Ptr(0.20),
				MaxToolErrorRate:                 float64Ptr(0.10),
				MaxToolResultTruncationRate:      float64Ptr(0.25),
				MaxAvgRuntimeErrors:              float64Ptr(0.20),
				MaxAvgTotalTokens:                float64Ptr(120000),
			},
		},
	}
}

func printQualityGateProfiles(w io.Writer) {
	for _, profile := range qualityGateProfileDefinitions() {
		fmt.Fprintf(w, "%s\t%s\n", profile.Name, profile.Description)
		for _, line := range qualityGateConfigLines(profile.Gates) {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}

func qualityGateConfigLines(g qualityGateConfig) []string {
	var lines []string
	add := func(name string, value *float64) {
		if value == nil || *value < 0 {
			return
		}
		lines = append(lines, fmt.Sprintf("%s=%s", name, formatGateFloat(*value)))
	}
	add("min-pass-rate", g.MinPassRate)
	add("min-completion-rate", g.MinCompletionRate)
	add("min-memory-update-rate", g.MinMemoryUpdateRate)
	add("min-runtime-surface-rate", g.MinRuntimeSurfaceRate)
	add("min-source-network-rate", g.MinSourceNetworkRate)
	add("min-source-access-verified-rate", g.MinSourceAccessVerifiedRate)
	add("min-expectation-capability-pass-rate", g.MinExpectationCapabilityPassRate)
	add("min-session-search-context-hit-rate", g.MinSessionSearchContextHitRate)
	add("min-tool-repair-success-rate", g.MinToolRepairSuccessRate)
	add("min-verifier-pass-rate", g.MinVerifierPassRate)
	add("max-focused-task-error-rate", g.MaxFocusedTaskErrorRate)
	add("max-forced-no-tools-rate", g.MaxForcedNoToolsRate)
	add("max-loop-guard-intervention-rate", g.MaxLoopGuardInterventionRate)
	add("max-plan-error-rate", g.MaxPlanErrorRate)
	add("max-source-discovery-only-rate", g.MaxSourceDiscoveryOnlyRate)
	add("max-source-dynamic-partial-rate", g.MaxSourceDynamicPartialRate)
	add("max-subagent-error-rate", g.MaxSubagentErrorRate)
	add("max-tool-error-rate", g.MaxToolErrorRate)
	add("max-tool-context-truncation-rate", g.MaxToolContextTruncationRate)
	add("max-tool-result-truncation-rate", g.MaxToolResultTruncationRate)
	add("max-avg-runtime-errors", g.MaxAvgRuntimeErrors)
	add("max-avg-context-compactions", g.MaxAvgContextCompactions)
	add("max-avg-reactive-context-compactions", g.MaxAvgReactiveCompactions)
	add("max-avg-total-tokens", g.MaxAvgTotalTokens)
	return lines
}

func applyQualityGateProfile(g *qualityGateConfig, profile string, flagSet func(name string) bool) error {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		return nil
	}
	if g == nil {
		return nil
	}
	profileConfig, err := qualityGateProfileConfig(profile)
	if err != nil {
		return err
	}
	apply := func(name string, dst **float64, src *float64) {
		if src == nil || (flagSet != nil && flagSet(name)) {
			return
		}
		*dst = cloneFloat64Ptr(src)
	}
	apply("min-pass-rate", &g.MinPassRate, profileConfig.MinPassRate)
	apply("min-completion-rate", &g.MinCompletionRate, profileConfig.MinCompletionRate)
	apply("min-memory-update-rate", &g.MinMemoryUpdateRate, profileConfig.MinMemoryUpdateRate)
	apply("min-runtime-surface-rate", &g.MinRuntimeSurfaceRate, profileConfig.MinRuntimeSurfaceRate)
	apply("min-source-network-rate", &g.MinSourceNetworkRate, profileConfig.MinSourceNetworkRate)
	apply("min-source-access-verified-rate", &g.MinSourceAccessVerifiedRate, profileConfig.MinSourceAccessVerifiedRate)
	apply("min-expectation-capability-pass-rate", &g.MinExpectationCapabilityPassRate, profileConfig.MinExpectationCapabilityPassRate)
	apply("min-session-search-context-hit-rate", &g.MinSessionSearchContextHitRate, profileConfig.MinSessionSearchContextHitRate)
	apply("min-tool-repair-success-rate", &g.MinToolRepairSuccessRate, profileConfig.MinToolRepairSuccessRate)
	apply("min-verifier-pass-rate", &g.MinVerifierPassRate, profileConfig.MinVerifierPassRate)
	apply("max-focused-task-error-rate", &g.MaxFocusedTaskErrorRate, profileConfig.MaxFocusedTaskErrorRate)
	apply("max-forced-no-tools-rate", &g.MaxForcedNoToolsRate, profileConfig.MaxForcedNoToolsRate)
	apply("max-loop-guard-intervention-rate", &g.MaxLoopGuardInterventionRate, profileConfig.MaxLoopGuardInterventionRate)
	apply("max-plan-error-rate", &g.MaxPlanErrorRate, profileConfig.MaxPlanErrorRate)
	apply("max-source-discovery-only-rate", &g.MaxSourceDiscoveryOnlyRate, profileConfig.MaxSourceDiscoveryOnlyRate)
	apply("max-source-dynamic-partial-rate", &g.MaxSourceDynamicPartialRate, profileConfig.MaxSourceDynamicPartialRate)
	apply("max-subagent-error-rate", &g.MaxSubagentErrorRate, profileConfig.MaxSubagentErrorRate)
	apply("max-tool-error-rate", &g.MaxToolErrorRate, profileConfig.MaxToolErrorRate)
	apply("max-tool-context-truncation-rate", &g.MaxToolContextTruncationRate, profileConfig.MaxToolContextTruncationRate)
	apply("max-tool-result-truncation-rate", &g.MaxToolResultTruncationRate, profileConfig.MaxToolResultTruncationRate)
	apply("max-avg-runtime-errors", &g.MaxAvgRuntimeErrors, profileConfig.MaxAvgRuntimeErrors)
	apply("max-avg-context-compactions", &g.MaxAvgContextCompactions, profileConfig.MaxAvgContextCompactions)
	apply("max-avg-reactive-context-compactions", &g.MaxAvgReactiveCompactions, profileConfig.MaxAvgReactiveCompactions)
	apply("max-avg-total-tokens", &g.MaxAvgTotalTokens, profileConfig.MaxAvgTotalTokens)
	return nil
}

func qualityGateProfileConfig(profile string) (qualityGateConfig, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	for _, def := range qualityGateProfileDefinitions() {
		if def.Name == profile {
			return def.Gates, nil
		}
	}
	return qualityGateConfig{}, fmt.Errorf("--quality-profile must be one of: %s", strings.Join(qualityGateProfileNames(), ", "))
}

func qualityGateProfileNames() []string {
	defs := qualityGateProfileDefinitions()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	sort.Strings(names)
	return names
}

func float64Ptr(value float64) *float64 {
	return &value
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
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
	RuntimeErrors              int
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
	ContextCompactionExamples  []agenteval.ContextCompaction
	LoopGuardInterventions     int
	ForcedNoTools              int
	SourceAccessResults        int
	SourceAccessVerified       int
	SourceAccessDiscoveryOnly  int
	SourceAccessNetwork        int
	SourceAccessDynamicPartial int
	SourceAccessExamples       []agenteval.SourceAccessExample
	MemoryUpdates              int
	MemoryUpdateAdd            int
	MemoryUpdateReplace        int
	MemoryUpdateRemove         int
	SessionSearchCalls         int
	SessionSearchResults       int
	SessionSearchContextHits   int
	SessionSearchMatchedTerms  int
	ToolDurationMS             int64
	ToolContextTruncated       int
	ToolContextOmittedBytes    int
	ToolArgsTruncated          int
	ToolArgsOmittedBytes       int
	ToolResultsTruncated       int
	ToolResultsOmittedBytes    int
	ToolResultArtifacts        int
	ToolTruncationExamples     []agenteval.ToolTruncationExample
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
	DebugBriefByTag            map[string]int
	ExpectationScenarios       int
	ExpectationSuites          map[string]int
	ExpectationCapabilities    map[string]int
	ExpectationCapabilityPass  map[string]int
	ExpectationCapabilityFail  map[string]int
	ExpectationRequiredTools   map[string]int
	ExpectationSourceAccess    map[string]int
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
		s.RuntimeErrors += v
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
	s.ContextCompactionExamples = appendContextCompactionExamples(s.ContextCompactionExamples, res.ContextCompactions.Examples, batchSummaryExamplesPerKind)
	s.LoopGuardInterventions += res.ToolStats.LoopGuardInterventions
	s.ForcedNoTools += res.ToolStats.ForcedNoTools
	s.SourceAccessResults += res.ToolStats.SourceAccessResults
	s.SourceAccessVerified += res.ToolStats.SourceAccessVerified
	s.SourceAccessDiscoveryOnly += res.ToolStats.SourceAccessDiscoveryOnly
	s.SourceAccessNetwork += res.ToolStats.SourceAccessNetwork
	s.SourceAccessDynamicPartial += res.ToolStats.SourceAccessDynamicPartial
	s.SourceAccessExamples = appendSourceAccessExamples(s.SourceAccessExamples, res.SourceAccessExamples, batchSummaryExamplesPerKind)
	s.MemoryUpdates += res.ToolStats.MemoryUpdates
	s.MemoryUpdateAdd += res.ToolStats.MemoryUpdateAdd
	s.MemoryUpdateReplace += res.ToolStats.MemoryUpdateReplace
	s.MemoryUpdateRemove += res.ToolStats.MemoryUpdateRemove
	s.SessionSearchCalls += res.ToolStats.SessionSearchCalls
	s.SessionSearchResults += res.ToolStats.SessionSearchResults
	s.SessionSearchContextHits += res.ToolStats.SessionSearchContextHits
	s.SessionSearchMatchedTerms += res.ToolStats.SessionSearchMatchedTerms
	s.ToolDurationMS += res.ToolStats.ToolDurationMS
	s.ToolContextTruncated += res.ToolStats.ToolContextTruncated
	s.ToolContextOmittedBytes += res.ToolStats.ToolContextOmittedBytes
	s.ToolArgsTruncated += res.ToolTruncation.ArgsTruncated
	s.ToolArgsOmittedBytes += res.ToolTruncation.ArgsOmittedBytes
	s.ToolResultsTruncated += res.ToolTruncation.ResultsTruncated
	s.ToolResultsOmittedBytes += res.ToolTruncation.ResultsOmittedBytes
	s.ToolResultArtifacts += res.ToolTruncation.ResultArtifacts
	s.ToolTruncationExamples = appendToolTruncationExamples(s.ToolTruncationExamples, res.ToolTruncationExamples, batchSummaryExamplesPerKind)
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
	if brief := agenteval.BuildDebugBrief(res); brief != nil {
		if s.DebugBriefByTag == nil {
			s.DebugBriefByTag = map[string]int{}
		}
		for _, tag := range brief.Tags {
			s.DebugBriefByTag[tag]++
		}
	}
	if res.Expectations != nil {
		s.addExpectations(*res.Expectations, res.OK)
	}
}

func (s *batchSummary) addExpectations(exp agenteval.DebugScenarioExpectations, ok bool) {
	s.ExpectationScenarios++
	addCountMapValues(&s.ExpectationSuites, exp.Suites)
	addCountMapValues(&s.ExpectationRequiredTools, expectationRequiredToolNames(exp))
	for _, req := range exp.RequiredSourceAccess {
		status := strings.TrimSpace(req.Status)
		if status == "" {
			status = "any"
		}
		addCountMapValue(&s.ExpectationSourceAccess, status)
	}
	caps := expectationCapabilitySet(exp)
	keys := make([]string, 0, len(caps))
	for cap := range caps {
		keys = append(keys, cap)
	}
	sort.Strings(keys)
	addCountMapValues(&s.ExpectationCapabilities, keys)
	if ok {
		addCountMapValues(&s.ExpectationCapabilityPass, keys)
	} else {
		addCountMapValues(&s.ExpectationCapabilityFail, keys)
	}
}

func expectationRequiredToolNames(exp agenteval.DebugScenarioExpectations) []string {
	tools := map[string]bool{}
	add := func(tool string) {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			tools[tool] = true
		}
	}
	for _, tool := range exp.RequiredTools {
		add(tool)
	}
	for tool := range exp.RequiredToolCounts {
		add(tool)
	}
	for tool := range exp.RequiredToolResultText {
		add(tool)
	}
	for _, req := range exp.RequiredSourceAccess {
		add(req.Tool)
	}
	for _, req := range exp.RequiredToolArgContains {
		add(req.Tool)
	}
	for _, req := range exp.RequiredToolOrder {
		add(req.Earlier)
		add(req.Later)
	}
	for _, req := range exp.RequiredCommandBeforeTool {
		add(req.Tool)
	}
	for _, req := range exp.RequiredCommandAfterTool {
		add(req.Tool)
	}
	for _, tool := range exp.RequiredTruncatedResults {
		add(tool)
	}
	for _, tool := range exp.RequiredResultArtifacts {
		add(tool)
	}
	for tool := range exp.MaxSuccessfulToolCallsByTool {
		add(tool)
	}
	out := make([]string, 0, len(tools))
	for tool := range tools {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func addCountMapValues(dst *map[string]int, values []string) {
	for _, value := range values {
		addCountMapValue(dst, value)
	}
}

func addCountMapValue(dst *map[string]int, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if *dst == nil {
		*dst = map[string]int{}
	}
	(*dst)[value]++
}

func expectationCapabilitySet(exp agenteval.DebugScenarioExpectations) map[string]bool {
	caps := map[string]bool{}
	if strings.TrimSpace(exp.SessionID) != "" {
		caps["session"] = true
	}
	if exp.ExecutePlan || exp.RequireNoPlanErrors {
		caps["plan"] = true
	}
	if exp.EnableMemory {
		caps["memory"] = true
	}
	if exp.VerifyCommand != "" {
		caps["verifier"] = true
	}
	if len(exp.RequiredSourceAccess) > 0 {
		caps["source_access"] = true
	}
	for _, req := range exp.RequiredSourceAccess {
		addExpectationToolCapabilities(caps, req.Tool)
	}
	if exp.RequiredContextCompactions > 0 ||
		exp.RequiredReactiveCompactions > 0 ||
		exp.RequiredCompactionRemovedMsgs > 0 ||
		len(exp.RequiredContextSummaryText) > 0 {
		caps["context_compaction"] = true
	}
	if len(exp.RequiredFocusedTaskCounts) > 0 ||
		len(exp.RequiredSubagentModeCounts) > 0 ||
		exp.RequireNoDelegationErrors {
		caps["delegation"] = true
	}
	for _, tool := range expectationRequiredToolNames(exp) {
		addExpectationToolCapabilities(caps, tool)
	}
	for stat := range exp.RequiredToolStatsAtLeast {
		addExpectationStatCapabilities(caps, stat)
	}
	for range exp.RequiredCommandBeforeTool {
		caps["workspace"] = true
	}
	for range exp.RequiredCommandAfterTool {
		caps["workspace"] = true
	}
	if len(exp.RequiredCommands) > 0 || len(exp.RequiredCommandCounts) > 0 {
		caps["workspace"] = true
	}
	return caps
}

func addExpectationToolCapabilities(caps map[string]bool, tool string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	switch {
	case tool == agent.MemoryToolName:
		caps["memory"] = true
	case tool == agent.SessionSearchToolName:
		caps["session_search"] = true
	case tool == agent.PlanToolName:
		caps["plan"] = true
	case tool == agent.SubagentToolName || tool == agent.FocusedTaskToolName:
		caps["delegation"] = true
	case tool == "web_fetch" || tool == "web_search":
		caps["web"] = true
		caps["source_access"] = true
	case strings.HasPrefix(tool, "browser_"):
		caps["browser"] = true
		caps["source_access"] = true
	case tool == "mcp":
		caps["mcp"] = true
	default:
		if isWorkspaceTool(tool) {
			caps["workspace"] = true
		}
	}
}

func addExpectationStatCapabilities(caps map[string]bool, stat string) {
	switch {
	case strings.HasPrefix(stat, "memory_"):
		caps["memory"] = true
	case strings.HasPrefix(stat, "session_search_"):
		caps["session_search"] = true
	case strings.HasPrefix(stat, "source_access_"):
		caps["source_access"] = true
	case strings.Contains(stat, "focused_task") || strings.Contains(stat, "subagent"):
		caps["delegation"] = true
	case strings.Contains(stat, "context_compaction"):
		caps["context_compaction"] = true
	}
}

func isWorkspaceTool(tool string) bool {
	for _, name := range evalWorkspaceToolNames() {
		if tool == name {
			return true
		}
	}
	return false
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
	fmt.Fprintf(w, " rates=pass:%s,completed:%s,memory_update:%s,runtime_surface:%s,tool_error:%s,focused_task_error:%s,subagent_error:%s,plan_error:%s,repair_success:%s,verifier_pass:%s,evidence_verified:%s,source_network:%s,source_discovery:%s,source_dynamic_partial:%s avg_tokens=%.1f/%.1f",
		formatPercent(batchRatio(s.Passed, s.Total)),
		formatPercent(batchRatio(s.EndCompleted, s.Total)),
		formatPercent(batchRatio(s.MemoryUpdates, s.Total)),
		formatPercent(batchRatio(s.RuntimeSurfaceScenarios, s.Total)),
		formatOptionalPercent(batchOptionalRatio(s.ToolErrors, s.ToolCalls)),
		formatOptionalPercent(batchOptionalRatio(s.FocusedTaskErrors, s.FocusedTaskCalls)),
		formatOptionalPercent(batchOptionalRatio(s.SubagentErrors, s.SubagentCalls)),
		formatOptionalPercent(batchOptionalRatio(s.PlanErrors, s.PlanCalls)),
		formatOptionalPercent(batchOptionalRatio(s.ToolRepairSucceeded, s.ToolRepairCalls)),
		formatOptionalPercent(batchOptionalRatio(s.VerifierPassed, s.VerifierRuns)),
		formatOptionalPercent(batchOptionalRatio(s.SourceAccessVerified, s.SourceAccessResults)),
		formatOptionalPercent(batchOptionalRatio(s.SourceAccessNetwork, s.SourceAccessResults)),
		formatOptionalPercent(batchOptionalRatio(s.SourceAccessDiscoveryOnly, s.SourceAccessResults)),
		formatOptionalPercent(batchOptionalRatio(s.SourceAccessDynamicPartial, s.SourceAccessResults)),
		batchAverage(s.InputTokens, s.Total),
		batchAverage(s.OutputTokens, s.Total),
	)
	fmt.Fprintf(w, " context_pressure=avg_compactions:%.2f,avg_reactive:%.2f,avg_removed:%.1f,tool_ctx_trunc:%s",
		batchAverage(s.ContextCompactions, s.Total),
		batchAverage(s.ContextCompactionsReactive, s.Total),
		batchAverage(s.ContextCompactionRemoved, s.Total),
		formatOptionalPercent(batchOptionalRatio(s.ToolContextTruncated, s.ToolCalls)),
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
	if hasBatchSessionSearchStats(s) {
		fmt.Fprintf(w, " session_search=calls:%d,results:%d,context:%d,terms:%d",
			s.SessionSearchCalls,
			s.SessionSearchResults,
			s.SessionSearchContextHits,
			s.SessionSearchMatchedTerms,
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
	if len(s.DebugBriefByTag) > 0 {
		fmt.Fprintf(w, " debug_brief=%s", formatStringIntCounts(s.DebugBriefByTag))
	}
	if s.ExpectationScenarios > 0 {
		fmt.Fprintf(w, " expectations=scenarios:%d", s.ExpectationScenarios)
		if len(s.ExpectationCapabilities) > 0 {
			fmt.Fprintf(w, " expectation_capabilities=%s", formatStringIntCounts(s.ExpectationCapabilities))
			fmt.Fprintf(w, " expectation_capability_pass=%s", formatPassTotalCounts(s.ExpectationCapabilityPass, s.ExpectationCapabilities))
		}
		if len(s.ExpectationRequiredTools) > 0 {
			fmt.Fprintf(w, " expectation_tools=%s", formatStringIntCounts(s.ExpectationRequiredTools))
		}
		if len(s.ExpectationSourceAccess) > 0 {
			fmt.Fprintf(w, " expectation_source_access=%s", formatStringIntCounts(s.ExpectationSourceAccess))
		}
		if len(s.ExpectationSuites) > 0 {
			fmt.Fprintf(w, " expectation_suites=%s", formatStringIntCounts(s.ExpectationSuites))
		}
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

func printBatchQualityGates(w io.Writer, meta evalJSONLMetadata, failures []string) {
	if !hasQualityGateThresholds(meta) {
		return
	}
	status := "passed"
	if len(failures) > 0 {
		status = "failed"
	}
	fmt.Fprintf(w, "QUALITY_GATES status=%s", status)
	if strings.TrimSpace(meta.QualityProfile) != "" {
		fmt.Fprintf(w, " profile=%s", strings.TrimSpace(meta.QualityProfile))
	}
	if len(failures) > 0 {
		fmt.Fprintf(w, " failures=%d", len(failures))
	}
	fmt.Fprintln(w)
	for _, failure := range failures {
		fmt.Fprintf(w, "  gate_failure: %s\n", failure)
	}
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

func formatDebugBriefTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out := append([]string(nil), tags...)
	sort.Strings(out)
	return strings.Join(out, ",")
}

func batchRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func batchOptionalRatio(numerator, denominator int) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := batchRatio(numerator, denominator)
	return &value
}

func batchAverage(total, count int) float64 {
	if count <= 0 {
		return 0
	}
	return float64(total) / float64(count)
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value*100)
}

func formatOptionalPercent(value *float64) string {
	if value == nil {
		return "n/a"
	}
	return formatPercent(*value)
}

func validateQualityGateConfig(g qualityGateConfig) error {
	for _, gate := range []struct {
		name  string
		value *float64
		rate  bool
	}{
		{"--min-pass-rate", g.MinPassRate, true},
		{"--min-completion-rate", g.MinCompletionRate, true},
		{"--min-memory-update-rate", g.MinMemoryUpdateRate, true},
		{"--min-runtime-surface-rate", g.MinRuntimeSurfaceRate, true},
		{"--min-source-network-rate", g.MinSourceNetworkRate, true},
		{"--min-source-access-verified-rate", g.MinSourceAccessVerifiedRate, true},
		{"--min-expectation-capability-pass-rate", g.MinExpectationCapabilityPassRate, true},
		{"--min-session-search-context-hit-rate", g.MinSessionSearchContextHitRate, true},
		{"--min-tool-repair-success-rate", g.MinToolRepairSuccessRate, true},
		{"--min-verifier-pass-rate", g.MinVerifierPassRate, true},
		{"--max-focused-task-error-rate", g.MaxFocusedTaskErrorRate, true},
		{"--max-forced-no-tools-rate", g.MaxForcedNoToolsRate, true},
		{"--max-loop-guard-intervention-rate", g.MaxLoopGuardInterventionRate, true},
		{"--max-plan-error-rate", g.MaxPlanErrorRate, true},
		{"--max-source-discovery-only-rate", g.MaxSourceDiscoveryOnlyRate, true},
		{"--max-source-dynamic-partial-rate", g.MaxSourceDynamicPartialRate, true},
		{"--max-subagent-error-rate", g.MaxSubagentErrorRate, true},
		{"--max-tool-error-rate", g.MaxToolErrorRate, true},
		{"--max-tool-context-truncation-rate", g.MaxToolContextTruncationRate, true},
		{"--max-tool-result-truncation-rate", g.MaxToolResultTruncationRate, true},
		{"--max-avg-runtime-errors", g.MaxAvgRuntimeErrors, false},
		{"--max-avg-context-compactions", g.MaxAvgContextCompactions, false},
		{"--max-avg-reactive-context-compactions", g.MaxAvgReactiveCompactions, false},
		{"--max-avg-total-tokens", g.MaxAvgTotalTokens, false},
	} {
		if gate.value == nil {
			continue
		}
		if math.IsNaN(*gate.value) || math.IsInf(*gate.value, 0) {
			return fmt.Errorf("%s must be finite", gate.name)
		}
		if *gate.value < 0 {
			if *gate.value == -1 {
				continue
			}
			return fmt.Errorf("%s must be disabled with -1 or set to a non-negative value", gate.name)
		}
		if gate.rate && *gate.value > 1 {
			return fmt.Errorf("%s must be between 0 and 1", gate.name)
		}
	}
	return nil
}

func qualityGateFailures(s batchSummary, g qualityGateConfig) []string {
	var failures []string
	checkMin := func(name string, actual float64, threshold *float64, available bool) {
		if threshold == nil || *threshold < 0 {
			return
		}
		if !available {
			failures = append(failures, fmt.Sprintf("%s unavailable, want >= %s", name, formatGateFloat(*threshold)))
			return
		}
		if actual < *threshold {
			failures = append(failures, fmt.Sprintf("%s %s < min %s", name, formatGateFloat(actual), formatGateFloat(*threshold)))
		}
	}
	checkMax := func(name string, actual float64, threshold *float64, available bool) {
		if threshold == nil || *threshold < 0 {
			return
		}
		if !available {
			return
		}
		if actual > *threshold {
			failures = append(failures, fmt.Sprintf("%s %s > max %s", name, formatGateFloat(actual), formatGateFloat(*threshold)))
		}
	}
	checkMin("pass_rate", batchRatio(s.Passed, s.Total), g.MinPassRate, s.Total > 0)
	checkMin("completion_rate", batchRatio(s.EndCompleted, s.Total), g.MinCompletionRate, s.Total > 0)
	checkMin("memory_update_rate", batchRatio(s.MemoryUpdates, s.Total), g.MinMemoryUpdateRate, s.Total > 0)
	checkMin("runtime_surface_rate", batchRatio(s.RuntimeSurfaceScenarios, s.Total), g.MinRuntimeSurfaceRate, s.Total > 0)
	checkMin("source_network_rate", batchRatio(s.SourceAccessNetwork, s.SourceAccessResults), g.MinSourceNetworkRate, s.SourceAccessResults > 0)
	checkMin("source_access_verified_rate", batchRatio(s.SourceAccessVerified, s.SourceAccessResults), g.MinSourceAccessVerifiedRate, s.SourceAccessResults > 0)
	expectationCapabilityPassed, expectationCapabilityTotal := expectationCapabilityPassTotals(s)
	checkMin("expectation_capability_pass_rate", batchRatio(expectationCapabilityPassed, expectationCapabilityTotal), g.MinExpectationCapabilityPassRate, expectationCapabilityTotal > 0)
	checkMin("session_search_context_hit_rate", batchRatio(s.SessionSearchContextHits, s.SessionSearchResults), g.MinSessionSearchContextHitRate, s.SessionSearchResults > 0)
	checkMin("tool_repair_success_rate", batchRatio(s.ToolRepairSucceeded, s.ToolRepairCalls), g.MinToolRepairSuccessRate, s.ToolRepairCalls > 0)
	checkMin("verifier_pass_rate", batchRatio(s.VerifierPassed, s.VerifierRuns), g.MinVerifierPassRate, s.VerifierRuns > 0)
	checkMax("focused_task_error_rate", batchRatio(s.FocusedTaskErrors, s.FocusedTaskCalls), g.MaxFocusedTaskErrorRate, s.FocusedTaskCalls > 0)
	checkMax("forced_no_tools_rate", batchRatio(s.ForcedNoTools, s.ToolCalls), g.MaxForcedNoToolsRate, s.ToolCalls > 0)
	checkMax("loop_guard_intervention_rate", batchRatio(s.LoopGuardInterventions, s.ToolCalls), g.MaxLoopGuardInterventionRate, s.ToolCalls > 0)
	checkMax("plan_error_rate", batchRatio(s.PlanErrors, s.PlanCalls), g.MaxPlanErrorRate, s.PlanCalls > 0)
	checkMax("source_discovery_only_rate", batchRatio(s.SourceAccessDiscoveryOnly, s.SourceAccessResults), g.MaxSourceDiscoveryOnlyRate, s.SourceAccessResults > 0)
	checkMax("source_dynamic_partial_rate", batchRatio(s.SourceAccessDynamicPartial, s.SourceAccessResults), g.MaxSourceDynamicPartialRate, s.SourceAccessResults > 0)
	checkMax("subagent_error_rate", batchRatio(s.SubagentErrors, s.SubagentCalls), g.MaxSubagentErrorRate, s.SubagentCalls > 0)
	checkMax("tool_error_rate", batchRatio(s.ToolErrors, s.ToolCalls), g.MaxToolErrorRate, s.ToolCalls > 0)
	checkMax("tool_context_truncation_rate", batchRatio(s.ToolContextTruncated, s.ToolCalls), g.MaxToolContextTruncationRate, s.ToolCalls > 0)
	checkMax("tool_result_truncation_rate", batchRatio(s.ToolResultsTruncated, s.ToolCalls), g.MaxToolResultTruncationRate, s.ToolCalls > 0)
	checkMax("avg_runtime_errors", batchAverage(s.RuntimeErrors, s.Total), g.MaxAvgRuntimeErrors, s.Total > 0)
	checkMax("avg_context_compactions", batchAverage(s.ContextCompactions, s.Total), g.MaxAvgContextCompactions, s.Total > 0)
	checkMax("avg_reactive_context_compactions", batchAverage(s.ContextCompactionsReactive, s.Total), g.MaxAvgReactiveCompactions, s.Total > 0)
	checkMax("avg_total_tokens", batchAverage(s.InputTokens+s.OutputTokens, s.Total), g.MaxAvgTotalTokens, s.Total > 0)
	sort.Strings(failures)
	return failures
}

func formatGateFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
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

func formatPassTotalCounts(passed, total map[string]int) string {
	if len(total) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(total))
	for key := range total {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d/%d", key, passed[key], total[key]))
	}
	return strings.Join(parts, ",")
}

type evalJSONLMetadata struct {
	SchemaVersion                    int      `json:"schema_version"`
	Suite                            string   `json:"suite,omitempty"`
	Model                            string   `json:"model,omitempty"`
	ProviderLabel                    string   `json:"provider_label,omitempty"`
	Executor                         string   `json:"executor"`
	Temperature                      string   `json:"temperature,omitempty"`
	TopP                             string   `json:"top_p,omitempty"`
	MaxTokens                        string   `json:"max_tokens,omitempty"`
	Seed                             string   `json:"seed,omitempty"`
	RuntimeEvalMode                  bool     `json:"runtime_eval_mode,omitempty"`
	RuntimeTools                     string   `json:"runtime_tools,omitempty"`
	RuntimeAllTools                  bool     `json:"runtime_all_tools,omitempty"`
	RuntimeMemory                    bool     `json:"runtime_memory,omitempty"`
	RuntimeWeb                       bool     `json:"runtime_web,omitempty"`
	RuntimeBrowser                   bool     `json:"runtime_browser,omitempty"`
	TraceDeltas                      bool     `json:"trace_deltas,omitempty"`
	RuntimeMCP                       bool     `json:"runtime_mcp,omitempty"`
	TimeoutMS                        int64    `json:"timeout_ms"`
	QualityProfile                   string   `json:"quality_profile,omitempty"`
	MinPassRate                      *float64 `json:"min_pass_rate,omitempty"`
	MinCompletionRate                *float64 `json:"min_completion_rate,omitempty"`
	MinMemoryUpdateRate              *float64 `json:"min_memory_update_rate,omitempty"`
	MinRuntimeSurfaceRate            *float64 `json:"min_runtime_surface_rate,omitempty"`
	MinSourceNetworkRate             *float64 `json:"min_source_network_rate,omitempty"`
	MinSourceAccessVerifiedRate      *float64 `json:"min_source_access_verified_rate,omitempty"`
	MinExpectationCapabilityPassRate *float64 `json:"min_expectation_capability_pass_rate,omitempty"`
	MinSessionSearchContextHitRate   *float64 `json:"min_session_search_context_hit_rate,omitempty"`
	MinToolRepairSuccessRate         *float64 `json:"min_tool_repair_success_rate,omitempty"`
	MinVerifierPassRate              *float64 `json:"min_verifier_pass_rate,omitempty"`
	MaxFocusedTaskErrorRate          *float64 `json:"max_focused_task_error_rate,omitempty"`
	MaxForcedNoToolsRate             *float64 `json:"max_forced_no_tools_rate,omitempty"`
	MaxLoopGuardInterventionRate     *float64 `json:"max_loop_guard_intervention_rate,omitempty"`
	MaxPlanErrorRate                 *float64 `json:"max_plan_error_rate,omitempty"`
	MaxSourceDiscoveryOnlyRate       *float64 `json:"max_source_discovery_only_rate,omitempty"`
	MaxSourceDynamicPartialRate      *float64 `json:"max_source_dynamic_partial_rate,omitempty"`
	MaxSubagentErrorRate             *float64 `json:"max_subagent_error_rate,omitempty"`
	MaxToolErrorRate                 *float64 `json:"max_tool_error_rate,omitempty"`
	MaxToolContextTruncationRate     *float64 `json:"max_tool_context_truncation_rate,omitempty"`
	MaxToolResultTruncationRate      *float64 `json:"max_tool_result_truncation_rate,omitempty"`
	MaxAvgRuntimeErrors              *float64 `json:"max_avg_runtime_errors,omitempty"`
	MaxAvgContextCompactions         *float64 `json:"max_avg_context_compactions,omitempty"`
	MaxAvgReactiveCompactions        *float64 `json:"max_avg_reactive_context_compactions,omitempty"`
	MaxAvgTotalTokens                *float64 `json:"max_avg_total_tokens,omitempty"`
}

func evalJSONLMetadataFromConfig(suite, model, providerLabel, executor, temperature, topP, maxTokens, seed string, runtimeEvalMode bool, runtimeTools string, runtimeAllTools, runtimeMemory, runtimeWeb, runtimeBrowser, traceDeltas bool, runtimeMCPConfig string, timeout time.Duration, qualityProfile string, gates qualityGateConfig) evalJSONLMetadata {
	model = strings.TrimSpace(model)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("AFFENTCTL_MODEL"))
	}
	providerLabel = strings.TrimSpace(providerLabel)
	if providerLabel == "" {
		providerLabel = strings.TrimSpace(os.Getenv("AFFENTEVAL_PROVIDER_LABEL"))
	}
	return evalJSONLMetadata{
		SchemaVersion:                    evalJSONLSchemaVersion,
		Suite:                            strings.TrimSpace(suite),
		Model:                            model,
		ProviderLabel:                    providerLabel,
		Executor:                         normalizedEvalExecutor(executor),
		Temperature:                      strings.TrimSpace(temperature),
		TopP:                             strings.TrimSpace(topP),
		MaxTokens:                        strings.TrimSpace(maxTokens),
		Seed:                             strings.TrimSpace(seed),
		RuntimeEvalMode:                  runtimeEvalMode,
		RuntimeTools:                     strings.TrimSpace(runtimeTools),
		RuntimeAllTools:                  runtimeAllTools,
		RuntimeMemory:                    runtimeMemory,
		RuntimeWeb:                       runtimeWeb,
		RuntimeBrowser:                   runtimeBrowser,
		TraceDeltas:                      traceDeltas,
		RuntimeMCP:                       strings.TrimSpace(runtimeMCPConfig) != "",
		TimeoutMS:                        timeout.Milliseconds(),
		QualityProfile:                   strings.ToLower(strings.TrimSpace(qualityProfile)),
		MinPassRate:                      enabledQualityGateValue(gates.MinPassRate),
		MinCompletionRate:                enabledQualityGateValue(gates.MinCompletionRate),
		MinMemoryUpdateRate:              enabledQualityGateValue(gates.MinMemoryUpdateRate),
		MinRuntimeSurfaceRate:            enabledQualityGateValue(gates.MinRuntimeSurfaceRate),
		MinSourceNetworkRate:             enabledQualityGateValue(gates.MinSourceNetworkRate),
		MinSourceAccessVerifiedRate:      enabledQualityGateValue(gates.MinSourceAccessVerifiedRate),
		MinExpectationCapabilityPassRate: enabledQualityGateValue(gates.MinExpectationCapabilityPassRate),
		MinSessionSearchContextHitRate:   enabledQualityGateValue(gates.MinSessionSearchContextHitRate),
		MinToolRepairSuccessRate:         enabledQualityGateValue(gates.MinToolRepairSuccessRate),
		MinVerifierPassRate:              enabledQualityGateValue(gates.MinVerifierPassRate),
		MaxFocusedTaskErrorRate:          enabledQualityGateValue(gates.MaxFocusedTaskErrorRate),
		MaxForcedNoToolsRate:             enabledQualityGateValue(gates.MaxForcedNoToolsRate),
		MaxLoopGuardInterventionRate:     enabledQualityGateValue(gates.MaxLoopGuardInterventionRate),
		MaxPlanErrorRate:                 enabledQualityGateValue(gates.MaxPlanErrorRate),
		MaxSourceDiscoveryOnlyRate:       enabledQualityGateValue(gates.MaxSourceDiscoveryOnlyRate),
		MaxSourceDynamicPartialRate:      enabledQualityGateValue(gates.MaxSourceDynamicPartialRate),
		MaxSubagentErrorRate:             enabledQualityGateValue(gates.MaxSubagentErrorRate),
		MaxToolErrorRate:                 enabledQualityGateValue(gates.MaxToolErrorRate),
		MaxToolContextTruncationRate:     enabledQualityGateValue(gates.MaxToolContextTruncationRate),
		MaxToolResultTruncationRate:      enabledQualityGateValue(gates.MaxToolResultTruncationRate),
		MaxAvgRuntimeErrors:              enabledQualityGateValue(gates.MaxAvgRuntimeErrors),
		MaxAvgContextCompactions:         enabledQualityGateValue(gates.MaxAvgContextCompactions),
		MaxAvgReactiveCompactions:        enabledQualityGateValue(gates.MaxAvgReactiveCompactions),
		MaxAvgTotalTokens:                enabledQualityGateValue(gates.MaxAvgTotalTokens),
	}
}

func enabledQualityGateValue(value *float64) *float64 {
	if value == nil || *value < 0 {
		return nil
	}
	clone := *value
	return &clone
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
	Expectations               *agenteval.DebugScenarioExpectations       `json:"expectations,omitempty"`
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
	MemoryUpdateExamples       []agenteval.MemoryUpdateExample            `json:"memory_update_examples,omitempty"`
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
	ContextCompactionExamples  []agenteval.ContextCompaction              `json:"context_compaction_examples,omitempty"`
	LoopGuardInterventions     int                                        `json:"loop_guard_interventions"`
	ForcedNoTools              int                                        `json:"forced_no_tools"`
	SourceAccessResults        int                                        `json:"source_access_results"`
	SourceAccessVerified       int                                        `json:"source_access_verified"`
	SourceAccessDiscoveryOnly  int                                        `json:"source_access_discovery_only"`
	SourceAccessNetwork        int                                        `json:"source_access_network"`
	SourceAccessDynamicPartial int                                        `json:"source_access_dynamic_partial"`
	SourceAccessExamples       []agenteval.SourceAccessExample            `json:"source_access_examples,omitempty"`
	MemoryUpdates              int                                        `json:"memory_updates"`
	MemoryUpdateAdd            int                                        `json:"memory_update_add"`
	MemoryUpdateReplace        int                                        `json:"memory_update_replace"`
	MemoryUpdateRemove         int                                        `json:"memory_update_remove"`
	SessionSearchCalls         int                                        `json:"session_search_calls,omitempty"`
	SessionSearchResults       int                                        `json:"session_search_results,omitempty"`
	SessionSearchContextHits   int                                        `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms  int                                        `json:"session_search_matched_terms,omitempty"`
	ToolDurationMS             int64                                      `json:"tool_duration_ms"`
	ToolContextTruncated       int                                        `json:"tool_context_truncated"`
	ToolContextOmittedBytes    int                                        `json:"tool_context_omitted_bytes"`
	ToolArgsTruncated          int                                        `json:"tool_args_truncated"`
	ToolArgsOmittedBytes       int                                        `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated       int                                        `json:"tool_results_truncated"`
	ToolResultsOmittedBytes    int                                        `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts        int                                        `json:"tool_result_artifacts"`
	ToolTruncationExamples     []agenteval.ToolTruncationExample          `json:"tool_truncation_examples,omitempty"`
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
	DebugBrief                 *agenteval.DebugBrief                      `json:"debug_brief,omitempty"`

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
	Type                        string                                     `json:"type"`
	Scenarios                   int                                        `json:"scenarios"`
	Passed                      int                                        `json:"passed"`
	Failed                      int                                        `json:"failed"`
	PassRate                    float64                                    `json:"pass_rate"`
	CompletionRate              float64                                    `json:"completion_rate"`
	MemoryUpdateRate            float64                                    `json:"memory_update_rate"`
	ToolErrorRate               *float64                                   `json:"tool_error_rate,omitempty"`
	FocusedTaskErrorRate        *float64                                   `json:"focused_task_error_rate,omitempty"`
	SubagentErrorRate           *float64                                   `json:"subagent_error_rate,omitempty"`
	ForcedNoToolsRate           *float64                                   `json:"forced_no_tools_rate,omitempty"`
	LoopGuardInterventionRate   *float64                                   `json:"loop_guard_intervention_rate,omitempty"`
	PlanErrorRate               *float64                                   `json:"plan_error_rate,omitempty"`
	ToolRepairSuccessRate       *float64                                   `json:"tool_repair_success_rate,omitempty"`
	VerifierPassRate            *float64                                   `json:"verifier_pass_rate,omitempty"`
	SourceAccessVerifiedRate    *float64                                   `json:"source_access_verified_rate,omitempty"`
	SourceNetworkRate           *float64                                   `json:"source_network_rate,omitempty"`
	SourceDiscoveryOnlyRate     *float64                                   `json:"source_discovery_only_rate,omitempty"`
	SourceDynamicPartialRate    *float64                                   `json:"source_dynamic_partial_rate,omitempty"`
	SessionSearchContextHitRate *float64                                   `json:"session_search_context_hit_rate,omitempty"`
	AvgRuntimeErrors            float64                                    `json:"avg_runtime_errors"`
	AvgContextCompactions       float64                                    `json:"avg_context_compactions"`
	AvgReactiveCompactions      float64                                    `json:"avg_reactive_context_compactions"`
	AvgContextRemovedMessages   float64                                    `json:"avg_context_removed_messages"`
	ToolContextTruncationRate   *float64                                   `json:"tool_context_truncation_rate,omitempty"`
	ToolResultTruncationRate    *float64                                   `json:"tool_result_truncation_rate,omitempty"`
	DurationMS                  int64                                      `json:"duration_ms"`
	ToolCalls                   int                                        `json:"tool_calls"`
	ToolErrors                  int                                        `json:"tool_errors"`
	ToolRepaired                int                                        `json:"tool_repaired"`
	ToolNameCanonicalized       int                                        `json:"tool_name_canonicalized"`
	ToolRepairCalls             int                                        `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded         int                                        `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed            int                                        `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes             int                                        `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind            map[string]int                             `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind           map[string]int                             `json:"tool_failure_by_kind,omitempty"`
	ToolFailureExamples         map[string][]agenteval.ToolFailureExample  `json:"tool_failure_examples,omitempty"`
	RuntimeErrorByKind          map[string]int                             `json:"runtime_error_by_kind,omitempty"`
	RuntimeErrorExamples        map[string][]agenteval.RuntimeErrorExample `json:"runtime_error_examples,omitempty"`
	RuntimeSurfaceRate          float64                                    `json:"runtime_surface_rate"`
	RuntimeSurfaceScenarios     int                                        `json:"runtime_surface_scenarios,omitempty"`
	RuntimeSurfaceTools         map[string]int                             `json:"runtime_surface_tools,omitempty"`
	RuntimeSurfaceCapabilities  map[string]int                             `json:"runtime_surface_capabilities,omitempty"`
	LoopDecisions               int                                        `json:"loop_decisions,omitempty"`
	LoopDecisionByKind          map[string]int                             `json:"loop_decision_by_kind,omitempty"`
	LoopDecisionByDecision      map[string]int                             `json:"loop_decision_by_decision,omitempty"`
	LoopDecisionExamples        []agenteval.LoopDecision                   `json:"loop_decision_examples,omitempty"`
	ContextCompactions          int                                        `json:"context_compactions,omitempty"`
	ContextCompactionsReactive  int                                        `json:"context_compactions_reactive,omitempty"`
	ContextCompactionRemoved    int                                        `json:"context_compaction_removed_messages,omitempty"`
	ContextCompactionSummary    int                                        `json:"context_compaction_summary_bytes,omitempty"`
	ContextCompactionExamples   []agenteval.ContextCompaction              `json:"context_compaction_examples,omitempty"`
	LoopGuardInterventions      int                                        `json:"loop_guard_interventions"`
	ForcedNoTools               int                                        `json:"forced_no_tools"`
	SourceAccessResults         int                                        `json:"source_access_results"`
	SourceAccessVerified        int                                        `json:"source_access_verified"`
	SourceAccessDiscoveryOnly   int                                        `json:"source_access_discovery_only"`
	SourceAccessNetwork         int                                        `json:"source_access_network"`
	SourceAccessDynamicPartial  int                                        `json:"source_access_dynamic_partial"`
	SourceAccessExamples        []agenteval.SourceAccessExample            `json:"source_access_examples,omitempty"`
	MemoryUpdates               int                                        `json:"memory_updates"`
	MemoryUpdateAdd             int                                        `json:"memory_update_add"`
	MemoryUpdateReplace         int                                        `json:"memory_update_replace"`
	MemoryUpdateRemove          int                                        `json:"memory_update_remove"`
	SessionSearchCalls          int                                        `json:"session_search_calls,omitempty"`
	SessionSearchResults        int                                        `json:"session_search_results,omitempty"`
	SessionSearchContextHits    int                                        `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms   int                                        `json:"session_search_matched_terms,omitempty"`
	ToolDurationMS              int64                                      `json:"tool_duration_ms"`
	ToolContextTruncated        int                                        `json:"tool_context_truncated"`
	ToolContextOmittedBytes     int                                        `json:"tool_context_omitted_bytes"`
	ToolArgsTruncated           int                                        `json:"tool_args_truncated"`
	ToolArgsOmittedBytes        int                                        `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated        int                                        `json:"tool_results_truncated"`
	ToolResultsOmittedBytes     int                                        `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts         int                                        `json:"tool_result_artifacts"`
	ToolTruncationExamples      []agenteval.ToolTruncationExample          `json:"tool_truncation_examples,omitempty"`
	VerifierRuns                int                                        `json:"verifier_runs"`
	VerifierPassed              int                                        `json:"verifier_passed"`
	VerifierFailed              int                                        `json:"verifier_failed"`
	VerifierOutputTruncated     int                                        `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes  int                                        `json:"verifier_output_omitted_bytes"`
	TraceSchemaVersions         map[int]int                                `json:"trace_schema_versions,omitempty"`
	TraceEvents                 int                                        `json:"trace_events,omitempty"`
	TraceEventTypes             map[string]int                             `json:"trace_event_types,omitempty"`
	InputTokens                 int                                        `json:"input_tokens"`
	OutputTokens                int                                        `json:"output_tokens"`
	AvgInputTokens              float64                                    `json:"avg_input_tokens"`
	AvgOutputTokens             float64                                    `json:"avg_output_tokens"`
	AvgTotalTokens              float64                                    `json:"avg_total_tokens"`
	EndCompleted                int                                        `json:"end_completed"`
	EndMaxTurns                 int                                        `json:"end_max_turns"`
	EndErrors                   int                                        `json:"end_errors"`
	EndCancelled                int                                        `json:"end_cancelled"`
	EndUnknown                  int                                        `json:"end_unknown"`
	FailureKinds                map[string]int                             `json:"failure_kinds,omitempty"`
	FailureHints                failureHintMap                             `json:"failure_hints,omitempty"`
	ToolFailureHints            failureHintMap                             `json:"tool_failure_hints,omitempty"`
	RuntimeErrorHints           failureHintMap                             `json:"runtime_error_hints,omitempty"`
	DebugBriefByTag             map[string]int                             `json:"debug_brief_by_tag,omitempty"`
	ExpectationScenarios        int                                        `json:"expectation_scenarios,omitempty"`
	ExpectationSuites           map[string]int                             `json:"expectation_suites,omitempty"`
	ExpectationCapabilities     map[string]int                             `json:"expectation_capabilities,omitempty"`
	ExpectationCapabilityPassed map[string]int                             `json:"expectation_capability_passed,omitempty"`
	ExpectationCapabilityFailed map[string]int                             `json:"expectation_capability_failed,omitempty"`
	ExpectationCapabilityRate   map[string]float64                         `json:"expectation_capability_pass_rate,omitempty"`
	ExpectationRequiredTools    map[string]int                             `json:"expectation_required_tools,omitempty"`
	ExpectationSourceAccess     map[string]int                             `json:"expectation_source_access,omitempty"`
	QualityGatesPassed          *bool                                      `json:"quality_gates_passed,omitempty"`
	QualityGateFailures         []string                                   `json:"quality_gate_failures,omitempty"`
	RemovedWorkspaces           int                                        `json:"removed_workspaces"`
	CleanupErrors               int                                        `json:"cleanup_errors"`

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
		Expectations:               res.Expectations,
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
		MemoryUpdateExamples:       cloneMemoryUpdateExamples(res.MemoryUpdateExamples),
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
		ContextCompactionExamples:  cloneContextCompactionExamples(res.ContextCompactions.Examples),
		LoopGuardInterventions:     res.ToolStats.LoopGuardInterventions,
		ForcedNoTools:              res.ToolStats.ForcedNoTools,
		SourceAccessResults:        res.ToolStats.SourceAccessResults,
		SourceAccessVerified:       res.ToolStats.SourceAccessVerified,
		SourceAccessDiscoveryOnly:  res.ToolStats.SourceAccessDiscoveryOnly,
		SourceAccessNetwork:        res.ToolStats.SourceAccessNetwork,
		SourceAccessDynamicPartial: res.ToolStats.SourceAccessDynamicPartial,
		SourceAccessExamples:       cloneSourceAccessExamples(res.SourceAccessExamples),
		MemoryUpdates:              res.ToolStats.MemoryUpdates,
		MemoryUpdateAdd:            res.ToolStats.MemoryUpdateAdd,
		MemoryUpdateReplace:        res.ToolStats.MemoryUpdateReplace,
		MemoryUpdateRemove:         res.ToolStats.MemoryUpdateRemove,
		SessionSearchCalls:         res.ToolStats.SessionSearchCalls,
		SessionSearchResults:       res.ToolStats.SessionSearchResults,
		SessionSearchContextHits:   res.ToolStats.SessionSearchContextHits,
		SessionSearchMatchedTerms:  res.ToolStats.SessionSearchMatchedTerms,
		ToolDurationMS:             res.ToolStats.ToolDurationMS,
		ToolContextTruncated:       res.ToolStats.ToolContextTruncated,
		ToolContextOmittedBytes:    res.ToolStats.ToolContextOmittedBytes,
		ToolArgsTruncated:          res.ToolTruncation.ArgsTruncated,
		ToolArgsOmittedBytes:       res.ToolTruncation.ArgsOmittedBytes,
		ToolResultsTruncated:       res.ToolTruncation.ResultsTruncated,
		ToolResultsOmittedBytes:    res.ToolTruncation.ResultsOmittedBytes,
		ToolResultArtifacts:        res.ToolTruncation.ResultArtifacts,
		ToolTruncationExamples:     cloneToolTruncationExamples(res.ToolTruncationExamples),
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
		DebugBrief:                 agenteval.BuildDebugBrief(res),
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

func printBatchSummaryJSONL(w io.Writer, meta evalJSONLMetadata, s batchSummary, gateFailures []string) {
	writeJSONLine(w, batchSummaryRecord{
		evalJSONLMetadata:           meta,
		Type:                        "summary",
		Scenarios:                   s.Total,
		Passed:                      s.Passed,
		Failed:                      s.Failed,
		PassRate:                    batchRatio(s.Passed, s.Total),
		CompletionRate:              batchRatio(s.EndCompleted, s.Total),
		MemoryUpdateRate:            batchRatio(s.MemoryUpdates, s.Total),
		ToolErrorRate:               batchOptionalRatio(s.ToolErrors, s.ToolCalls),
		FocusedTaskErrorRate:        batchOptionalRatio(s.FocusedTaskErrors, s.FocusedTaskCalls),
		SubagentErrorRate:           batchOptionalRatio(s.SubagentErrors, s.SubagentCalls),
		ForcedNoToolsRate:           batchOptionalRatio(s.ForcedNoTools, s.ToolCalls),
		LoopGuardInterventionRate:   batchOptionalRatio(s.LoopGuardInterventions, s.ToolCalls),
		PlanErrorRate:               batchOptionalRatio(s.PlanErrors, s.PlanCalls),
		ToolRepairSuccessRate:       batchOptionalRatio(s.ToolRepairSucceeded, s.ToolRepairCalls),
		VerifierPassRate:            batchOptionalRatio(s.VerifierPassed, s.VerifierRuns),
		SourceAccessVerifiedRate:    batchOptionalRatio(s.SourceAccessVerified, s.SourceAccessResults),
		SourceNetworkRate:           batchOptionalRatio(s.SourceAccessNetwork, s.SourceAccessResults),
		SourceDiscoveryOnlyRate:     batchOptionalRatio(s.SourceAccessDiscoveryOnly, s.SourceAccessResults),
		SourceDynamicPartialRate:    batchOptionalRatio(s.SourceAccessDynamicPartial, s.SourceAccessResults),
		SessionSearchContextHitRate: batchOptionalRatio(s.SessionSearchContextHits, s.SessionSearchResults),
		AvgRuntimeErrors:            batchAverage(s.RuntimeErrors, s.Total),
		AvgContextCompactions:       batchAverage(s.ContextCompactions, s.Total),
		AvgReactiveCompactions:      batchAverage(s.ContextCompactionsReactive, s.Total),
		AvgContextRemovedMessages:   batchAverage(s.ContextCompactionRemoved, s.Total),
		ToolContextTruncationRate:   batchOptionalRatio(s.ToolContextTruncated, s.ToolCalls),
		ToolResultTruncationRate:    batchOptionalRatio(s.ToolResultsTruncated, s.ToolCalls),
		DurationMS:                  s.Duration.Milliseconds(),
		ToolCalls:                   s.ToolCalls,
		ToolErrors:                  s.ToolErrors,
		ToolRepaired:                s.ToolRepaired,
		ToolNameCanonicalized:       s.ToolNameCanonicalized,
		ToolRepairCalls:             s.ToolRepairCalls,
		ToolRepairSucceeded:         s.ToolRepairSucceeded,
		ToolRepairFailed:            s.ToolRepairFailed,
		ToolRepairNotes:             s.ToolRepairNotes,
		ToolRepairByKind:            cloneStringIntMap(s.ToolRepairByKind),
		ToolFailureByKind:           cloneStringIntMap(s.ToolFailureByKind),
		ToolFailureExamples:         cloneToolFailureExamples(s.ToolFailureExamples),
		RuntimeErrorByKind:          cloneStringIntMap(s.RuntimeErrorByKind),
		RuntimeErrorExamples:        cloneRuntimeErrorExamples(s.RuntimeErrorExamples),
		RuntimeSurfaceRate:          batchRatio(s.RuntimeSurfaceScenarios, s.Total),
		RuntimeSurfaceScenarios:     s.RuntimeSurfaceScenarios,
		RuntimeSurfaceTools:         cloneStringIntMap(s.RuntimeSurfaceTools),
		RuntimeSurfaceCapabilities:  cloneStringIntMap(s.RuntimeSurfaceCapabilities),
		LoopDecisions:               s.LoopDecisions,
		LoopDecisionByKind:          cloneStringIntMap(s.LoopDecisionByKind),
		LoopDecisionByDecision:      cloneStringIntMap(s.LoopDecisionByDecision),
		LoopDecisionExamples:        cloneLoopDecisionExamples(s.LoopDecisionExamples),
		ContextCompactions:          s.ContextCompactions,
		ContextCompactionsReactive:  s.ContextCompactionsReactive,
		ContextCompactionRemoved:    s.ContextCompactionRemoved,
		ContextCompactionSummary:    s.ContextCompactionSummary,
		ContextCompactionExamples:   cloneContextCompactionExamples(s.ContextCompactionExamples),
		LoopGuardInterventions:      s.LoopGuardInterventions,
		ForcedNoTools:               s.ForcedNoTools,
		SourceAccessResults:         s.SourceAccessResults,
		SourceAccessVerified:        s.SourceAccessVerified,
		SourceAccessDiscoveryOnly:   s.SourceAccessDiscoveryOnly,
		SourceAccessNetwork:         s.SourceAccessNetwork,
		SourceAccessDynamicPartial:  s.SourceAccessDynamicPartial,
		SourceAccessExamples:        cloneSourceAccessExamples(s.SourceAccessExamples),
		MemoryUpdates:               s.MemoryUpdates,
		MemoryUpdateAdd:             s.MemoryUpdateAdd,
		MemoryUpdateReplace:         s.MemoryUpdateReplace,
		MemoryUpdateRemove:          s.MemoryUpdateRemove,
		SessionSearchCalls:          s.SessionSearchCalls,
		SessionSearchResults:        s.SessionSearchResults,
		SessionSearchContextHits:    s.SessionSearchContextHits,
		SessionSearchMatchedTerms:   s.SessionSearchMatchedTerms,
		ToolDurationMS:              s.ToolDurationMS,
		ToolContextTruncated:        s.ToolContextTruncated,
		ToolContextOmittedBytes:     s.ToolContextOmittedBytes,
		ToolArgsTruncated:           s.ToolArgsTruncated,
		ToolArgsOmittedBytes:        s.ToolArgsOmittedBytes,
		ToolResultsTruncated:        s.ToolResultsTruncated,
		ToolResultsOmittedBytes:     s.ToolResultsOmittedBytes,
		ToolResultArtifacts:         s.ToolResultArtifacts,
		ToolTruncationExamples:      cloneToolTruncationExamples(s.ToolTruncationExamples),
		VerifierRuns:                s.VerifierRuns,
		VerifierPassed:              s.VerifierPassed,
		VerifierFailed:              s.VerifierFailed,
		VerifierOutputTruncated:     s.VerifierOutputTruncated,
		VerifierOutputOmittedBytes:  s.VerifierOutputOmittedBytes,
		TraceSchemaVersions:         cloneTraceSchemaVersions(s.TraceSchemaVersions),
		TraceEvents:                 s.TraceEvents,
		TraceEventTypes:             cloneStringIntMap(s.TraceEventTypes),
		InputTokens:                 s.InputTokens,
		OutputTokens:                s.OutputTokens,
		AvgInputTokens:              batchAverage(s.InputTokens, s.Total),
		AvgOutputTokens:             batchAverage(s.OutputTokens, s.Total),
		AvgTotalTokens:              batchAverage(s.InputTokens+s.OutputTokens, s.Total),
		EndCompleted:                s.EndCompleted,
		EndMaxTurns:                 s.EndMaxTurns,
		EndErrors:                   s.EndErrors,
		EndCancelled:                s.EndCancelled,
		EndUnknown:                  s.EndUnknown,
		FailureKinds:                cloneFailureKinds(s.FailureKinds),
		FailureHints:                failureHintsForKinds(s.FailureKinds),
		ToolFailureHints:            toolFailureHintsForKinds(s.ToolFailureByKind),
		RuntimeErrorHints:           failureHintsForKinds(s.RuntimeErrorByKind),
		DebugBriefByTag:             cloneStringIntMap(s.DebugBriefByTag),
		ExpectationScenarios:        s.ExpectationScenarios,
		ExpectationSuites:           cloneStringIntMap(s.ExpectationSuites),
		ExpectationCapabilities:     cloneStringIntMap(s.ExpectationCapabilities),
		ExpectationCapabilityPassed: cloneStringIntMap(s.ExpectationCapabilityPass),
		ExpectationCapabilityFailed: cloneStringIntMap(s.ExpectationCapabilityFail),
		ExpectationCapabilityRate:   expectationCapabilityPassRates(s.ExpectationCapabilities, s.ExpectationCapabilityPass),
		ExpectationRequiredTools:    cloneStringIntMap(s.ExpectationRequiredTools),
		ExpectationSourceAccess:     cloneStringIntMap(s.ExpectationSourceAccess),
		QualityGatesPassed:          qualityGatesPassedForJSONL(meta, gateFailures),
		QualityGateFailures:         append([]string(nil), gateFailures...),
		RemovedWorkspaces:           s.RemovedWorkspaces,
		CleanupErrors:               s.CleanupErrors,
		FocusedTaskCalls:            s.FocusedTaskCalls,
		FocusedTaskByType:           cloneStringIntMap(s.FocusedTaskByType),
		FocusedTaskErrors:           s.FocusedTaskErrors,
		SubagentCalls:               s.SubagentCalls,
		SubagentByMode:              cloneStringIntMap(s.SubagentByMode),
		SubagentErrors:              s.SubagentErrors,
		PlanCalls:                   s.PlanCalls,
		PlanByAction:                cloneStringIntMap(s.PlanByAction),
		PlanErrors:                  s.PlanErrors,
	})
}

func qualityGatesPassedForJSONL(meta evalJSONLMetadata, failures []string) *bool {
	if !hasQualityGateThresholds(meta) {
		return nil
	}
	passed := len(failures) == 0
	return &passed
}

func hasQualityGateThresholds(meta evalJSONLMetadata) bool {
	return meta.MinPassRate != nil ||
		meta.MinCompletionRate != nil ||
		meta.MinMemoryUpdateRate != nil ||
		meta.MinRuntimeSurfaceRate != nil ||
		meta.MinSourceNetworkRate != nil ||
		meta.MinSourceAccessVerifiedRate != nil ||
		meta.MinExpectationCapabilityPassRate != nil ||
		meta.MinSessionSearchContextHitRate != nil ||
		meta.MinToolRepairSuccessRate != nil ||
		meta.MinVerifierPassRate != nil ||
		meta.MaxFocusedTaskErrorRate != nil ||
		meta.MaxForcedNoToolsRate != nil ||
		meta.MaxLoopGuardInterventionRate != nil ||
		meta.MaxPlanErrorRate != nil ||
		meta.MaxSourceDiscoveryOnlyRate != nil ||
		meta.MaxSourceDynamicPartialRate != nil ||
		meta.MaxSubagentErrorRate != nil ||
		meta.MaxToolErrorRate != nil ||
		meta.MaxToolContextTruncationRate != nil ||
		meta.MaxToolResultTruncationRate != nil ||
		meta.MaxAvgRuntimeErrors != nil ||
		meta.MaxAvgContextCompactions != nil ||
		meta.MaxAvgReactiveCompactions != nil ||
		meta.MaxAvgTotalTokens != nil
}

func expectationCapabilityPassRates(total, passed map[string]int) map[string]float64 {
	if len(total) == 0 {
		return nil
	}
	out := map[string]float64{}
	for cap, count := range total {
		if count <= 0 {
			continue
		}
		out[cap] = float64(passed[cap]) / float64(count)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func expectationCapabilityPassTotals(s batchSummary) (passed int, total int) {
	for cap, count := range s.ExpectationCapabilities {
		if count <= 0 {
			continue
		}
		total += count
		passed += s.ExpectationCapabilityPass[cap]
	}
	return passed, total
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

func cloneSourceAccessExamples(in []agenteval.SourceAccessExample) []agenteval.SourceAccessExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.SourceAccessExample(nil), in...)
}

func cloneMemoryUpdateExamples(in []agenteval.MemoryUpdateExample) []agenteval.MemoryUpdateExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.MemoryUpdateExample(nil), in...)
}

func cloneToolTruncationExamples(in []agenteval.ToolTruncationExample) []agenteval.ToolTruncationExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.ToolTruncationExample(nil), in...)
}

func cloneLoopDecisionExamples(in []agenteval.LoopDecision) []agenteval.LoopDecision {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.LoopDecision(nil), in...)
}

func cloneContextCompactionExamples(in []agenteval.ContextCompaction) []agenteval.ContextCompaction {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.ContextCompaction(nil), in...)
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

func appendSourceAccessExamples(dst, src []agenteval.SourceAccessExample, limit int) []agenteval.SourceAccessExample {
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

func appendToolTruncationExamples(dst, src []agenteval.ToolTruncationExample, limit int) []agenteval.ToolTruncationExample {
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

func appendContextCompactionExamples(dst, src []agenteval.ContextCompaction, limit int) []agenteval.ContextCompaction {
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
	if brief := agenteval.BuildDebugBrief(res); brief != nil {
		fmt.Fprintf(w, " debug_brief=%s", formatDebugBriefTags(brief.Tags))
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

func hasBatchSessionSearchStats(stats batchSummary) bool {
	return stats.SessionSearchCalls > 0 ||
		stats.SessionSearchResults > 0 ||
		stats.SessionSearchContextHits > 0 ||
		stats.SessionSearchMatchedTerms > 0
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

func validateRunConfig(
	temperature,
	topP,
	maxTokens,
	seed string,
	timeout time.Duration,
	executor string,
	scenarios []agenteval.BatchScenario,
	workRoot string,
	workRootSet bool,
	verifierOutputCap int,
	runtimeEvalMode bool,
	runtimeTools string,
	runtimeAllTools bool,
	runtimeMemory bool,
	runtimeWeb bool,
	runtimeBrowser bool,
	runtimeMCPConfig string,
) error {
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be a positive duration")
	}
	if verifierOutputCap <= 0 {
		return fmt.Errorf("--verifier-output-cap must be positive")
	}
	if err := validateEvalExecutor(executor, len(scenarios), workRoot, workRootSet); err != nil {
		return err
	}
	sampling, err := parseEvalSampling(temperature, topP, maxTokens, seed)
	if err != nil {
		return err
	}
	if err := sampling.Validate(); err != nil {
		return evalSamplingFlagError(err)
	}
	if err := validateRuntimeToolSurface(scenarios, runtimeEvalMode, runtimeTools, runtimeAllTools, runtimeMemory, runtimeWeb, runtimeBrowser, runtimeMCPConfig); err != nil {
		return err
	}
	return nil
}

func validateRuntimeToolSurface(
	scenarios []agenteval.BatchScenario,
	runtimeEvalMode bool,
	runtimeTools string,
	runtimeAllTools bool,
	runtimeMemory bool,
	runtimeWeb bool,
	runtimeBrowser bool,
	runtimeMCPConfig string,
) error {
	evalMode := runtimeEvalMode || strings.TrimSpace(runtimeTools) != "" || runtimeAllTools
	if !evalMode {
		return nil
	}
	enabled, all := enabledRuntimeToolSet(runtimeTools, runtimeAllTools, runtimeMemory, runtimeWeb, runtimeBrowser, runtimeMCPConfig)
	if all {
		return nil
	}
	var missing []string
	for _, scenario := range scenarios {
		var scenarioMissing []string
		for _, tool := range requiredRuntimeTools(scenario) {
			if enabled[tool] || (tool == "memory" && scenario.EnableMemory) {
				continue
			}
			scenarioMissing = append(scenarioMissing, tool)
		}
		if len(scenarioMissing) > 0 {
			sort.Strings(scenarioMissing)
			missing = append(missing, fmt.Sprintf("%s missing %s", scenario.Name, strings.Join(scenarioMissing, ", ")))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("runtime eval mode starts with no tools; selected scenario(s) require unavailable tools: %s. Enable them with --runtime-tools, --runtime-web, --runtime-browser, --runtime-memory, or --runtime-all-tools", strings.Join(missing, "; "))
}

func enabledRuntimeToolSet(runtimeTools string, runtimeAllTools, runtimeMemory, runtimeWeb, runtimeBrowser bool, runtimeMCPConfig string) (map[string]bool, bool) {
	enabled := map[string]bool{}
	add := func(names ...string) {
		for _, name := range names {
			if strings.TrimSpace(name) != "" {
				enabled[name] = true
			}
		}
	}
	if runtimeMemory {
		add("memory")
	}
	if runtimeWeb {
		add("web_fetch", "web_search")
	}
	if runtimeBrowser {
		add(evalBrowserToolNames()...)
	}
	if strings.TrimSpace(runtimeMCPConfig) != "" {
		add("mcp")
	}
	if runtimeAllTools {
		return enabled, true
	}
	for _, raw := range strings.FieldsFunc(runtimeTools, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		switch name := strings.TrimSpace(raw); name {
		case "", "none":
			continue
		case "all":
			return enabled, true
		case "workspace":
			add(evalWorkspaceToolNames()...)
		case "readonly_workspace":
			add(evalReadonlyWorkspaceToolNames()...)
		case "web":
			add("web_fetch")
		case "browser":
			add(evalBrowserToolNames()...)
		case "recall":
			add("memory", agent.SessionSearchToolName)
		case "delegation":
			add("subagent_run", "run_task")
		default:
			add(name)
		}
	}
	return enabled, false
}

func requiredRuntimeTools(scenario agenteval.BatchScenario) []string {
	required := map[string]bool{}
	for _, tool := range scenario.RequiredTools {
		if strings.TrimSpace(tool) != "" {
			required[strings.TrimSpace(tool)] = true
		}
	}
	if len(scenario.RequiredCommands) > 0 ||
		len(scenario.RequiredCommandCounts) > 0 ||
		len(scenario.RequiredCommandBeforeTool) > 0 ||
		len(scenario.RequiredCommandAfterTool) > 0 {
		required["shell"] = true
	}
	out := make([]string, 0, len(required))
	for tool := range required {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func evalWorkspaceToolNames() []string {
	return []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", "symbol_context", "repo_search"}
}

func evalReadonlyWorkspaceToolNames() []string {
	return []string{"read_file", "file_context", "list_files", "symbol_context", "repo_search"}
}

func evalBrowserToolNames() []string {
	return []string{"browser_navigate", "browser_snapshot", "browser_find", "browser_network", "browser_network_read", "browser_click", "browser_scroll", "browser_type", "browser_wait", "browser_screenshot"}
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
