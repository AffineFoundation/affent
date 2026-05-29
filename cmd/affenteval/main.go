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
	"github.com/affinefoundation/affent/internal/sessionstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	evalJSONLSchemaVersion      = 1
	batchSummaryExamplesPerKind = 2
)

type expectationCapabilityFailureExample struct {
	Capability        string         `json:"capability"`
	Scenario          string         `json:"scenario"`
	FailureKinds      map[string]int `json:"failure_kinds,omitempty"`
	DebugBriefTags    []string       `json:"debug_brief_tags,omitempty"`
	TracePath         string         `json:"trace_path,omitempty"`
	TimelinePath      string         `json:"timeline_path,omitempty"`
	DebugManifestPath string         `json:"debug_manifest_path,omitempty"`
}

type expectationDomainFailureExample struct {
	Domain            string         `json:"domain"`
	Scenario          string         `json:"scenario"`
	FailureKinds      map[string]int `json:"failure_kinds,omitempty"`
	DebugBriefTags    []string       `json:"debug_brief_tags,omitempty"`
	TracePath         string         `json:"trace_path,omitempty"`
	TimelinePath      string         `json:"timeline_path,omitempty"`
	DebugManifestPath string         `json:"debug_manifest_path,omitempty"`
}

type expectationDomainRuntimeTotals struct {
	Scenarios                  int
	Passed                     int
	Failed                     int
	Duration                   time.Duration
	ToolCalls                  int
	ToolErrors                 int
	LoopGuardInterventions     int
	SourceAccessResults        int
	SourceAccessVerified       int
	SourceAccessNetwork        int
	SourceAccessDiscoveryOnly  int
	SourceAccessDynamicPartial int
	MemoryUpdates              int
	RuntimeErrors              int
	InputTokens                int
	OutputTokens               int
}

type expectationDomainMetrics struct {
	Scenarios                  int      `json:"scenarios"`
	Passed                     int      `json:"passed"`
	Failed                     int      `json:"failed"`
	PassRate                   float64  `json:"pass_rate"`
	AvgDurationMS              float64  `json:"avg_duration_ms"`
	AvgToolCalls               float64  `json:"avg_tool_calls"`
	AvgRuntimeErrors           float64  `json:"avg_runtime_errors"`
	AvgTotalTokens             float64  `json:"avg_total_tokens"`
	MemoryUpdateRate           float64  `json:"memory_update_rate"`
	ToolErrorRate              *float64 `json:"tool_error_rate,omitempty"`
	LoopGuardInterventionRate  *float64 `json:"loop_guard_intervention_rate,omitempty"`
	SourceAccessVerifiedRate   *float64 `json:"source_access_verified_rate,omitempty"`
	SourceNetworkRate          *float64 `json:"source_network_rate,omitempty"`
	SourceDiscoveryOnlyRate    *float64 `json:"source_discovery_only_rate,omitempty"`
	SourceDynamicPartialRate   *float64 `json:"source_dynamic_partial_rate,omitempty"`
	SourceAccessResults        int      `json:"source_access_results,omitempty"`
	SourceAccessVerified       int      `json:"source_access_verified,omitempty"`
	SourceAccessNetwork        int      `json:"source_access_network,omitempty"`
	SourceAccessDiscoveryOnly  int      `json:"source_access_discovery_only,omitempty"`
	SourceAccessDynamicPartial int      `json:"source_access_dynamic_partial,omitempty"`
	ToolCalls                  int      `json:"tool_calls,omitempty"`
	ToolErrors                 int      `json:"tool_errors,omitempty"`
	LoopGuardInterventions     int      `json:"loop_guard_interventions,omitempty"`
	RuntimeErrors              int      `json:"runtime_errors,omitempty"`
	InputTokens                int      `json:"input_tokens,omitempty"`
	OutputTokens               int      `json:"output_tokens,omitempty"`
}

type batchFailureExample struct {
	Scenario          string `json:"scenario"`
	Failure           string `json:"failure"`
	TracePath         string `json:"trace_path,omitempty"`
	TimelinePath      string `json:"timeline_path,omitempty"`
	DebugManifestPath string `json:"debug_manifest_path,omitempty"`
}

type batchDebugBriefTagExample struct {
	Scenario          string         `json:"scenario"`
	FailureKinds      map[string]int `json:"failure_kinds,omitempty"`
	TracePath         string         `json:"trace_path,omitempty"`
	TimelinePath      string         `json:"timeline_path,omitempty"`
	DebugManifestPath string         `json:"debug_manifest_path,omitempty"`
}

type batchConversationRepairExample struct {
	Scenario              string `json:"scenario"`
	SessionID             string `json:"session_id,omitempty"`
	MissingToolResults    int    `json:"missing_tool_results,omitempty"`
	DuplicateToolResults  int    `json:"duplicate_tool_results,omitempty"`
	UnexpectedToolResults int    `json:"unexpected_tool_results,omitempty"`
	FailureKind           string `json:"failure_kind,omitempty"`
	Next                  string `json:"next,omitempty"`
}

type stringFloatMapFlag map[string]float64

type stringSetFlag map[string]bool

func (f *stringSetFlag) Set(raw string) error {
	for _, value := range splitCSV(raw) {
		if value == "" {
			continue
		}
		if *f == nil {
			*f = stringSetFlag{}
		}
		(*f)[value] = true
	}
	if len(*f) == 0 {
		return fmt.Errorf("value must be non-empty")
	}
	return nil
}

func (f *stringSetFlag) String() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	return strings.Join(sortedStringSetFlagValues(*f), ",")
}

func (f stringSetFlag) values() []string {
	return sortedStringSetFlagValues(f)
}

func (f *stringFloatMapFlag) Set(raw string) error {
	key, valueText, ok := strings.Cut(strings.TrimSpace(raw), "=")
	if !ok {
		return fmt.Errorf("want tag=rate")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("tag must be non-empty")
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(valueText), 64)
	if err != nil {
		return fmt.Errorf("parse rate: %w", err)
	}
	if *f == nil {
		*f = stringFloatMapFlag{}
	}
	(*f)[key] = value
	return nil
}

func (f *stringFloatMapFlag) String() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*f))
	for _, key := range sortedFloatMapKeys(map[string]float64(*f)) {
		parts = append(parts, fmt.Sprintf("%s=%s", key, formatGateFloat((*f)[key])))
	}
	return strings.Join(parts, ",")
}

func (f stringFloatMapFlag) clone() map[string]float64 {
	if len(f) == 0 {
		return nil
	}
	clone := make(map[string]float64, len(f))
	for key, value := range f {
		clone[key] = value
	}
	return clone
}

func sortedStringSetFlagValues(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

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
		debugBriefTagGates                        stringFloatMapFlag
		expectationCapGates                       stringSetFlag
		expectationDomainGates                    stringSetFlag
		expectationDomainMinVerifiedGates         stringFloatMapFlag
		expectationDomainMaxAvgTokensGates        stringFloatMapFlag
		expectationDomainMaxAvgToolCallsGates     stringFloatMapFlag
		expectationDomainMaxAvgRuntimeErrorsGates stringFloatMapFlag
		expectationDomainMaxToolErrorGates        stringFloatMapFlag
		expectationDomainMaxLoopGuardGates        stringFloatMapFlag
		list                                      = fs.Bool("list", false, "list built-in scenarios and exit")
		listCoverage                              = fs.Bool("list-coverage", false, "list selected scenario expectation coverage and exit")
		listSuites                                = fs.Bool("list-suites", false, "list built-in scenario suites and exit")
		listQualityProfiles                       = fs.Bool("list-quality-profiles", false, "list built-in quality gate profiles and exit")
		suite                                     = fs.String("suite", "", "scenario suite to run/list (e.g. small-model-tools)")
		scenarioCSV                               = fs.String("scenario", "", "comma-separated scenario names; empty runs all")
		prompt                                    = fs.String("prompt", "", "run one ad-hoc prompt; use '-' for stdin")
		promptFile                                = fs.String("prompt-file", "", "run one ad-hoc prompt read from file")
		adHocName                                 = fs.String("name", "adhoc", "scenario name for --prompt/--prompt-file debug runs")
		adHocSessionID                            = fs.String("session-id", "", "session id for --prompt/--prompt-file runs; without a prompt, diagnose SESSION/events.jsonl from --session-state-root")
		adHocMaxTurns                             = fs.Int("max-turns", agenteval.DefaultBatchMaxTurnSteps, "max assistant/tool loop steps for --prompt/--prompt-file debug runs")
		adHocVerify                               = fs.String("verify-command", "", "optional verifier command for --prompt/--prompt-file debug runs")
		traceFile                                 = fs.String("trace-file", "", "parse an existing trace/events JSONL file and write debug artifacts without running a model")
		traceOutputDir                            = fs.String("trace-output-dir", "", "directory for --trace-file debug artifacts; default is TRACE_DIR/affenteval-debug")
		traceWorkspace                            = fs.String("trace-workspace", "", "workspace root for --trace-file scenario checks; default is the trace debug output dir")
		sessionStateRoot                          = fs.String("session-state-root", "", "parent directory for --session-id trace debug artifacts; default AFFENTSERVE_MEMORY_ROOT or repo-local .tmp/runtime-workspace/session-state")
		repoRoot                                  = fs.String("repo-root", ".", "Affent repository root")
		workRoot                                  = fs.String("work-root", "", "directory for temporary scenario workspaces; default $TMPDIR/affent-eval")
		baseURL                                   = fs.String("base-url", "", "OpenAI-compatible endpoint (env: AFFENTCTL_BASE_URL)")
		apiKey                                    = fs.String("api-key", "", "API key (env: AFFENTCTL_API_KEY)")
		model                                     = fs.String("model", "", "model id (env: AFFENTCTL_MODEL)")
		providerLabel                             = fs.String("provider-label", "", "provider label written to JSONL for comparisons (env: AFFENTEVAL_PROVIDER_LABEL)")
		temperature                               = fs.String("temperature", "0", "sampling temperature forwarded to affentctl")
		topP                                      = fs.String("top-p", "", "top-p sampling forwarded to affentctl; empty keeps provider default")
		maxTokens                                 = fs.String("max-tokens", "", "max output tokens forwarded to affentctl; empty keeps provider default")
		seed                                      = fs.String("seed", "", "deterministic-sampling seed forwarded to affentctl; empty keeps provider default")
		executor                                  = fs.String("executor", "local", "affentctl tool executor for scenario runs: local, sandbox, or docker:<container>")
		runtimeEvalMode                           = fs.Bool("runtime-eval-mode", true, "pass affentctl --eval-mode during scenario runs; default true so evals start with no tools")
		runtimeTools                              = fs.String("runtime-tools", "", "comma-separated affentctl --eval-tools allowlist, e.g. readonly_workspace,web,recall or read_file,shell")
		runtimeAllTools                           = fs.Bool("runtime-all-tools", false, "pass affentctl --eval-all-tools to enable the full tool surface under runtime eval mode")
		runtimeMemory                             = fs.Bool("runtime-memory", false, "pass affentctl --memory=true during scenario runs; useful for memory-only opt-in")
		runtimeWeb                                = fs.Bool("runtime-web", false, "pass affentctl --web --web-search during scenario runs for external retrieval/debug evals")
		runtimeBrowser                            = fs.Bool("runtime-browser", false, "pass affentctl --browser during scenario runs for rendered-page/browser debug evals")
		runtimeMCPConfig                          = fs.String("runtime-mcp-config", "", "pass affentctl --mcp-config PATH during scenario runs; useful to opt into MCP only")
		traceDeltas                               = fs.Bool("trace-deltas", false, "retain streaming message delta events in trace JSONL for deep debugging; default skips deltas to keep traces compact")
		timeout                                   = fs.Duration("timeout", 5*time.Minute, "per-scenario timeout")
		verifierOutputCap                         = fs.Int("verifier-output-cap", agenteval.DefaultVerifierOutputCapBytes, "maximum verifier output bytes buffered per scenario")
		jsonl                                     = fs.Bool("jsonl", false, "emit machine-readable JSONL records instead of text")
		keepWorkspaces                            = fs.Bool("keep-workspaces", false, "keep passing scenario workspaces; failing scenario workspaces are always kept")
		qualityProfile                            = fs.String("quality-profile", "", "predefined quality gate profile: longrun or web-evidence; explicit gate flags override profile thresholds")
		gates                                     = qualityGateConfig{
			MinPassRate:                           fs.Float64("min-pass-rate", -1, "optional quality gate: minimum batch pass rate, 0..1"),
			MinCompletionRate:                     fs.Float64("min-completion-rate", -1, "optional quality gate: minimum completed-turn rate, 0..1"),
			MinMemoryUpdateRate:                   fs.Float64("min-memory-update-rate", -1, "optional quality gate: minimum confirmed memory updates per scenario, 0..1"),
			MinLoopTurnCheckpointRate:             fs.Float64("min-loop-turn-checkpoint-rate", -1, "optional quality gate: minimum scenario rate with persisted loop turn checkpoints, 0..1"),
			MinLoopProtocolFeedRate:               fs.Float64("min-loop-protocol-feed-rate", -1, "optional quality gate: minimum scenario rate with loop protocol feeds, 0..1"),
			MinLoopProtocolCalibrationRequestRate: fs.Float64("min-loop-protocol-calibration-request-rate", -1, "optional quality gate: minimum scenario rate with loop protocol calibration requests, 0..1"),
			MinLoopProtocolCalibrationRate:        fs.Float64("min-loop-protocol-calibration-rate", -1, "optional quality gate: minimum scenario rate with accepted loop protocol calibrations, 0..1"),
			MinRuntimeSurfaceRate:                 fs.Float64("min-runtime-surface-rate", -1, "optional quality gate: minimum scenario rate with recorded runtime surface, 0..1"),
			MinTraceEventRate:                     fs.Float64("min-trace-event-rate", -1, "optional quality gate: minimum scenario rate with parsed trace events, 0..1"),
			MinSourceNetworkRate:                  fs.Float64("min-source-network-rate", -1, "optional quality gate: minimum network/API source access rate, 0..1"),
			MinSourceAccessVerifiedRate:           fs.Float64("min-source-access-verified-rate", -1, "optional quality gate: minimum verified SourceAccess rate, 0..1"),
			MinExpectationCapabilityPassRate:      fs.Float64("min-expectation-capability-pass-rate", -1, "optional quality gate: minimum pass rate across declared expectation capability instances, 0..1"),
			MinEachExpectationCapabilityPassRate:  fs.Float64("min-each-expectation-capability-pass-rate", -1, "optional quality gate: minimum pass rate for each declared expectation capability family, 0..1"),
			MinExpectationDomainPassRate:          fs.Float64("min-expectation-domain-pass-rate", -1, "optional quality gate: minimum pass rate across declared expectation domain instances, 0..1"),
			MinEachExpectationDomainPassRate:      fs.Float64("min-each-expectation-domain-pass-rate", -1, "optional quality gate: minimum pass rate for each declared expectation domain, 0..1"),
			MinSessionSearchContextHitRate:        fs.Float64("min-session-search-context-hit-rate", -1, "optional quality gate: minimum session_search context-hit rate, 0..1"),
			MinSessionSearchMatchedTermsPerCall:   fs.Float64("min-session-search-matched-terms-per-call", -1, "optional quality gate: minimum average unique matched session_search terms per call"),
			MinToolRepairSuccessRate:              fs.Float64("min-tool-repair-success-rate", -1, "optional quality gate: minimum successful tool-call repair rate, 0..1"),
			MinVerifierPassRate:                   fs.Float64("min-verifier-pass-rate", -1, "optional quality gate: minimum verifier pass rate, 0..1"),
			MaxFocusedTaskErrorRate:               fs.Float64("max-focused-task-error-rate", -1, "optional quality gate: maximum focused-task error rate per focused-task call, 0..1"),
			MaxForcedNoToolsRate:                  fs.Float64("max-forced-no-tools-rate", -1, "optional quality gate: maximum forced no-tool follow-up rate per tool call, 0..1"),
			MaxLoopGuardInterventionRate:          fs.Float64("max-loop-guard-intervention-rate", -1, "optional quality gate: maximum loop guard intervention rate per tool call, 0..1"),
			MaxPlanErrorRate:                      fs.Float64("max-plan-error-rate", -1, "optional quality gate: maximum plan tool error rate per plan call, 0..1"),
			MaxMemorySearchMissRate:               fs.Float64("max-memory-search-miss-rate", -1, "optional quality gate: maximum memory search miss rate per memory search call, 0..1"),
			MaxSourceDiscoveryOnlyRate:            fs.Float64("max-source-discovery-only-rate", -1, "optional quality gate: maximum discovery-only source access rate, 0..1"),
			MaxSourceDynamicPartialRate:           fs.Float64("max-source-dynamic-partial-rate", -1, "optional quality gate: maximum dynamic-partial source access rate, 0..1"),
			MaxSubagentErrorRate:                  fs.Float64("max-subagent-error-rate", -1, "optional quality gate: maximum subagent error rate per subagent call, 0..1"),
			MaxToolErrorRate:                      fs.Float64("max-tool-error-rate", -1, "optional quality gate: maximum tool error rate, 0..1"),
			MaxToolContextTruncationRate:          fs.Float64("max-tool-context-truncation-rate", -1, "optional quality gate: maximum tool-context truncation rate, 0..1"),
			MaxToolResultTruncationRate:           fs.Float64("max-tool-result-truncation-rate", -1, "optional quality gate: maximum tool-result event truncation rate, 0..1"),
			MaxAvgRuntimeErrors:                   fs.Float64("max-avg-runtime-errors", -1, "optional quality gate: maximum average runtime error events per scenario"),
			MaxAvgContextCompactions:              fs.Float64("max-avg-context-compactions", -1, "optional quality gate: maximum average context compactions per scenario"),
			MaxAvgReactiveCompactions:             fs.Float64("max-avg-reactive-context-compactions", -1, "optional quality gate: maximum average reactive context compactions per scenario"),
			MaxAvgContextRemovedMessages:          fs.Float64("max-avg-context-removed-messages", -1, "optional quality gate: maximum average messages removed by context compaction per scenario"),
			MaxAvgContextSummaryBytes:             fs.Float64("max-avg-context-summary-bytes", -1, "optional quality gate: maximum average context compaction summary bytes per scenario"),
			MaxAvgContextSummaryMissing:           fs.Float64("max-avg-context-summary-missing", -1, "optional quality gate: maximum average missing context compaction summaries per scenario"),
			MaxAvgContextSummaryEmpty:             fs.Float64("max-avg-context-summary-empty", -1, "optional quality gate: maximum average empty context compaction summaries per scenario"),
			MaxAvgContextInjections:               fs.Float64("max-avg-context-injections", -1, "optional quality gate: maximum average injected system-context blocks per scenario"),
			MaxAvgContextInjectionBytes:           fs.Float64("max-avg-context-injection-bytes", -1, "optional quality gate: maximum average injected system-context bytes per scenario"),
			MaxAvgContextInjectionEstimatedTokens: fs.Float64("max-avg-context-injection-estimated-tokens", -1, "optional quality gate: maximum average estimated injected system-context tokens per scenario"),
			MaxAvgToolCalls:                       fs.Float64("max-avg-tool-calls", -1, "optional quality gate: maximum average tool calls per scenario"),
			MaxAvgDurationMS:                      fs.Float64("max-avg-duration-ms", -1, "optional quality gate: maximum average scenario duration in milliseconds"),
			MaxAvgTotalTokens:                     fs.Float64("max-avg-total-tokens", -1, "optional quality gate: maximum average total tokens per scenario"),
			MaxScenarioTotalTokens:                fs.Float64("max-scenario-total-tokens", -1, "optional quality gate: maximum total tokens for any single scenario"),
		}
	)
	fs.Var(&debugBriefTagGates, "max-debug-brief-tag-rate", "optional repeatable quality gate: maximum scenario rate for a debug_brief tag, as tag=rate; use tag=-1 to disable a profile default")
	fs.Var(&expectationCapGates, "require-expectation-capability", "optional repeatable quality gate: require at least one scenario declaring this expectation capability; accepts comma-separated values")
	fs.Var(&expectationDomainGates, "require-expectation-domain", "optional repeatable quality gate: require at least one scenario declaring this task domain; accepts comma-separated values")
	fs.Var(&expectationDomainMinVerifiedGates, "min-expectation-domain-source-access-verified-rate", "optional repeatable quality gate: minimum verified SourceAccess rate for a task domain, as domain=rate")
	fs.Var(&expectationDomainMaxAvgTokensGates, "max-expectation-domain-avg-total-tokens", "optional repeatable quality gate: maximum average total tokens for a task domain, as domain=tokens")
	fs.Var(&expectationDomainMaxAvgToolCallsGates, "max-expectation-domain-avg-tool-calls", "optional repeatable quality gate: maximum average tool calls for a task domain, as domain=count")
	fs.Var(&expectationDomainMaxAvgRuntimeErrorsGates, "max-expectation-domain-avg-runtime-errors", "optional repeatable quality gate: maximum average runtime errors for a task domain, as domain=count")
	fs.Var(&expectationDomainMaxToolErrorGates, "max-expectation-domain-tool-error-rate", "optional repeatable quality gate: maximum tool error rate for a task domain, as domain=rate")
	fs.Var(&expectationDomainMaxLoopGuardGates, "max-expectation-domain-loop-guard-intervention-rate", "optional repeatable quality gate: maximum loop guard intervention rate for a task domain, as domain=rate")
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
	if len(debugBriefTagGates) > 0 {
		gates.MaxDebugBriefTagRates = debugBriefTagGates.clone()
	}
	if len(expectationCapGates) > 0 {
		gates.RequiredExpectationCapabilities = expectationCapGates.values()
	}
	if len(expectationDomainGates) > 0 {
		gates.RequiredExpectationDomains = expectationDomainGates.values()
	}
	if len(expectationDomainMinVerifiedGates) > 0 {
		gates.MinExpectationDomainSourceAccessVerifiedRates = expectationDomainMinVerifiedGates.clone()
	}
	if len(expectationDomainMaxAvgTokensGates) > 0 {
		gates.MaxExpectationDomainAvgTotalTokens = expectationDomainMaxAvgTokensGates.clone()
	}
	if len(expectationDomainMaxAvgToolCallsGates) > 0 {
		gates.MaxExpectationDomainAvgToolCalls = expectationDomainMaxAvgToolCallsGates.clone()
	}
	if len(expectationDomainMaxAvgRuntimeErrorsGates) > 0 {
		gates.MaxExpectationDomainAvgRuntimeErrors = expectationDomainMaxAvgRuntimeErrorsGates.clone()
	}
	if len(expectationDomainMaxToolErrorGates) > 0 {
		gates.MaxExpectationDomainToolErrorRates = expectationDomainMaxToolErrorGates.clone()
	}
	if len(expectationDomainMaxLoopGuardGates) > 0 {
		gates.MaxExpectationDomainLoopGuardInterventionRates = expectationDomainMaxLoopGuardGates.clone()
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
		scenarios, err := selectedEvalScenarios(*suite, *scenarioCSV, "", "", "", "", 1, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
			return 64
		}
		for _, scenario := range scenarios {
			fmt.Println(scenario.Name)
		}
		return 0
	}
	if *listCoverage {
		if strings.TrimSpace(*prompt) != "" || strings.TrimSpace(*promptFile) != "" {
			fmt.Fprintln(os.Stderr, "coverage: --list-coverage cannot be combined with --prompt or --prompt-file")
			return 64
		}
		scenarios, err := selectedEvalScenarios(*suite, *scenarioCSV, "", "", "", "", 1, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
			return 64
		}
		printScenarioCoverage(os.Stdout, scenarios, gates, *qualityProfile)
		return 0
	}
	tracePath := strings.TrimSpace(*traceFile)
	traceName := strings.TrimSpace(*adHocName)
	if tracePath == "" && strings.TrimSpace(*adHocSessionID) != "" && strings.TrimSpace(*prompt) == "" && strings.TrimSpace(*promptFile) == "" {
		resolved, err := resolveSessionTracePath(strings.TrimSpace(*adHocSessionID), strings.TrimSpace(*sessionStateRoot), strings.TrimSpace(*repoRoot))
		if err != nil {
			fmt.Fprintf(os.Stderr, "session-id: %v\n", err)
			return 64
		}
		tracePath = resolved
		if !flagWasSet(fs, "name") || traceName == "" || traceName == "adhoc" {
			traceName = strings.TrimSpace(*adHocSessionID)
		}
	}
	if tracePath != "" {
		traceWorkspaceValue := strings.TrimSpace(*traceWorkspace)
		if traceWorkspaceValue == "" {
			inferred, err := traceWorkspaceFromSessionMetadata(filepath.Dir(tracePath))
			if err != nil {
				fmt.Fprintf(os.Stderr, "trace-file: %v\n", err)
				return 64
			}
			traceWorkspaceValue = inferred
		}
		var scenarioForTrace *agenteval.BatchScenario
		if strings.TrimSpace(*suite) != "" || strings.TrimSpace(*scenarioCSV) != "" {
			scenarios, err := selectedEvalScenarios(*suite, *scenarioCSV, "", "", "", "", 1, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "trace-file: %v\n", err)
				return 64
			}
			if len(scenarios) != 1 {
				fmt.Fprintf(os.Stderr, "trace-file: --suite/--scenario must select exactly one scenario when applying expectations to an existing trace; selected %d\n", len(scenarios))
				return 64
			}
			scenario := scenarios[0]
			scenarioForTrace = &scenario
			if !flagWasSet(fs, "name") || traceName == "" || traceName == "adhoc" {
				traceName = scenario.Name
			}
		}
		res, err := agenteval.WriteTraceDebugArtifacts(agenteval.TraceDebugOptions{
			TracePath:    tracePath,
			OutputDir:    strings.TrimSpace(*traceOutputDir),
			Name:         traceName,
			Scenario:     scenarioForTrace,
			WorkspaceDir: traceWorkspaceValue,
		})
		if err != nil && strings.TrimSpace(res.BatchScenario) == "" {
			fmt.Fprintf(os.Stderr, "trace-file: %v\n", err)
			return 64
		}
		traceGates := qualityGateConfigForTraceFile(gates, func(name string) bool {
			return flagWasSet(fs, name)
		})
		meta := evalJSONLMetadataFromConfig(*suite, *model, *providerLabel, *executor, *temperature, *topP, *maxTokens, *seed, *runtimeEvalMode, *runtimeTools, *runtimeAllTools, *runtimeMemory, *runtimeWeb, *runtimeBrowser, *traceDeltas, *runtimeMCPConfig, *timeout, *qualityProfile, traceGates)
		summary := summarizeBatchResults([]agenteval.BatchResult{res})
		gateFailures := qualityGateFailures(summary, traceGates)
		gatesPassed := qualityGatesPassedForJSONL(meta, gateFailures)
		if err := agenteval.UpdateDebugManifestQualityGates(res.DebugManifestPath, gatesPassed, gateFailures); err != nil {
			fmt.Fprintf(os.Stderr, "trace-file: %v\n", err)
			return 64
		}
		if *jsonl {
			printBatchResultJSONL(os.Stdout, meta, res)
			printBatchSummaryJSONL(os.Stdout, meta, summary, gateFailures)
		} else {
			printBatchResult(os.Stdout, res)
			printBatchQualityGates(os.Stdout, meta, summary, gateFailures)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "trace-file: %v\n", err)
			return 64
		}
		if !res.OK {
			return 1
		}
		if len(gateFailures) > 0 {
			fmt.Fprintln(os.Stderr, "quality gates failed:")
			for _, failure := range gateFailures {
				fmt.Fprintf(os.Stderr, "  - %s\n", failure)
			}
			return 1
		}
		return 0
	}
	scenarios, err := selectedEvalScenarios(*suite, *scenarioCSV, *prompt, *promptFile, *adHocName, *adHocSessionID, *adHocMaxTurns, *adHocVerify)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		return 64
	}
	if failures := qualityGatePreflightFailures(scenarios, gates); len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "quality gate preflight failed:")
		for _, failure := range failures {
			fmt.Fprintf(os.Stderr, "  - %s\n", failure)
		}
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
	jsonlMeta := evalJSONLMetadataFromConfig(*suite, *model, *providerLabel, *executor, *temperature, *topP, *maxTokens, *seed, *runtimeEvalMode, *runtimeTools, *runtimeAllTools, *runtimeMemory, *runtimeWeb, *runtimeBrowser, *traceDeltas, *runtimeMCPConfig, *timeout, *qualityProfile, gates)
	deferPassingCleanup := !*keepWorkspaces && hasQualityGateThresholds(jsonlMeta)
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
		CleanupPassingWorkspaces: !*keepWorkspaces && !deferPassingCleanup,
	}
	ctx := context.Background()
	var summary batchSummary
	var gateFailures []string
	if deferPassingCleanup {
		results := make([]agenteval.BatchResult, 0, len(scenarios))
		for _, scenario := range scenarios {
			res := runner.Run(ctx, scenario)
			results = append(results, res)
		}
		summary = summarizeBatchResults(results)
		gateFailures = qualityGateFailures(summary, gates)
		if len(gateFailures) == 0 {
			cleanupPassingBatchResults(results)
			summary = summarizeBatchResults(results)
		}
		for _, res := range results {
			if *jsonl {
				printBatchResultJSONL(os.Stdout, jsonlMeta, res)
			} else {
				printBatchResult(os.Stdout, res)
			}
		}
	} else {
		for _, scenario := range scenarios {
			res := runner.Run(ctx, scenario)
			summary.add(res)
			if *jsonl {
				printBatchResultJSONL(os.Stdout, jsonlMeta, res)
			} else {
				printBatchResult(os.Stdout, res)
			}
		}
		gateFailures = qualityGateFailures(summary, gates)
	}
	if *jsonl {
		printBatchSummaryJSONL(os.Stdout, jsonlMeta, summary, gateFailures)
	} else {
		printBatchSummary(os.Stdout, summary)
		printBatchQualityGates(os.Stdout, jsonlMeta, summary, gateFailures)
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
	MinPassRate                                    *float64
	MinCompletionRate                              *float64
	MinMemoryUpdateRate                            *float64
	MinLoopTurnCheckpointRate                      *float64
	MinLoopProtocolFeedRate                        *float64
	MinLoopProtocolCalibrationRequestRate          *float64
	MinLoopProtocolCalibrationRate                 *float64
	MinRuntimeSurfaceRate                          *float64
	MinTraceEventRate                              *float64
	MinSourceNetworkRate                           *float64
	MinSourceAccessVerifiedRate                    *float64
	MinExpectationCapabilityPassRate               *float64
	MinEachExpectationCapabilityPassRate           *float64
	MinExpectationDomainPassRate                   *float64
	MinEachExpectationDomainPassRate               *float64
	MinSessionSearchContextHitRate                 *float64
	MinSessionSearchMatchedTermsPerCall            *float64
	MinToolRepairSuccessRate                       *float64
	MinVerifierPassRate                            *float64
	MaxFocusedTaskErrorRate                        *float64
	MaxForcedNoToolsRate                           *float64
	MaxLoopGuardInterventionRate                   *float64
	MaxPlanErrorRate                               *float64
	MaxMemorySearchMissRate                        *float64
	MaxSourceDiscoveryOnlyRate                     *float64
	MaxSourceDynamicPartialRate                    *float64
	MaxSubagentErrorRate                           *float64
	MaxToolErrorRate                               *float64
	MaxToolContextTruncationRate                   *float64
	MaxToolResultTruncationRate                    *float64
	MaxAvgRuntimeErrors                            *float64
	MaxAvgContextCompactions                       *float64
	MaxAvgReactiveCompactions                      *float64
	MaxAvgContextRemovedMessages                   *float64
	MaxAvgContextSummaryBytes                      *float64
	MaxAvgContextSummaryMissing                    *float64
	MaxAvgContextSummaryEmpty                      *float64
	MaxAvgContextInjections                        *float64
	MaxAvgContextInjectionBytes                    *float64
	MaxAvgContextInjectionEstimatedTokens          *float64
	MaxAvgToolCalls                                *float64
	MaxAvgDurationMS                               *float64
	MaxAvgTotalTokens                              *float64
	MaxScenarioTotalTokens                         *float64
	MaxDebugBriefTagRates                          map[string]float64
	MinExpectationDomainSourceAccessVerifiedRates  map[string]float64
	MaxExpectationDomainAvgTotalTokens             map[string]float64
	MaxExpectationDomainAvgToolCalls               map[string]float64
	MaxExpectationDomainAvgRuntimeErrors           map[string]float64
	MaxExpectationDomainToolErrorRates             map[string]float64
	MaxExpectationDomainLoopGuardInterventionRates map[string]float64
	RequiredExpectationCapabilities                []string
	RequiredExpectationDomains                     []string
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
			Description: "general long-run stability gates for task completion, memory/session recovery, source repo workflows, tool recovery, delegation/plan errors, truncation, runtime errors, and token cost",
			Gates: qualityGateConfig{
				MinPassRate:                           float64Ptr(0.80),
				MinCompletionRate:                     float64Ptr(0.90),
				MinMemoryUpdateRate:                   float64Ptr(0.10),
				MinLoopTurnCheckpointRate:             float64Ptr(0.05),
				MinLoopProtocolFeedRate:               float64Ptr(0.05),
				MinLoopProtocolCalibrationRequestRate: float64Ptr(0.05),
				MinLoopProtocolCalibrationRate:        float64Ptr(0.05),
				MinExpectationCapabilityPassRate:      float64Ptr(0.80),
				MinEachExpectationCapabilityPassRate:  float64Ptr(0.50),
				MinExpectationDomainPassRate:          float64Ptr(0.80),
				MinEachExpectationDomainPassRate:      float64Ptr(0.50),
				MinSessionSearchContextHitRate:        float64Ptr(0.75),
				MinSessionSearchMatchedTermsPerCall:   float64Ptr(1.0),
				MinRuntimeSurfaceRate:                 float64Ptr(0.90),
				MinTraceEventRate:                     float64Ptr(0.90),
				MaxFocusedTaskErrorRate:               float64Ptr(0.10),
				MaxForcedNoToolsRate:                  float64Ptr(0.10),
				MaxLoopGuardInterventionRate:          float64Ptr(0.20),
				MaxPlanErrorRate:                      float64Ptr(0.05),
				MaxSubagentErrorRate:                  float64Ptr(0.10),
				MaxToolErrorRate:                      float64Ptr(0.08),
				MaxToolContextTruncationRate:          float64Ptr(0.30),
				MaxToolResultTruncationRate:           float64Ptr(0.20),
				MaxAvgRuntimeErrors:                   float64Ptr(0.20),
				MaxAvgReactiveCompactions:             float64Ptr(0.50),
				MaxAvgContextRemovedMessages:          float64Ptr(120),
				MaxAvgContextSummaryBytes:             float64Ptr(24000),
				MaxAvgContextSummaryMissing:           float64Ptr(0),
				MaxAvgContextSummaryEmpty:             float64Ptr(0),
				MaxAvgContextInjections:               float64Ptr(8),
				MaxAvgContextInjectionBytes:           float64Ptr(24000),
				MaxAvgContextInjectionEstimatedTokens: float64Ptr(6000),
				MaxAvgToolCalls:                       float64Ptr(14),
				MaxAvgDurationMS:                      float64Ptr(180000),
				MaxAvgTotalTokens:                     float64Ptr(120000),
				MaxScenarioTotalTokens:                float64Ptr(240000),
				RequiredExpectationCapabilities:       []string{"context_compaction", "delegation", "input_budget", "longrun_recovery", "loop_protocol", "memory", "plan", "research_checkpoint", "session", "session_schedule", "session_search", "skill", "skill_install", "source_repo", "trace", "verifier", "workspace"},
				RequiredExpectationDomains:            []string{"bittensor", "code_pr", "context_compaction", "longrun_recovery", "market", "memory", "schedule_automation", "session_recovery"},
				MaxDebugBriefTagRates: map[string]float64{
					"context_compaction:summary_empty":   0,
					"context_compaction:summary_missing": 0,
					"durable_completion":                 0,
					"empty_recall:no_recent_sessions":    0,
					"loop_protocol:calibration_backlog":  0,
					"loop_protocol:fixture":              0,
					"loop_protocol:setup_tool_overrun":   0,
					"loop_guard:forced_no_tools":         0,
					"recall:memory_no_topic_anchors":     0,
					"recall:no_context":                  0,
					"recall:no_matched_terms":            0,
					"recall:weak_context":                0,
					"recall:weak_matched_terms":          0,
					"source_repo:setup":                  0,
					"tool_budget:turn_overrun":           0,
					"tool_failure:unclassified":          0,
					"tool_repair:failed":                 0,
					"truncation:missing_artifact":        0,
					"verifier:abnormal":                  0,
					"verifier:failed":                    0,
					"verifier:not_run":                   0,
					"workspace_path:absolute":            0,
				},
			},
		},
		{
			Name:        "web-evidence",
			Description: "web and browser evidence gates for current-fact tasks, emphasizing verified SourceAccess, network/API evidence, low discovery-only output, and bounded cost",
			Gates: qualityGateConfig{
				MinPassRate:                           float64Ptr(0.80),
				MinCompletionRate:                     float64Ptr(0.90),
				MinExpectationCapabilityPassRate:      float64Ptr(0.80),
				MinEachExpectationCapabilityPassRate:  float64Ptr(0.50),
				MinExpectationDomainPassRate:          float64Ptr(0.80),
				MinEachExpectationDomainPassRate:      float64Ptr(0.50),
				MinRuntimeSurfaceRate:                 float64Ptr(0.90),
				MinTraceEventRate:                     float64Ptr(0.90),
				MinSourceNetworkRate:                  float64Ptr(0.50),
				MinSourceAccessVerifiedRate:           float64Ptr(0.90),
				MaxForcedNoToolsRate:                  float64Ptr(0.10),
				MaxFocusedTaskErrorRate:               float64Ptr(0.10),
				MaxLoopGuardInterventionRate:          float64Ptr(0.25),
				MaxSourceDiscoveryOnlyRate:            float64Ptr(0.15),
				MaxSourceDynamicPartialRate:           float64Ptr(0.20),
				MaxSubagentErrorRate:                  float64Ptr(0.10),
				MaxToolErrorRate:                      float64Ptr(0.10),
				MaxToolResultTruncationRate:           float64Ptr(0.25),
				MaxAvgRuntimeErrors:                   float64Ptr(0.20),
				MaxAvgContextRemovedMessages:          float64Ptr(80),
				MaxAvgContextSummaryBytes:             float64Ptr(20000),
				MaxAvgContextSummaryMissing:           float64Ptr(0),
				MaxAvgContextSummaryEmpty:             float64Ptr(0),
				MaxAvgContextInjections:               float64Ptr(6),
				MaxAvgContextInjectionBytes:           float64Ptr(18000),
				MaxAvgContextInjectionEstimatedTokens: float64Ptr(4500),
				MaxAvgToolCalls:                       float64Ptr(18),
				MaxAvgDurationMS:                      float64Ptr(240000),
				MaxAvgTotalTokens:                     float64Ptr(120000),
				MaxScenarioTotalTokens:                float64Ptr(240000),
				RequiredExpectationCapabilities:       []string{"browser", "delegated_source_evidence", "source_access", "web"},
				RequiredExpectationDomains:            []string{"web_evidence"},
				MinExpectationDomainSourceAccessVerifiedRates: map[string]float64{
					"web_evidence": 0.90,
				},
				MaxExpectationDomainAvgTotalTokens: map[string]float64{
					"web_evidence": 120000,
				},
				MaxExpectationDomainAvgToolCalls: map[string]float64{
					"web_evidence": 18,
				},
				MaxExpectationDomainAvgRuntimeErrors: map[string]float64{
					"web_evidence": 0.20,
				},
				MaxExpectationDomainToolErrorRates: map[string]float64{
					"web_evidence": 0.10,
				},
				MaxExpectationDomainLoopGuardInterventionRates: map[string]float64{
					"web_evidence": 0.25,
				},
				MaxDebugBriefTagRates: map[string]float64{
					"browser_network:unread_refs":                 0,
					"browser_scroll:stuck_without_network":        0,
					"context_compaction:summary_empty":            0,
					"context_compaction:summary_missing":          0,
					"source_discovery_only_all":                   0,
					"source_dynamic_without_decision":             0,
					"source_dynamic_without_network":              0,
					"source_network:missing_response_diagnostics": 0,
					"source_network:partial_read":                 0,
					"source_unverified_all":                       0,
					"research_checkpoint:no_external_evidence":    0,
					"truncation:missing_artifact":                 0,
				},
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
	add("min-loop-turn-checkpoint-rate", g.MinLoopTurnCheckpointRate)
	add("min-loop-protocol-feed-rate", g.MinLoopProtocolFeedRate)
	add("min-loop-protocol-calibration-request-rate", g.MinLoopProtocolCalibrationRequestRate)
	add("min-loop-protocol-calibration-rate", g.MinLoopProtocolCalibrationRate)
	add("min-runtime-surface-rate", g.MinRuntimeSurfaceRate)
	add("min-trace-event-rate", g.MinTraceEventRate)
	add("min-source-network-rate", g.MinSourceNetworkRate)
	add("min-source-access-verified-rate", g.MinSourceAccessVerifiedRate)
	add("min-expectation-capability-pass-rate", g.MinExpectationCapabilityPassRate)
	add("min-each-expectation-capability-pass-rate", g.MinEachExpectationCapabilityPassRate)
	add("min-expectation-domain-pass-rate", g.MinExpectationDomainPassRate)
	add("min-each-expectation-domain-pass-rate", g.MinEachExpectationDomainPassRate)
	add("min-session-search-context-hit-rate", g.MinSessionSearchContextHitRate)
	add("min-session-search-matched-terms-per-call", g.MinSessionSearchMatchedTermsPerCall)
	add("min-tool-repair-success-rate", g.MinToolRepairSuccessRate)
	add("min-verifier-pass-rate", g.MinVerifierPassRate)
	add("max-focused-task-error-rate", g.MaxFocusedTaskErrorRate)
	add("max-forced-no-tools-rate", g.MaxForcedNoToolsRate)
	add("max-loop-guard-intervention-rate", g.MaxLoopGuardInterventionRate)
	add("max-plan-error-rate", g.MaxPlanErrorRate)
	add("max-memory-search-miss-rate", g.MaxMemorySearchMissRate)
	add("max-source-discovery-only-rate", g.MaxSourceDiscoveryOnlyRate)
	add("max-source-dynamic-partial-rate", g.MaxSourceDynamicPartialRate)
	add("max-subagent-error-rate", g.MaxSubagentErrorRate)
	add("max-tool-error-rate", g.MaxToolErrorRate)
	add("max-tool-context-truncation-rate", g.MaxToolContextTruncationRate)
	add("max-tool-result-truncation-rate", g.MaxToolResultTruncationRate)
	add("max-avg-runtime-errors", g.MaxAvgRuntimeErrors)
	add("max-avg-context-compactions", g.MaxAvgContextCompactions)
	add("max-avg-reactive-context-compactions", g.MaxAvgReactiveCompactions)
	add("max-avg-context-removed-messages", g.MaxAvgContextRemovedMessages)
	add("max-avg-context-summary-bytes", g.MaxAvgContextSummaryBytes)
	add("max-avg-context-summary-missing", g.MaxAvgContextSummaryMissing)
	add("max-avg-context-summary-empty", g.MaxAvgContextSummaryEmpty)
	add("max-avg-context-injections", g.MaxAvgContextInjections)
	add("max-avg-context-injection-bytes", g.MaxAvgContextInjectionBytes)
	add("max-avg-context-injection-estimated-tokens", g.MaxAvgContextInjectionEstimatedTokens)
	add("max-avg-tool-calls", g.MaxAvgToolCalls)
	add("max-avg-duration-ms", g.MaxAvgDurationMS)
	add("max-avg-total-tokens", g.MaxAvgTotalTokens)
	add("max-scenario-total-tokens", g.MaxScenarioTotalTokens)
	for _, tag := range sortedFloatMapKeys(g.MaxDebugBriefTagRates) {
		value := g.MaxDebugBriefTagRates[tag]
		if value < 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("max-debug-brief-tag-rate=%s=%s", tag, formatGateFloat(value)))
	}
	addMap := func(name string, values map[string]float64) {
		for _, key := range sortedFloatMapKeys(values) {
			value := values[key]
			if value < 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s=%s=%s", name, key, formatGateFloat(value)))
		}
	}
	addMap("min-expectation-domain-source-access-verified-rate", g.MinExpectationDomainSourceAccessVerifiedRates)
	addMap("max-expectation-domain-avg-total-tokens", g.MaxExpectationDomainAvgTotalTokens)
	addMap("max-expectation-domain-avg-tool-calls", g.MaxExpectationDomainAvgToolCalls)
	addMap("max-expectation-domain-avg-runtime-errors", g.MaxExpectationDomainAvgRuntimeErrors)
	addMap("max-expectation-domain-tool-error-rate", g.MaxExpectationDomainToolErrorRates)
	addMap("max-expectation-domain-loop-guard-intervention-rate", g.MaxExpectationDomainLoopGuardInterventionRates)
	if len(g.RequiredExpectationCapabilities) > 0 {
		lines = append(lines, fmt.Sprintf("require-expectation-capability=%s", strings.Join(g.RequiredExpectationCapabilities, ",")))
	}
	if len(g.RequiredExpectationDomains) > 0 {
		lines = append(lines, fmt.Sprintf("require-expectation-domain=%s", strings.Join(g.RequiredExpectationDomains, ",")))
	}
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
	apply("min-loop-turn-checkpoint-rate", &g.MinLoopTurnCheckpointRate, profileConfig.MinLoopTurnCheckpointRate)
	apply("min-loop-protocol-feed-rate", &g.MinLoopProtocolFeedRate, profileConfig.MinLoopProtocolFeedRate)
	apply("min-loop-protocol-calibration-request-rate", &g.MinLoopProtocolCalibrationRequestRate, profileConfig.MinLoopProtocolCalibrationRequestRate)
	apply("min-loop-protocol-calibration-rate", &g.MinLoopProtocolCalibrationRate, profileConfig.MinLoopProtocolCalibrationRate)
	apply("min-runtime-surface-rate", &g.MinRuntimeSurfaceRate, profileConfig.MinRuntimeSurfaceRate)
	apply("min-trace-event-rate", &g.MinTraceEventRate, profileConfig.MinTraceEventRate)
	apply("min-source-network-rate", &g.MinSourceNetworkRate, profileConfig.MinSourceNetworkRate)
	apply("min-source-access-verified-rate", &g.MinSourceAccessVerifiedRate, profileConfig.MinSourceAccessVerifiedRate)
	apply("min-expectation-capability-pass-rate", &g.MinExpectationCapabilityPassRate, profileConfig.MinExpectationCapabilityPassRate)
	apply("min-each-expectation-capability-pass-rate", &g.MinEachExpectationCapabilityPassRate, profileConfig.MinEachExpectationCapabilityPassRate)
	apply("min-expectation-domain-pass-rate", &g.MinExpectationDomainPassRate, profileConfig.MinExpectationDomainPassRate)
	apply("min-each-expectation-domain-pass-rate", &g.MinEachExpectationDomainPassRate, profileConfig.MinEachExpectationDomainPassRate)
	apply("min-session-search-context-hit-rate", &g.MinSessionSearchContextHitRate, profileConfig.MinSessionSearchContextHitRate)
	apply("min-session-search-matched-terms-per-call", &g.MinSessionSearchMatchedTermsPerCall, profileConfig.MinSessionSearchMatchedTermsPerCall)
	apply("min-tool-repair-success-rate", &g.MinToolRepairSuccessRate, profileConfig.MinToolRepairSuccessRate)
	apply("min-verifier-pass-rate", &g.MinVerifierPassRate, profileConfig.MinVerifierPassRate)
	apply("max-focused-task-error-rate", &g.MaxFocusedTaskErrorRate, profileConfig.MaxFocusedTaskErrorRate)
	apply("max-forced-no-tools-rate", &g.MaxForcedNoToolsRate, profileConfig.MaxForcedNoToolsRate)
	apply("max-loop-guard-intervention-rate", &g.MaxLoopGuardInterventionRate, profileConfig.MaxLoopGuardInterventionRate)
	apply("max-plan-error-rate", &g.MaxPlanErrorRate, profileConfig.MaxPlanErrorRate)
	apply("max-memory-search-miss-rate", &g.MaxMemorySearchMissRate, profileConfig.MaxMemorySearchMissRate)
	apply("max-source-discovery-only-rate", &g.MaxSourceDiscoveryOnlyRate, profileConfig.MaxSourceDiscoveryOnlyRate)
	apply("max-source-dynamic-partial-rate", &g.MaxSourceDynamicPartialRate, profileConfig.MaxSourceDynamicPartialRate)
	apply("max-subagent-error-rate", &g.MaxSubagentErrorRate, profileConfig.MaxSubagentErrorRate)
	apply("max-tool-error-rate", &g.MaxToolErrorRate, profileConfig.MaxToolErrorRate)
	apply("max-tool-context-truncation-rate", &g.MaxToolContextTruncationRate, profileConfig.MaxToolContextTruncationRate)
	apply("max-tool-result-truncation-rate", &g.MaxToolResultTruncationRate, profileConfig.MaxToolResultTruncationRate)
	apply("max-avg-runtime-errors", &g.MaxAvgRuntimeErrors, profileConfig.MaxAvgRuntimeErrors)
	apply("max-avg-context-compactions", &g.MaxAvgContextCompactions, profileConfig.MaxAvgContextCompactions)
	apply("max-avg-reactive-context-compactions", &g.MaxAvgReactiveCompactions, profileConfig.MaxAvgReactiveCompactions)
	apply("max-avg-context-removed-messages", &g.MaxAvgContextRemovedMessages, profileConfig.MaxAvgContextRemovedMessages)
	apply("max-avg-context-summary-bytes", &g.MaxAvgContextSummaryBytes, profileConfig.MaxAvgContextSummaryBytes)
	apply("max-avg-context-summary-missing", &g.MaxAvgContextSummaryMissing, profileConfig.MaxAvgContextSummaryMissing)
	apply("max-avg-context-summary-empty", &g.MaxAvgContextSummaryEmpty, profileConfig.MaxAvgContextSummaryEmpty)
	apply("max-avg-context-injections", &g.MaxAvgContextInjections, profileConfig.MaxAvgContextInjections)
	apply("max-avg-context-injection-bytes", &g.MaxAvgContextInjectionBytes, profileConfig.MaxAvgContextInjectionBytes)
	apply("max-avg-context-injection-estimated-tokens", &g.MaxAvgContextInjectionEstimatedTokens, profileConfig.MaxAvgContextInjectionEstimatedTokens)
	apply("max-avg-tool-calls", &g.MaxAvgToolCalls, profileConfig.MaxAvgToolCalls)
	apply("max-avg-duration-ms", &g.MaxAvgDurationMS, profileConfig.MaxAvgDurationMS)
	apply("max-avg-total-tokens", &g.MaxAvgTotalTokens, profileConfig.MaxAvgTotalTokens)
	apply("max-scenario-total-tokens", &g.MaxScenarioTotalTokens, profileConfig.MaxScenarioTotalTokens)
	if len(profileConfig.MaxDebugBriefTagRates) > 0 {
		profileTags := cloneStringFloatMap(profileConfig.MaxDebugBriefTagRates)
		if flagSet != nil && flagSet("max-debug-brief-tag-rate") {
			for tag, threshold := range g.MaxDebugBriefTagRates {
				profileTags[tag] = threshold
			}
		}
		g.MaxDebugBriefTagRates = profileTags
	}
	applyMap := func(name string, dst *map[string]float64, src map[string]float64) {
		if len(src) == 0 {
			return
		}
		profileValues := cloneStringFloatMap(src)
		if flagSet != nil && flagSet(name) {
			for key, threshold := range *dst {
				profileValues[key] = threshold
			}
		}
		*dst = profileValues
	}
	applyMap("min-expectation-domain-source-access-verified-rate", &g.MinExpectationDomainSourceAccessVerifiedRates, profileConfig.MinExpectationDomainSourceAccessVerifiedRates)
	applyMap("max-expectation-domain-avg-total-tokens", &g.MaxExpectationDomainAvgTotalTokens, profileConfig.MaxExpectationDomainAvgTotalTokens)
	applyMap("max-expectation-domain-avg-tool-calls", &g.MaxExpectationDomainAvgToolCalls, profileConfig.MaxExpectationDomainAvgToolCalls)
	applyMap("max-expectation-domain-avg-runtime-errors", &g.MaxExpectationDomainAvgRuntimeErrors, profileConfig.MaxExpectationDomainAvgRuntimeErrors)
	applyMap("max-expectation-domain-tool-error-rate", &g.MaxExpectationDomainToolErrorRates, profileConfig.MaxExpectationDomainToolErrorRates)
	applyMap("max-expectation-domain-loop-guard-intervention-rate", &g.MaxExpectationDomainLoopGuardInterventionRates, profileConfig.MaxExpectationDomainLoopGuardInterventionRates)
	if len(profileConfig.RequiredExpectationCapabilities) > 0 {
		profileCapabilities := cloneStringSlice(profileConfig.RequiredExpectationCapabilities)
		if flagSet != nil && flagSet("require-expectation-capability") {
			profileCapabilities = append(profileCapabilities, g.RequiredExpectationCapabilities...)
		}
		g.RequiredExpectationCapabilities = uniqueSortedStrings(profileCapabilities)
	}
	if len(profileConfig.RequiredExpectationDomains) > 0 {
		profileDomains := cloneStringSlice(profileConfig.RequiredExpectationDomains)
		if flagSet != nil && flagSet("require-expectation-domain") {
			profileDomains = append(profileDomains, g.RequiredExpectationDomains...)
		}
		g.RequiredExpectationDomains = uniqueSortedStrings(profileDomains)
	}
	return nil
}

func qualityGateConfigForTraceFile(g qualityGateConfig, flagSet func(name string) bool) qualityGateConfig {
	out := g
	if flagSet == nil || !flagSet("require-expectation-capability") {
		out.RequiredExpectationCapabilities = nil
	}
	if flagSet == nil || !flagSet("require-expectation-domain") {
		out.RequiredExpectationDomains = nil
	}
	if flagSet == nil || !flagSet("min-expectation-capability-pass-rate") {
		out.MinExpectationCapabilityPassRate = nil
	}
	if flagSet == nil || !flagSet("min-each-expectation-capability-pass-rate") {
		out.MinEachExpectationCapabilityPassRate = nil
	}
	if flagSet == nil || !flagSet("min-expectation-domain-pass-rate") {
		out.MinExpectationDomainPassRate = nil
	}
	if flagSet == nil || !flagSet("min-each-expectation-domain-pass-rate") {
		out.MinEachExpectationDomainPassRate = nil
	}
	if flagSet == nil || !flagSet("min-expectation-domain-source-access-verified-rate") {
		out.MinExpectationDomainSourceAccessVerifiedRates = nil
	}
	if flagSet == nil || !flagSet("max-expectation-domain-avg-total-tokens") {
		out.MaxExpectationDomainAvgTotalTokens = nil
	}
	if flagSet == nil || !flagSet("max-expectation-domain-avg-tool-calls") {
		out.MaxExpectationDomainAvgToolCalls = nil
	}
	if flagSet == nil || !flagSet("max-expectation-domain-avg-runtime-errors") {
		out.MaxExpectationDomainAvgRuntimeErrors = nil
	}
	if flagSet == nil || !flagSet("max-expectation-domain-tool-error-rate") {
		out.MaxExpectationDomainToolErrorRates = nil
	}
	if flagSet == nil || !flagSet("max-expectation-domain-loop-guard-intervention-rate") {
		out.MaxExpectationDomainLoopGuardInterventionRates = nil
	}
	return out
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
	Total                                   int
	Passed                                  int
	Failed                                  int
	Duration                                time.Duration
	ToolCalls                               int
	ToolErrors                              int
	ToolRepaired                            int
	ToolNameCanonicalized                   int
	ToolRepairCalls                         int
	ToolRepairSucceeded                     int
	ToolRepairFailed                        int
	ToolRepairNotes                         int
	ToolRepairByKind                        map[string]int
	ToolRepairExamples                      []agenteval.ToolRepairExample
	ConversationRepairs                     int
	ConversationRepairMissingToolResults    int
	ConversationRepairDuplicateResults      int
	ConversationRepairUnexpectedResults     int
	ConversationRepairByKind                map[string]int
	ConversationRepairExamples              []batchConversationRepairExample
	ToolFailureByKind                       map[string]int
	ToolFailureExamples                     map[string][]agenteval.ToolFailureExample
	LoopGuardExamples                       []agenteval.LoopGuardExample
	RuntimeErrors                           int
	RuntimeErrorByKind                      map[string]int
	RuntimeErrorExamples                    map[string][]agenteval.RuntimeErrorExample
	RuntimeSurfaceScenarios                 int
	RuntimeSurfaceTools                     map[string]int
	RuntimeSurfaceCapabilities              map[string]int
	TaskStateScenarios                      int
	TaskStateByStatus                       map[string]int
	TaskStateByVerification                 map[string]int
	TaskStateByRequestMode                  map[string]int
	TaskStateByRequestSource                map[string]int
	TaskStateByScheduleKind                 map[string]int
	TaskStateChangedFiles                   int
	TaskStateAttemptedActions               int
	TaskStateFailedActions                  int
	TaskStateEvidence                       int
	LoopDecisions                           int
	LoopDecisionByKind                      map[string]int
	LoopDecisionByDecision                  map[string]int
	LoopDecisionExamples                    []agenteval.LoopDecision
	LoopTurnCheckpointScenarios             int
	LoopTurnCheckpoints                     int
	LoopTurnCheckpointExamples              []agenteval.LoopTurnCheckpoint
	LoopProtocolFeedScenarios               int
	LoopProtocolFeeds                       int
	LoopProtocolFeedByMode                  map[string]int
	LoopProtocolFeedExamples                []agenteval.LoopProtocolFeed
	LoopProtocolCalibrationRequestScenarios int
	LoopProtocolCalibrationRequests         int
	LoopProtocolCalibrationRequestExamples  []agenteval.LoopProtocolCalibration
	LoopProtocolCalibrationScenarios        int
	LoopProtocolCalibrations                int
	LoopProtocolCalibrationExamples         []agenteval.LoopProtocolCalibration
	ContextCompactions                      int
	ContextCompactionsReactive              int
	ContextCompactionRemoved                int
	ContextCompactionReducedBytes           int
	ContextCompactionSummary                int
	ContextCompactionSummaryMissing         int
	ContextCompactionSummaryEmpty           int
	ContextCompactionPolicyObserved         int
	ContextCompactionMaxPolicyPressure      int
	ContextCompactionExamples               []agenteval.ContextCompaction
	ContextInjections                       int
	ContextInjectionBySource                map[string]int
	ContextInjectionBytes                   int
	ContextInjectionEstimatedTokens         int
	ContextInjectionExamples                []agenteval.ContextInjection
	LoopGuardInterventions                  int
	ForcedNoTools                           int
	SourceAccessResults                     int
	SourceAccessVerified                    int
	SourceAccessDiscoveryOnly               int
	SourceAccessNetwork                     int
	SourceAccessDynamicPartial              int
	SourceAccessExamples                    []agenteval.SourceAccessExample
	BrowserScrollExamples                   []agenteval.BrowserScrollExample
	BrowserNetworkExamples                  []agenteval.BrowserNetworkSearchExample
	MemoryUpdates                           int
	MemoryUpdateAdd                         int
	MemoryUpdateReplace                     int
	MemoryUpdateRemove                      int
	MemorySearchCalls                       int
	MemorySearchMisses                      int
	MemoryUpdateExamples                    []agenteval.MemoryUpdateExample
	MemorySearchMissExamples                []agenteval.MemorySearchMissExample
	SessionSearchCalls                      int
	SessionSearchResults                    int
	SessionSearchContextHits                int
	SessionSearchMatchedTerms               int
	SessionSearchRecent                     int
	SessionSearchExamples                   []agenteval.SessionSearchExample
	ToolDurationMS                          int64
	ToolContextTruncated                    int
	ToolContextOmittedBytes                 int
	ToolArgsTruncated                       int
	ToolArgsOmittedBytes                    int
	ToolResultsTruncated                    int
	ToolResultsOmittedBytes                 int
	ToolResultArtifacts                     int
	ToolResultMissingArtifacts              int
	ToolContextArtifacts                    int
	ToolContextMissingArtifacts             int
	ToolTruncationExamples                  []agenteval.ToolTruncationExample
	VerifierRuns                            int
	VerifierPassed                          int
	VerifierFailed                          int
	VerifierOutputTruncated                 int
	VerifierOutputOmittedBytes              int
	TraceSchemaVersions                     map[int]int
	TraceEventScenarios                     int
	TraceEvents                             int
	TraceEventTypes                         map[string]int
	InputTokens                             int
	OutputTokens                            int
	MaxScenarioTotalTokens                  int
	MaxScenarioTokenScenario                string
	EndCompleted                            int
	EndMaxTurns                             int
	EndErrors                               int
	EndCancelled                            int
	EndUnknown                              int
	FailureKinds                            map[string]int
	FailureExamples                         map[string][]batchFailureExample
	DebugBriefByTag                         map[string]int
	DebugBriefTagExamples                   map[string][]batchDebugBriefTagExample
	ExpectationScenarios                    int
	ExpectationSuites                       map[string]int
	ExpectationDomains                      map[string]int
	ExpectationDomainPass                   map[string]int
	ExpectationDomainFail                   map[string]int
	ExpectationDomainFailureExamples        map[string][]expectationDomainFailureExample
	ExpectationDomainRuntime                map[string]*expectationDomainRuntimeTotals
	ExpectationCapabilities                 map[string]int
	ExpectationCapabilityPass               map[string]int
	ExpectationCapabilityFail               map[string]int
	ExpectationCapabilityFailureExamples    map[string][]expectationCapabilityFailureExample
	ExpectationRequiredTools                map[string]int
	ExpectationSourceAccess                 map[string]int
	RemovedWorkspaces                       int
	CleanupErrors                           int

	// Delegation aggregates focused-task / subagent usage across all
	// scenarios in the batch. Zero-valued when no scenario used a
	// delegation tool — the JSONL emitter omits empty sub-maps so a
	// batch with no delegation activity produces a clean record.
	FocusedTaskCalls      int
	FocusedTaskByType     map[string]int
	FocusedTaskSources    map[string]int
	FocusedTaskErrors     int
	FocusedTaskIncomplete int
	SubagentCalls         int
	SubagentByMode        map[string]int
	SubagentSources       map[string]int
	SubagentErrors        int
	SubagentIncomplete    int

	// Plan aggregates persisted-plan tool usage across scenarios.
	// Zero-valued when no scenario used the plan tool.
	PlanCalls    int
	PlanByAction map[string]int
	PlanErrors   int
	PlanExamples []agenteval.PlanExample
}

func summarizeBatchResults(results []agenteval.BatchResult) batchSummary {
	var summary batchSummary
	for _, res := range results {
		summary.add(res)
	}
	return summary
}

func cleanupPassingBatchResults(results []agenteval.BatchResult) {
	for i := range results {
		if !results[i].OK || results[i].WorkspaceRemoved || strings.TrimSpace(results[i].Workspace) == "" {
			continue
		}
		if err := os.RemoveAll(results[i].Workspace); err != nil {
			results[i].CleanupError = err.Error()
			continue
		}
		results[i].WorkspaceRemoved = true
	}
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
	s.ToolRepairExamples = appendToolRepairExamples(s.ToolRepairExamples, res.ToolRepairExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.ConversationRepairs += len(res.ConversationRepairs)
	for _, repair := range res.ConversationRepairs {
		s.ConversationRepairMissingToolResults += repair.MissingToolResults
		s.ConversationRepairDuplicateResults += repair.DuplicateToolResults
		s.ConversationRepairUnexpectedResults += repair.UnexpectedToolResults
		kind := strings.TrimSpace(repair.FailureKind)
		if kind == "" {
			kind = "unknown"
		}
		if s.ConversationRepairByKind == nil {
			s.ConversationRepairByKind = map[string]int{}
		}
		s.ConversationRepairByKind[kind]++
	}
	s.ConversationRepairExamples = appendConversationRepairExamples(s.ConversationRepairExamples, res.ConversationRepairs, res.BatchScenario, batchSummaryExamplesPerKind)
	s.LoopGuardExamples = appendLoopGuardExamples(s.LoopGuardExamples, res.LoopGuardExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	for k, v := range res.ToolStats.ToolFailureByKind {
		if s.ToolFailureByKind == nil {
			s.ToolFailureByKind = map[string]int{}
		}
		s.ToolFailureByKind[k] += v
	}
	mergeToolFailureExamples(&s.ToolFailureExamples, res.ToolFailureExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	for k, v := range res.RuntimeErrorByKind {
		if s.RuntimeErrorByKind == nil {
			s.RuntimeErrorByKind = map[string]int{}
		}
		s.RuntimeErrorByKind[k] += v
		s.RuntimeErrors += v
	}
	mergeRuntimeErrorExamples(&s.RuntimeErrorExamples, res.RuntimeErrorExamples, res.BatchScenario, batchSummaryExamplesPerKind)
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
	if task := agenteval.CloneTaskStateSnapshotPtr(res.TaskState); task != nil {
		s.TaskStateScenarios++
		if status := strings.TrimSpace(task.Status); status != "" {
			if s.TaskStateByStatus == nil {
				s.TaskStateByStatus = map[string]int{}
			}
			s.TaskStateByStatus[status]++
		}
		if verification := strings.TrimSpace(task.VerificationState); verification != "" {
			if s.TaskStateByVerification == nil {
				s.TaskStateByVerification = map[string]int{}
			}
			s.TaskStateByVerification[verification]++
		}
		if mode := strings.TrimSpace(task.RequestMode); mode != "" {
			if s.TaskStateByRequestMode == nil {
				s.TaskStateByRequestMode = map[string]int{}
			}
			s.TaskStateByRequestMode[mode]++
		}
		if source := strings.TrimSpace(task.RequestSource); source != "" {
			if s.TaskStateByRequestSource == nil {
				s.TaskStateByRequestSource = map[string]int{}
			}
			s.TaskStateByRequestSource[source]++
		}
		if kind := strings.TrimSpace(task.ScheduleKind); kind != "" {
			if s.TaskStateByScheduleKind == nil {
				s.TaskStateByScheduleKind = map[string]int{}
			}
			s.TaskStateByScheduleKind[kind]++
		}
		s.TaskStateChangedFiles += len(task.ChangedFiles)
		s.TaskStateAttemptedActions += len(task.AttemptedActions)
		s.TaskStateFailedActions += len(task.FailedActions)
		s.TaskStateEvidence += len(task.Evidence)
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
	s.LoopDecisionExamples = appendLoopDecisionExamples(s.LoopDecisionExamples, res.LoopDecisionStats.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	if res.LoopTurnCheckpoints.Count > 0 {
		s.LoopTurnCheckpointScenarios++
	}
	s.LoopTurnCheckpoints += res.LoopTurnCheckpoints.Count
	s.LoopTurnCheckpointExamples = appendLoopTurnCheckpointExamples(s.LoopTurnCheckpointExamples, res.LoopTurnCheckpoints.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	if res.LoopProtocolFeeds.Count > 0 {
		s.LoopProtocolFeedScenarios++
	}
	s.LoopProtocolFeeds += res.LoopProtocolFeeds.Count
	for k, v := range res.LoopProtocolFeeds.ByMode {
		if s.LoopProtocolFeedByMode == nil {
			s.LoopProtocolFeedByMode = map[string]int{}
		}
		s.LoopProtocolFeedByMode[k] += v
	}
	s.LoopProtocolFeedExamples = appendLoopProtocolFeedExamples(s.LoopProtocolFeedExamples, res.LoopProtocolFeeds.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	if res.LoopProtocolCalibrationRequests.Count > 0 {
		s.LoopProtocolCalibrationRequestScenarios++
	}
	s.LoopProtocolCalibrationRequests += res.LoopProtocolCalibrationRequests.Count
	s.LoopProtocolCalibrationRequestExamples = appendLoopProtocolCalibrationExamples(s.LoopProtocolCalibrationRequestExamples, res.LoopProtocolCalibrationRequests.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	if res.LoopProtocolCalibrations.Count > 0 {
		s.LoopProtocolCalibrationScenarios++
	}
	s.LoopProtocolCalibrations += res.LoopProtocolCalibrations.Count
	s.LoopProtocolCalibrationExamples = appendLoopProtocolCalibrationExamples(s.LoopProtocolCalibrationExamples, res.LoopProtocolCalibrations.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.ContextCompactions += res.ContextCompactions.Count
	s.ContextCompactionsReactive += res.ContextCompactions.Reactive
	s.ContextCompactionRemoved += res.ContextCompactions.RemovedMessages
	s.ContextCompactionReducedBytes += res.ContextCompactions.ReducedBytes
	s.ContextCompactionSummary += res.ContextCompactions.SummaryBytes
	s.ContextCompactionSummaryMissing += res.ContextCompactions.SummaryMissing
	s.ContextCompactionSummaryEmpty += res.ContextCompactions.SummaryEmpty
	s.ContextCompactionPolicyObserved += res.ContextCompactions.PolicyObserved
	if res.ContextCompactions.MaxPolicyPressurePercent > s.ContextCompactionMaxPolicyPressure {
		s.ContextCompactionMaxPolicyPressure = res.ContextCompactions.MaxPolicyPressurePercent
	}
	s.ContextCompactionExamples = appendContextCompactionExamples(s.ContextCompactionExamples, res.ContextCompactions.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.ContextInjections += res.ContextInjections.Count
	for k, v := range res.ContextInjections.BySource {
		if s.ContextInjectionBySource == nil {
			s.ContextInjectionBySource = map[string]int{}
		}
		s.ContextInjectionBySource[k] += v
	}
	s.ContextInjectionBytes += res.ContextInjections.Bytes
	s.ContextInjectionEstimatedTokens += res.ContextInjections.EstimatedTokens
	s.ContextInjectionExamples = appendContextInjectionExamples(s.ContextInjectionExamples, res.ContextInjections.Examples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.LoopGuardInterventions += res.ToolStats.LoopGuardInterventions
	s.ForcedNoTools += res.ToolStats.ForcedNoTools
	s.SourceAccessResults += res.ToolStats.SourceAccessResults
	s.SourceAccessVerified += res.ToolStats.SourceAccessVerified
	s.SourceAccessDiscoveryOnly += res.ToolStats.SourceAccessDiscoveryOnly
	s.SourceAccessNetwork += res.ToolStats.SourceAccessNetwork
	s.SourceAccessDynamicPartial += res.ToolStats.SourceAccessDynamicPartial
	s.SourceAccessExamples = appendSourceAccessExamples(s.SourceAccessExamples, res.SourceAccessExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.BrowserScrollExamples = appendBrowserScrollExamples(s.BrowserScrollExamples, res.BrowserScrollExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.BrowserNetworkExamples = appendBrowserNetworkExamples(s.BrowserNetworkExamples, res.BrowserNetworkExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.MemoryUpdates += res.ToolStats.MemoryUpdates
	s.MemoryUpdateAdd += res.ToolStats.MemoryUpdateAdd
	s.MemoryUpdateReplace += res.ToolStats.MemoryUpdateReplace
	s.MemoryUpdateRemove += res.ToolStats.MemoryUpdateRemove
	s.MemorySearchCalls += res.ToolStats.MemorySearchCalls
	s.MemorySearchMisses += res.ToolStats.MemorySearchMisses
	s.MemoryUpdateExamples = appendMemoryUpdateExamples(s.MemoryUpdateExamples, res.MemoryUpdateExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.MemorySearchMissExamples = appendMemorySearchMissExamples(s.MemorySearchMissExamples, res.MemorySearchMissExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.SessionSearchCalls += res.ToolStats.SessionSearchCalls
	s.SessionSearchResults += res.ToolStats.SessionSearchResults
	s.SessionSearchContextHits += res.ToolStats.SessionSearchContextHits
	s.SessionSearchMatchedTerms += res.ToolStats.SessionSearchMatchedTerms
	s.SessionSearchRecent += res.ToolStats.SessionSearchRecent
	s.SessionSearchExamples = appendSessionSearchExamples(s.SessionSearchExamples, res.SessionSearchExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	s.ToolDurationMS += res.ToolStats.ToolDurationMS
	s.ToolContextTruncated += max(res.ToolStats.ToolContextTruncated, res.ToolTruncation.ContextTruncated)
	s.ToolContextOmittedBytes += max(res.ToolStats.ToolContextOmittedBytes, res.ToolTruncation.ContextOmittedBytes)
	s.ToolArgsTruncated += res.ToolTruncation.ArgsTruncated
	s.ToolArgsOmittedBytes += res.ToolTruncation.ArgsOmittedBytes
	s.ToolResultsTruncated += res.ToolTruncation.ResultsTruncated
	s.ToolResultsOmittedBytes += res.ToolTruncation.ResultsOmittedBytes
	s.ToolResultArtifacts += res.ToolTruncation.ResultArtifacts
	s.ToolResultMissingArtifacts += toolResultMissingArtifacts(res.ToolTruncation)
	s.ToolContextArtifacts += res.ToolTruncation.ContextArtifacts
	s.ToolContextMissingArtifacts += res.ToolTruncation.ContextMissingArtifacts
	s.ToolTruncationExamples = appendToolTruncationExamples(s.ToolTruncationExamples, res.ToolTruncationExamples, res.BatchScenario, batchSummaryExamplesPerKind)
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
	if res.TraceEvents > 0 {
		s.TraceEventScenarios++
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
	totalTokens := res.Usage.InputTokens + res.Usage.OutputTokens
	if totalTokens > s.MaxScenarioTotalTokens {
		s.MaxScenarioTotalTokens = totalTokens
		s.MaxScenarioTokenScenario = res.BatchScenario
	}
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
	s.FocusedTaskIncomplete += d.FocusedTaskIncomplete
	for k, v := range d.FocusedTaskByType {
		if s.FocusedTaskByType == nil {
			s.FocusedTaskByType = map[string]int{}
		}
		s.FocusedTaskByType[k] += v
	}
	for k, v := range d.FocusedTaskSourceFindingsByType {
		if s.FocusedTaskSources == nil {
			s.FocusedTaskSources = map[string]int{}
		}
		s.FocusedTaskSources[k] += v
	}
	s.SubagentCalls += d.SubagentCalls
	s.SubagentErrors += d.SubagentErrors
	s.SubagentIncomplete += d.SubagentIncomplete
	for k, v := range d.SubagentByMode {
		if s.SubagentByMode == nil {
			s.SubagentByMode = map[string]int{}
		}
		s.SubagentByMode[k] += v
	}
	for k, v := range d.SubagentSourceEvidenceByMode {
		if s.SubagentSources == nil {
			s.SubagentSources = map[string]int{}
		}
		s.SubagentSources[k] += v
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
	s.PlanExamples = appendPlanExamples(s.PlanExamples, res.PlanExamples, res.BatchScenario, batchSummaryExamplesPerKind)
	for _, failure := range res.Failures {
		kind := failureKind(failure)
		if s.FailureKinds == nil {
			s.FailureKinds = map[string]int{}
		}
		s.FailureKinds[kind]++
		s.addFailureExample(kind, res, failure)
	}
	if brief := agenteval.BuildDebugBrief(res); brief != nil {
		if s.DebugBriefByTag == nil {
			s.DebugBriefByTag = map[string]int{}
		}
		for _, tag := range brief.Tags {
			s.DebugBriefByTag[tag]++
			s.addDebugBriefTagExample(tag, res)
		}
	}
	if res.Expectations != nil {
		s.addExpectations(res)
	}
}

func (s *batchSummary) addDebugBriefTagExample(tag string, res agenteval.BatchResult) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return
	}
	if s.DebugBriefTagExamples == nil {
		s.DebugBriefTagExamples = map[string][]batchDebugBriefTagExample{}
	}
	if len(s.DebugBriefTagExamples[tag]) >= batchSummaryExamplesPerKind {
		return
	}
	s.DebugBriefTagExamples[tag] = append(s.DebugBriefTagExamples[tag], batchDebugBriefTagExample{
		Scenario:          res.BatchScenario,
		FailureKinds:      failureKindsForResult(res.Failures),
		TracePath:         res.TracePath,
		TimelinePath:      retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved),
		DebugManifestPath: retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved),
	})
}

func (s *batchSummary) addFailureExample(kind string, res agenteval.BatchResult, failure string) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "unknown"
	}
	if s.FailureExamples == nil {
		s.FailureExamples = map[string][]batchFailureExample{}
	}
	if len(s.FailureExamples[kind]) >= batchSummaryExamplesPerKind {
		return
	}
	s.FailureExamples[kind] = append(s.FailureExamples[kind], batchFailureExample{
		Scenario:          res.BatchScenario,
		Failure:           compactFailureText(failure),
		TracePath:         res.TracePath,
		TimelinePath:      retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved),
		DebugManifestPath: retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved),
	})
}

func (s *batchSummary) addExpectations(res agenteval.BatchResult) {
	exp := *res.Expectations
	s.ExpectationScenarios++
	addCountMapValues(&s.ExpectationSuites, exp.Suites)
	addCountMapValues(&s.ExpectationDomains, exp.Domains)
	s.addExpectationDomainRuntime(exp.Domains, res)
	if res.OK {
		addCountMapValues(&s.ExpectationDomainPass, exp.Domains)
	} else {
		addCountMapValues(&s.ExpectationDomainFail, exp.Domains)
		s.addExpectationDomainFailureExamples(exp.Domains, res)
	}
	addCountMapValues(&s.ExpectationRequiredTools, expectationRequiredToolNames(exp))
	for _, req := range exp.RequiredSourceAccess {
		status := strings.TrimSpace(req.Status)
		if status == "" {
			status = "any"
		}
		addCountMapValue(&s.ExpectationSourceAccess, status)
	}
	keys := agenteval.ExpectationCapabilityNames(exp)
	addCountMapValues(&s.ExpectationCapabilities, keys)
	if res.OK {
		addCountMapValues(&s.ExpectationCapabilityPass, keys)
	} else {
		addCountMapValues(&s.ExpectationCapabilityFail, keys)
		s.addExpectationCapabilityFailureExamples(keys, res)
	}
}

func (s *batchSummary) addExpectationDomainRuntime(domains []string, res agenteval.BatchResult) {
	domains = uniqueSortedStrings(domains)
	if len(domains) == 0 {
		return
	}
	for _, domain := range domains {
		if s.ExpectationDomainRuntime == nil {
			s.ExpectationDomainRuntime = map[string]*expectationDomainRuntimeTotals{}
		}
		totals := s.ExpectationDomainRuntime[domain]
		if totals == nil {
			totals = &expectationDomainRuntimeTotals{}
			s.ExpectationDomainRuntime[domain] = totals
		}
		totals.Scenarios++
		if res.OK {
			totals.Passed++
		} else {
			totals.Failed++
		}
		totals.Duration += res.Duration
		totals.ToolCalls += res.ToolCalls
		totals.ToolErrors += res.ToolStats.ToolErrors
		totals.LoopGuardInterventions += res.ToolStats.LoopGuardInterventions
		totals.SourceAccessResults += res.ToolStats.SourceAccessResults
		totals.SourceAccessVerified += res.ToolStats.SourceAccessVerified
		totals.SourceAccessNetwork += res.ToolStats.SourceAccessNetwork
		totals.SourceAccessDiscoveryOnly += res.ToolStats.SourceAccessDiscoveryOnly
		totals.SourceAccessDynamicPartial += res.ToolStats.SourceAccessDynamicPartial
		totals.MemoryUpdates += res.ToolStats.MemoryUpdates
		for _, count := range res.RuntimeErrorByKind {
			totals.RuntimeErrors += count
		}
		totals.InputTokens += res.Usage.InputTokens
		totals.OutputTokens += res.Usage.OutputTokens
	}
}

func (s *batchSummary) addExpectationDomainFailureExamples(domains []string, res agenteval.BatchResult) {
	if len(domains) == 0 {
		return
	}
	brief := agenteval.BuildDebugBrief(res)
	var tags []string
	if brief != nil {
		tags = append([]string(nil), brief.Tags...)
		sort.Strings(tags)
	}
	failureKinds := failureKindsForResult(res.Failures)
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		if s.ExpectationDomainFailureExamples == nil {
			s.ExpectationDomainFailureExamples = map[string][]expectationDomainFailureExample{}
		}
		if len(s.ExpectationDomainFailureExamples[domain]) >= batchSummaryExamplesPerKind {
			continue
		}
		s.ExpectationDomainFailureExamples[domain] = append(s.ExpectationDomainFailureExamples[domain], expectationDomainFailureExample{
			Domain:            domain,
			Scenario:          res.BatchScenario,
			FailureKinds:      cloneStringIntMap(failureKinds),
			DebugBriefTags:    tags,
			TracePath:         res.TracePath,
			TimelinePath:      retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved),
			DebugManifestPath: retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved),
		})
	}
}

func (s *batchSummary) addExpectationCapabilityFailureExamples(capabilities []string, res agenteval.BatchResult) {
	if len(capabilities) == 0 {
		return
	}
	brief := agenteval.BuildDebugBrief(res)
	var tags []string
	if brief != nil {
		tags = append([]string(nil), brief.Tags...)
		sort.Strings(tags)
	}
	failureKinds := failureKindsForResult(res.Failures)
	for _, cap := range capabilities {
		if s.ExpectationCapabilityFailureExamples == nil {
			s.ExpectationCapabilityFailureExamples = map[string][]expectationCapabilityFailureExample{}
		}
		if len(s.ExpectationCapabilityFailureExamples[cap]) >= batchSummaryExamplesPerKind {
			continue
		}
		s.ExpectationCapabilityFailureExamples[cap] = append(s.ExpectationCapabilityFailureExamples[cap], expectationCapabilityFailureExample{
			Capability:        cap,
			Scenario:          res.BatchScenario,
			FailureKinds:      cloneStringIntMap(failureKinds),
			DebugBriefTags:    tags,
			TracePath:         res.TracePath,
			TimelinePath:      retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved),
			DebugManifestPath: retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved),
		})
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
	if len(exp.RequiredCommands) > 0 ||
		len(exp.RequiredCommandCounts) > 0 ||
		len(exp.RequiredCommandOrder) > 0 ||
		len(exp.RequiredCommandBeforeTool) > 0 ||
		len(exp.RequiredCommandAfterTool) > 0 {
		add("shell")
	}
	if len(exp.RequiredSessionSearch) > 0 || len(exp.RequiredRecentSessionSearch) > 0 {
		add(agent.SessionSearchToolName)
	}
	if len(exp.RequiredFocusedTaskCounts) > 0 || len(exp.RequiredFocusedTaskSourceCounts) > 0 {
		add(agent.FocusedTaskToolName)
	}
	if len(exp.RequiredSubagentModeCounts) > 0 || len(exp.RequiredSubagentSourceCounts) > 0 {
		add(agent.SubagentToolName)
	}
	for _, tool := range exp.RequiredTruncatedResults {
		add(tool)
	}
	for _, tool := range exp.RequiredResultArtifacts {
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

func batchResultExpectationCapabilityNames(res agenteval.BatchResult) []string {
	if res.Expectations == nil {
		return nil
	}
	return agenteval.ExpectationCapabilityNames(*res.Expectations)
}

func batchResultExpectationCapabilityOutcome(res agenteval.BatchResult, names []string) string {
	return agenteval.ExpectationCapabilityOutcome(res.OK, names)
}

func batchResultExpectationCapabilityPassedNames(res agenteval.BatchResult, names []string) []string {
	return agenteval.ExpectationCapabilityPassedNames(res.OK, names)
}

func batchResultExpectationCapabilityFailedNames(res agenteval.BatchResult, names []string) []string {
	return agenteval.ExpectationCapabilityFailedNames(res.OK, names)
}

func printBatchSummary(w io.Writer, s batchSummary) {
	resultMissingArtifacts := s.ToolResultMissingArtifacts
	if resultMissingArtifacts == 0 && s.ToolResultsTruncated > s.ToolResultArtifacts {
		resultMissingArtifacts = s.ToolResultsTruncated - s.ToolResultArtifacts
	}
	missingArtifacts := resultMissingArtifacts + s.ToolContextMissingArtifacts
	fmt.Fprintf(w, "SUMMARY scenarios=%d passed=%d failed=%d duration=%s avg_duration_ms=%.0f tools=%d errors=%d repaired=%d canonicalized=%d loop_guard=%d forced_no_tools=%d tool_ms=%d trunc=args:%d,results:%d,artifacts:%d,ctx_artifacts:%d,missing_artifacts:%d omitted=%d/%d verifier=run:%d,passed:%d,failed:%d,truncated:%d,omitted:%d tokens=%d/%d ends=completed:%d,max_turns:%d,error:%d,cancelled:%d,unknown:%d failure_kinds=%s removed_workspaces=%d cleanup_errors=%d",
		s.Total,
		s.Passed,
		s.Failed,
		s.Duration.Round(time.Millisecond),
		batchAverageInt64(s.Duration.Milliseconds(), s.Total),
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
		s.ToolContextArtifacts,
		missingArtifacts,
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
	fmt.Fprintf(w, " rates=pass:%s,completed:%s,memory_update:%s,memory_search_miss:%s,loop_turn_checkpoint:%s,loop_protocol_feed:%s,loop_protocol_calibration_request:%s,loop_protocol_calibration:%s,runtime_surface:%s,tool_error:%s,focused_task_error:%s,subagent_error:%s,plan_error:%s,repair_success:%s,verifier_pass:%s,evidence_verified:%s,source_network:%s,source_discovery:%s,source_dynamic_partial:%s avg_tools=%.1f avg_tokens=%.1f/%.1f",
		formatPercent(batchRatio(s.Passed, s.Total)),
		formatPercent(batchRatio(s.EndCompleted, s.Total)),
		formatPercent(batchRatio(s.MemoryUpdates, s.Total)),
		formatOptionalPercent(batchOptionalRatio(s.MemorySearchMisses, s.MemorySearchCalls)),
		formatPercent(batchRatio(s.LoopTurnCheckpointScenarios, s.Total)),
		formatPercent(batchRatio(s.LoopProtocolFeedScenarios, s.Total)),
		formatPercent(batchRatio(s.LoopProtocolCalibrationRequestScenarios, s.Total)),
		formatPercent(batchRatio(s.LoopProtocolCalibrationScenarios, s.Total)),
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
		batchAverage(s.ToolCalls, s.Total),
		batchAverage(s.InputTokens, s.Total),
		batchAverage(s.OutputTokens, s.Total),
	)
	fmt.Fprintf(w, " context_pressure=avg_compactions:%.2f,avg_reactive:%.2f,avg_removed:%.1f,avg_reduced_bytes:%.0f,avg_summary_bytes:%.0f,avg_summary_missing:%.2f,avg_summary_empty:%.2f,policy_observed:%d,max_policy_pressure:%d%%,avg_injections:%.2f,avg_injection_bytes:%.0f,avg_injection_tokens:%.0f,tool_ctx_trunc:%s",
		batchAverage(s.ContextCompactions, s.Total),
		batchAverage(s.ContextCompactionsReactive, s.Total),
		batchAverage(s.ContextCompactionRemoved, s.Total),
		batchAverage(s.ContextCompactionReducedBytes, s.Total),
		batchAverage(s.ContextCompactionSummary, s.Total),
		batchAverage(s.ContextCompactionSummaryMissing, s.Total),
		batchAverage(s.ContextCompactionSummaryEmpty, s.Total),
		s.ContextCompactionPolicyObserved,
		s.ContextCompactionMaxPolicyPressure,
		batchAverage(s.ContextInjections, s.Total),
		batchAverage(s.ContextInjectionBytes, s.Total),
		batchAverage(s.ContextInjectionEstimatedTokens, s.Total),
		formatOptionalPercent(batchOptionalRatio(s.ToolContextTruncated, s.ToolCalls)),
	)
	if s.MaxScenarioTotalTokens > 0 {
		if s.MaxScenarioTokenScenario != "" {
			fmt.Fprintf(w, " max_scenario_tokens=%d:%s", s.MaxScenarioTotalTokens, s.MaxScenarioTokenScenario)
		} else {
			fmt.Fprintf(w, " max_scenario_tokens=%d", s.MaxScenarioTotalTokens)
		}
	}
	if hasBatchRepairStats(s) {
		fmt.Fprintf(w, " repair_calls=%d,ok=%d,failed=%d", s.ToolRepairCalls, s.ToolRepairSucceeded, s.ToolRepairFailed)
	}
	if len(s.ToolRepairByKind) > 0 {
		fmt.Fprintf(w, " repair_kinds=%s", formatStringIntCounts(s.ToolRepairByKind))
	}
	if s.ConversationRepairs > 0 {
		fmt.Fprintf(w, " conversation_repairs=%d,missing_tool_results=%d", s.ConversationRepairs, s.ConversationRepairMissingToolResults)
		if s.ConversationRepairDuplicateResults > 0 {
			fmt.Fprintf(w, ",duplicate_tool_results=%d", s.ConversationRepairDuplicateResults)
		}
		if s.ConversationRepairUnexpectedResults > 0 {
			fmt.Fprintf(w, ",unexpected_tool_results=%d", s.ConversationRepairUnexpectedResults)
		}
		if len(s.ConversationRepairByKind) > 0 {
			fmt.Fprintf(w, " conversation_repair_kinds=%s", formatStringIntCounts(s.ConversationRepairByKind))
		}
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
	if s.MemorySearchCalls > 0 || s.MemorySearchMisses > 0 {
		fmt.Fprintf(w, " memory_search=calls:%d,misses:%d", s.MemorySearchCalls, s.MemorySearchMisses)
	}
	if hasBatchSessionSearchStats(s) {
		recent := ""
		if s.SessionSearchRecent > 0 {
			recent = fmt.Sprintf(",recent:%d", s.SessionSearchRecent)
		}
		fmt.Fprintf(w, " session_search=calls:%d,results:%d,context:%d,terms:%d%s,terms_per_call:%s",
			s.SessionSearchCalls,
			s.SessionSearchResults,
			s.SessionSearchContextHits,
			s.SessionSearchMatchedTerms,
			recent,
			formatOptionalNumber(batchOptionalRatio(s.SessionSearchMatchedTerms, s.SessionSearchCalls)),
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
	if s.TaskStateScenarios > 0 {
		fmt.Fprintf(w, " task_state=scenarios:%d,changed_files:%d,attempted_actions:%d,failed_actions:%d,evidence:%d",
			s.TaskStateScenarios,
			s.TaskStateChangedFiles,
			s.TaskStateAttemptedActions,
			s.TaskStateFailedActions,
			s.TaskStateEvidence,
		)
		if len(s.TaskStateByStatus) > 0 {
			fmt.Fprintf(w, " task_state_status=%s", formatStringIntCounts(s.TaskStateByStatus))
		}
		if len(s.TaskStateByVerification) > 0 {
			fmt.Fprintf(w, " task_state_verification=%s", formatStringIntCounts(s.TaskStateByVerification))
		}
		if len(s.TaskStateByRequestMode) > 0 {
			fmt.Fprintf(w, " task_state_request_modes=%s", formatStringIntCounts(s.TaskStateByRequestMode))
		}
		if len(s.TaskStateByRequestSource) > 0 {
			fmt.Fprintf(w, " task_state_request_sources=%s", formatStringIntCounts(s.TaskStateByRequestSource))
		}
		if len(s.TaskStateByScheduleKind) > 0 {
			fmt.Fprintf(w, " task_state_schedule_kinds=%s", formatStringIntCounts(s.TaskStateByScheduleKind))
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
	if s.LoopTurnCheckpoints > 0 {
		fmt.Fprintf(w, " loop_turn_checkpoint_scenarios=%d loop_turn_checkpoints=%d", s.LoopTurnCheckpointScenarios, s.LoopTurnCheckpoints)
	}
	if s.LoopProtocolFeeds > 0 {
		fmt.Fprintf(w, " loop_protocol_feed_scenarios=%d loop_protocol_feeds=%d", s.LoopProtocolFeedScenarios, s.LoopProtocolFeeds)
		if len(s.LoopProtocolFeedByMode) > 0 {
			fmt.Fprintf(w, " loop_protocol_feed_modes=%s", formatStringIntCounts(s.LoopProtocolFeedByMode))
		}
	}
	if s.LoopProtocolCalibrationRequests > 0 || s.LoopProtocolCalibrations > 0 {
		fmt.Fprintf(w, " loop_protocol_calibration=scenarios:%d/%d,requests:%d,answers:%d",
			s.LoopProtocolCalibrationRequestScenarios,
			s.LoopProtocolCalibrationScenarios,
			s.LoopProtocolCalibrationRequests,
			s.LoopProtocolCalibrations,
		)
	}
	if s.ContextCompactions > 0 {
		fmt.Fprintf(w, " compactions=%d,reactive=%d,removed=%d,reduced_bytes=%d,summary_bytes=%d,summary_missing=%d,summary_empty=%d,policy_observed=%d,max_policy_pressure=%d%%",
			s.ContextCompactions,
			s.ContextCompactionsReactive,
			s.ContextCompactionRemoved,
			s.ContextCompactionReducedBytes,
			s.ContextCompactionSummary,
			s.ContextCompactionSummaryMissing,
			s.ContextCompactionSummaryEmpty,
			s.ContextCompactionPolicyObserved,
			s.ContextCompactionMaxPolicyPressure,
		)
	}
	if s.ContextInjections > 0 {
		fmt.Fprintf(w, " context_injections=%d,bytes=%d,est_tokens=%d",
			s.ContextInjections,
			s.ContextInjectionBytes,
			s.ContextInjectionEstimatedTokens,
		)
		if len(s.ContextInjectionBySource) > 0 {
			fmt.Fprintf(w, " context_injection_sources=%s", formatStringIntCounts(s.ContextInjectionBySource))
		}
	}
	if s.TraceEvents > 0 {
		fmt.Fprintf(w, " trace_events=%d", s.TraceEvents)
		if len(s.TraceEventTypes) > 0 {
			fmt.Fprintf(w, " trace_event_types=%s", formatStringIntCounts(s.TraceEventTypes))
		}
		fmt.Fprintf(w, " trace_event_scenarios=%d,rate=%s", s.TraceEventScenarios, formatPercent(batchRatio(s.TraceEventScenarios, s.Total)))
	}
	if hasBatchToolContextTruncation(s) {
		fmt.Fprintf(w, " ctx_trunc=%d,omitted=%d,artifacts=%d,missing_artifacts=%d", s.ToolContextTruncated, s.ToolContextOmittedBytes, s.ToolContextArtifacts, s.ToolContextMissingArtifacts)
	}
	if len(s.DebugBriefByTag) > 0 {
		fmt.Fprintf(w, " debug_brief=%s", formatStringIntCounts(s.DebugBriefByTag))
	}
	if s.ExpectationScenarios > 0 {
		fmt.Fprintf(w, " expectations=scenarios:%d", s.ExpectationScenarios)
		if len(s.ExpectationCapabilities) > 0 {
			expectationCapabilityPassed, expectationCapabilityTotal := expectationCapabilityPassTotals(s)
			fmt.Fprintf(w, " expectation_capabilities=%s", formatStringIntCounts(s.ExpectationCapabilities))
			fmt.Fprintf(w, " expectation_capability_pass=%s", formatPassTotalCounts(s.ExpectationCapabilityPass, s.ExpectationCapabilities))
			fmt.Fprintf(w, " expectation_capability_pass_rate=%s", formatOptionalPercent(batchOptionalRatio(expectationCapabilityPassed, expectationCapabilityTotal)))
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
		if len(s.ExpectationDomains) > 0 {
			expectationDomainPassed, expectationDomainTotal := expectationDomainPassTotals(s)
			fmt.Fprintf(w, " expectation_domains=%s", formatStringIntCounts(s.ExpectationDomains))
			fmt.Fprintf(w, " expectation_domain_pass=%s", formatPassTotalCounts(s.ExpectationDomainPass, s.ExpectationDomains))
			fmt.Fprintf(w, " expectation_domain_pass_rate=%s", formatOptionalPercent(batchOptionalRatio(expectationDomainPassed, expectationDomainTotal)))
		}
	}
	printDelegationRollup(w, s.FocusedTaskCalls, s.FocusedTaskByType, s.FocusedTaskSources, s.FocusedTaskErrors, s.FocusedTaskIncomplete, s.SubagentCalls, s.SubagentByMode, s.SubagentSources, s.SubagentErrors, s.SubagentIncomplete)
	printPlanRollup(w, s.PlanCalls, s.PlanByAction, s.PlanErrors)
	fmt.Fprintln(w)
	printFailureHintLines(w, s.FailureKinds, "")
	printFailureExampleLines(w, s.FailureExamples, "")
	printDebugBriefTagExampleLines(w, s.DebugBriefTagExamples, "")
	printToolRepairExampleLines(w, s.ToolRepairExamples, "")
	printConversationRepairExampleLines(w, s.ConversationRepairExamples, "")
	printToolFailureHintLines(w, s.ToolFailureByKind, "")
	printToolFailureExampleLines(w, s.ToolFailureExamples, "")
	printLoopGuardExampleLines(w, s.LoopGuardExamples, "")
	printSourceAccessExampleLines(w, s.SourceAccessExamples, "")
	printBrowserScrollExampleLines(w, s.BrowserScrollExamples, "")
	printBrowserNetworkExampleLines(w, s.BrowserNetworkExamples, "")
	printMemoryUpdateExampleLines(w, s.MemoryUpdateExamples, "")
	printMemorySearchMissExampleLines(w, s.MemorySearchMissExamples, "")
	printFailureHintLines(w, s.RuntimeErrorByKind, "")
	printRuntimeErrorExampleLines(w, s.RuntimeErrorExamples, "")
	printLoopDecisionExampleLines(w, s.LoopDecisionExamples, "")
	printLoopTurnCheckpointExampleLines(w, s.LoopTurnCheckpointExamples, "")
	printLoopProtocolFeedExampleLines(w, s.LoopProtocolFeedExamples, "")
	printLoopProtocolCalibrationExampleLines(w, "loop_protocol_calibration_request_example", s.LoopProtocolCalibrationRequestExamples, "")
	printLoopProtocolCalibrationExampleLines(w, "loop_protocol_calibration_example", s.LoopProtocolCalibrationExamples, "")
	printContextCompactionExampleLines(w, s.ContextCompactionExamples, "")
	printContextInjectionExampleLines(w, s.ContextInjectionExamples, "")
	printSessionSearchExampleLines(w, s.SessionSearchExamples, "")
	printPlanExampleLines(w, s.PlanExamples, "")
	printToolTruncationExampleLines(w, s.ToolTruncationExamples, "")
	printExpectationDomainFailureExampleLines(w, s.ExpectationDomainFailureExamples, "")
	printExpectationCapabilityFailureExampleLines(w, s.ExpectationCapabilityFailureExamples, "")
}

func printBatchQualityGates(w io.Writer, meta evalJSONLMetadata, summary batchSummary, failures []string) {
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
		if tag := qualityGateDebugBriefTag(failure); tag != "" {
			printQualityGateDebugBriefExamples(w, tag, summary.DebugBriefTagExamples[tag], "    ")
		}
	}
}

func qualityGateDebugBriefTag(failure string) string {
	const prefix = "debug_brief_tag_rate["
	failure = strings.TrimSpace(failure)
	if !strings.HasPrefix(failure, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(failure, prefix)
	end := strings.Index(rest, "]")
	if end <= 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func printQualityGateDebugBriefExamples(w io.Writer, tag string, examples []batchDebugBriefTagExample, indent string) {
	tag = strings.TrimSpace(tag)
	if tag == "" || len(examples) == 0 {
		return
	}
	for _, ex := range examples {
		fmt.Fprintf(w, "%sdebug_brief_example[%s]: scenario=%s", indent, tag, ex.Scenario)
		if len(ex.FailureKinds) > 0 {
			fmt.Fprintf(w, " failure_kinds=%s", formatStringIntCounts(ex.FailureKinds))
		}
		if ex.TracePath != "" {
			fmt.Fprintf(w, " trace=%s", ex.TracePath)
		}
		if ex.TimelinePath != "" {
			fmt.Fprintf(w, " timeline=%s", ex.TimelinePath)
		}
		if ex.DebugManifestPath != "" {
			fmt.Fprintf(w, " debug_manifest=%s", ex.DebugManifestPath)
		}
		fmt.Fprintln(w)
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

func mergeToolFailureExamples(dst *map[string][]agenteval.ToolFailureExample, src map[string][]agenteval.ToolFailureExample, scenario string, maxPerKind int) {
	if dst == nil || maxPerKind <= 0 {
		return
	}
	for kind, values := range src {
		for _, ex := range values {
			if len((*dst)[kind]) >= maxPerKind {
				break
			}
			if *dst == nil {
				*dst = map[string][]agenteval.ToolFailureExample{}
			}
			if ex.Scenario == "" {
				ex.Scenario = scenario
			}
			(*dst)[kind] = append((*dst)[kind], ex)
		}
	}
}

func mergeRuntimeErrorExamples(dst *map[string][]agenteval.RuntimeErrorExample, src map[string][]agenteval.RuntimeErrorExample, scenario string, maxPerKind int) {
	if dst == nil || maxPerKind <= 0 {
		return
	}
	for kind, values := range src {
		for _, ex := range values {
			if len((*dst)[kind]) >= maxPerKind {
				break
			}
			if *dst == nil {
				*dst = map[string][]agenteval.RuntimeErrorExample{}
			}
			if ex.Scenario == "" {
				ex.Scenario = scenario
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

func printDelegationRollup(w io.Writer, focusedTaskCalls int, focusedTaskByType map[string]int, focusedTaskSources map[string]int, focusedTaskErrors int, focusedTaskIncomplete int, subagentCalls int, subagentByMode map[string]int, subagentSources map[string]int, subagentErrors int, subagentIncomplete int) {
	if focusedTaskCalls == 0 && subagentCalls == 0 {
		return
	}
	fmt.Fprintf(w, " delegation=focused_tasks:%d,subagents:%d", focusedTaskCalls, subagentCalls)
	if focusedTaskErrors > 0 || subagentErrors > 0 {
		fmt.Fprintf(w, " delegation_errors=focused_tasks:%d,subagents:%d", focusedTaskErrors, subagentErrors)
	}
	if focusedTaskIncomplete > 0 || subagentIncomplete > 0 {
		fmt.Fprintf(w, " delegation_incomplete=focused_tasks:%d,subagents:%d", focusedTaskIncomplete, subagentIncomplete)
	}
	if len(focusedTaskByType) > 0 {
		fmt.Fprintf(w, " focused_task_by_type=%s", formatStringIntCounts(focusedTaskByType))
	}
	if len(focusedTaskSources) > 0 {
		fmt.Fprintf(w, " focused_task_sources=%s", formatStringIntCounts(focusedTaskSources))
	}
	if len(subagentByMode) > 0 {
		fmt.Fprintf(w, " subagent_by_mode=%s", formatStringIntCounts(subagentByMode))
	}
	if len(subagentSources) > 0 {
		fmt.Fprintf(w, " subagent_sources=%s", formatStringIntCounts(subagentSources))
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

func compactFailureText(failure string) string {
	failure = strings.TrimSpace(strings.Join(strings.Fields(failure), " "))
	const max = 240
	if len(failure) <= max {
		return failure
	}
	return failure[:max] + "..."
}

func formatDebugBriefTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out := append([]string(nil), tags...)
	sort.Strings(out)
	return strings.Join(out, ",")
}

func conversationRepairSummary(repairs []sse.ConversationRepairedPayload) (count int, missingToolResults int, duplicateToolResults int, unexpectedToolResults int, byKind map[string]int) {
	count = len(repairs)
	for _, repair := range repairs {
		missingToolResults += repair.MissingToolResults
		duplicateToolResults += repair.DuplicateToolResults
		unexpectedToolResults += repair.UnexpectedToolResults
		kind := strings.TrimSpace(repair.FailureKind)
		if kind == "" {
			kind = "unknown"
		}
		if byKind == nil {
			byKind = map[string]int{}
		}
		byKind[kind]++
	}
	return count, missingToolResults, duplicateToolResults, unexpectedToolResults, byKind
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

func optionalInt(value int, ok bool) *int {
	if !ok {
		return nil
	}
	return &value
}

func batchAverage(total, count int) float64 {
	if count <= 0 {
		return 0
	}
	return float64(total) / float64(count)
}

func batchAverageInt64(total int64, count int) float64 {
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

func formatOptionalNumber(value *float64) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *value)
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
		{"--min-loop-turn-checkpoint-rate", g.MinLoopTurnCheckpointRate, true},
		{"--min-loop-protocol-feed-rate", g.MinLoopProtocolFeedRate, true},
		{"--min-loop-protocol-calibration-request-rate", g.MinLoopProtocolCalibrationRequestRate, true},
		{"--min-loop-protocol-calibration-rate", g.MinLoopProtocolCalibrationRate, true},
		{"--min-runtime-surface-rate", g.MinRuntimeSurfaceRate, true},
		{"--min-trace-event-rate", g.MinTraceEventRate, true},
		{"--min-source-network-rate", g.MinSourceNetworkRate, true},
		{"--min-source-access-verified-rate", g.MinSourceAccessVerifiedRate, true},
		{"--min-expectation-capability-pass-rate", g.MinExpectationCapabilityPassRate, true},
		{"--min-each-expectation-capability-pass-rate", g.MinEachExpectationCapabilityPassRate, true},
		{"--min-expectation-domain-pass-rate", g.MinExpectationDomainPassRate, true},
		{"--min-each-expectation-domain-pass-rate", g.MinEachExpectationDomainPassRate, true},
		{"--min-session-search-context-hit-rate", g.MinSessionSearchContextHitRate, true},
		{"--min-session-search-matched-terms-per-call", g.MinSessionSearchMatchedTermsPerCall, false},
		{"--min-tool-repair-success-rate", g.MinToolRepairSuccessRate, true},
		{"--min-verifier-pass-rate", g.MinVerifierPassRate, true},
		{"--max-focused-task-error-rate", g.MaxFocusedTaskErrorRate, true},
		{"--max-forced-no-tools-rate", g.MaxForcedNoToolsRate, true},
		{"--max-loop-guard-intervention-rate", g.MaxLoopGuardInterventionRate, true},
		{"--max-plan-error-rate", g.MaxPlanErrorRate, true},
		{"--max-memory-search-miss-rate", g.MaxMemorySearchMissRate, true},
		{"--max-source-discovery-only-rate", g.MaxSourceDiscoveryOnlyRate, true},
		{"--max-source-dynamic-partial-rate", g.MaxSourceDynamicPartialRate, true},
		{"--max-subagent-error-rate", g.MaxSubagentErrorRate, true},
		{"--max-tool-error-rate", g.MaxToolErrorRate, true},
		{"--max-tool-context-truncation-rate", g.MaxToolContextTruncationRate, true},
		{"--max-tool-result-truncation-rate", g.MaxToolResultTruncationRate, true},
		{"--max-avg-runtime-errors", g.MaxAvgRuntimeErrors, false},
		{"--max-avg-context-compactions", g.MaxAvgContextCompactions, false},
		{"--max-avg-reactive-context-compactions", g.MaxAvgReactiveCompactions, false},
		{"--max-avg-context-removed-messages", g.MaxAvgContextRemovedMessages, false},
		{"--max-avg-context-summary-bytes", g.MaxAvgContextSummaryBytes, false},
		{"--max-avg-context-summary-missing", g.MaxAvgContextSummaryMissing, false},
		{"--max-avg-context-summary-empty", g.MaxAvgContextSummaryEmpty, false},
		{"--max-avg-context-injections", g.MaxAvgContextInjections, false},
		{"--max-avg-context-injection-bytes", g.MaxAvgContextInjectionBytes, false},
		{"--max-avg-context-injection-estimated-tokens", g.MaxAvgContextInjectionEstimatedTokens, false},
		{"--max-avg-tool-calls", g.MaxAvgToolCalls, false},
		{"--max-avg-duration-ms", g.MaxAvgDurationMS, false},
		{"--max-avg-total-tokens", g.MaxAvgTotalTokens, false},
		{"--max-scenario-total-tokens", g.MaxScenarioTotalTokens, false},
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
	for tag, value := range g.MaxDebugBriefTagRates {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("--max-debug-brief-tag-rate tag must be non-empty")
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("--max-debug-brief-tag-rate[%s] must be finite", tag)
		}
		if value < 0 {
			if value == -1 {
				continue
			}
			return fmt.Errorf("--max-debug-brief-tag-rate[%s] must be disabled with -1 or set between 0 and 1", tag)
		}
		if value > 1 {
			return fmt.Errorf("--max-debug-brief-tag-rate[%s] must be between 0 and 1", tag)
		}
	}
	for _, gate := range []struct {
		name   string
		values map[string]float64
		rate   bool
	}{
		{"--min-expectation-domain-source-access-verified-rate", g.MinExpectationDomainSourceAccessVerifiedRates, true},
		{"--max-expectation-domain-avg-total-tokens", g.MaxExpectationDomainAvgTotalTokens, false},
		{"--max-expectation-domain-avg-tool-calls", g.MaxExpectationDomainAvgToolCalls, false},
		{"--max-expectation-domain-avg-runtime-errors", g.MaxExpectationDomainAvgRuntimeErrors, false},
		{"--max-expectation-domain-tool-error-rate", g.MaxExpectationDomainToolErrorRates, true},
		{"--max-expectation-domain-loop-guard-intervention-rate", g.MaxExpectationDomainLoopGuardInterventionRates, true},
	} {
		if err := validateStringFloatGateMap(gate.name, gate.values, gate.rate); err != nil {
			return err
		}
	}
	for _, cap := range g.RequiredExpectationCapabilities {
		if strings.TrimSpace(cap) == "" {
			return fmt.Errorf("--require-expectation-capability value must be non-empty")
		}
	}
	for _, domain := range g.RequiredExpectationDomains {
		if strings.TrimSpace(domain) == "" {
			return fmt.Errorf("--require-expectation-domain value must be non-empty")
		}
	}
	return nil
}

func validateStringFloatGateMap(name string, values map[string]float64, rate bool) error {
	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s domain must be non-empty", name)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s[%s] must be finite", name, key)
		}
		if value < 0 {
			if value == -1 {
				continue
			}
			if rate {
				return fmt.Errorf("%s[%s] must be disabled with -1 or set between 0 and 1", name, key)
			}
			return fmt.Errorf("%s[%s] must be disabled with -1 or set to a non-negative value", name, key)
		}
		if rate && value > 1 {
			return fmt.Errorf("%s[%s] must be between 0 and 1", name, key)
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
	checkMin("loop_turn_checkpoint_rate", batchRatio(s.LoopTurnCheckpointScenarios, s.Total), g.MinLoopTurnCheckpointRate, s.Total > 0)
	checkMin("loop_protocol_feed_rate", batchRatio(s.LoopProtocolFeedScenarios, s.Total), g.MinLoopProtocolFeedRate, s.Total > 0)
	checkMin("loop_protocol_calibration_request_rate", batchRatio(s.LoopProtocolCalibrationRequestScenarios, s.Total), g.MinLoopProtocolCalibrationRequestRate, s.Total > 0)
	checkMin("loop_protocol_calibration_rate", batchRatio(s.LoopProtocolCalibrationScenarios, s.Total), g.MinLoopProtocolCalibrationRate, s.Total > 0)
	checkMin("runtime_surface_rate", batchRatio(s.RuntimeSurfaceScenarios, s.Total), g.MinRuntimeSurfaceRate, s.Total > 0)
	checkMin("trace_event_rate", batchRatio(s.TraceEventScenarios, s.Total), g.MinTraceEventRate, s.Total > 0)
	checkMin("source_network_rate", batchRatio(s.SourceAccessNetwork, s.SourceAccessResults), g.MinSourceNetworkRate, s.SourceAccessResults > 0)
	checkMin("source_access_verified_rate", batchRatio(s.SourceAccessVerified, s.SourceAccessResults), g.MinSourceAccessVerifiedRate, s.SourceAccessResults > 0)
	expectationCapabilityPassed, expectationCapabilityTotal := expectationCapabilityPassTotals(s)
	checkMin("expectation_capability_pass_rate", batchRatio(expectationCapabilityPassed, expectationCapabilityTotal), g.MinExpectationCapabilityPassRate, expectationCapabilityTotal > 0)
	failures = append(failures, expectationCapabilityFamilyGateFailures(s, g.MinEachExpectationCapabilityPassRate)...)
	expectationDomainPassed, expectationDomainTotal := expectationDomainPassTotals(s)
	checkMin("expectation_domain_pass_rate", batchRatio(expectationDomainPassed, expectationDomainTotal), g.MinExpectationDomainPassRate, expectationDomainTotal > 0)
	failures = append(failures, expectationDomainFamilyGateFailures(s, g.MinEachExpectationDomainPassRate)...)
	for _, cap := range g.RequiredExpectationCapabilities {
		if s.ExpectationCapabilities[cap] == 0 {
			failures = append(failures, fmt.Sprintf("expectation_capability[%s] unavailable, want >= 1 scenario", cap))
		}
	}
	for _, domain := range g.RequiredExpectationDomains {
		if s.ExpectationDomains[domain] == 0 {
			failures = append(failures, fmt.Sprintf("expectation_domain[%s] unavailable, want >= 1 scenario", domain))
		}
	}
	failures = append(failures, expectationDomainMetricGateFailures(s, g)...)
	checkMin("session_search_context_hit_rate", batchRatio(s.SessionSearchContextHits, s.SessionSearchResults), g.MinSessionSearchContextHitRate, s.SessionSearchResults > 0)
	checkMin("session_search_matched_terms_per_call", batchRatio(s.SessionSearchMatchedTerms, s.SessionSearchCalls), g.MinSessionSearchMatchedTermsPerCall, s.SessionSearchCalls > 0)
	checkMin("tool_repair_success_rate", batchRatio(s.ToolRepairSucceeded, s.ToolRepairCalls), g.MinToolRepairSuccessRate, s.ToolRepairCalls > 0)
	checkMin("verifier_pass_rate", batchRatio(s.VerifierPassed, s.VerifierRuns), g.MinVerifierPassRate, s.VerifierRuns > 0)
	checkMax("focused_task_error_rate", batchRatio(s.FocusedTaskErrors, s.FocusedTaskCalls), g.MaxFocusedTaskErrorRate, s.FocusedTaskCalls > 0)
	checkMax("forced_no_tools_rate", batchRatio(s.ForcedNoTools, s.ToolCalls), g.MaxForcedNoToolsRate, s.ToolCalls > 0)
	checkMax("loop_guard_intervention_rate", batchRatio(s.LoopGuardInterventions, s.ToolCalls), g.MaxLoopGuardInterventionRate, s.ToolCalls > 0)
	checkMax("plan_error_rate", batchRatio(s.PlanErrors, s.PlanCalls), g.MaxPlanErrorRate, s.PlanCalls > 0)
	checkMax("memory_search_miss_rate", batchRatio(s.MemorySearchMisses, s.MemorySearchCalls), g.MaxMemorySearchMissRate, s.MemorySearchCalls > 0)
	checkMax("source_discovery_only_rate", batchRatio(s.SourceAccessDiscoveryOnly, s.SourceAccessResults), g.MaxSourceDiscoveryOnlyRate, s.SourceAccessResults > 0)
	checkMax("source_dynamic_partial_rate", batchRatio(s.SourceAccessDynamicPartial, s.SourceAccessResults), g.MaxSourceDynamicPartialRate, s.SourceAccessResults > 0)
	checkMax("subagent_error_rate", batchRatio(s.SubagentErrors, s.SubagentCalls), g.MaxSubagentErrorRate, s.SubagentCalls > 0)
	checkMax("tool_error_rate", batchRatio(s.ToolErrors, s.ToolCalls), g.MaxToolErrorRate, s.ToolCalls > 0)
	checkMax("tool_context_truncation_rate", batchRatio(s.ToolContextTruncated, s.ToolCalls), g.MaxToolContextTruncationRate, s.ToolCalls > 0)
	checkMax("tool_result_truncation_rate", batchRatio(s.ToolResultsTruncated, s.ToolCalls), g.MaxToolResultTruncationRate, s.ToolCalls > 0)
	checkMax("avg_runtime_errors", batchAverage(s.RuntimeErrors, s.Total), g.MaxAvgRuntimeErrors, s.Total > 0)
	checkMax("avg_context_compactions", batchAverage(s.ContextCompactions, s.Total), g.MaxAvgContextCompactions, s.Total > 0)
	checkMax("avg_reactive_context_compactions", batchAverage(s.ContextCompactionsReactive, s.Total), g.MaxAvgReactiveCompactions, s.Total > 0)
	checkMax("avg_context_removed_messages", batchAverage(s.ContextCompactionRemoved, s.Total), g.MaxAvgContextRemovedMessages, s.Total > 0)
	checkMax("avg_context_summary_bytes", batchAverage(s.ContextCompactionSummary, s.Total), g.MaxAvgContextSummaryBytes, s.Total > 0)
	checkMax("avg_context_summary_missing", batchAverage(s.ContextCompactionSummaryMissing, s.Total), g.MaxAvgContextSummaryMissing, s.Total > 0)
	checkMax("avg_context_summary_empty", batchAverage(s.ContextCompactionSummaryEmpty, s.Total), g.MaxAvgContextSummaryEmpty, s.Total > 0)
	checkMax("avg_context_injections", batchAverage(s.ContextInjections, s.Total), g.MaxAvgContextInjections, s.Total > 0)
	checkMax("avg_context_injection_bytes", batchAverage(s.ContextInjectionBytes, s.Total), g.MaxAvgContextInjectionBytes, s.Total > 0)
	checkMax("avg_context_injection_estimated_tokens", batchAverage(s.ContextInjectionEstimatedTokens, s.Total), g.MaxAvgContextInjectionEstimatedTokens, s.Total > 0)
	checkMax("avg_tool_calls", batchAverage(s.ToolCalls, s.Total), g.MaxAvgToolCalls, s.Total > 0)
	checkMax("avg_duration_ms", batchAverageInt64(s.Duration.Milliseconds(), s.Total), g.MaxAvgDurationMS, s.Total > 0)
	checkMax("avg_total_tokens", batchAverage(s.InputTokens+s.OutputTokens, s.Total), g.MaxAvgTotalTokens, s.Total > 0)
	if g.MaxScenarioTotalTokens != nil && *g.MaxScenarioTotalTokens >= 0 && s.Total > 0 && float64(s.MaxScenarioTotalTokens) > *g.MaxScenarioTotalTokens {
		scenario := strings.TrimSpace(s.MaxScenarioTokenScenario)
		if scenario == "" {
			scenario = "unknown"
		}
		failures = append(failures, fmt.Sprintf("scenario_total_tokens[%s] %s > max %s", scenario, formatGateFloat(float64(s.MaxScenarioTotalTokens)), formatGateFloat(*g.MaxScenarioTotalTokens)))
	}
	for _, tag := range sortedFloatMapKeys(g.MaxDebugBriefTagRates) {
		threshold := g.MaxDebugBriefTagRates[tag]
		if threshold < 0 {
			continue
		}
		checkMax("debug_brief_tag_rate["+tag+"]", batchRatio(s.DebugBriefByTag[tag], s.Total), &threshold, s.Total > 0)
	}
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

func printFailureExampleLines(w io.Writer, examples map[string][]batchFailureExample, indent string) {
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
			fmt.Fprintf(w, "%sfailure_example[%s]: scenario=%s", indent, kind, ex.Scenario)
			if ex.Failure != "" {
				fmt.Fprintf(w, " failure=%q", ex.Failure)
			}
			if ex.TracePath != "" {
				fmt.Fprintf(w, " trace=%s", ex.TracePath)
			}
			if ex.TimelinePath != "" {
				fmt.Fprintf(w, " timeline=%s", ex.TimelinePath)
			}
			if ex.DebugManifestPath != "" {
				fmt.Fprintf(w, " debug_manifest=%s", ex.DebugManifestPath)
			}
			fmt.Fprintln(w)
		}
	}
}

func printDebugBriefTagExampleLines(w io.Writer, examples map[string][]batchDebugBriefTagExample, indent string) {
	if len(examples) == 0 {
		return
	}
	tags := make([]string, 0, len(examples))
	for tag := range examples {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		for _, ex := range examples[tag] {
			fmt.Fprintf(w, "%sdebug_brief_example[%s]: scenario=%s", indent, tag, ex.Scenario)
			if len(ex.FailureKinds) > 0 {
				fmt.Fprintf(w, " failure_kinds=%s", formatStringIntCounts(ex.FailureKinds))
			}
			if ex.TracePath != "" {
				fmt.Fprintf(w, " trace=%s", ex.TracePath)
			}
			if ex.TimelinePath != "" {
				fmt.Fprintf(w, " timeline=%s", ex.TimelinePath)
			}
			if ex.DebugManifestPath != "" {
				fmt.Fprintf(w, " debug_manifest=%s", ex.DebugManifestPath)
			}
			fmt.Fprintln(w)
		}
	}
}

func printToolRepairExampleLines(w io.Writer, examples []agenteval.ToolRepairExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%stool_repair_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " tool=%s", ex.Tool)
		if ex.OriginalTool != "" {
			fmt.Fprintf(w, " original=%s", ex.OriginalTool)
		}
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if len(ex.RepairKinds) > 0 {
			fmt.Fprintf(w, " kinds=%s", strings.Join(ex.RepairKinds, ","))
		}
		fmt.Fprintf(w, " canonicalized=%t args_repaired=%t exit=%d", ex.Canonicalized, ex.ArgsRepaired, ex.ExitCode)
		if len(ex.RepairNotes) > 0 {
			fmt.Fprintf(w, " note=%q", ex.RepairNotes[0])
		}
		fmt.Fprintln(w)
	}
}

func printConversationRepairExampleLines(w io.Writer, examples []batchConversationRepairExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sconversation_repair_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.SessionID != "" {
			fmt.Fprintf(w, " session=%s", ex.SessionID)
		}
		fmt.Fprintf(w, " missing_tool_results=%d", ex.MissingToolResults)
		if ex.DuplicateToolResults > 0 {
			fmt.Fprintf(w, " duplicate_tool_results=%d", ex.DuplicateToolResults)
		}
		if ex.UnexpectedToolResults > 0 {
			fmt.Fprintf(w, " unexpected_tool_results=%d", ex.UnexpectedToolResults)
		}
		if ex.FailureKind != "" {
			fmt.Fprintf(w, " kind=%s", ex.FailureKind)
		}
		if ex.Next != "" {
			fmt.Fprintf(w, " next=%q", textutil.Preview(ex.Next, 160))
		}
		fmt.Fprintln(w)
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
			fmt.Fprintf(w, "%stool_failure_example[%s]:", indent, kind)
			if ex.Scenario != "" {
				fmt.Fprintf(w, " scenario=%s", ex.Scenario)
			}
			fmt.Fprintf(w, " tool=%s", ex.Tool)
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

func printLoopGuardExampleLines(w io.Writer, examples []agenteval.LoopGuardExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sloop_guard_example[%s]:", indent, ex.Kind)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " category=%s tool=%s", ex.Category, ex.Tool)
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.ArgsSummary != "" {
			fmt.Fprintf(w, " args=%s", ex.ArgsSummary)
		}
		fmt.Fprintf(w, " exit=%d", ex.ExitCode)
		if ex.GuardSummary != "" {
			fmt.Fprintf(w, " guard=%s", ex.GuardSummary)
		}
		if ex.SuggestedNextStep != "" {
			fmt.Fprintf(w, " next=%s", ex.SuggestedNextStep)
		}
		if ex.ResultSummary != "" {
			fmt.Fprintf(w, " result=%s", ex.ResultSummary)
		}
		fmt.Fprintln(w)
	}
}

func printSourceAccessExampleLines(w io.Writer, examples []agenteval.SourceAccessExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%ssource_access_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " status=%s tool=%s", ex.Status, ex.Tool)
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.URL != "" {
			fmt.Fprintf(w, " url=%s", ex.URL)
		}
		if ex.RequestedURL != "" {
			fmt.Fprintf(w, " requested=%s", ex.RequestedURL)
		}
		if ex.SourceMethod != "" {
			fmt.Fprintf(w, " method=%s", ex.SourceMethod)
		}
		if ex.HTTPStatus != "" {
			fmt.Fprintf(w, " http_status=%s", ex.HTTPStatus)
		}
		if ex.ContentType != "" {
			fmt.Fprintf(w, " content_type=%s", ex.ContentType)
		}
		if ex.JSONPath != "" {
			fmt.Fprintf(w, " json_path=%s", ex.JSONPath)
		}
		if ex.Ref != "" {
			fmt.Fprintf(w, " ref=%s", ex.Ref)
		}
		if ex.ResultPreview != "" {
			fmt.Fprintf(w, " preview=%q", ex.ResultPreview)
		}
		fmt.Fprintln(w)
	}
}

func printBrowserNetworkExampleLines(w io.Writer, examples []agenteval.BrowserNetworkSearchExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sbrowser_network_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " status=%s", ex.Status)
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.CurrentPageURL != "" {
			fmt.Fprintf(w, " page=%s", ex.CurrentPageURL)
		}
		if ex.Query != "" {
			fmt.Fprintf(w, " query=%q", ex.Query)
		}
		if ex.EvidenceStatus != "" {
			fmt.Fprintf(w, " evidence_status=%q", ex.EvidenceStatus)
		}
		if len(ex.Refs) > 0 {
			fmt.Fprintf(w, " refs=%s", strings.Join(ex.Refs, ","))
		}
		if len(ex.Previews) > 0 {
			fmt.Fprintf(w, " previews=%q", strings.Join(ex.Previews, " | "))
		}
		if ex.RequiresRead {
			fmt.Fprintf(w, " requires_read=true")
		}
		if ex.NotCitable {
			fmt.Fprintf(w, " not_citable=true")
		}
		if ex.SuggestedNextStep != "" {
			fmt.Fprintf(w, " next=%q", ex.SuggestedNextStep)
		}
		fmt.Fprintln(w)
	}
}

func printBrowserScrollExampleLines(w io.Writer, examples []agenteval.BrowserScrollExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sbrowser_scroll_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " status=%s", ex.Status)
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.URL != "" {
			fmt.Fprintf(w, " url=%s", ex.URL)
		}
		if ex.Direction != "" {
			fmt.Fprintf(w, " direction=%s", ex.Direction)
		}
		if ex.Movement != "" {
			fmt.Fprintf(w, " movement=%s", ex.Movement)
		}
		if ex.Boundary != "" {
			fmt.Fprintf(w, " boundary=%s", ex.Boundary)
		}
		if ex.BeforeY != "" || ex.AfterY != "" || ex.MaxY != "" {
			fmt.Fprintf(w, " y=%s->%s/%s", ex.BeforeY, ex.AfterY, ex.MaxY)
		}
		if ex.SuggestedNextStep != "" {
			fmt.Fprintf(w, " next=%q", ex.SuggestedNextStep)
		}
		if ex.ResultPreview != "" {
			fmt.Fprintf(w, " preview=%q", ex.ResultPreview)
		}
		fmt.Fprintln(w)
	}
}

func printMemoryUpdateExampleLines(w io.Writer, examples []agenteval.MemoryUpdateExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%smemory_update_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " action=%s target=%s location=%s", ex.Action, ex.Target, ex.Location)
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.Topic != "" {
			fmt.Fprintf(w, " topic=%s", ex.Topic)
		}
		if ex.Preview != "" {
			fmt.Fprintf(w, " preview=%q", ex.Preview)
		}
		if ex.PreviousPreview != "" {
			fmt.Fprintf(w, " previous=%q", ex.PreviousPreview)
		}
		if ex.NextPreview != "" {
			fmt.Fprintf(w, " next=%q", ex.NextPreview)
		}
		fmt.Fprintln(w)
	}
}

func printMemorySearchMissExampleLines(w io.Writer, examples []agenteval.MemorySearchMissExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%smemory_search_miss_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.Target != "" {
			fmt.Fprintf(w, " target=%s", ex.Target)
		}
		if ex.Topic != "" {
			fmt.Fprintf(w, " topic=%s", ex.Topic)
		}
		if ex.Query != "" {
			fmt.Fprintf(w, " query=%q", ex.Query)
		}
		if ex.TopicCount > 0 {
			fmt.Fprintf(w, " topic_count=%d", ex.TopicCount)
		}
		if len(ex.Topics) > 0 {
			fmt.Fprintf(w, " topics=%s", strings.Join(ex.Topics, ","))
		}
		if ex.Message != "" {
			fmt.Fprintf(w, " message=%q", textutil.Preview(ex.Message, 180))
		}
		fmt.Fprintln(w)
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
			fmt.Fprintf(w, "%sruntime_error_example[%s]:", indent, kind)
			if ex.Scenario != "" {
				fmt.Fprintf(w, " scenario=%s", ex.Scenario)
			}
			fmt.Fprintf(w, " %s\n", ex.Message)
		}
	}
}

func printLoopDecisionExampleLines(w io.Writer, examples []agenteval.LoopDecision, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sloop_decision_example[%s]:", indent, ex.Kind)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " decision=%s", ex.Decision)
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
		if ex.TokenBudget > 0 || ex.ObservedInputTokens > 0 || ex.ProjectedInputTokens > 0 {
			fmt.Fprintf(w, " input_budget=%d observed_input=%d projected_input=%d", ex.TokenBudget, ex.ObservedInputTokens, ex.ProjectedInputTokens)
		}
		fmt.Fprintln(w)
	}
}

func printLoopTurnCheckpointExampleLines(w io.Writer, examples []agenteval.LoopTurnCheckpoint, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sloop_turn_checkpoint_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.LoopID != "" {
			fmt.Fprintf(w, " loop_id=%s", ex.LoopID)
		}
		if ex.Status != "" {
			fmt.Fprintf(w, " status=%s", ex.Status)
		}
		if ex.TurnID != "" {
			fmt.Fprintf(w, " turn=%s", ex.TurnID)
		}
		if ex.EndReason != "" {
			fmt.Fprintf(w, " end=%s", ex.EndReason)
		}
		if ex.ProtocolPath != "" {
			fmt.Fprintf(w, " path=%s", ex.ProtocolPath)
		}
		if ex.EventSeq > 0 {
			fmt.Fprintf(w, " event_seq=%d", ex.EventSeq)
		}
		if ex.TurnCheckpoints > 0 {
			fmt.Fprintf(w, " checkpoints=%d", ex.TurnCheckpoints)
		}
		if ex.InputTokens > 0 || ex.OutputTokens > 0 {
			fmt.Fprintf(w, " tokens=%d/%d", ex.InputTokens, ex.OutputTokens)
		}
		fmt.Fprintf(w, " tools=%d errors=%d guards=%d forced_no_tools=%d memory_updates=%d memory_searches=%d memory_misses=%d session_search=%d\n",
			ex.ToolRequests,
			ex.ToolErrors,
			ex.LoopGuards,
			ex.ForcedNoTools,
			ex.MemoryUpdates,
			ex.MemorySearchCalls,
			ex.MemoryMisses,
			ex.SessionSearchCalls,
		)
	}
}

func printLoopProtocolFeedExampleLines(w io.Writer, examples []agenteval.LoopProtocolFeed, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%sloop_protocol_feed_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.LoopID != "" {
			fmt.Fprintf(w, " loop_id=%s", ex.LoopID)
		}
		fmt.Fprintf(w, " mode=%s feed=%d", ex.Mode, ex.FeedNumber)
		if ex.ProtocolFeeds > 0 && ex.ProtocolFeeds != ex.FeedNumber {
			fmt.Fprintf(w, " total=%d", ex.ProtocolFeeds)
		}
		if ex.ProtocolPath != "" {
			fmt.Fprintf(w, " path=%s", ex.ProtocolPath)
		}
		if ex.PlanLabel != "" {
			fmt.Fprintf(w, " plan=%s", ex.PlanLabel)
		}
		if ex.PlanCurrentStepIndex > 0 {
			fmt.Fprintf(w, " current=%d", ex.PlanCurrentStepIndex)
			if ex.PlanCurrentStepStatus != "" {
				fmt.Fprintf(w, ":%s", ex.PlanCurrentStepStatus)
			}
		}
		if ex.PlanCurrentStep != "" {
			fmt.Fprintf(w, " step=%q", textutil.Preview(ex.PlanCurrentStep, 96))
		}
		if ex.CurrentSituation != "" {
			fmt.Fprintf(w, " situation=%q", textutil.Preview(ex.CurrentSituation, 120))
		}
		if ex.LastTurnID != "" || ex.LastTurnMemorySearchCalls > 0 || ex.LastTurnSessionSearchCalls > 0 {
			fmt.Fprintf(w, " last_turn=%q", textutil.Preview(loopProtocolFeedLastTurnSummary(ex), 140))
		}
		fmt.Fprintln(w)
	}
}

func printLoopProtocolCalibrationExampleLines(w io.Writer, label string, examples []agenteval.LoopProtocolCalibration, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%s%s:", indent, label)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.LoopID != "" {
			fmt.Fprintf(w, " loop_id=%s", ex.LoopID)
		}
		if ex.Status != "" {
			fmt.Fprintf(w, " status=%s", ex.Status)
		}
		fmt.Fprintf(w, " questions=%d answers=%d", ex.CalibrationQuestions, ex.CalibrationAnswers)
		if ex.ProtocolPath != "" {
			fmt.Fprintf(w, " path=%s", ex.ProtocolPath)
		}
		if ex.EventSeq > 0 {
			fmt.Fprintf(w, " event_seq=%d", ex.EventSeq)
		}
		if ex.LastCalibrationQuestion != "" {
			fmt.Fprintf(w, " question=%q", textutil.Preview(ex.LastCalibrationQuestion, 120))
		}
		if ex.LastCalibrationAnswer != "" {
			fmt.Fprintf(w, " answer=%q", textutil.Preview(ex.LastCalibrationAnswer, 120))
		}
		fmt.Fprintln(w)
	}
}

func loopProtocolFeedLastTurnSummary(ex agenteval.LoopProtocolFeed) string {
	var parts []string
	if ex.LastTurnID != "" {
		parts = append(parts, "id="+ex.LastTurnID)
	}
	if ex.LastTurnEndReason != "" {
		parts = append(parts, "reason="+ex.LastTurnEndReason)
	}
	if ex.LastTurnToolRequests > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", ex.LastTurnToolRequests))
	}
	if ex.LastTurnMemoryUpdates > 0 {
		parts = append(parts, fmt.Sprintf("memory_updates=%d", ex.LastTurnMemoryUpdates))
	}
	if ex.LastTurnMemorySearchCalls > 0 {
		parts = append(parts, fmt.Sprintf("memory_searches=%d", ex.LastTurnMemorySearchCalls))
	}
	if ex.LastTurnMemorySearchMisses > 0 {
		parts = append(parts, fmt.Sprintf("memory_misses=%d", ex.LastTurnMemorySearchMisses))
	}
	if ex.LastTurnSessionSearchCalls > 0 {
		parts = append(parts, fmt.Sprintf("session_search=%d", ex.LastTurnSessionSearchCalls))
	}
	if ex.LastTurnLoopGuards > 0 {
		parts = append(parts, fmt.Sprintf("loop_guards=%d", ex.LastTurnLoopGuards))
	}
	return strings.Join(parts, " ")
}

func printContextCompactionExampleLines(w io.Writer, examples []agenteval.ContextCompaction, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%scontext_compaction_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.TurnID != "" {
			fmt.Fprintf(w, " turn=%s", ex.TurnID)
		}
		fmt.Fprintf(w, " reactive=%t messages=%d->%d removed=%d",
			ex.Reactive,
			ex.BeforeMessages,
			ex.AfterMessages,
			ex.RemovedMessages,
		)
		if ex.BeforeBytes > 0 || ex.AfterBytes > 0 || ex.ReducedBytes > 0 {
			fmt.Fprintf(w, " bytes=%d->%d reduced=%d", ex.BeforeBytes, ex.AfterBytes, ex.ReducedBytes)
		}
		if ex.EstimatedInputTokens > 0 || ex.TriggerInputTokens > 0 || ex.ModelContextWindowTokens > 0 || ex.ReservedOutputTokens > 0 || ex.CompactTriggerInputPercent > 0 {
			fmt.Fprintf(w, " policy=estimated:%d,trigger:%d,model_window:%d,reserved_output:%d,trigger_percent:%d,pressure:%d%%",
				ex.EstimatedInputTokens,
				ex.TriggerInputTokens,
				ex.ModelContextWindowTokens,
				ex.ReservedOutputTokens,
				ex.CompactTriggerInputPercent,
				contextCompactionPolicyPressurePercent(ex),
			)
		}
		fmt.Fprintf(w, " summary_state=%s summary_bytes=%d",
			contextCompactionExampleSummaryState(ex),
			ex.SummaryBytes,
		)
		if ex.Reason != "" {
			fmt.Fprintf(w, " reason=%s", ex.Reason)
		}
		if ex.LoopProtocolAnchor != "" {
			fmt.Fprintf(w, " loop_anchor=%q", textutil.Preview(ex.LoopProtocolAnchor, 160))
		}
		if ex.SummaryPreview != "" {
			fmt.Fprintf(w, " preview=%q", textutil.Preview(ex.SummaryPreview, 180))
		}
		fmt.Fprintln(w)
	}
}

func contextCompactionExampleSummaryState(ex agenteval.ContextCompaction) string {
	if ex.SummaryBytes > 0 || strings.TrimSpace(ex.SummaryPreview) != "" {
		return "present"
	}
	if !ex.SummaryPresentKnown {
		return "unknown"
	}
	if !ex.SummaryPresent {
		return "missing"
	}
	return "empty"
}

func contextCompactionPolicyPressurePercent(ex agenteval.ContextCompaction) int {
	if ex.EstimatedInputTokens <= 0 || ex.TriggerInputTokens <= 0 {
		return 0
	}
	return (ex.EstimatedInputTokens*100 + ex.TriggerInputTokens - 1) / ex.TriggerInputTokens
}

func printContextInjectionExampleLines(w io.Writer, examples []agenteval.ContextInjection, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%scontext_injection_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		if ex.TurnID != "" {
			fmt.Fprintf(w, " turn=%s", ex.TurnID)
		}
		fmt.Fprintf(w, " source=%s bytes=%d estimated_tokens=%d", ex.Source, ex.Bytes, ex.EstimatedTokens)
		if ex.Title != "" {
			fmt.Fprintf(w, " title=%q", textutil.Preview(ex.Title, 96))
		}
		if ex.Summary != "" {
			fmt.Fprintf(w, " summary=%q", textutil.Preview(ex.Summary, 140))
		}
		if ex.Preview != "" {
			fmt.Fprintf(w, " preview=%q", textutil.Preview(ex.Preview, 160))
		}
		fmt.Fprintln(w)
	}
}

func printSessionSearchExampleLines(w io.Writer, examples []agenteval.SessionSearchExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%ssession_search_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " query=%q total=%d", ex.Query, ex.Total)
		if ex.RecentSessions > 0 {
			fmt.Fprintf(w, " recent=%d", ex.RecentSessions)
		}
		if ex.SessionID != "" {
			fmt.Fprintf(w, " session=%s", ex.SessionID)
		}
		if ex.RecentSessionID != "" {
			fmt.Fprintf(w, " recent_session=%s", ex.RecentSessionID)
		}
		if ex.TurnIdx > 0 {
			fmt.Fprintf(w, " turn=%d", ex.TurnIdx)
		}
		if ex.MessageIdx > 0 {
			fmt.Fprintf(w, " message=%d", ex.MessageIdx)
		}
		if ex.ModTime != "" {
			fmt.Fprintf(w, " mod_time=%s", ex.ModTime)
		}
		if ex.RecentModTime != "" {
			fmt.Fprintf(w, " recent_mod_time=%s", ex.RecentModTime)
		}
		if len(ex.MatchedTerms) > 0 {
			fmt.Fprintf(w, " terms=%s", strings.Join(ex.MatchedTerms, ","))
		}
		if ex.ContextIncluded {
			fmt.Fprintf(w, " context=true")
		}
		if ex.Message != "" {
			fmt.Fprintf(w, " message=%q", ex.Message)
		}
		if ex.RecentUserPreview != "" {
			fmt.Fprintf(w, " recent_user=%q", textutil.Preview(ex.RecentUserPreview, 120))
		}
		if ex.RecentAssistantPreview != "" {
			fmt.Fprintf(w, " recent_assistant=%q", textutil.Preview(ex.RecentAssistantPreview, 120))
		}
		fmt.Fprintln(w)
	}
}

func printPlanExampleLines(w io.Writer, examples []agenteval.PlanExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%splan_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " action=%s", ex.Action)
		if ex.Index > 0 {
			fmt.Fprintf(w, " index=%d", ex.Index)
		}
		if ex.Status != "" {
			fmt.Fprintf(w, " status=%s", ex.Status)
		}
		if ex.TotalSteps > 0 {
			fmt.Fprintf(w, " progress=%d/%d", ex.CompletedSteps, ex.TotalSteps)
		}
		if ex.CurrentStepIndex > 0 {
			fmt.Fprintf(w, " current=%d:%s", ex.CurrentStepIndex, ex.CurrentStepStatus)
		}
		if ex.StepText != "" {
			fmt.Fprintf(w, " step=%q", ex.StepText)
		}
		if len(ex.Evidence) > 0 {
			fmt.Fprintf(w, " evidence=%s", strings.Join(ex.Evidence, ","))
		}
		if ex.Error {
			fmt.Fprintf(w, " error=true")
		}
		if ex.Skipped {
			fmt.Fprintf(w, " skipped=true")
		}
		if len(ex.FailureKinds) > 0 {
			fmt.Fprintf(w, " failure_kinds=%s", strings.Join(ex.FailureKinds, ","))
		}
		if ex.ResultMessage != "" {
			fmt.Fprintf(w, " message=%q", ex.ResultMessage)
		}
		fmt.Fprintln(w)
	}
}

func printToolTruncationExampleLines(w io.Writer, examples []agenteval.ToolTruncationExample, indent string) {
	for _, ex := range examples {
		fmt.Fprintf(w, "%stool_truncation_example:", indent)
		if ex.Scenario != "" {
			fmt.Fprintf(w, " scenario=%s", ex.Scenario)
		}
		fmt.Fprintf(w, " tool=%s", ex.Tool)
		if ex.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", ex.CallID)
		}
		if ex.ArgsTruncated || ex.ArgsOmittedBytes > 0 {
			fmt.Fprintf(w, " args=truncated:%t,bytes:%d,omitted:%d,cap:%d", ex.ArgsTruncated, ex.ArgsBytes, ex.ArgsOmittedBytes, ex.ArgsCapBytes)
		}
		if ex.ResultTruncated || ex.ResultOmittedBytes > 0 {
			fmt.Fprintf(w, " result=truncated:%t,bytes:%d,omitted:%d,cap:%d", ex.ResultTruncated, ex.ResultBytes, ex.ResultOmittedBytes, ex.ResultCapBytes)
		}
		if ex.ResultSummary != "" {
			fmt.Fprintf(w, " summary=%q", ex.ResultSummary)
		}
		if ex.ContextOmittedBytes > 0 || ex.ContextBytes > 0 || ex.ContextEstimatedTokens > 0 {
			fmt.Fprintf(w, " context=bytes:%d,omitted:%d,tokens:%d", ex.ContextBytes, ex.ContextOmittedBytes, ex.ContextEstimatedTokens)
		}
		if ex.ResultArtifactPath != "" {
			fmt.Fprintf(w, " artifact=%s", ex.ResultArtifactPath)
		}
		fmt.Fprintln(w)
	}
}

func printExpectationCapabilityFailureExampleLines(w io.Writer, examples map[string][]expectationCapabilityFailureExample, indent string) {
	if len(examples) == 0 {
		return
	}
	caps := make([]string, 0, len(examples))
	for cap := range examples {
		caps = append(caps, cap)
	}
	sort.Strings(caps)
	for _, cap := range caps {
		for _, ex := range examples[cap] {
			fmt.Fprintf(w, "%sexpectation_capability_failure[%s]: scenario=%s", indent, cap, ex.Scenario)
			if len(ex.FailureKinds) > 0 {
				fmt.Fprintf(w, " failure_kinds=%s", formatStringIntCounts(ex.FailureKinds))
			}
			if len(ex.DebugBriefTags) > 0 {
				fmt.Fprintf(w, " debug_brief=%s", formatDebugBriefTags(ex.DebugBriefTags))
			}
			if ex.TracePath != "" {
				fmt.Fprintf(w, " trace=%s", ex.TracePath)
			}
			if ex.TimelinePath != "" {
				fmt.Fprintf(w, " timeline=%s", ex.TimelinePath)
			}
			if ex.DebugManifestPath != "" {
				fmt.Fprintf(w, " debug_manifest=%s", ex.DebugManifestPath)
			}
			fmt.Fprintln(w)
		}
	}
}

func printExpectationDomainFailureExampleLines(w io.Writer, examples map[string][]expectationDomainFailureExample, indent string) {
	if len(examples) == 0 {
		return
	}
	domains := make([]string, 0, len(examples))
	for domain := range examples {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, domain := range domains {
		for _, ex := range examples[domain] {
			fmt.Fprintf(w, "%sexpectation_domain_failure[%s]: scenario=%s", indent, domain, ex.Scenario)
			if len(ex.FailureKinds) > 0 {
				fmt.Fprintf(w, " failure_kinds=%s", formatStringIntCounts(ex.FailureKinds))
			}
			if len(ex.DebugBriefTags) > 0 {
				fmt.Fprintf(w, " debug_brief=%s", formatDebugBriefTags(ex.DebugBriefTags))
			}
			if ex.TracePath != "" {
				fmt.Fprintf(w, " trace=%s", ex.TracePath)
			}
			if ex.TimelinePath != "" {
				fmt.Fprintf(w, " timeline=%s", ex.TimelinePath)
			}
			if ex.DebugManifestPath != "" {
				fmt.Fprintf(w, " debug_manifest=%s", ex.DebugManifestPath)
			}
			fmt.Fprintln(w)
		}
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
	case "browser_launch_failed":
		return "Chromium could not start; install browser runtime dependencies, set AFFENT_BROWSER_BINARY to a working Chrome/Chromium binary, and rerun browser smoke tests before trusting browser/web eval failures"
	case "llm_timeout":
		return "upstream LLM streaming stalled past the per-call timeout; inspect provider queue/TTFT/chunk gaps or raise the runtime/eval timeout for slow reasoning models"
	case "llm_incomplete_stream":
		return "upstream closed the SSE stream before finish_reason; inspect model server, proxy, KV-cache, crash, or OOM logs rather than treating this as a verifier failure"
	case "context_overflow":
		return "upstream rejected the request because the prompt/context window was too large; compaction, shorter history, or smaller tool context is needed"
	case "loop_protocol_fixture":
		return "the eval scenario declares loop protocol feed/calibration expectations but the current-session LOOP.md fixture is missing, non-running, or has unreadable state; fix the fixture before rerunning model evals"
	case "source_repo_setup":
		return "the eval scenario failed while preparing its source repository before the agent turn; inspect source_repo_url/ref/dir, setup commands, git availability, and local/remote access before treating this as model behavior"
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
	case "command_failed":
		return "a shell command exited non-zero without a more specific Failure kind; inspect the command, cwd, exit code, and result excerpt before changing model guidance"
	case "tool_failed":
		return "a tool failed without a more specific Failure kind; inspect the tool result and add structured Failure/Next metadata at the tool boundary if recovery is ambiguous"
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
	case "loop_protocol_activation_status":
		return "loop_protocol draft activation or update used missing/invalid LOOP.md metadata status; use patch_draft for compact setup changes, keep the draft status=draft, ask/record calibration as needed, then activate with complete_activation"
	case "loop_protocol_activation_invalid":
		return "loop_protocol activation failed validation; fill unresolved LOOP.md fields, keep Current Situation compact, and retry only after the protocol is complete"
	case "loop_protocol_activation_unready":
		return "loop_protocol activation was blocked because no user calibration answer is recorded; ask one concise calibration question and wait before retrying activation"
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

func sortedFloatMapKeys(counts map[string]float64) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	SchemaVersion                                  int                `json:"schema_version"`
	Suite                                          string             `json:"suite,omitempty"`
	Model                                          string             `json:"model,omitempty"`
	ProviderLabel                                  string             `json:"provider_label,omitempty"`
	Executor                                       string             `json:"executor"`
	Temperature                                    string             `json:"temperature,omitempty"`
	TopP                                           string             `json:"top_p,omitempty"`
	MaxTokens                                      string             `json:"max_tokens,omitempty"`
	Seed                                           string             `json:"seed,omitempty"`
	RuntimeEvalMode                                bool               `json:"runtime_eval_mode,omitempty"`
	RuntimeTools                                   string             `json:"runtime_tools,omitempty"`
	RuntimeAllTools                                bool               `json:"runtime_all_tools,omitempty"`
	RuntimeMemory                                  bool               `json:"runtime_memory,omitempty"`
	RuntimeWeb                                     bool               `json:"runtime_web,omitempty"`
	RuntimeBrowser                                 bool               `json:"runtime_browser,omitempty"`
	TraceDeltas                                    bool               `json:"trace_deltas,omitempty"`
	RuntimeMCP                                     bool               `json:"runtime_mcp,omitempty"`
	TimeoutMS                                      int64              `json:"timeout_ms"`
	QualityProfile                                 string             `json:"quality_profile,omitempty"`
	MinPassRate                                    *float64           `json:"min_pass_rate,omitempty"`
	MinCompletionRate                              *float64           `json:"min_completion_rate,omitempty"`
	MinMemoryUpdateRate                            *float64           `json:"min_memory_update_rate,omitempty"`
	MinLoopTurnCheckpointRate                      *float64           `json:"min_loop_turn_checkpoint_rate,omitempty"`
	MinLoopProtocolFeedRate                        *float64           `json:"min_loop_protocol_feed_rate,omitempty"`
	MinLoopProtocolCalibrationRequestRate          *float64           `json:"min_loop_protocol_calibration_request_rate,omitempty"`
	MinLoopProtocolCalibrationRate                 *float64           `json:"min_loop_protocol_calibration_rate,omitempty"`
	MinRuntimeSurfaceRate                          *float64           `json:"min_runtime_surface_rate,omitempty"`
	MinTraceEventRate                              *float64           `json:"min_trace_event_rate,omitempty"`
	MinSourceNetworkRate                           *float64           `json:"min_source_network_rate,omitempty"`
	MinSourceAccessVerifiedRate                    *float64           `json:"min_source_access_verified_rate,omitempty"`
	MinExpectationCapabilityPassRate               *float64           `json:"min_expectation_capability_pass_rate,omitempty"`
	MinEachExpectationCapabilityPassRate           *float64           `json:"min_each_expectation_capability_pass_rate,omitempty"`
	MinExpectationDomainPassRate                   *float64           `json:"min_expectation_domain_pass_rate,omitempty"`
	MinEachExpectationDomainPassRate               *float64           `json:"min_each_expectation_domain_pass_rate,omitempty"`
	MinSessionSearchContextHitRate                 *float64           `json:"min_session_search_context_hit_rate,omitempty"`
	MinSessionSearchMatchedTermsPerCall            *float64           `json:"min_session_search_matched_terms_per_call,omitempty"`
	MinToolRepairSuccessRate                       *float64           `json:"min_tool_repair_success_rate,omitempty"`
	MinVerifierPassRate                            *float64           `json:"min_verifier_pass_rate,omitempty"`
	MaxFocusedTaskErrorRate                        *float64           `json:"max_focused_task_error_rate,omitempty"`
	MaxForcedNoToolsRate                           *float64           `json:"max_forced_no_tools_rate,omitempty"`
	MaxLoopGuardInterventionRate                   *float64           `json:"max_loop_guard_intervention_rate,omitempty"`
	MaxPlanErrorRate                               *float64           `json:"max_plan_error_rate,omitempty"`
	MaxMemorySearchMissRate                        *float64           `json:"max_memory_search_miss_rate,omitempty"`
	MaxSourceDiscoveryOnlyRate                     *float64           `json:"max_source_discovery_only_rate,omitempty"`
	MaxSourceDynamicPartialRate                    *float64           `json:"max_source_dynamic_partial_rate,omitempty"`
	MaxSubagentErrorRate                           *float64           `json:"max_subagent_error_rate,omitempty"`
	MaxToolErrorRate                               *float64           `json:"max_tool_error_rate,omitempty"`
	MaxToolContextTruncationRate                   *float64           `json:"max_tool_context_truncation_rate,omitempty"`
	MaxToolResultTruncationRate                    *float64           `json:"max_tool_result_truncation_rate,omitempty"`
	MaxAvgRuntimeErrors                            *float64           `json:"max_avg_runtime_errors,omitempty"`
	MaxAvgContextCompactions                       *float64           `json:"max_avg_context_compactions,omitempty"`
	MaxAvgReactiveCompactions                      *float64           `json:"max_avg_reactive_context_compactions,omitempty"`
	MaxAvgContextRemovedMessages                   *float64           `json:"max_avg_context_removed_messages,omitempty"`
	MaxAvgContextSummaryBytes                      *float64           `json:"max_avg_context_summary_bytes,omitempty"`
	MaxAvgContextSummaryMissing                    *float64           `json:"max_avg_context_summary_missing,omitempty"`
	MaxAvgContextSummaryEmpty                      *float64           `json:"max_avg_context_summary_empty,omitempty"`
	MaxAvgContextInjections                        *float64           `json:"max_avg_context_injections,omitempty"`
	MaxAvgContextInjectionBytes                    *float64           `json:"max_avg_context_injection_bytes,omitempty"`
	MaxAvgContextInjectionEstimatedTokens          *float64           `json:"max_avg_context_injection_estimated_tokens,omitempty"`
	MaxAvgToolCalls                                *float64           `json:"max_avg_tool_calls,omitempty"`
	MaxAvgDurationMS                               *float64           `json:"max_avg_duration_ms,omitempty"`
	MaxAvgTotalTokens                              *float64           `json:"max_avg_total_tokens,omitempty"`
	MaxScenarioTotalTokens                         *float64           `json:"max_scenario_total_tokens,omitempty"`
	MaxDebugBriefTagRates                          map[string]float64 `json:"max_debug_brief_tag_rates,omitempty"`
	MinExpectationDomainSourceAccessVerifiedRates  map[string]float64 `json:"min_expectation_domain_source_access_verified_rates,omitempty"`
	MaxExpectationDomainAvgTotalTokens             map[string]float64 `json:"max_expectation_domain_avg_total_tokens,omitempty"`
	MaxExpectationDomainAvgToolCalls               map[string]float64 `json:"max_expectation_domain_avg_tool_calls,omitempty"`
	MaxExpectationDomainAvgRuntimeErrors           map[string]float64 `json:"max_expectation_domain_avg_runtime_errors,omitempty"`
	MaxExpectationDomainToolErrorRates             map[string]float64 `json:"max_expectation_domain_tool_error_rates,omitempty"`
	MaxExpectationDomainLoopGuardInterventionRates map[string]float64 `json:"max_expectation_domain_loop_guard_intervention_rates,omitempty"`
	RequiredExpectationCapabilities                []string           `json:"required_expectation_capabilities,omitempty"`
	RequiredExpectationDomains                     []string           `json:"required_expectation_domains,omitempty"`
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
		SchemaVersion:                         evalJSONLSchemaVersion,
		Suite:                                 strings.TrimSpace(suite),
		Model:                                 model,
		ProviderLabel:                         providerLabel,
		Executor:                              normalizedEvalExecutor(executor),
		Temperature:                           strings.TrimSpace(temperature),
		TopP:                                  strings.TrimSpace(topP),
		MaxTokens:                             strings.TrimSpace(maxTokens),
		Seed:                                  strings.TrimSpace(seed),
		RuntimeEvalMode:                       runtimeEvalMode,
		RuntimeTools:                          strings.TrimSpace(runtimeTools),
		RuntimeAllTools:                       runtimeAllTools,
		RuntimeMemory:                         runtimeMemory,
		RuntimeWeb:                            runtimeWeb,
		RuntimeBrowser:                        runtimeBrowser,
		TraceDeltas:                           traceDeltas,
		RuntimeMCP:                            strings.TrimSpace(runtimeMCPConfig) != "",
		TimeoutMS:                             timeout.Milliseconds(),
		QualityProfile:                        strings.ToLower(strings.TrimSpace(qualityProfile)),
		MinPassRate:                           enabledQualityGateValue(gates.MinPassRate),
		MinCompletionRate:                     enabledQualityGateValue(gates.MinCompletionRate),
		MinMemoryUpdateRate:                   enabledQualityGateValue(gates.MinMemoryUpdateRate),
		MinLoopTurnCheckpointRate:             enabledQualityGateValue(gates.MinLoopTurnCheckpointRate),
		MinLoopProtocolFeedRate:               enabledQualityGateValue(gates.MinLoopProtocolFeedRate),
		MinLoopProtocolCalibrationRequestRate: enabledQualityGateValue(gates.MinLoopProtocolCalibrationRequestRate),
		MinLoopProtocolCalibrationRate:        enabledQualityGateValue(gates.MinLoopProtocolCalibrationRate),
		MinRuntimeSurfaceRate:                 enabledQualityGateValue(gates.MinRuntimeSurfaceRate),
		MinTraceEventRate:                     enabledQualityGateValue(gates.MinTraceEventRate),
		MinSourceNetworkRate:                  enabledQualityGateValue(gates.MinSourceNetworkRate),
		MinSourceAccessVerifiedRate:           enabledQualityGateValue(gates.MinSourceAccessVerifiedRate),
		MinExpectationCapabilityPassRate:      enabledQualityGateValue(gates.MinExpectationCapabilityPassRate),
		MinEachExpectationCapabilityPassRate:  enabledQualityGateValue(gates.MinEachExpectationCapabilityPassRate),
		MinExpectationDomainPassRate:          enabledQualityGateValue(gates.MinExpectationDomainPassRate),
		MinEachExpectationDomainPassRate:      enabledQualityGateValue(gates.MinEachExpectationDomainPassRate),
		MinSessionSearchContextHitRate:        enabledQualityGateValue(gates.MinSessionSearchContextHitRate),
		MinSessionSearchMatchedTermsPerCall:   enabledQualityGateValue(gates.MinSessionSearchMatchedTermsPerCall),
		MinToolRepairSuccessRate:              enabledQualityGateValue(gates.MinToolRepairSuccessRate),
		MinVerifierPassRate:                   enabledQualityGateValue(gates.MinVerifierPassRate),
		MaxFocusedTaskErrorRate:               enabledQualityGateValue(gates.MaxFocusedTaskErrorRate),
		MaxForcedNoToolsRate:                  enabledQualityGateValue(gates.MaxForcedNoToolsRate),
		MaxLoopGuardInterventionRate:          enabledQualityGateValue(gates.MaxLoopGuardInterventionRate),
		MaxPlanErrorRate:                      enabledQualityGateValue(gates.MaxPlanErrorRate),
		MaxMemorySearchMissRate:               enabledQualityGateValue(gates.MaxMemorySearchMissRate),
		MaxSourceDiscoveryOnlyRate:            enabledQualityGateValue(gates.MaxSourceDiscoveryOnlyRate),
		MaxSourceDynamicPartialRate:           enabledQualityGateValue(gates.MaxSourceDynamicPartialRate),
		MaxSubagentErrorRate:                  enabledQualityGateValue(gates.MaxSubagentErrorRate),
		MaxToolErrorRate:                      enabledQualityGateValue(gates.MaxToolErrorRate),
		MaxToolContextTruncationRate:          enabledQualityGateValue(gates.MaxToolContextTruncationRate),
		MaxToolResultTruncationRate:           enabledQualityGateValue(gates.MaxToolResultTruncationRate),
		MaxAvgRuntimeErrors:                   enabledQualityGateValue(gates.MaxAvgRuntimeErrors),
		MaxAvgContextCompactions:              enabledQualityGateValue(gates.MaxAvgContextCompactions),
		MaxAvgReactiveCompactions:             enabledQualityGateValue(gates.MaxAvgReactiveCompactions),
		MaxAvgContextRemovedMessages:          enabledQualityGateValue(gates.MaxAvgContextRemovedMessages),
		MaxAvgContextSummaryBytes:             enabledQualityGateValue(gates.MaxAvgContextSummaryBytes),
		MaxAvgContextSummaryMissing:           enabledQualityGateValue(gates.MaxAvgContextSummaryMissing),
		MaxAvgContextSummaryEmpty:             enabledQualityGateValue(gates.MaxAvgContextSummaryEmpty),
		MaxAvgContextInjections:               enabledQualityGateValue(gates.MaxAvgContextInjections),
		MaxAvgContextInjectionBytes:           enabledQualityGateValue(gates.MaxAvgContextInjectionBytes),
		MaxAvgContextInjectionEstimatedTokens: enabledQualityGateValue(gates.MaxAvgContextInjectionEstimatedTokens),
		MaxAvgToolCalls:                       enabledQualityGateValue(gates.MaxAvgToolCalls),
		MaxAvgDurationMS:                      enabledQualityGateValue(gates.MaxAvgDurationMS),
		MaxAvgTotalTokens:                     enabledQualityGateValue(gates.MaxAvgTotalTokens),
		MaxScenarioTotalTokens:                enabledQualityGateValue(gates.MaxScenarioTotalTokens),
		MaxDebugBriefTagRates:                 enabledQualityGateMap(gates.MaxDebugBriefTagRates),
		MinExpectationDomainSourceAccessVerifiedRates:  enabledQualityGateMap(gates.MinExpectationDomainSourceAccessVerifiedRates),
		MaxExpectationDomainAvgTotalTokens:             enabledQualityGateMap(gates.MaxExpectationDomainAvgTotalTokens),
		MaxExpectationDomainAvgToolCalls:               enabledQualityGateMap(gates.MaxExpectationDomainAvgToolCalls),
		MaxExpectationDomainAvgRuntimeErrors:           enabledQualityGateMap(gates.MaxExpectationDomainAvgRuntimeErrors),
		MaxExpectationDomainToolErrorRates:             enabledQualityGateMap(gates.MaxExpectationDomainToolErrorRates),
		MaxExpectationDomainLoopGuardInterventionRates: enabledQualityGateMap(gates.MaxExpectationDomainLoopGuardInterventionRates),
		RequiredExpectationCapabilities:                cloneStringSlice(gates.RequiredExpectationCapabilities),
		RequiredExpectationDomains:                     cloneStringSlice(gates.RequiredExpectationDomains),
	}
}

func enabledQualityGateValue(value *float64) *float64 {
	if value == nil || *value < 0 {
		return nil
	}
	clone := *value
	return &clone
}

func enabledQualityGateMap(values map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(values))
	for key, value := range values {
		if value >= 0 {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	Type                                   string                                     `json:"type"`
	Scenario                               string                                     `json:"scenario"`
	OK                                     bool                                       `json:"ok"`
	RunExitCode                            int                                        `json:"run_exit_code"`
	DurationMS                             int64                                      `json:"duration_ms"`
	Workspace                              string                                     `json:"workspace"`
	TracePath                              string                                     `json:"trace_path"`
	DebugManifestPath                      string                                     `json:"debug_manifest_path,omitempty"`
	TimelinePath                           string                                     `json:"timeline_path,omitempty"`
	FinalTextPath                          string                                     `json:"final_text_path,omitempty"`
	StdoutPath                             string                                     `json:"stdout_path,omitempty"`
	StderrPath                             string                                     `json:"stderr_path,omitempty"`
	AffentctlCommand                       []string                                   `json:"affentctl_command,omitempty"`
	Expectations                           *agenteval.DebugScenarioExpectations       `json:"expectations,omitempty"`
	ExpectationCapabilityNames             []string                                   `json:"expectation_capability_names,omitempty"`
	ExpectationCapabilityOutcome           string                                     `json:"expectation_capability_outcome,omitempty"`
	ExpectationCapabilityPassedNames       []string                                   `json:"expectation_capability_passed_names,omitempty"`
	ExpectationCapabilityFailedNames       []string                                   `json:"expectation_capability_failed_names,omitempty"`
	TraceSchemaVersion                     int                                        `json:"trace_schema_version,omitempty"`
	TurnEndReason                          string                                     `json:"turn_end_reason,omitempty"`
	ToolCalls                              int                                        `json:"tool_calls"`
	ToolErrors                             int                                        `json:"tool_errors"`
	ToolRepaired                           int                                        `json:"tool_repaired"`
	ToolNameCanonicalized                  int                                        `json:"tool_name_canonicalized"`
	ToolRepairCalls                        int                                        `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded                    int                                        `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed                       int                                        `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes                        int                                        `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind                       map[string]int                             `json:"tool_repair_by_kind,omitempty"`
	ToolRepairExamples                     []agenteval.ToolRepairExample              `json:"tool_repair_examples,omitempty"`
	ConversationRepairs                    []sse.ConversationRepairedPayload          `json:"conversation_repairs,omitempty"`
	ToolFailureByKind                      map[string]int                             `json:"tool_failure_by_kind,omitempty"`
	ToolFailureExamples                    map[string][]agenteval.ToolFailureExample  `json:"tool_failure_examples,omitempty"`
	LoopGuardExamples                      []agenteval.LoopGuardExample               `json:"loop_guard_examples,omitempty"`
	MemoryUpdateExamples                   []agenteval.MemoryUpdateExample            `json:"memory_update_examples,omitempty"`
	RuntimeErrorByKind                     map[string]int                             `json:"runtime_error_by_kind,omitempty"`
	RuntimeErrorExamples                   map[string][]agenteval.RuntimeErrorExample `json:"runtime_error_examples,omitempty"`
	RuntimeSurface                         *runtimeSurfaceSummary                     `json:"runtime_surface,omitempty"`
	TaskState                              *agenteval.TaskStateSnapshot               `json:"task_state,omitempty"`
	TaskStateStatus                        string                                     `json:"task_state_status,omitempty"`
	TaskStateVerification                  string                                     `json:"task_state_verification,omitempty"`
	TaskStateRequestMode                   string                                     `json:"task_state_request_mode,omitempty"`
	TaskStateRequestSource                 string                                     `json:"task_state_request_source,omitempty"`
	TaskStateScheduleID                    string                                     `json:"task_state_schedule_id,omitempty"`
	TaskStateScheduleKind                  string                                     `json:"task_state_schedule_kind,omitempty"`
	TaskStateChangedFiles                  int                                        `json:"task_state_changed_files,omitempty"`
	TaskStateAttemptedActions              int                                        `json:"task_state_attempted_actions,omitempty"`
	TaskStateFailedActions                 int                                        `json:"task_state_failed_actions,omitempty"`
	TaskStateEvidence                      int                                        `json:"task_state_evidence,omitempty"`
	RuntimeSurfaceScenarios                int                                        `json:"runtime_surface_scenarios,omitempty"`
	RuntimeSurfaceTools                    map[string]int                             `json:"runtime_surface_tools,omitempty"`
	RuntimeSurfaceCapabilities             map[string]int                             `json:"runtime_surface_capabilities,omitempty"`
	LoopDecisions                          int                                        `json:"loop_decisions,omitempty"`
	LoopDecisionByKind                     map[string]int                             `json:"loop_decision_by_kind,omitempty"`
	LoopDecisionByDecision                 map[string]int                             `json:"loop_decision_by_decision,omitempty"`
	LoopDecisionExamples                   []agenteval.LoopDecision                   `json:"loop_decision_examples,omitempty"`
	LoopTurnCheckpoints                    int                                        `json:"loop_turn_checkpoints,omitempty"`
	LoopTurnCheckpointExamples             []agenteval.LoopTurnCheckpoint             `json:"loop_turn_checkpoint_examples,omitempty"`
	LoopProtocolFeeds                      int                                        `json:"loop_protocol_feeds,omitempty"`
	LoopProtocolFeedByMode                 map[string]int                             `json:"loop_protocol_feed_by_mode,omitempty"`
	LoopProtocolFeedExamples               []agenteval.LoopProtocolFeed               `json:"loop_protocol_feed_examples,omitempty"`
	LoopProtocolCalibrationRequests        int                                        `json:"loop_protocol_calibration_requests,omitempty"`
	LoopProtocolCalibrationRequestExamples []agenteval.LoopProtocolCalibration        `json:"loop_protocol_calibration_request_examples,omitempty"`
	LoopProtocolCalibrations               int                                        `json:"loop_protocol_calibrations,omitempty"`
	LoopProtocolCalibrationExamples        []agenteval.LoopProtocolCalibration        `json:"loop_protocol_calibration_examples,omitempty"`
	ContextCompactions                     int                                        `json:"context_compactions,omitempty"`
	ContextCompactionsReactive             int                                        `json:"context_compactions_reactive,omitempty"`
	ContextCompactionRemoved               int                                        `json:"context_compaction_removed_messages,omitempty"`
	ContextCompactionReducedBytes          int                                        `json:"context_compaction_reduced_bytes,omitempty"`
	ContextCompactionSummary               int                                        `json:"context_compaction_summary_bytes,omitempty"`
	ContextCompactionSummaryMissing        int                                        `json:"context_compaction_summary_missing,omitempty"`
	ContextCompactionSummaryEmpty          int                                        `json:"context_compaction_summary_empty,omitempty"`
	ContextCompactionPolicyObserved        int                                        `json:"context_compaction_policy_observed,omitempty"`
	ContextCompactionMaxPolicyPressure     int                                        `json:"context_compaction_max_policy_pressure_percent,omitempty"`
	ContextCompactionExamples              []agenteval.ContextCompaction              `json:"context_compaction_examples,omitempty"`
	ContextInjections                      int                                        `json:"context_injections,omitempty"`
	ContextInjectionBySource               map[string]int                             `json:"context_injection_by_source,omitempty"`
	ContextInjectionBytes                  int                                        `json:"context_injection_bytes,omitempty"`
	ContextInjectionEstimatedTokens        int                                        `json:"context_injection_estimated_tokens,omitempty"`
	ContextInjectionExamples               []agenteval.ContextInjection               `json:"context_injection_examples,omitempty"`
	LoopGuardInterventions                 int                                        `json:"loop_guard_interventions"`
	ForcedNoTools                          int                                        `json:"forced_no_tools"`
	SourceAccessResults                    int                                        `json:"source_access_results"`
	SourceAccessVerified                   int                                        `json:"source_access_verified"`
	SourceAccessDiscoveryOnly              int                                        `json:"source_access_discovery_only"`
	SourceAccessNetwork                    int                                        `json:"source_access_network"`
	SourceAccessDynamicPartial             int                                        `json:"source_access_dynamic_partial"`
	SourceAccessExamples                   []agenteval.SourceAccessExample            `json:"source_access_examples,omitempty"`
	BrowserScrollExamples                  []agenteval.BrowserScrollExample           `json:"browser_scroll_examples,omitempty"`
	BrowserNetworkExamples                 []agenteval.BrowserNetworkSearchExample    `json:"browser_network_examples,omitempty"`
	MemoryUpdates                          int                                        `json:"memory_updates"`
	MemoryUpdateAdd                        int                                        `json:"memory_update_add"`
	MemoryUpdateReplace                    int                                        `json:"memory_update_replace"`
	MemoryUpdateRemove                     int                                        `json:"memory_update_remove"`
	MemorySearchCalls                      int                                        `json:"memory_search_calls,omitempty"`
	MemorySearchMisses                     int                                        `json:"memory_search_misses,omitempty"`
	MemorySearchMissExamples               []agenteval.MemorySearchMissExample        `json:"memory_search_miss_examples,omitempty"`
	SessionSearchCalls                     int                                        `json:"session_search_calls,omitempty"`
	SessionSearchResults                   int                                        `json:"session_search_results,omitempty"`
	SessionSearchContextHits               int                                        `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms              int                                        `json:"session_search_matched_terms,omitempty"`
	SessionSearchRecent                    int                                        `json:"session_search_recent_sessions,omitempty"`
	SessionSearchExamples                  []agenteval.SessionSearchExample           `json:"session_search_examples,omitempty"`
	ToolDurationMS                         int64                                      `json:"tool_duration_ms"`
	ToolContextTruncated                   int                                        `json:"tool_context_truncated"`
	ToolContextOmittedBytes                int                                        `json:"tool_context_omitted_bytes"`
	ToolArgsTruncated                      int                                        `json:"tool_args_truncated"`
	ToolArgsOmittedBytes                   int                                        `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated                   int                                        `json:"tool_results_truncated"`
	ToolResultsOmittedBytes                int                                        `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts                    int                                        `json:"tool_result_artifacts"`
	ToolResultMissingArtifacts             int                                        `json:"tool_result_missing_artifacts,omitempty"`
	ToolContextArtifacts                   int                                        `json:"tool_context_artifacts,omitempty"`
	ToolContextMissingArtifacts            int                                        `json:"tool_context_missing_artifacts,omitempty"`
	ToolTruncationExamples                 []agenteval.ToolTruncationExample          `json:"tool_truncation_examples,omitempty"`
	VerifierCommand                        string                                     `json:"verifier_command,omitempty"`
	VerifierRan                            bool                                       `json:"verifier_ran"`
	VerifierOK                             bool                                       `json:"verifier_ok"`
	VerifierExitCode                       int                                        `json:"verifier_exit_code"`
	VerifierDurationMS                     int64                                      `json:"verifier_duration_ms"`
	VerifierOutputBytes                    int                                        `json:"verifier_output_bytes"`
	VerifierOutputTruncated                bool                                       `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes             int                                        `json:"verifier_output_omitted_bytes"`
	VerifierOutputCapBytes                 int                                        `json:"verifier_output_cap_bytes"`
	TraceEvents                            int                                        `json:"trace_events,omitempty"`
	TraceEventTypes                        map[string]int                             `json:"trace_event_types,omitempty"`
	InputTokens                            int                                        `json:"input_tokens"`
	OutputTokens                           int                                        `json:"output_tokens"`
	WorkspaceRemoved                       bool                                       `json:"workspace_removed,omitempty"`
	CleanupError                           string                                     `json:"cleanup_error,omitempty"`
	Failures                               []string                                   `json:"failures,omitempty"`
	FailureKinds                           map[string]int                             `json:"failure_kinds,omitempty"`
	FailureHints                           failureHintMap                             `json:"failure_hints,omitempty"`
	ToolFailureHints                       failureHintMap                             `json:"tool_failure_hints,omitempty"`
	RuntimeErrorHints                      failureHintMap                             `json:"runtime_error_hints,omitempty"`
	DebugBrief                             *agenteval.DebugBrief                      `json:"debug_brief,omitempty"`

	// Per-scenario delegation breakdown. Fields are omitted from the
	// JSONL when the scenario used no delegation tools, so older
	// records and delegation-free runs stay compact and noise-free.
	FocusedTaskCalls      int            `json:"focused_task_calls,omitempty"`
	FocusedTaskByType     map[string]int `json:"focused_task_by_type,omitempty"`
	FocusedTaskSources    map[string]int `json:"focused_task_sources,omitempty"`
	FocusedTaskErrors     int            `json:"focused_task_errors,omitempty"`
	FocusedTaskIncomplete int            `json:"focused_task_incomplete,omitempty"`
	SubagentCalls         int            `json:"subagent_calls,omitempty"`
	SubagentByMode        map[string]int `json:"subagent_by_mode,omitempty"`
	SubagentSources       map[string]int `json:"subagent_sources,omitempty"`
	SubagentErrors        int            `json:"subagent_errors,omitempty"`
	SubagentIncomplete    int            `json:"subagent_incomplete,omitempty"`

	// Per-scenario plan-tool breakdown. Fields are omitted from the
	// JSONL when the scenario did not call the plan tool.
	PlanCalls    int                     `json:"plan_calls,omitempty"`
	PlanByAction map[string]int          `json:"plan_by_action,omitempty"`
	PlanErrors   int                     `json:"plan_errors,omitempty"`
	PlanExamples []agenteval.PlanExample `json:"plan_examples,omitempty"`
}

type batchSummaryRecord struct {
	evalJSONLMetadata
	Type                                    string                                           `json:"type"`
	Scenarios                               int                                              `json:"scenarios"`
	Passed                                  int                                              `json:"passed"`
	Failed                                  int                                              `json:"failed"`
	PassRate                                float64                                          `json:"pass_rate"`
	CompletionRate                          float64                                          `json:"completion_rate"`
	MemoryUpdateRate                        float64                                          `json:"memory_update_rate"`
	MemorySearchMissRate                    *float64                                         `json:"memory_search_miss_rate,omitempty"`
	LoopTurnCheckpointRate                  float64                                          `json:"loop_turn_checkpoint_rate"`
	LoopProtocolFeedRate                    float64                                          `json:"loop_protocol_feed_rate"`
	LoopProtocolCalibrationRequestRate      float64                                          `json:"loop_protocol_calibration_request_rate"`
	LoopProtocolCalibrationRate             float64                                          `json:"loop_protocol_calibration_rate"`
	ToolErrorRate                           *float64                                         `json:"tool_error_rate,omitempty"`
	FocusedTaskErrorRate                    *float64                                         `json:"focused_task_error_rate,omitempty"`
	SubagentErrorRate                       *float64                                         `json:"subagent_error_rate,omitempty"`
	ForcedNoToolsRate                       *float64                                         `json:"forced_no_tools_rate,omitempty"`
	LoopGuardInterventionRate               *float64                                         `json:"loop_guard_intervention_rate,omitempty"`
	PlanErrorRate                           *float64                                         `json:"plan_error_rate,omitempty"`
	ToolRepairSuccessRate                   *float64                                         `json:"tool_repair_success_rate,omitempty"`
	VerifierPassRate                        *float64                                         `json:"verifier_pass_rate,omitempty"`
	SourceAccessVerifiedRate                *float64                                         `json:"source_access_verified_rate,omitempty"`
	SourceNetworkRate                       *float64                                         `json:"source_network_rate,omitempty"`
	SourceDiscoveryOnlyRate                 *float64                                         `json:"source_discovery_only_rate,omitempty"`
	SourceDynamicPartialRate                *float64                                         `json:"source_dynamic_partial_rate,omitempty"`
	SessionSearchContextHitRate             *float64                                         `json:"session_search_context_hit_rate,omitempty"`
	SessionSearchMatchedTermsPerCall        *float64                                         `json:"session_search_matched_terms_per_call,omitempty"`
	TraceEventRate                          float64                                          `json:"trace_event_rate"`
	AvgRuntimeErrors                        float64                                          `json:"avg_runtime_errors"`
	AvgContextCompactions                   float64                                          `json:"avg_context_compactions"`
	AvgReactiveCompactions                  float64                                          `json:"avg_reactive_context_compactions"`
	AvgContextRemovedMessages               float64                                          `json:"avg_context_removed_messages"`
	AvgContextReducedBytes                  float64                                          `json:"avg_context_reduced_bytes"`
	AvgContextSummaryBytes                  float64                                          `json:"avg_context_summary_bytes"`
	AvgContextSummaryMissing                float64                                          `json:"avg_context_summary_missing"`
	AvgContextSummaryEmpty                  float64                                          `json:"avg_context_summary_empty"`
	ContextCompactionPolicyObserved         int                                              `json:"context_compaction_policy_observed,omitempty"`
	ContextCompactionMaxPolicyPressure      int                                              `json:"context_compaction_max_policy_pressure_percent,omitempty"`
	AvgContextInjections                    float64                                          `json:"avg_context_injections"`
	AvgContextInjectionBytes                float64                                          `json:"avg_context_injection_bytes"`
	AvgContextInjectionEstimatedTokens      float64                                          `json:"avg_context_injection_estimated_tokens"`
	AvgToolCalls                            float64                                          `json:"avg_tool_calls"`
	ToolContextTruncationRate               *float64                                         `json:"tool_context_truncation_rate,omitempty"`
	ToolResultTruncationRate                *float64                                         `json:"tool_result_truncation_rate,omitempty"`
	DurationMS                              int64                                            `json:"duration_ms"`
	AvgDurationMS                           float64                                          `json:"avg_duration_ms"`
	ToolCalls                               int                                              `json:"tool_calls"`
	ToolErrors                              int                                              `json:"tool_errors"`
	ToolRepaired                            int                                              `json:"tool_repaired"`
	ToolNameCanonicalized                   int                                              `json:"tool_name_canonicalized"`
	ToolRepairCalls                         int                                              `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded                     int                                              `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed                        int                                              `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes                         int                                              `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind                        map[string]int                                   `json:"tool_repair_by_kind,omitempty"`
	ToolRepairExamples                      []agenteval.ToolRepairExample                    `json:"tool_repair_examples,omitempty"`
	ConversationRepairs                     int                                              `json:"conversation_repairs,omitempty"`
	ConversationRepairMissingToolResults    int                                              `json:"conversation_repair_missing_tool_results,omitempty"`
	ConversationRepairDuplicateResults      int                                              `json:"conversation_repair_duplicate_tool_results,omitempty"`
	ConversationRepairUnexpectedResults     int                                              `json:"conversation_repair_unexpected_tool_results,omitempty"`
	ConversationRepairByKind                map[string]int                                   `json:"conversation_repair_by_kind,omitempty"`
	ConversationRepairExamples              []batchConversationRepairExample                 `json:"conversation_repair_examples,omitempty"`
	ToolFailureByKind                       map[string]int                                   `json:"tool_failure_by_kind,omitempty"`
	ToolFailureExamples                     map[string][]agenteval.ToolFailureExample        `json:"tool_failure_examples,omitempty"`
	LoopGuardExamples                       []agenteval.LoopGuardExample                     `json:"loop_guard_examples,omitempty"`
	RuntimeErrorByKind                      map[string]int                                   `json:"runtime_error_by_kind,omitempty"`
	RuntimeErrorExamples                    map[string][]agenteval.RuntimeErrorExample       `json:"runtime_error_examples,omitempty"`
	RuntimeSurfaceRate                      float64                                          `json:"runtime_surface_rate"`
	RuntimeSurfaceScenarios                 int                                              `json:"runtime_surface_scenarios,omitempty"`
	RuntimeSurfaceTools                     map[string]int                                   `json:"runtime_surface_tools,omitempty"`
	RuntimeSurfaceCapabilities              map[string]int                                   `json:"runtime_surface_capabilities,omitempty"`
	TaskStateRate                           float64                                          `json:"task_state_rate"`
	TaskStateScenarios                      int                                              `json:"task_state_scenarios,omitempty"`
	TaskStateByStatus                       map[string]int                                   `json:"task_state_by_status,omitempty"`
	TaskStateByVerification                 map[string]int                                   `json:"task_state_by_verification,omitempty"`
	TaskStateByRequestMode                  map[string]int                                   `json:"task_state_by_request_mode,omitempty"`
	TaskStateByRequestSource                map[string]int                                   `json:"task_state_by_request_source,omitempty"`
	TaskStateByScheduleKind                 map[string]int                                   `json:"task_state_by_schedule_kind,omitempty"`
	TaskStateChangedFiles                   int                                              `json:"task_state_changed_files,omitempty"`
	TaskStateAttemptedActions               int                                              `json:"task_state_attempted_actions,omitempty"`
	TaskStateFailedActions                  int                                              `json:"task_state_failed_actions,omitempty"`
	TaskStateEvidence                       int                                              `json:"task_state_evidence,omitempty"`
	LoopDecisions                           int                                              `json:"loop_decisions,omitempty"`
	LoopDecisionByKind                      map[string]int                                   `json:"loop_decision_by_kind,omitempty"`
	LoopDecisionByDecision                  map[string]int                                   `json:"loop_decision_by_decision,omitempty"`
	LoopDecisionExamples                    []agenteval.LoopDecision                         `json:"loop_decision_examples,omitempty"`
	LoopTurnCheckpointScenarios             int                                              `json:"loop_turn_checkpoint_scenarios,omitempty"`
	LoopTurnCheckpoints                     int                                              `json:"loop_turn_checkpoints,omitempty"`
	LoopTurnCheckpointExamples              []agenteval.LoopTurnCheckpoint                   `json:"loop_turn_checkpoint_examples,omitempty"`
	LoopProtocolFeedScenarios               int                                              `json:"loop_protocol_feed_scenarios,omitempty"`
	LoopProtocolFeeds                       int                                              `json:"loop_protocol_feeds,omitempty"`
	LoopProtocolFeedByMode                  map[string]int                                   `json:"loop_protocol_feed_by_mode,omitempty"`
	LoopProtocolFeedExamples                []agenteval.LoopProtocolFeed                     `json:"loop_protocol_feed_examples,omitempty"`
	LoopProtocolCalibrationRequestScenarios int                                              `json:"loop_protocol_calibration_request_scenarios,omitempty"`
	LoopProtocolCalibrationRequests         int                                              `json:"loop_protocol_calibration_requests,omitempty"`
	LoopProtocolCalibrationRequestExamples  []agenteval.LoopProtocolCalibration              `json:"loop_protocol_calibration_request_examples,omitempty"`
	LoopProtocolCalibrationScenarios        int                                              `json:"loop_protocol_calibration_scenarios,omitempty"`
	LoopProtocolCalibrations                int                                              `json:"loop_protocol_calibrations,omitempty"`
	LoopProtocolCalibrationExamples         []agenteval.LoopProtocolCalibration              `json:"loop_protocol_calibration_examples,omitempty"`
	ContextCompactions                      int                                              `json:"context_compactions,omitempty"`
	ContextCompactionsReactive              int                                              `json:"context_compactions_reactive,omitempty"`
	ContextCompactionRemoved                int                                              `json:"context_compaction_removed_messages,omitempty"`
	ContextCompactionReducedBytes           int                                              `json:"context_compaction_reduced_bytes,omitempty"`
	ContextCompactionSummary                int                                              `json:"context_compaction_summary_bytes,omitempty"`
	ContextCompactionSummaryMissing         int                                              `json:"context_compaction_summary_missing,omitempty"`
	ContextCompactionSummaryEmpty           int                                              `json:"context_compaction_summary_empty,omitempty"`
	ContextCompactionExamples               []agenteval.ContextCompaction                    `json:"context_compaction_examples,omitempty"`
	ContextInjections                       int                                              `json:"context_injections,omitempty"`
	ContextInjectionBySource                map[string]int                                   `json:"context_injection_by_source,omitempty"`
	ContextInjectionBytes                   int                                              `json:"context_injection_bytes,omitempty"`
	ContextInjectionEstimatedTokens         int                                              `json:"context_injection_estimated_tokens,omitempty"`
	ContextInjectionExamples                []agenteval.ContextInjection                     `json:"context_injection_examples,omitempty"`
	LoopGuardInterventions                  int                                              `json:"loop_guard_interventions"`
	ForcedNoTools                           int                                              `json:"forced_no_tools"`
	SourceAccessResults                     int                                              `json:"source_access_results"`
	SourceAccessVerified                    int                                              `json:"source_access_verified"`
	SourceAccessDiscoveryOnly               int                                              `json:"source_access_discovery_only"`
	SourceAccessNetwork                     int                                              `json:"source_access_network"`
	SourceAccessDynamicPartial              int                                              `json:"source_access_dynamic_partial"`
	SourceAccessExamples                    []agenteval.SourceAccessExample                  `json:"source_access_examples,omitempty"`
	BrowserScrollExamples                   []agenteval.BrowserScrollExample                 `json:"browser_scroll_examples,omitempty"`
	BrowserNetworkExamples                  []agenteval.BrowserNetworkSearchExample          `json:"browser_network_examples,omitempty"`
	MemoryUpdates                           int                                              `json:"memory_updates"`
	MemoryUpdateAdd                         int                                              `json:"memory_update_add"`
	MemoryUpdateReplace                     int                                              `json:"memory_update_replace"`
	MemoryUpdateRemove                      int                                              `json:"memory_update_remove"`
	MemorySearchCalls                       int                                              `json:"memory_search_calls,omitempty"`
	MemorySearchMisses                      int                                              `json:"memory_search_misses,omitempty"`
	MemoryUpdateExamples                    []agenteval.MemoryUpdateExample                  `json:"memory_update_examples,omitempty"`
	MemorySearchMissExamples                []agenteval.MemorySearchMissExample              `json:"memory_search_miss_examples,omitempty"`
	SessionSearchCalls                      int                                              `json:"session_search_calls,omitempty"`
	SessionSearchResults                    int                                              `json:"session_search_results,omitempty"`
	SessionSearchContextHits                int                                              `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms               int                                              `json:"session_search_matched_terms,omitempty"`
	SessionSearchRecent                     int                                              `json:"session_search_recent_sessions,omitempty"`
	SessionSearchExamples                   []agenteval.SessionSearchExample                 `json:"session_search_examples,omitempty"`
	ToolDurationMS                          int64                                            `json:"tool_duration_ms"`
	ToolContextTruncated                    int                                              `json:"tool_context_truncated"`
	ToolContextOmittedBytes                 int                                              `json:"tool_context_omitted_bytes"`
	ToolArgsTruncated                       int                                              `json:"tool_args_truncated"`
	ToolArgsOmittedBytes                    int                                              `json:"tool_args_omitted_bytes"`
	ToolResultsTruncated                    int                                              `json:"tool_results_truncated"`
	ToolResultsOmittedBytes                 int                                              `json:"tool_results_omitted_bytes"`
	ToolResultArtifacts                     int                                              `json:"tool_result_artifacts"`
	ToolResultMissingArtifacts              int                                              `json:"tool_result_missing_artifacts,omitempty"`
	ToolContextArtifacts                    int                                              `json:"tool_context_artifacts,omitempty"`
	ToolContextMissingArtifacts             int                                              `json:"tool_context_missing_artifacts,omitempty"`
	ToolTruncationExamples                  []agenteval.ToolTruncationExample                `json:"tool_truncation_examples,omitempty"`
	VerifierRuns                            int                                              `json:"verifier_runs"`
	VerifierPassed                          int                                              `json:"verifier_passed"`
	VerifierFailed                          int                                              `json:"verifier_failed"`
	VerifierOutputTruncated                 int                                              `json:"verifier_output_truncated"`
	VerifierOutputOmittedBytes              int                                              `json:"verifier_output_omitted_bytes"`
	TraceSchemaVersions                     map[int]int                                      `json:"trace_schema_versions,omitempty"`
	TraceEventScenarios                     int                                              `json:"trace_event_scenarios,omitempty"`
	TraceEvents                             int                                              `json:"trace_events,omitempty"`
	TraceEventTypes                         map[string]int                                   `json:"trace_event_types,omitempty"`
	InputTokens                             int                                              `json:"input_tokens"`
	OutputTokens                            int                                              `json:"output_tokens"`
	AvgInputTokens                          float64                                          `json:"avg_input_tokens"`
	AvgOutputTokens                         float64                                          `json:"avg_output_tokens"`
	AvgTotalTokens                          float64                                          `json:"avg_total_tokens"`
	MaxScenarioTotalTokens                  int                                              `json:"max_scenario_total_tokens,omitempty"`
	MaxScenarioTokenScenario                string                                           `json:"max_scenario_token_scenario,omitempty"`
	EndCompleted                            int                                              `json:"end_completed"`
	EndMaxTurns                             int                                              `json:"end_max_turns"`
	EndErrors                               int                                              `json:"end_errors"`
	EndCancelled                            int                                              `json:"end_cancelled"`
	EndUnknown                              int                                              `json:"end_unknown"`
	FailureKinds                            map[string]int                                   `json:"failure_kinds,omitempty"`
	FailureExamples                         map[string][]batchFailureExample                 `json:"failure_examples,omitempty"`
	FailureHints                            failureHintMap                                   `json:"failure_hints,omitempty"`
	ToolFailureHints                        failureHintMap                                   `json:"tool_failure_hints,omitempty"`
	RuntimeErrorHints                       failureHintMap                                   `json:"runtime_error_hints,omitempty"`
	DebugBriefByTag                         map[string]int                                   `json:"debug_brief_by_tag,omitempty"`
	DebugBriefTagExamples                   map[string][]batchDebugBriefTagExample           `json:"debug_brief_tag_examples,omitempty"`
	ExpectationScenarios                    int                                              `json:"expectation_scenarios,omitempty"`
	ExpectationSuites                       map[string]int                                   `json:"expectation_suites,omitempty"`
	ExpectationDomains                      map[string]int                                   `json:"expectation_domains,omitempty"`
	ExpectationDomainPassed                 map[string]int                                   `json:"expectation_domain_passed,omitempty"`
	ExpectationDomainFailed                 map[string]int                                   `json:"expectation_domain_failed,omitempty"`
	ExpectationDomainRate                   map[string]float64                               `json:"expectation_domain_pass_rate,omitempty"`
	ExpectationDomainMetrics                map[string]expectationDomainMetrics              `json:"expectation_domain_metrics,omitempty"`
	ExpectationDomainTotal                  *int                                             `json:"expectation_domain_total,omitempty"`
	ExpectationDomainPassedTotal            *int                                             `json:"expectation_domain_passed_total,omitempty"`
	ExpectationDomainFailedTotal            *int                                             `json:"expectation_domain_failed_total,omitempty"`
	ExpectationDomainPassRateTotal          *float64                                         `json:"expectation_domain_pass_rate_total,omitempty"`
	ExpectationDomainFailureExamples        map[string][]expectationDomainFailureExample     `json:"expectation_domain_failure_examples,omitempty"`
	ExpectationCapabilities                 map[string]int                                   `json:"expectation_capabilities,omitempty"`
	ExpectationCapabilityPassed             map[string]int                                   `json:"expectation_capability_passed,omitempty"`
	ExpectationCapabilityFailed             map[string]int                                   `json:"expectation_capability_failed,omitempty"`
	ExpectationCapabilityRate               map[string]float64                               `json:"expectation_capability_pass_rate,omitempty"`
	ExpectationCapabilityTotal              *int                                             `json:"expectation_capability_total,omitempty"`
	ExpectationCapabilityPassedTotal        *int                                             `json:"expectation_capability_passed_total,omitempty"`
	ExpectationCapabilityFailedTotal        *int                                             `json:"expectation_capability_failed_total,omitempty"`
	ExpectationCapabilityPassRateTotal      *float64                                         `json:"expectation_capability_pass_rate_total,omitempty"`
	ExpectationCapabilityFailureExamples    map[string][]expectationCapabilityFailureExample `json:"expectation_capability_failure_examples,omitempty"`
	ExpectationRequiredTools                map[string]int                                   `json:"expectation_required_tools,omitempty"`
	ExpectationSourceAccess                 map[string]int                                   `json:"expectation_source_access,omitempty"`
	QualityGatesPassed                      *bool                                            `json:"quality_gates_passed,omitempty"`
	QualityGateFailures                     []string                                         `json:"quality_gate_failures,omitempty"`
	RemovedWorkspaces                       int                                              `json:"removed_workspaces"`
	CleanupErrors                           int                                              `json:"cleanup_errors"`

	// Per-batch delegation aggregates. Same omitempty discipline as
	// the per-scenario record so a batch with zero delegation usage
	// emits a record without any focused_task_* / subagent_* fields.
	FocusedTaskCalls      int            `json:"focused_task_calls,omitempty"`
	FocusedTaskByType     map[string]int `json:"focused_task_by_type,omitempty"`
	FocusedTaskSources    map[string]int `json:"focused_task_sources,omitempty"`
	FocusedTaskErrors     int            `json:"focused_task_errors,omitempty"`
	FocusedTaskIncomplete int            `json:"focused_task_incomplete,omitempty"`
	SubagentCalls         int            `json:"subagent_calls,omitempty"`
	SubagentByMode        map[string]int `json:"subagent_by_mode,omitempty"`
	SubagentSources       map[string]int `json:"subagent_sources,omitempty"`
	SubagentErrors        int            `json:"subagent_errors,omitempty"`
	SubagentIncomplete    int            `json:"subagent_incomplete,omitempty"`

	// Per-batch plan-tool aggregates. Omitted when no scenario used plan.
	PlanCalls    int                     `json:"plan_calls,omitempty"`
	PlanByAction map[string]int          `json:"plan_by_action,omitempty"`
	PlanErrors   int                     `json:"plan_errors,omitempty"`
	PlanExamples []agenteval.PlanExample `json:"plan_examples,omitempty"`
}

type runtimeSurfaceSummary struct {
	ToolCount                    int                      `json:"tool_count"`
	Tools                        []string                 `json:"tools,omitempty"`
	WorkspacePathArgs            map[string][]string      `json:"workspace_path_args,omitempty"`
	ToolCallCaps                 map[string]int           `json:"tool_call_caps,omitempty"`
	Capabilities                 *sse.RuntimeCapabilities `json:"capabilities,omitempty"`
	Workspace                    *sse.RuntimeWorkspace    `json:"workspace,omitempty"`
	MaxTurnSteps                 int                      `json:"max_turn_steps,omitempty"`
	MaxToolCalls                 int                      `json:"max_tool_calls,omitempty"`
	MaxTurnInputTokens           int                      `json:"max_turn_input_tokens,omitempty"`
	ModelContextWindowTokens     int                      `json:"model_context_window_tokens,omitempty"`
	ReservedOutputTokens         int                      `json:"reserved_output_tokens,omitempty"`
	CompactTriggerInputTokens    int                      `json:"compact_trigger_input_tokens,omitempty"`
	CompactTriggerInputPercent   int                      `json:"compact_trigger_input_percent,omitempty"`
	ToolResultEventCapBytes      int                      `json:"tool_result_event_cap_bytes,omitempty"`
	ToolResultContextMaxBytes    int                      `json:"tool_result_context_max_bytes,omitempty"`
	ToolResultContextBudgetBytes int                      `json:"tool_result_context_budget_bytes,omitempty"`
	ToolResultArtifactPrefix     string                   `json:"tool_result_artifact_prefix,omitempty"`
	TurnToolOverride             bool                     `json:"turn_tool_override,omitempty"`
}

func printBatchResultJSONL(w io.Writer, meta evalJSONLMetadata, res agenteval.BatchResult) {
	failureKinds := failureKindsForResult(res.Failures)
	expectationCapabilityNames := batchResultExpectationCapabilityNames(res)
	expectationCapabilityOutcome := batchResultExpectationCapabilityOutcome(res, expectationCapabilityNames)
	writeJSONLine(w, batchResultRecord{
		evalJSONLMetadata:                      meta,
		Type:                                   "scenario",
		Scenario:                               res.BatchScenario,
		OK:                                     res.OK,
		RunExitCode:                            res.RunExitCode,
		DurationMS:                             res.Duration.Milliseconds(),
		Workspace:                              res.Workspace,
		TracePath:                              res.TracePath,
		DebugManifestPath:                      retainedDebugPath(res.DebugManifestPath, res.WorkspaceRemoved),
		TimelinePath:                           retainedDebugPath(res.TimelinePath, res.WorkspaceRemoved),
		FinalTextPath:                          retainedDebugPath(res.FinalTextPath, res.WorkspaceRemoved),
		StdoutPath:                             retainedDebugPath(res.StdoutPath, res.WorkspaceRemoved),
		StderrPath:                             retainedDebugPath(res.StderrPath, res.WorkspaceRemoved),
		AffentctlCommand:                       append([]string(nil), res.AffentctlCommand...),
		Expectations:                           res.Expectations,
		ExpectationCapabilityNames:             expectationCapabilityNames,
		ExpectationCapabilityOutcome:           expectationCapabilityOutcome,
		ExpectationCapabilityPassedNames:       batchResultExpectationCapabilityPassedNames(res, expectationCapabilityNames),
		ExpectationCapabilityFailedNames:       batchResultExpectationCapabilityFailedNames(res, expectationCapabilityNames),
		TraceSchemaVersion:                     res.TraceSchemaVersion,
		TurnEndReason:                          res.TurnEndReason,
		ToolCalls:                              res.ToolCalls,
		ToolErrors:                             res.ToolStats.ToolErrors,
		ToolRepaired:                           res.ToolStats.ToolArgsRepaired,
		ToolNameCanonicalized:                  res.ToolStats.ToolNameCanonicalized,
		ToolRepairCalls:                        res.Repair.Calls,
		ToolRepairSucceeded:                    res.Repair.SucceededCalls,
		ToolRepairFailed:                       res.Repair.FailedCalls,
		ToolRepairNotes:                        res.Repair.Notes,
		ToolRepairByKind:                       cloneStringIntMap(res.Repair.ByKind),
		ToolRepairExamples:                     cloneToolRepairExamples(res.ToolRepairExamples),
		ConversationRepairs:                    cloneConversationRepairs(res.ConversationRepairs),
		ToolFailureByKind:                      cloneStringIntMap(res.ToolStats.ToolFailureByKind),
		ToolFailureExamples:                    cloneToolFailureExamples(res.ToolFailureExamples),
		LoopGuardExamples:                      cloneLoopGuardExamples(res.LoopGuardExamples),
		MemoryUpdateExamples:                   cloneMemoryUpdateExamples(res.MemoryUpdateExamples),
		RuntimeErrorByKind:                     cloneStringIntMap(res.RuntimeErrorByKind),
		RuntimeErrorExamples:                   cloneRuntimeErrorExamples(res.RuntimeErrorExamples),
		RuntimeSurface:                         runtimeSurfaceSummaryForJSONL(res.RuntimeSurface),
		TaskState:                              agenteval.CloneTaskStateSnapshotPtr(res.TaskState),
		TaskStateStatus:                        res.TaskState.Status,
		TaskStateVerification:                  res.TaskState.VerificationState,
		TaskStateRequestMode:                   res.TaskState.RequestMode,
		TaskStateRequestSource:                 res.TaskState.RequestSource,
		TaskStateScheduleID:                    res.TaskState.ScheduleID,
		TaskStateScheduleKind:                  res.TaskState.ScheduleKind,
		TaskStateChangedFiles:                  len(res.TaskState.ChangedFiles),
		TaskStateAttemptedActions:              len(res.TaskState.AttemptedActions),
		TaskStateFailedActions:                 len(res.TaskState.FailedActions),
		TaskStateEvidence:                      len(res.TaskState.Evidence),
		LoopDecisions:                          res.LoopDecisionStats.Count,
		LoopDecisionByKind:                     cloneStringIntMap(res.LoopDecisionStats.ByKind),
		LoopDecisionByDecision:                 cloneStringIntMap(res.LoopDecisionStats.ByDecision),
		LoopDecisionExamples:                   cloneLoopDecisionExamples(res.LoopDecisionStats.Examples),
		LoopTurnCheckpoints:                    res.LoopTurnCheckpoints.Count,
		LoopTurnCheckpointExamples:             cloneLoopTurnCheckpointExamples(res.LoopTurnCheckpoints.Examples),
		LoopProtocolFeeds:                      res.LoopProtocolFeeds.Count,
		LoopProtocolFeedByMode:                 cloneStringIntMap(res.LoopProtocolFeeds.ByMode),
		LoopProtocolFeedExamples:               cloneLoopProtocolFeedExamples(res.LoopProtocolFeeds.Examples),
		LoopProtocolCalibrationRequests:        res.LoopProtocolCalibrationRequests.Count,
		LoopProtocolCalibrationRequestExamples: cloneLoopProtocolCalibrationExamples(res.LoopProtocolCalibrationRequests.Examples),
		LoopProtocolCalibrations:               res.LoopProtocolCalibrations.Count,
		LoopProtocolCalibrationExamples:        cloneLoopProtocolCalibrationExamples(res.LoopProtocolCalibrations.Examples),
		ContextCompactions:                     res.ContextCompactions.Count,
		ContextCompactionsReactive:             res.ContextCompactions.Reactive,
		ContextCompactionRemoved:               res.ContextCompactions.RemovedMessages,
		ContextCompactionReducedBytes:          res.ContextCompactions.ReducedBytes,
		ContextCompactionSummary:               res.ContextCompactions.SummaryBytes,
		ContextCompactionSummaryMissing:        res.ContextCompactions.SummaryMissing,
		ContextCompactionSummaryEmpty:          res.ContextCompactions.SummaryEmpty,
		ContextCompactionPolicyObserved:        res.ContextCompactions.PolicyObserved,
		ContextCompactionMaxPolicyPressure:     res.ContextCompactions.MaxPolicyPressurePercent,
		ContextCompactionExamples:              cloneContextCompactionExamples(res.ContextCompactions.Examples),
		ContextInjections:                      res.ContextInjections.Count,
		ContextInjectionBySource:               cloneStringIntMap(res.ContextInjections.BySource),
		ContextInjectionBytes:                  res.ContextInjections.Bytes,
		ContextInjectionEstimatedTokens:        res.ContextInjections.EstimatedTokens,
		ContextInjectionExamples:               cloneContextInjectionExamples(res.ContextInjections.Examples),
		LoopGuardInterventions:                 res.ToolStats.LoopGuardInterventions,
		ForcedNoTools:                          res.ToolStats.ForcedNoTools,
		SourceAccessResults:                    res.ToolStats.SourceAccessResults,
		SourceAccessVerified:                   res.ToolStats.SourceAccessVerified,
		SourceAccessDiscoveryOnly:              res.ToolStats.SourceAccessDiscoveryOnly,
		SourceAccessNetwork:                    res.ToolStats.SourceAccessNetwork,
		SourceAccessDynamicPartial:             res.ToolStats.SourceAccessDynamicPartial,
		SourceAccessExamples:                   cloneSourceAccessExamples(res.SourceAccessExamples),
		BrowserScrollExamples:                  cloneBrowserScrollExamples(res.BrowserScrollExamples),
		BrowserNetworkExamples:                 cloneBrowserNetworkExamples(res.BrowserNetworkExamples),
		MemoryUpdates:                          res.ToolStats.MemoryUpdates,
		MemoryUpdateAdd:                        res.ToolStats.MemoryUpdateAdd,
		MemoryUpdateReplace:                    res.ToolStats.MemoryUpdateReplace,
		MemoryUpdateRemove:                     res.ToolStats.MemoryUpdateRemove,
		MemorySearchCalls:                      res.ToolStats.MemorySearchCalls,
		MemorySearchMisses:                     res.ToolStats.MemorySearchMisses,
		MemorySearchMissExamples:               cloneMemorySearchMissExamples(res.MemorySearchMissExamples),
		SessionSearchCalls:                     res.ToolStats.SessionSearchCalls,
		SessionSearchResults:                   res.ToolStats.SessionSearchResults,
		SessionSearchContextHits:               res.ToolStats.SessionSearchContextHits,
		SessionSearchMatchedTerms:              res.ToolStats.SessionSearchMatchedTerms,
		SessionSearchRecent:                    res.ToolStats.SessionSearchRecent,
		SessionSearchExamples:                  cloneSessionSearchExamples(res.SessionSearchExamples),
		ToolDurationMS:                         res.ToolStats.ToolDurationMS,
		ToolContextTruncated:                   max(res.ToolStats.ToolContextTruncated, res.ToolTruncation.ContextTruncated),
		ToolContextOmittedBytes:                max(res.ToolStats.ToolContextOmittedBytes, res.ToolTruncation.ContextOmittedBytes),
		ToolArgsTruncated:                      res.ToolTruncation.ArgsTruncated,
		ToolArgsOmittedBytes:                   res.ToolTruncation.ArgsOmittedBytes,
		ToolResultsTruncated:                   res.ToolTruncation.ResultsTruncated,
		ToolResultsOmittedBytes:                res.ToolTruncation.ResultsOmittedBytes,
		ToolResultArtifacts:                    res.ToolTruncation.ResultArtifacts,
		ToolResultMissingArtifacts:             toolResultMissingArtifacts(res.ToolTruncation),
		ToolContextArtifacts:                   res.ToolTruncation.ContextArtifacts,
		ToolContextMissingArtifacts:            res.ToolTruncation.ContextMissingArtifacts,
		ToolTruncationExamples:                 cloneToolTruncationExamples(res.ToolTruncationExamples),
		VerifierCommand:                        res.Verifier.Command,
		VerifierRan:                            res.Verifier.Ran,
		VerifierOK:                             res.Verifier.OK,
		VerifierExitCode:                       res.Verifier.ExitCode,
		VerifierDurationMS:                     res.Verifier.Duration.Milliseconds(),
		VerifierOutputBytes:                    res.Verifier.OutputBytes,
		VerifierOutputTruncated:                res.Verifier.OutputTruncated,
		VerifierOutputOmittedBytes:             res.Verifier.OutputOmittedBytes,
		VerifierOutputCapBytes:                 res.Verifier.OutputCapBytes,
		TraceEvents:                            res.TraceEvents,
		TraceEventTypes:                        cloneStringIntMap(res.TraceEventTypes),
		InputTokens:                            res.Usage.InputTokens,
		OutputTokens:                           res.Usage.OutputTokens,
		WorkspaceRemoved:                       res.WorkspaceRemoved,
		CleanupError:                           res.CleanupError,
		Failures:                               res.Failures,
		FailureKinds:                           failureKinds,
		FailureHints:                           failureHintsForKinds(failureKinds),
		ToolFailureHints:                       toolFailureHintsForKinds(res.ToolStats.ToolFailureByKind),
		RuntimeErrorHints:                      failureHintsForKinds(res.RuntimeErrorByKind),
		DebugBrief:                             agenteval.BuildDebugBrief(res),
		FocusedTaskCalls:                       res.Delegation.FocusedTaskCalls,
		FocusedTaskByType:                      res.Delegation.FocusedTaskByType,
		FocusedTaskSources:                     res.Delegation.FocusedTaskSourceFindingsByType,
		FocusedTaskErrors:                      res.Delegation.FocusedTaskErrors,
		FocusedTaskIncomplete:                  res.Delegation.FocusedTaskIncomplete,
		SubagentCalls:                          res.Delegation.SubagentCalls,
		SubagentByMode:                         res.Delegation.SubagentByMode,
		SubagentSources:                        res.Delegation.SubagentSourceEvidenceByMode,
		SubagentErrors:                         res.Delegation.SubagentErrors,
		SubagentIncomplete:                     res.Delegation.SubagentIncomplete,
		PlanCalls:                              res.Plan.Calls,
		PlanByAction:                           cloneStringIntMap(res.Plan.ByAction),
		PlanErrors:                             res.Plan.Errors,
		PlanExamples:                           clonePlanExamples(res.PlanExamples),
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
		WorkspacePathArgs:            runtimeSurfaceWorkspacePathArgs(surface),
		ToolCallCaps:                 runtimeSurfaceToolCallCaps(surface),
		Capabilities:                 &caps,
		Workspace:                    surface.Workspace,
		MaxTurnSteps:                 surface.MaxTurnSteps,
		MaxToolCalls:                 surface.MaxToolCalls,
		MaxTurnInputTokens:           surface.MaxTurnInputTokens,
		ModelContextWindowTokens:     surface.ModelContextWindowTokens,
		ReservedOutputTokens:         surface.ReservedOutputTokens,
		CompactTriggerInputTokens:    surface.CompactTriggerInputTokens,
		CompactTriggerInputPercent:   surface.CompactTriggerInputPercent,
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

func runtimeSurfaceWorkspacePathArgs(surface *sse.RuntimeSurfacePayload) map[string][]string {
	if surface == nil || len(surface.Tools) == 0 {
		return nil
	}
	out := map[string][]string{}
	for _, tool := range surface.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || tool.ArgPolicy == nil || len(tool.ArgPolicy.WorkspacePathArgs) == 0 {
			continue
		}
		names := append([]string(nil), tool.ArgPolicy.WorkspacePathArgs...)
		sort.Strings(names)
		out[name] = names
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runtimeSurfaceToolCallCaps(surface *sse.RuntimeSurfacePayload) map[string]int {
	if surface == nil || len(surface.ToolCallCaps) == 0 {
		return nil
	}
	caps := make(map[string]int, len(surface.ToolCallCaps))
	for _, cap := range surface.ToolCallCaps {
		name := strings.TrimSpace(cap.Tool)
		if name == "" || cap.Max <= 0 {
			continue
		}
		caps[name] = cap.Max
	}
	if len(caps) == 0 {
		return nil
	}
	return caps
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
	expectationCapabilityPassed, expectationCapabilityTotal := expectationCapabilityPassTotals(s)
	expectationDomainPassed, expectationDomainTotal := expectationDomainPassTotals(s)
	writeJSONLine(w, batchSummaryRecord{
		evalJSONLMetadata:                       meta,
		Type:                                    "summary",
		Scenarios:                               s.Total,
		Passed:                                  s.Passed,
		Failed:                                  s.Failed,
		PassRate:                                batchRatio(s.Passed, s.Total),
		CompletionRate:                          batchRatio(s.EndCompleted, s.Total),
		MemoryUpdateRate:                        batchRatio(s.MemoryUpdates, s.Total),
		MemorySearchMissRate:                    batchOptionalRatio(s.MemorySearchMisses, s.MemorySearchCalls),
		LoopTurnCheckpointRate:                  batchRatio(s.LoopTurnCheckpointScenarios, s.Total),
		LoopProtocolFeedRate:                    batchRatio(s.LoopProtocolFeedScenarios, s.Total),
		LoopProtocolCalibrationRequestRate:      batchRatio(s.LoopProtocolCalibrationRequestScenarios, s.Total),
		LoopProtocolCalibrationRate:             batchRatio(s.LoopProtocolCalibrationScenarios, s.Total),
		ToolErrorRate:                           batchOptionalRatio(s.ToolErrors, s.ToolCalls),
		FocusedTaskErrorRate:                    batchOptionalRatio(s.FocusedTaskErrors, s.FocusedTaskCalls),
		SubagentErrorRate:                       batchOptionalRatio(s.SubagentErrors, s.SubagentCalls),
		ForcedNoToolsRate:                       batchOptionalRatio(s.ForcedNoTools, s.ToolCalls),
		LoopGuardInterventionRate:               batchOptionalRatio(s.LoopGuardInterventions, s.ToolCalls),
		PlanErrorRate:                           batchOptionalRatio(s.PlanErrors, s.PlanCalls),
		ToolRepairSuccessRate:                   batchOptionalRatio(s.ToolRepairSucceeded, s.ToolRepairCalls),
		VerifierPassRate:                        batchOptionalRatio(s.VerifierPassed, s.VerifierRuns),
		SourceAccessVerifiedRate:                batchOptionalRatio(s.SourceAccessVerified, s.SourceAccessResults),
		SourceNetworkRate:                       batchOptionalRatio(s.SourceAccessNetwork, s.SourceAccessResults),
		SourceDiscoveryOnlyRate:                 batchOptionalRatio(s.SourceAccessDiscoveryOnly, s.SourceAccessResults),
		SourceDynamicPartialRate:                batchOptionalRatio(s.SourceAccessDynamicPartial, s.SourceAccessResults),
		SessionSearchContextHitRate:             batchOptionalRatio(s.SessionSearchContextHits, s.SessionSearchResults),
		SessionSearchMatchedTermsPerCall:        batchOptionalRatio(s.SessionSearchMatchedTerms, s.SessionSearchCalls),
		TraceEventRate:                          batchRatio(s.TraceEventScenarios, s.Total),
		AvgRuntimeErrors:                        batchAverage(s.RuntimeErrors, s.Total),
		AvgContextCompactions:                   batchAverage(s.ContextCompactions, s.Total),
		AvgReactiveCompactions:                  batchAverage(s.ContextCompactionsReactive, s.Total),
		AvgContextRemovedMessages:               batchAverage(s.ContextCompactionRemoved, s.Total),
		AvgContextReducedBytes:                  batchAverage(s.ContextCompactionReducedBytes, s.Total),
		AvgContextSummaryBytes:                  batchAverage(s.ContextCompactionSummary, s.Total),
		AvgContextSummaryMissing:                batchAverage(s.ContextCompactionSummaryMissing, s.Total),
		AvgContextSummaryEmpty:                  batchAverage(s.ContextCompactionSummaryEmpty, s.Total),
		ContextCompactionPolicyObserved:         s.ContextCompactionPolicyObserved,
		ContextCompactionMaxPolicyPressure:      s.ContextCompactionMaxPolicyPressure,
		AvgContextInjections:                    batchAverage(s.ContextInjections, s.Total),
		AvgContextInjectionBytes:                batchAverage(s.ContextInjectionBytes, s.Total),
		AvgContextInjectionEstimatedTokens:      batchAverage(s.ContextInjectionEstimatedTokens, s.Total),
		AvgToolCalls:                            batchAverage(s.ToolCalls, s.Total),
		ToolContextTruncationRate:               batchOptionalRatio(s.ToolContextTruncated, s.ToolCalls),
		ToolResultTruncationRate:                batchOptionalRatio(s.ToolResultsTruncated, s.ToolCalls),
		DurationMS:                              s.Duration.Milliseconds(),
		AvgDurationMS:                           batchAverageInt64(s.Duration.Milliseconds(), s.Total),
		ToolCalls:                               s.ToolCalls,
		ToolErrors:                              s.ToolErrors,
		ToolRepaired:                            s.ToolRepaired,
		ToolNameCanonicalized:                   s.ToolNameCanonicalized,
		ToolRepairCalls:                         s.ToolRepairCalls,
		ToolRepairSucceeded:                     s.ToolRepairSucceeded,
		ToolRepairFailed:                        s.ToolRepairFailed,
		ToolRepairNotes:                         s.ToolRepairNotes,
		ToolRepairByKind:                        cloneStringIntMap(s.ToolRepairByKind),
		ToolRepairExamples:                      cloneToolRepairExamples(s.ToolRepairExamples),
		ConversationRepairs:                     s.ConversationRepairs,
		ConversationRepairMissingToolResults:    s.ConversationRepairMissingToolResults,
		ConversationRepairDuplicateResults:      s.ConversationRepairDuplicateResults,
		ConversationRepairUnexpectedResults:     s.ConversationRepairUnexpectedResults,
		ConversationRepairByKind:                cloneStringIntMap(s.ConversationRepairByKind),
		ConversationRepairExamples:              cloneConversationRepairExamples(s.ConversationRepairExamples),
		ToolFailureByKind:                       cloneStringIntMap(s.ToolFailureByKind),
		ToolFailureExamples:                     cloneToolFailureExamples(s.ToolFailureExamples),
		LoopGuardExamples:                       cloneLoopGuardExamples(s.LoopGuardExamples),
		RuntimeErrorByKind:                      cloneStringIntMap(s.RuntimeErrorByKind),
		RuntimeErrorExamples:                    cloneRuntimeErrorExamples(s.RuntimeErrorExamples),
		RuntimeSurfaceRate:                      batchRatio(s.RuntimeSurfaceScenarios, s.Total),
		RuntimeSurfaceScenarios:                 s.RuntimeSurfaceScenarios,
		RuntimeSurfaceTools:                     cloneStringIntMap(s.RuntimeSurfaceTools),
		RuntimeSurfaceCapabilities:              cloneStringIntMap(s.RuntimeSurfaceCapabilities),
		TaskStateRate:                           batchRatio(s.TaskStateScenarios, s.Total),
		TaskStateScenarios:                      s.TaskStateScenarios,
		TaskStateByStatus:                       cloneStringIntMap(s.TaskStateByStatus),
		TaskStateByVerification:                 cloneStringIntMap(s.TaskStateByVerification),
		TaskStateByRequestMode:                  cloneStringIntMap(s.TaskStateByRequestMode),
		TaskStateByRequestSource:                cloneStringIntMap(s.TaskStateByRequestSource),
		TaskStateByScheduleKind:                 cloneStringIntMap(s.TaskStateByScheduleKind),
		TaskStateChangedFiles:                   s.TaskStateChangedFiles,
		TaskStateAttemptedActions:               s.TaskStateAttemptedActions,
		TaskStateFailedActions:                  s.TaskStateFailedActions,
		TaskStateEvidence:                       s.TaskStateEvidence,
		LoopDecisions:                           s.LoopDecisions,
		LoopDecisionByKind:                      cloneStringIntMap(s.LoopDecisionByKind),
		LoopDecisionByDecision:                  cloneStringIntMap(s.LoopDecisionByDecision),
		LoopDecisionExamples:                    cloneLoopDecisionExamples(s.LoopDecisionExamples),
		LoopTurnCheckpointScenarios:             s.LoopTurnCheckpointScenarios,
		LoopTurnCheckpoints:                     s.LoopTurnCheckpoints,
		LoopTurnCheckpointExamples:              cloneLoopTurnCheckpointExamples(s.LoopTurnCheckpointExamples),
		LoopProtocolFeedScenarios:               s.LoopProtocolFeedScenarios,
		LoopProtocolFeeds:                       s.LoopProtocolFeeds,
		LoopProtocolFeedByMode:                  cloneStringIntMap(s.LoopProtocolFeedByMode),
		LoopProtocolFeedExamples:                cloneLoopProtocolFeedExamples(s.LoopProtocolFeedExamples),
		LoopProtocolCalibrationRequestScenarios: s.LoopProtocolCalibrationRequestScenarios,
		LoopProtocolCalibrationRequests:         s.LoopProtocolCalibrationRequests,
		LoopProtocolCalibrationRequestExamples:  cloneLoopProtocolCalibrationExamples(s.LoopProtocolCalibrationRequestExamples),
		LoopProtocolCalibrationScenarios:        s.LoopProtocolCalibrationScenarios,
		LoopProtocolCalibrations:                s.LoopProtocolCalibrations,
		LoopProtocolCalibrationExamples:         cloneLoopProtocolCalibrationExamples(s.LoopProtocolCalibrationExamples),
		ContextCompactions:                      s.ContextCompactions,
		ContextCompactionsReactive:              s.ContextCompactionsReactive,
		ContextCompactionRemoved:                s.ContextCompactionRemoved,
		ContextCompactionReducedBytes:           s.ContextCompactionReducedBytes,
		ContextCompactionSummary:                s.ContextCompactionSummary,
		ContextCompactionSummaryMissing:         s.ContextCompactionSummaryMissing,
		ContextCompactionSummaryEmpty:           s.ContextCompactionSummaryEmpty,
		ContextCompactionExamples:               cloneContextCompactionExamples(s.ContextCompactionExamples),
		ContextInjections:                       s.ContextInjections,
		ContextInjectionBySource:                cloneStringIntMap(s.ContextInjectionBySource),
		ContextInjectionBytes:                   s.ContextInjectionBytes,
		ContextInjectionEstimatedTokens:         s.ContextInjectionEstimatedTokens,
		ContextInjectionExamples:                cloneContextInjectionExamples(s.ContextInjectionExamples),
		LoopGuardInterventions:                  s.LoopGuardInterventions,
		ForcedNoTools:                           s.ForcedNoTools,
		SourceAccessResults:                     s.SourceAccessResults,
		SourceAccessVerified:                    s.SourceAccessVerified,
		SourceAccessDiscoveryOnly:               s.SourceAccessDiscoveryOnly,
		SourceAccessNetwork:                     s.SourceAccessNetwork,
		SourceAccessDynamicPartial:              s.SourceAccessDynamicPartial,
		SourceAccessExamples:                    cloneSourceAccessExamples(s.SourceAccessExamples),
		BrowserScrollExamples:                   cloneBrowserScrollExamples(s.BrowserScrollExamples),
		BrowserNetworkExamples:                  cloneBrowserNetworkExamples(s.BrowserNetworkExamples),
		MemoryUpdates:                           s.MemoryUpdates,
		MemoryUpdateAdd:                         s.MemoryUpdateAdd,
		MemoryUpdateReplace:                     s.MemoryUpdateReplace,
		MemoryUpdateRemove:                      s.MemoryUpdateRemove,
		MemorySearchCalls:                       s.MemorySearchCalls,
		MemorySearchMisses:                      s.MemorySearchMisses,
		MemoryUpdateExamples:                    cloneMemoryUpdateExamples(s.MemoryUpdateExamples),
		MemorySearchMissExamples:                cloneMemorySearchMissExamples(s.MemorySearchMissExamples),
		SessionSearchCalls:                      s.SessionSearchCalls,
		SessionSearchResults:                    s.SessionSearchResults,
		SessionSearchContextHits:                s.SessionSearchContextHits,
		SessionSearchMatchedTerms:               s.SessionSearchMatchedTerms,
		SessionSearchRecent:                     s.SessionSearchRecent,
		SessionSearchExamples:                   cloneSessionSearchExamples(s.SessionSearchExamples),
		ToolDurationMS:                          s.ToolDurationMS,
		ToolContextTruncated:                    s.ToolContextTruncated,
		ToolContextOmittedBytes:                 s.ToolContextOmittedBytes,
		ToolArgsTruncated:                       s.ToolArgsTruncated,
		ToolArgsOmittedBytes:                    s.ToolArgsOmittedBytes,
		ToolResultsTruncated:                    s.ToolResultsTruncated,
		ToolResultsOmittedBytes:                 s.ToolResultsOmittedBytes,
		ToolResultArtifacts:                     s.ToolResultArtifacts,
		ToolResultMissingArtifacts:              s.ToolResultMissingArtifacts,
		ToolContextArtifacts:                    s.ToolContextArtifacts,
		ToolContextMissingArtifacts:             s.ToolContextMissingArtifacts,
		ToolTruncationExamples:                  cloneToolTruncationExamples(s.ToolTruncationExamples),
		VerifierRuns:                            s.VerifierRuns,
		VerifierPassed:                          s.VerifierPassed,
		VerifierFailed:                          s.VerifierFailed,
		VerifierOutputTruncated:                 s.VerifierOutputTruncated,
		VerifierOutputOmittedBytes:              s.VerifierOutputOmittedBytes,
		TraceSchemaVersions:                     cloneTraceSchemaVersions(s.TraceSchemaVersions),
		TraceEventScenarios:                     s.TraceEventScenarios,
		TraceEvents:                             s.TraceEvents,
		TraceEventTypes:                         cloneStringIntMap(s.TraceEventTypes),
		InputTokens:                             s.InputTokens,
		OutputTokens:                            s.OutputTokens,
		AvgInputTokens:                          batchAverage(s.InputTokens, s.Total),
		AvgOutputTokens:                         batchAverage(s.OutputTokens, s.Total),
		AvgTotalTokens:                          batchAverage(s.InputTokens+s.OutputTokens, s.Total),
		MaxScenarioTotalTokens:                  s.MaxScenarioTotalTokens,
		MaxScenarioTokenScenario:                s.MaxScenarioTokenScenario,
		EndCompleted:                            s.EndCompleted,
		EndMaxTurns:                             s.EndMaxTurns,
		EndErrors:                               s.EndErrors,
		EndCancelled:                            s.EndCancelled,
		EndUnknown:                              s.EndUnknown,
		FailureKinds:                            cloneFailureKinds(s.FailureKinds),
		FailureExamples:                         cloneBatchFailureExamples(s.FailureExamples),
		FailureHints:                            failureHintsForKinds(s.FailureKinds),
		ToolFailureHints:                        toolFailureHintsForKinds(s.ToolFailureByKind),
		RuntimeErrorHints:                       failureHintsForKinds(s.RuntimeErrorByKind),
		DebugBriefByTag:                         cloneStringIntMap(s.DebugBriefByTag),
		DebugBriefTagExamples:                   cloneBatchDebugBriefTagExamples(s.DebugBriefTagExamples),
		ExpectationScenarios:                    s.ExpectationScenarios,
		ExpectationSuites:                       cloneStringIntMap(s.ExpectationSuites),
		ExpectationDomains:                      cloneStringIntMap(s.ExpectationDomains),
		ExpectationDomainPassed:                 cloneStringIntMap(s.ExpectationDomainPass),
		ExpectationDomainFailed:                 cloneStringIntMap(s.ExpectationDomainFail),
		ExpectationDomainRate:                   expectationDomainPassRates(s.ExpectationDomains, s.ExpectationDomainPass),
		ExpectationDomainMetrics:                expectationDomainRuntimeMetrics(s.ExpectationDomainRuntime),
		ExpectationDomainTotal:                  optionalInt(expectationDomainTotal, expectationDomainTotal > 0),
		ExpectationDomainPassedTotal:            optionalInt(expectationDomainPassed, expectationDomainTotal > 0),
		ExpectationDomainFailedTotal:            optionalInt(expectationDomainTotal-expectationDomainPassed, expectationDomainTotal > 0),
		ExpectationDomainPassRateTotal:          batchOptionalRatio(expectationDomainPassed, expectationDomainTotal),
		ExpectationDomainFailureExamples:        cloneExpectationDomainFailureExamples(s.ExpectationDomainFailureExamples),
		ExpectationCapabilities:                 cloneStringIntMap(s.ExpectationCapabilities),
		ExpectationCapabilityPassed:             cloneStringIntMap(s.ExpectationCapabilityPass),
		ExpectationCapabilityFailed:             cloneStringIntMap(s.ExpectationCapabilityFail),
		ExpectationCapabilityRate:               expectationCapabilityPassRates(s.ExpectationCapabilities, s.ExpectationCapabilityPass),
		ExpectationCapabilityTotal:              optionalInt(expectationCapabilityTotal, expectationCapabilityTotal > 0),
		ExpectationCapabilityPassedTotal:        optionalInt(expectationCapabilityPassed, expectationCapabilityTotal > 0),
		ExpectationCapabilityFailedTotal:        optionalInt(expectationCapabilityTotal-expectationCapabilityPassed, expectationCapabilityTotal > 0),
		ExpectationCapabilityPassRateTotal:      batchOptionalRatio(expectationCapabilityPassed, expectationCapabilityTotal),
		ExpectationCapabilityFailureExamples:    cloneExpectationCapabilityFailureExamples(s.ExpectationCapabilityFailureExamples),
		ExpectationRequiredTools:                cloneStringIntMap(s.ExpectationRequiredTools),
		ExpectationSourceAccess:                 cloneStringIntMap(s.ExpectationSourceAccess),
		QualityGatesPassed:                      qualityGatesPassedForJSONL(meta, gateFailures),
		QualityGateFailures:                     append([]string(nil), gateFailures...),
		RemovedWorkspaces:                       s.RemovedWorkspaces,
		CleanupErrors:                           s.CleanupErrors,
		FocusedTaskCalls:                        s.FocusedTaskCalls,
		FocusedTaskByType:                       cloneStringIntMap(s.FocusedTaskByType),
		FocusedTaskSources:                      cloneStringIntMap(s.FocusedTaskSources),
		FocusedTaskErrors:                       s.FocusedTaskErrors,
		FocusedTaskIncomplete:                   s.FocusedTaskIncomplete,
		SubagentCalls:                           s.SubagentCalls,
		SubagentByMode:                          cloneStringIntMap(s.SubagentByMode),
		SubagentSources:                         cloneStringIntMap(s.SubagentSources),
		SubagentErrors:                          s.SubagentErrors,
		SubagentIncomplete:                      s.SubagentIncomplete,
		PlanCalls:                               s.PlanCalls,
		PlanByAction:                            cloneStringIntMap(s.PlanByAction),
		PlanErrors:                              s.PlanErrors,
		PlanExamples:                            clonePlanExamples(s.PlanExamples),
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
		meta.MinLoopTurnCheckpointRate != nil ||
		meta.MinLoopProtocolFeedRate != nil ||
		meta.MinLoopProtocolCalibrationRequestRate != nil ||
		meta.MinLoopProtocolCalibrationRate != nil ||
		meta.MinRuntimeSurfaceRate != nil ||
		meta.MinTraceEventRate != nil ||
		meta.MinSourceNetworkRate != nil ||
		meta.MinSourceAccessVerifiedRate != nil ||
		meta.MinExpectationCapabilityPassRate != nil ||
		meta.MinEachExpectationCapabilityPassRate != nil ||
		meta.MinExpectationDomainPassRate != nil ||
		meta.MinEachExpectationDomainPassRate != nil ||
		meta.MinSessionSearchContextHitRate != nil ||
		meta.MinSessionSearchMatchedTermsPerCall != nil ||
		meta.MinToolRepairSuccessRate != nil ||
		meta.MinVerifierPassRate != nil ||
		meta.MaxFocusedTaskErrorRate != nil ||
		meta.MaxForcedNoToolsRate != nil ||
		meta.MaxLoopGuardInterventionRate != nil ||
		meta.MaxPlanErrorRate != nil ||
		meta.MaxMemorySearchMissRate != nil ||
		meta.MaxSourceDiscoveryOnlyRate != nil ||
		meta.MaxSourceDynamicPartialRate != nil ||
		meta.MaxSubagentErrorRate != nil ||
		meta.MaxToolErrorRate != nil ||
		meta.MaxToolContextTruncationRate != nil ||
		meta.MaxToolResultTruncationRate != nil ||
		meta.MaxAvgRuntimeErrors != nil ||
		meta.MaxAvgContextCompactions != nil ||
		meta.MaxAvgReactiveCompactions != nil ||
		meta.MaxAvgContextRemovedMessages != nil ||
		meta.MaxAvgContextSummaryBytes != nil ||
		meta.MaxAvgContextSummaryMissing != nil ||
		meta.MaxAvgContextSummaryEmpty != nil ||
		meta.MaxAvgContextInjections != nil ||
		meta.MaxAvgContextInjectionBytes != nil ||
		meta.MaxAvgContextInjectionEstimatedTokens != nil ||
		meta.MaxAvgToolCalls != nil ||
		meta.MaxAvgDurationMS != nil ||
		meta.MaxAvgTotalTokens != nil ||
		meta.MaxScenarioTotalTokens != nil ||
		len(meta.MaxDebugBriefTagRates) > 0 ||
		len(meta.MinExpectationDomainSourceAccessVerifiedRates) > 0 ||
		len(meta.MaxExpectationDomainAvgTotalTokens) > 0 ||
		len(meta.MaxExpectationDomainAvgToolCalls) > 0 ||
		len(meta.MaxExpectationDomainAvgRuntimeErrors) > 0 ||
		len(meta.MaxExpectationDomainToolErrorRates) > 0 ||
		len(meta.MaxExpectationDomainLoopGuardInterventionRates) > 0 ||
		len(meta.RequiredExpectationCapabilities) > 0 ||
		len(meta.RequiredExpectationDomains) > 0
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

func expectationCapabilityFamilyGateFailures(s batchSummary, threshold *float64) []string {
	if threshold == nil || *threshold < 0 {
		return nil
	}
	rates := expectationCapabilityPassRates(s.ExpectationCapabilities, s.ExpectationCapabilityPass)
	if len(rates) == 0 {
		return []string{fmt.Sprintf("expectation_capability_family_pass_rate unavailable, want >= %s", formatGateFloat(*threshold))}
	}
	var failures []string
	for cap, rate := range rates {
		if rate < *threshold {
			failures = append(failures, fmt.Sprintf("expectation_capability_pass_rate[%s] %s < min %s", cap, formatGateFloat(rate), formatGateFloat(*threshold)))
		}
	}
	sort.Strings(failures)
	return failures
}

func expectationDomainPassRates(total, passed map[string]int) map[string]float64 {
	if len(total) == 0 {
		return nil
	}
	out := map[string]float64{}
	for domain, count := range total {
		if count <= 0 {
			continue
		}
		out[domain] = float64(passed[domain]) / float64(count)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func expectationDomainRuntimeMetrics(totals map[string]*expectationDomainRuntimeTotals) map[string]expectationDomainMetrics {
	if len(totals) == 0 {
		return nil
	}
	out := make(map[string]expectationDomainMetrics, len(totals))
	for domain, total := range totals {
		if total == nil || total.Scenarios <= 0 {
			continue
		}
		out[domain] = expectationDomainMetrics{
			Scenarios:                  total.Scenarios,
			Passed:                     total.Passed,
			Failed:                     total.Failed,
			PassRate:                   batchRatio(total.Passed, total.Scenarios),
			AvgDurationMS:              batchAverageInt64(total.Duration.Milliseconds(), total.Scenarios),
			AvgToolCalls:               batchAverage(total.ToolCalls, total.Scenarios),
			AvgRuntimeErrors:           batchAverage(total.RuntimeErrors, total.Scenarios),
			AvgTotalTokens:             batchAverage(total.InputTokens+total.OutputTokens, total.Scenarios),
			MemoryUpdateRate:           batchRatio(total.MemoryUpdates, total.Scenarios),
			ToolErrorRate:              batchOptionalRatio(total.ToolErrors, total.ToolCalls),
			LoopGuardInterventionRate:  batchOptionalRatio(total.LoopGuardInterventions, total.ToolCalls),
			SourceAccessVerifiedRate:   batchOptionalRatio(total.SourceAccessVerified, total.SourceAccessResults),
			SourceNetworkRate:          batchOptionalRatio(total.SourceAccessNetwork, total.SourceAccessResults),
			SourceDiscoveryOnlyRate:    batchOptionalRatio(total.SourceAccessDiscoveryOnly, total.SourceAccessResults),
			SourceDynamicPartialRate:   batchOptionalRatio(total.SourceAccessDynamicPartial, total.SourceAccessResults),
			SourceAccessResults:        total.SourceAccessResults,
			SourceAccessVerified:       total.SourceAccessVerified,
			SourceAccessNetwork:        total.SourceAccessNetwork,
			SourceAccessDiscoveryOnly:  total.SourceAccessDiscoveryOnly,
			SourceAccessDynamicPartial: total.SourceAccessDynamicPartial,
			ToolCalls:                  total.ToolCalls,
			ToolErrors:                 total.ToolErrors,
			LoopGuardInterventions:     total.LoopGuardInterventions,
			RuntimeErrors:              total.RuntimeErrors,
			InputTokens:                total.InputTokens,
			OutputTokens:               total.OutputTokens,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func expectationDomainPassTotals(s batchSummary) (passed int, total int) {
	for domain, count := range s.ExpectationDomains {
		if count <= 0 {
			continue
		}
		total += count
		passed += s.ExpectationDomainPass[domain]
	}
	return passed, total
}

func expectationDomainFamilyGateFailures(s batchSummary, threshold *float64) []string {
	if threshold == nil || *threshold < 0 {
		return nil
	}
	rates := expectationDomainPassRates(s.ExpectationDomains, s.ExpectationDomainPass)
	if len(rates) == 0 {
		return []string{fmt.Sprintf("expectation_domain_family_pass_rate unavailable, want >= %s", formatGateFloat(*threshold))}
	}
	var failures []string
	for domain, rate := range rates {
		if rate < *threshold {
			failures = append(failures, fmt.Sprintf("expectation_domain_pass_rate[%s] %s < min %s", domain, formatGateFloat(rate), formatGateFloat(*threshold)))
		}
	}
	sort.Strings(failures)
	return failures
}

func qualityGatePreflightFailures(scenarios []agenteval.BatchScenario, g qualityGateConfig) []string {
	if len(g.RequiredExpectationCapabilities) == 0 && len(g.RequiredExpectationDomains) == 0 {
		return nil
	}
	availableCaps := map[string]bool{}
	availableDomains := map[string]bool{}
	for _, scenario := range scenarios {
		for _, cap := range agenteval.ScenarioExpectationCapabilityNames(scenario) {
			availableCaps[cap] = true
		}
		for _, domain := range agenteval.ScenarioExpectationDomains(scenario) {
			availableDomains[domain] = true
		}
	}
	var failures []string
	for _, cap := range g.RequiredExpectationCapabilities {
		cap = strings.TrimSpace(cap)
		if cap == "" || availableCaps[cap] {
			continue
		}
		failures = append(failures, fmt.Sprintf("expectation_capability[%s] unavailable, want >= 1 selected scenario", cap))
	}
	for _, domain := range g.RequiredExpectationDomains {
		domain = strings.TrimSpace(domain)
		if domain == "" || availableDomains[domain] {
			continue
		}
		failures = append(failures, fmt.Sprintf("expectation_domain[%s] unavailable, want >= 1 selected scenario", domain))
	}
	return failures
}

func printScenarioCoverage(w io.Writer, scenarios []agenteval.BatchScenario, gates qualityGateConfig, qualityProfile string) {
	suiteCounts := map[string]int{}
	capabilityCounts := map[string]int{}
	domainCounts := map[string]int{}
	capabilityScenarios := map[string][]string{}
	domainScenarios := map[string][]string{}
	for _, scenario := range scenarios {
		for _, suite := range uniqueSortedStrings(scenario.Suites) {
			suiteCounts[suite]++
		}
		for _, cap := range agenteval.ScenarioExpectationCapabilityNames(scenario) {
			capabilityCounts[cap]++
			capabilityScenarios[cap] = append(capabilityScenarios[cap], scenario.Name)
		}
		for _, domain := range agenteval.ScenarioExpectationDomains(scenario) {
			domainCounts[domain]++
			domainScenarios[domain] = append(domainScenarios[domain], scenario.Name)
		}
	}
	fmt.Fprintf(w, "COVERAGE scenarios=%d suites=%s capabilities=%s domains=%s\n",
		len(scenarios),
		formatStringIntCounts(suiteCounts),
		formatStringIntCounts(capabilityCounts),
		formatStringIntCounts(domainCounts),
	)
	printScenarioCoveragePreflight(w, scenarios, gates, qualityProfile)
	printScenarioCoverageIndex(w, "CAPABILITY_SCENARIOS", capabilityScenarios)
	printScenarioCoverageIndex(w, "DOMAIN_SCENARIOS", domainScenarios)
	fmt.Fprintln(w, "SCENARIOS")
	for _, scenario := range scenarios {
		fmt.Fprintf(w, "  %s suites=%s capabilities=%s domains=%s\n",
			scenario.Name,
			formatStringList(uniqueSortedStrings(scenario.Suites)),
			formatStringList(agenteval.ScenarioExpectationCapabilityNames(scenario)),
			formatStringList(agenteval.ScenarioExpectationDomains(scenario)),
		)
	}
}

func printScenarioCoveragePreflight(w io.Writer, scenarios []agenteval.BatchScenario, gates qualityGateConfig, qualityProfile string) {
	if !hasCoveragePreflightRequirements(gates) {
		return
	}
	status := "passed"
	failures := qualityGatePreflightFailures(scenarios, gates)
	if len(failures) > 0 {
		status = "failed"
	}
	profile := strings.TrimSpace(qualityProfile)
	if profile != "" {
		fmt.Fprintf(w, "COVERAGE_PREFLIGHT status=%s profile=%s\n", status, profile)
	} else {
		fmt.Fprintf(w, "COVERAGE_PREFLIGHT status=%s\n", status)
	}
	for _, failure := range failures {
		fmt.Fprintf(w, "  - %s\n", failure)
	}
}

func hasCoveragePreflightRequirements(g qualityGateConfig) bool {
	return len(g.RequiredExpectationCapabilities) > 0 || len(g.RequiredExpectationDomains) > 0
}

func printScenarioCoverageIndex(w io.Writer, title string, index map[string][]string) {
	fmt.Fprintln(w, title)
	if len(index) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	keys := make([]string, 0, len(index))
	for key := range index {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "  %s: %s\n", key, formatStringList(uniqueSortedStrings(index[key])))
	}
}

func formatStringList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func expectationDomainMetricGateFailures(s batchSummary, g qualityGateConfig) []string {
	metrics := expectationDomainRuntimeMetrics(s.ExpectationDomainRuntime)
	var failures []string
	checkMinMap := func(name string, thresholds map[string]float64, actual func(expectationDomainMetrics) (*float64, bool)) {
		for _, domain := range sortedFloatMapKeys(thresholds) {
			threshold := thresholds[domain]
			if threshold < 0 {
				continue
			}
			metric, ok := metrics[domain]
			if !ok {
				failures = append(failures, fmt.Sprintf("%s[%s] unavailable, want >= %s", name, domain, formatGateFloat(threshold)))
				continue
			}
			value, available := actual(metric)
			if !available || value == nil {
				failures = append(failures, fmt.Sprintf("%s[%s] unavailable, want >= %s", name, domain, formatGateFloat(threshold)))
				continue
			}
			if *value < threshold {
				failures = append(failures, fmt.Sprintf("%s[%s] %s < min %s", name, domain, formatGateFloat(*value), formatGateFloat(threshold)))
			}
		}
	}
	checkMaxMap := func(name string, thresholds map[string]float64, actual func(expectationDomainMetrics) (*float64, bool)) {
		for _, domain := range sortedFloatMapKeys(thresholds) {
			threshold := thresholds[domain]
			if threshold < 0 {
				continue
			}
			metric, ok := metrics[domain]
			if !ok {
				failures = append(failures, fmt.Sprintf("%s[%s] unavailable, want <= %s", name, domain, formatGateFloat(threshold)))
				continue
			}
			value, available := actual(metric)
			if !available || value == nil {
				continue
			}
			if *value > threshold {
				failures = append(failures, fmt.Sprintf("%s[%s] %s > max %s", name, domain, formatGateFloat(*value), formatGateFloat(threshold)))
			}
		}
	}
	floatPtr := func(value float64) *float64 { return &value }
	checkMinMap("expectation_domain_source_access_verified_rate", g.MinExpectationDomainSourceAccessVerifiedRates, func(metric expectationDomainMetrics) (*float64, bool) {
		return metric.SourceAccessVerifiedRate, metric.SourceAccessResults > 0
	})
	checkMaxMap("expectation_domain_avg_total_tokens", g.MaxExpectationDomainAvgTotalTokens, func(metric expectationDomainMetrics) (*float64, bool) {
		return floatPtr(metric.AvgTotalTokens), true
	})
	checkMaxMap("expectation_domain_avg_tool_calls", g.MaxExpectationDomainAvgToolCalls, func(metric expectationDomainMetrics) (*float64, bool) {
		return floatPtr(metric.AvgToolCalls), true
	})
	checkMaxMap("expectation_domain_avg_runtime_errors", g.MaxExpectationDomainAvgRuntimeErrors, func(metric expectationDomainMetrics) (*float64, bool) {
		return floatPtr(metric.AvgRuntimeErrors), true
	})
	checkMaxMap("expectation_domain_tool_error_rate", g.MaxExpectationDomainToolErrorRates, func(metric expectationDomainMetrics) (*float64, bool) {
		return metric.ToolErrorRate, metric.ToolCalls > 0
	})
	checkMaxMap("expectation_domain_loop_guard_intervention_rate", g.MaxExpectationDomainLoopGuardInterventionRates, func(metric expectationDomainMetrics) (*float64, bool) {
		return metric.LoopGuardInterventionRate, metric.ToolCalls > 0
	})
	sort.Strings(failures)
	return failures
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

func cloneStringFloatMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func uniqueSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneToolFailureExamples(in map[string][]agenteval.ToolFailureExample) map[string][]agenteval.ToolFailureExample {
	return cloneExampleMap(in)
}

func cloneToolRepairExamples(in []agenteval.ToolRepairExample) []agenteval.ToolRepairExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]agenteval.ToolRepairExample, 0, len(in))
	for _, ex := range in {
		if len(ex.RepairNotes) > 0 {
			ex.RepairNotes = append([]string(nil), ex.RepairNotes...)
		}
		if len(ex.RepairKinds) > 0 {
			ex.RepairKinds = append([]string(nil), ex.RepairKinds...)
		}
		out = append(out, ex)
	}
	return out
}

func cloneLoopGuardExamples(in []agenteval.LoopGuardExample) []agenteval.LoopGuardExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.LoopGuardExample(nil), in...)
}

func cloneRuntimeErrorExamples(in map[string][]agenteval.RuntimeErrorExample) map[string][]agenteval.RuntimeErrorExample {
	return cloneExampleMap(in)
}

func cloneBatchFailureExamples(in map[string][]batchFailureExample) map[string][]batchFailureExample {
	return cloneExampleMap(in)
}

func cloneBatchDebugBriefTagExamples(in map[string][]batchDebugBriefTagExample) map[string][]batchDebugBriefTagExample {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]batchDebugBriefTagExample, len(in))
	for tag, examples := range in {
		out[tag] = make([]batchDebugBriefTagExample, 0, len(examples))
		for _, ex := range examples {
			ex.FailureKinds = cloneStringIntMap(ex.FailureKinds)
			out[tag] = append(out[tag], ex)
		}
	}
	return out
}

func cloneExpectationCapabilityFailureExamples(in map[string][]expectationCapabilityFailureExample) map[string][]expectationCapabilityFailureExample {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]expectationCapabilityFailureExample, len(in))
	for cap, examples := range in {
		if len(examples) == 0 {
			continue
		}
		out[cap] = make([]expectationCapabilityFailureExample, 0, len(examples))
		for _, ex := range examples {
			ex.FailureKinds = cloneStringIntMap(ex.FailureKinds)
			if len(ex.DebugBriefTags) > 0 {
				ex.DebugBriefTags = append([]string(nil), ex.DebugBriefTags...)
			}
			out[cap] = append(out[cap], ex)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneExpectationDomainFailureExamples(in map[string][]expectationDomainFailureExample) map[string][]expectationDomainFailureExample {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]expectationDomainFailureExample, len(in))
	for domain, examples := range in {
		if len(examples) == 0 {
			continue
		}
		out[domain] = make([]expectationDomainFailureExample, 0, len(examples))
		for _, ex := range examples {
			ex.FailureKinds = cloneStringIntMap(ex.FailureKinds)
			if len(ex.DebugBriefTags) > 0 {
				ex.DebugBriefTags = append([]string(nil), ex.DebugBriefTags...)
			}
			out[domain] = append(out[domain], ex)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneSourceAccessExamples(in []agenteval.SourceAccessExample) []agenteval.SourceAccessExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.SourceAccessExample(nil), in...)
}

func cloneBrowserScrollExamples(in []agenteval.BrowserScrollExample) []agenteval.BrowserScrollExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.BrowserScrollExample(nil), in...)
}

func cloneBrowserNetworkExamples(in []agenteval.BrowserNetworkSearchExample) []agenteval.BrowserNetworkSearchExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]agenteval.BrowserNetworkSearchExample, 0, len(in))
	for _, ex := range in {
		if len(ex.Refs) > 0 {
			ex.Refs = append([]string(nil), ex.Refs...)
		}
		if len(ex.Previews) > 0 {
			ex.Previews = append([]string(nil), ex.Previews...)
		}
		out = append(out, ex)
	}
	return out
}

func cloneMemoryUpdateExamples(in []agenteval.MemoryUpdateExample) []agenteval.MemoryUpdateExample {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.MemoryUpdateExample(nil), in...)
}

func cloneMemorySearchMissExamples(in []agenteval.MemorySearchMissExample) []agenteval.MemorySearchMissExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]agenteval.MemorySearchMissExample, 0, len(in))
	for _, ex := range in {
		if len(ex.Topics) > 0 {
			ex.Topics = append([]string(nil), ex.Topics...)
		}
		out = append(out, ex)
	}
	return out
}

func cloneSessionSearchExamples(in []agenteval.SessionSearchExample) []agenteval.SessionSearchExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]agenteval.SessionSearchExample, 0, len(in))
	for _, ex := range in {
		if len(ex.MatchedTerms) > 0 {
			ex.MatchedTerms = append([]string(nil), ex.MatchedTerms...)
		}
		out = append(out, ex)
	}
	return out
}

func clonePlanExamples(in []agenteval.PlanExample) []agenteval.PlanExample {
	if len(in) == 0 {
		return nil
	}
	out := make([]agenteval.PlanExample, 0, len(in))
	for _, ex := range in {
		if len(ex.Evidence) > 0 {
			ex.Evidence = append([]string(nil), ex.Evidence...)
		}
		out = append(out, ex)
	}
	return out
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

func cloneLoopTurnCheckpointExamples(in []agenteval.LoopTurnCheckpoint) []agenteval.LoopTurnCheckpoint {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.LoopTurnCheckpoint(nil), in...)
}

func cloneConversationRepairs(in []sse.ConversationRepairedPayload) []sse.ConversationRepairedPayload {
	if len(in) == 0 {
		return nil
	}
	return append([]sse.ConversationRepairedPayload(nil), in...)
}

func cloneConversationRepairExamples(in []batchConversationRepairExample) []batchConversationRepairExample {
	if len(in) == 0 {
		return nil
	}
	return append([]batchConversationRepairExample(nil), in...)
}

func cloneLoopProtocolFeedExamples(in []agenteval.LoopProtocolFeed) []agenteval.LoopProtocolFeed {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.LoopProtocolFeed(nil), in...)
}

func cloneLoopProtocolCalibrationExamples(in []agenteval.LoopProtocolCalibration) []agenteval.LoopProtocolCalibration {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.LoopProtocolCalibration(nil), in...)
}

func cloneContextCompactionExamples(in []agenteval.ContextCompaction) []agenteval.ContextCompaction {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.ContextCompaction(nil), in...)
}

func cloneContextInjectionExamples(in []agenteval.ContextInjection) []agenteval.ContextInjection {
	if len(in) == 0 {
		return nil
	}
	return append([]agenteval.ContextInjection(nil), in...)
}

func appendLoopDecisionExamples(dst, src []agenteval.LoopDecision, scenario string, limit int) []agenteval.LoopDecision {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendLoopProtocolFeedExamples(dst, src []agenteval.LoopProtocolFeed, scenario string, limit int) []agenteval.LoopProtocolFeed {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendLoopTurnCheckpointExamples(dst, src []agenteval.LoopTurnCheckpoint, scenario string, limit int) []agenteval.LoopTurnCheckpoint {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendLoopProtocolCalibrationExamples(dst, src []agenteval.LoopProtocolCalibration, scenario string, limit int) []agenteval.LoopProtocolCalibration {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendToolRepairExamples(dst, src []agenteval.ToolRepairExample, scenario string, limit int) []agenteval.ToolRepairExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if len(ex.RepairNotes) > 0 {
			ex.RepairNotes = append([]string(nil), ex.RepairNotes...)
		}
		if len(ex.RepairKinds) > 0 {
			ex.RepairKinds = append([]string(nil), ex.RepairKinds...)
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendConversationRepairExamples(dst []batchConversationRepairExample, src []sse.ConversationRepairedPayload, scenario string, limit int) []batchConversationRepairExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, repair := range src {
		if len(dst) >= limit {
			break
		}
		dst = append(dst, batchConversationRepairExample{
			Scenario:              scenario,
			SessionID:             repair.SessionID,
			MissingToolResults:    repair.MissingToolResults,
			DuplicateToolResults:  repair.DuplicateToolResults,
			UnexpectedToolResults: repair.UnexpectedToolResults,
			FailureKind:           repair.FailureKind,
			Next:                  repair.Next,
		})
	}
	return dst
}

func appendLoopGuardExamples(dst, src []agenteval.LoopGuardExample, scenario string, limit int) []agenteval.LoopGuardExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendSourceAccessExamples(dst, src []agenteval.SourceAccessExample, scenario string, limit int) []agenteval.SourceAccessExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendBrowserScrollExamples(dst, src []agenteval.BrowserScrollExample, scenario string, limit int) []agenteval.BrowserScrollExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendBrowserNetworkExamples(dst, src []agenteval.BrowserNetworkSearchExample, scenario string, limit int) []agenteval.BrowserNetworkSearchExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if len(ex.Refs) > 0 {
			ex.Refs = append([]string(nil), ex.Refs...)
		}
		if len(ex.Previews) > 0 {
			ex.Previews = append([]string(nil), ex.Previews...)
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendMemoryUpdateExamples(dst, src []agenteval.MemoryUpdateExample, scenario string, limit int) []agenteval.MemoryUpdateExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendMemorySearchMissExamples(dst, src []agenteval.MemorySearchMissExample, scenario string, limit int) []agenteval.MemorySearchMissExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if len(ex.Topics) > 0 {
			ex.Topics = append([]string(nil), ex.Topics...)
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendSessionSearchExamples(dst, src []agenteval.SessionSearchExample, scenario string, limit int) []agenteval.SessionSearchExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if len(ex.MatchedTerms) > 0 {
			ex.MatchedTerms = append([]string(nil), ex.MatchedTerms...)
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendPlanExamples(dst, src []agenteval.PlanExample, scenario string, limit int) []agenteval.PlanExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if len(ex.Evidence) > 0 {
			ex.Evidence = append([]string(nil), ex.Evidence...)
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendToolTruncationExamples(dst, src []agenteval.ToolTruncationExample, scenario string, limit int) []agenteval.ToolTruncationExample {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendContextCompactionExamples(dst, src []agenteval.ContextCompaction, scenario string, limit int) []agenteval.ContextCompaction {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
		}
		dst = append(dst, ex)
	}
	return dst
}

func appendContextInjectionExamples(dst, src []agenteval.ContextInjection, scenario string, limit int) []agenteval.ContextInjection {
	if limit <= 0 || len(dst) >= limit {
		return dst
	}
	for _, ex := range src {
		if len(dst) >= limit {
			break
		}
		if ex.Scenario == "" {
			ex.Scenario = scenario
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
		missingArtifacts := toolResultMissingArtifacts(res.ToolTruncation) + res.ToolTruncation.ContextMissingArtifacts
		fmt.Fprintf(w, " trunc=args:%d,results:%d,artifacts:%d,ctx_artifacts:%d,missing_artifacts:%d omitted=%d/%d",
			res.ToolTruncation.ArgsTruncated,
			res.ToolTruncation.ResultsTruncated,
			res.ToolTruncation.ResultArtifacts,
			res.ToolTruncation.ContextArtifacts,
			missingArtifacts,
			res.ToolTruncation.ArgsOmittedBytes,
			res.ToolTruncation.ResultsOmittedBytes,
		)
	}
	if hasToolContextTruncation(res.ToolStats) || res.ToolTruncation.ContextTruncated > 0 || res.ToolTruncation.ContextOmittedBytes > 0 {
		fmt.Fprintf(w, " ctx_trunc=%d,omitted=%d,artifacts=%d,missing_artifacts=%d",
			max(res.ToolStats.ToolContextTruncated, res.ToolTruncation.ContextTruncated),
			max(res.ToolStats.ToolContextOmittedBytes, res.ToolTruncation.ContextOmittedBytes),
			res.ToolTruncation.ContextArtifacts,
			res.ToolTruncation.ContextMissingArtifacts,
		)
	}
	if res.Repair.HasAny() {
		fmt.Fprintf(w, " repair_calls=%d,ok=%d,failed=%d", res.Repair.Calls, res.Repair.SucceededCalls, res.Repair.FailedCalls)
	}
	if len(res.Repair.ByKind) > 0 {
		fmt.Fprintf(w, " repair_kinds=%s", formatStringIntCounts(res.Repair.ByKind))
	}
	if len(res.ConversationRepairs) > 0 {
		repairs, missing, duplicate, unexpected, byKind := conversationRepairSummary(res.ConversationRepairs)
		fmt.Fprintf(w, " conversation_repairs=%d,missing_tool_results=%d", repairs, missing)
		if duplicate > 0 {
			fmt.Fprintf(w, ",duplicate_tool_results=%d", duplicate)
		}
		if unexpected > 0 {
			fmt.Fprintf(w, ",unexpected_tool_results=%d", unexpected)
		}
		if len(byKind) > 0 {
			fmt.Fprintf(w, " conversation_repair_kinds=%s", formatStringIntCounts(byKind))
		}
	}
	if len(res.ToolStats.ToolFailureByKind) > 0 {
		fmt.Fprintf(w, " tool_failure_kinds=%s", formatStringIntCounts(res.ToolStats.ToolFailureByKind))
	}
	if res.ToolStats.ToolUnclassifiedErrors > 0 {
		fmt.Fprintf(w, " unclassified_tool_errors=%d", res.ToolStats.ToolUnclassifiedErrors)
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
	if res.ToolStats.MemorySearchCalls > 0 || res.ToolStats.MemorySearchMisses > 0 {
		fmt.Fprintf(w, " memory_search=calls:%d,misses:%d", res.ToolStats.MemorySearchCalls, res.ToolStats.MemorySearchMisses)
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
	if res.LoopTurnCheckpoints.Count > 0 {
		fmt.Fprintf(w, " loop_turn_checkpoints=%d", res.LoopTurnCheckpoints.Count)
	}
	if res.LoopProtocolFeeds.Count > 0 {
		fmt.Fprintf(w, " loop_protocol_feeds=%d", res.LoopProtocolFeeds.Count)
		if len(res.LoopProtocolFeeds.ByMode) > 0 {
			fmt.Fprintf(w, " loop_protocol_feed_modes=%s", formatStringIntCounts(res.LoopProtocolFeeds.ByMode))
		}
	}
	if res.LoopProtocolCalibrationRequests.Count > 0 || res.LoopProtocolCalibrations.Count > 0 {
		fmt.Fprintf(w, " loop_protocol_calibration=requests:%d,answers:%d", res.LoopProtocolCalibrationRequests.Count, res.LoopProtocolCalibrations.Count)
	}
	if res.ContextCompactions.Count > 0 {
		fmt.Fprintf(w, " compactions=%d,reactive=%d,removed=%d,reduced_bytes=%d,summary_bytes=%d,summary_missing=%d,summary_empty=%d,policy_observed=%d,max_policy_pressure=%d%%",
			res.ContextCompactions.Count,
			res.ContextCompactions.Reactive,
			res.ContextCompactions.RemovedMessages,
			res.ContextCompactions.ReducedBytes,
			res.ContextCompactions.SummaryBytes,
			res.ContextCompactions.SummaryMissing,
			res.ContextCompactions.SummaryEmpty,
			res.ContextCompactions.PolicyObserved,
			res.ContextCompactions.MaxPolicyPressurePercent,
		)
	}
	if res.ContextInjections.Count > 0 {
		fmt.Fprintf(w, " context_injections=%d,bytes=%d,est_tokens=%d",
			res.ContextInjections.Count,
			res.ContextInjections.Bytes,
			res.ContextInjections.EstimatedTokens,
		)
		if len(res.ContextInjections.BySource) > 0 {
			fmt.Fprintf(w, " context_injection_sources=%s", formatStringIntCounts(res.ContextInjections.BySource))
		}
	}
	if brief := agenteval.BuildDebugBrief(res); brief != nil {
		fmt.Fprintf(w, " debug_brief=%s", formatDebugBriefTags(brief.Tags))
	}
	printDelegationRollup(w, res.Delegation.FocusedTaskCalls, res.Delegation.FocusedTaskByType, res.Delegation.FocusedTaskSourceFindingsByType, res.Delegation.FocusedTaskErrors, res.Delegation.FocusedTaskIncomplete, res.Delegation.SubagentCalls, res.Delegation.SubagentByMode, res.Delegation.SubagentSourceEvidenceByMode, res.Delegation.SubagentErrors, res.Delegation.SubagentIncomplete)
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
	printToolRepairExampleLines(w, res.ToolRepairExamples, "  ")
	printConversationRepairExampleLines(w, appendConversationRepairExamples(nil, res.ConversationRepairs, res.BatchScenario, len(res.ConversationRepairs)), "  ")
	printToolFailureHintLines(w, res.ToolStats.ToolFailureByKind, "  ")
	printToolFailureExampleLines(w, res.ToolFailureExamples, "  ")
	printLoopGuardExampleLines(w, res.LoopGuardExamples, "  ")
	printSourceAccessExampleLines(w, res.SourceAccessExamples, "  ")
	printBrowserScrollExampleLines(w, res.BrowserScrollExamples, "  ")
	printBrowserNetworkExampleLines(w, res.BrowserNetworkExamples, "  ")
	printMemoryUpdateExampleLines(w, res.MemoryUpdateExamples, "  ")
	printMemorySearchMissExampleLines(w, res.MemorySearchMissExamples, "  ")
	printFailureHintLines(w, res.RuntimeErrorByKind, "  ")
	printRuntimeErrorExampleLines(w, res.RuntimeErrorExamples, "  ")
	printLoopDecisionExampleLines(w, res.LoopDecisionStats.Examples, "  ")
	printLoopTurnCheckpointExampleLines(w, res.LoopTurnCheckpoints.Examples, "  ")
	printLoopProtocolFeedExampleLines(w, res.LoopProtocolFeeds.Examples, "  ")
	printLoopProtocolCalibrationExampleLines(w, "loop_protocol_calibration_request_example", res.LoopProtocolCalibrationRequests.Examples, "  ")
	printLoopProtocolCalibrationExampleLines(w, "loop_protocol_calibration_example", res.LoopProtocolCalibrations.Examples, "  ")
	printContextCompactionExampleLines(w, res.ContextCompactions.Examples, "  ")
	printContextInjectionExampleLines(w, res.ContextInjections.Examples, "  ")
	printSessionSearchExampleLines(w, res.SessionSearchExamples, "  ")
	printPlanExampleLines(w, res.PlanExamples, "  ")
	printToolTruncationExampleLines(w, res.ToolTruncationExamples, "  ")
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

func toolResultMissingArtifacts(stats agenteval.ToolTruncationStats) int {
	if stats.ResultMissingArtifacts > 0 {
		return stats.ResultMissingArtifacts
	}
	if stats.ResultsTruncated > stats.ResultArtifacts {
		return stats.ResultsTruncated - stats.ResultArtifacts
	}
	return 0
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
		stats.SessionSearchMatchedTerms > 0 ||
		stats.SessionSearchRecent > 0
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
	case strings.Contains(lower, "requires loop protocol feeds"):
		return "loop_protocol_fixture"
	case strings.HasPrefix(lower, "source repo "):
		return "source_repo_setup"
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
			name = strings.TrimSpace(name)
			if name == "" || name == "source_access" {
				continue
			}
			enabled[name] = true
			if name == "web_fetch" || name == "web_search" || strings.HasPrefix(name, "browser_") {
				enabled["source_access"] = true
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
			add("web_fetch", "web_search")
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
	add := func(tool string) {
		if tool = strings.TrimSpace(tool); tool != "" {
			required[tool] = true
		}
	}
	for _, tool := range scenario.RequiredTools {
		add(tool)
	}
	for tool := range scenario.RequiredToolCounts {
		add(tool)
	}
	for stat := range scenario.RequiredToolStatsAtLeast {
		for _, tool := range runtimeToolsForRequiredStat(stat) {
			add(tool)
		}
	}
	for tool := range scenario.RequiredToolResultText {
		add(tool)
	}
	for _, req := range scenario.RequiredToolArgContains {
		add(req.Tool)
	}
	for _, tool := range scenario.RequiredTruncatedResults {
		add(tool)
	}
	for _, tool := range scenario.RequiredResultArtifacts {
		add(tool)
	}
	for _, order := range scenario.RequiredToolOrder {
		add(order.Earlier)
		add(order.Later)
	}
	for _, req := range scenario.RequiredSourceAccess {
		add(req.Tool)
	}
	if len(scenario.RequiredSessionSearch) > 0 || len(scenario.RequiredRecentSessionSearch) > 0 {
		add(agent.SessionSearchToolName)
	}
	if len(scenario.RequiredFocusedTaskCounts) > 0 || len(scenario.RequiredFocusedTaskSourceCounts) > 0 {
		add(agent.FocusedTaskToolName)
	}
	if len(scenario.RequiredSubagentModeCounts) > 0 || len(scenario.RequiredSubagentSourceCounts) > 0 {
		add(agent.SubagentToolName)
	}
	if len(scenario.RequiredCommands) > 0 ||
		len(scenario.RequiredCommandCounts) > 0 ||
		len(scenario.RequiredCommandOrder) > 0 ||
		len(scenario.RequiredCommandBeforeTool) > 0 ||
		len(scenario.RequiredCommandAfterTool) > 0 {
		add("shell")
	}
	for _, req := range scenario.RequiredCommandBeforeTool {
		add(req.Tool)
	}
	for _, req := range scenario.RequiredCommandAfterTool {
		add(req.Tool)
	}
	out := make([]string, 0, len(required))
	for tool := range required {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func runtimeToolsForRequiredStat(stat string) []string {
	stat = strings.TrimSpace(stat)
	switch {
	case stat == "memory_updates" || strings.HasPrefix(stat, "memory_update_") || stat == "memory_search_calls" || stat == "memory_search_misses":
		return []string{agent.MemoryToolName}
	case strings.HasPrefix(stat, "session_search_"):
		return []string{agent.SessionSearchToolName}
	case strings.HasPrefix(stat, "source_access_"):
		return []string{"source_access"}
	case strings.Contains(stat, "focused_task"):
		return []string{agent.FocusedTaskToolName}
	case strings.Contains(stat, "subagent"):
		return []string{agent.SubagentToolName}
	case strings.Contains(stat, "plan"):
		return []string{agent.PlanToolName}
	default:
		return nil
	}
}

func evalWorkspaceToolNames() []string {
	return []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", "symbol_context", "repo_search"}
}

func evalReadonlyWorkspaceToolNames() []string {
	return []string{"read_file", "file_context", "list_files", "symbol_context", "repo_search"}
}

func resolveSessionTracePath(sessionID, sessionStateRoot, repoRoot string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if err := agent.ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	var roots []string
	if root := strings.TrimSpace(sessionStateRoot); root != "" {
		roots = append(roots, root)
	} else {
		for _, root := range []string{
			os.Getenv("AFFENTEVAL_SESSION_STATE_ROOT"),
			os.Getenv("AFFENTSERVE_MEMORY_ROOT"),
			repoLocalSessionStateRoot(repoRoot),
		} {
			root = strings.TrimSpace(root)
			if root != "" {
				roots = append(roots, root)
			}
		}
	}
	seen := map[string]bool{}
	var inspected []string
	for _, root := range roots {
		root = filepath.Clean(root)
		if root == "." || seen[root] {
			continue
		}
		seen[root] = true
		tracePath := filepath.Join(root, sessionID, "events.jsonl")
		inspected = append(inspected, tracePath)
		if info, err := os.Stat(tracePath); err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("session %q trace path is a directory: %s", sessionID, tracePath)
			}
			return tracePath, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat session %q trace path %s: %w", sessionID, tracePath, err)
		}
	}
	if len(inspected) == 0 {
		return "", fmt.Errorf("session %q trace not found; set --session-state-root or AFFENTSERVE_MEMORY_ROOT", sessionID)
	}
	return "", fmt.Errorf("session %q trace not found; inspected: %s", sessionID, strings.Join(inspected, ", "))
}

func traceWorkspaceFromSessionMetadata(sessionDir string) (string, error) {
	meta, found, err := sessionstate.ReadMetadata(sessionDir)
	if err != nil {
		return "", fmt.Errorf("read session metadata: %w", err)
	}
	if !found {
		return "", nil
	}
	return strings.TrimSpace(meta.WorkspacePath), nil
}

func repoLocalSessionStateRoot(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}
	return filepath.Join(repoRoot, ".tmp", "runtime-workspace", "session-state")
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
