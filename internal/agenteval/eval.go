package agenteval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	DefaultBatchTimeout           = 5 * time.Minute
	DefaultBatchMaxTurnSteps      = 10
	DefaultVerifierOutputCapBytes = 1 * 1024 * 1024
	maxTraceLineBytes             = jsonl.DefaultMaxRecordBytes
)

type ToolOrderRequirement struct {
	Earlier string
	Later   string
}

type CommandToolOrderRequirement struct {
	Command string
	Tool    string
}

type ToolArgContainsRequirement struct {
	Tool      string
	Arg       string
	Substring string
	// Min is the required number of matching tool calls. Values <=0 default
	// to one so scenarios do not need to spell out the common case.
	Min int
}

type LoopDecisionRequirement struct {
	Kind     string
	Decision string
	Trigger  string
	// Min is the required number of matching loop.decision events. Values
	// <=0 default to one so scenarios can spell the common case tersely.
	Min int
}

type BatchScenario struct {
	Name                          string
	Suites                        []string
	Prompt                        string
	SessionID                     string
	ExecutePlan                   bool
	EnableMemory                  bool
	Files                         map[string]string
	VerifyCommand                 string
	VerifierTimeout               time.Duration
	ExpectedSkill                 string
	ForbiddenCommands             []string
	RequiredCommands              []string
	RequiredCommandCounts         map[string]int
	RequiredToolCounts            map[string]int
	RequiredToolFailureKindCounts map[string]int
	RequiredToolStatsAtLeast      map[string]int
	RequiredLoopDecisionKinds     map[string]int
	RequiredLoopDecisionResults   map[string]int
	RequiredLoopDecisionMatches   []LoopDecisionRequirement
	RequiredContextCompactions    int
	RequiredReactiveCompactions   int
	RequiredCompactionRemovedMsgs int
	RequiredCommandBeforeTool     []CommandToolOrderRequirement
	RequiredCommandAfterTool      []CommandToolOrderRequirement
	RequiredTools                 []string
	ForbiddenTools                []string
	RequiredFocusedTaskCounts     map[string]int
	RequiredSubagentModeCounts    map[string]int
	RequireNoDelegationErrors     bool
	RequireNoPlanErrors           bool
	RequiredFinalText             []string
	ForbiddenFinalText            []string
	RequiredToolResultText        map[string][]string
	RequiredToolArgContains       []ToolArgContainsRequirement
	RequiredTruncatedResults      []string
	RequiredResultArtifacts       []string
	RequiredToolOrder             []ToolOrderRequirement
	ProtectedFiles                []string
	ForbiddenFileSubstrings       map[string][]string
	MaxParentToolCalls            int
	MaxSuccessfulToolCallsByTool  map[string]int
	MaxTurns                      int
}

type BatchRunner struct {
	RepoRoot                 string
	WorkRoot                 string
	BaseURL                  string
	APIKey                   string
	Model                    string
	Temperature              string
	TopP                     string
	MaxTokens                string
	Seed                     string
	Executor                 string
	RuntimeEvalMode          bool
	RuntimeTools             string
	RuntimeAllTools          bool
	RuntimeMemory            bool
	RuntimeWeb               bool
	RuntimeBrowser           bool
	RuntimeMCPConfig         string
	TraceDeltas              bool
	GoBin                    string
	Timeout                  time.Duration
	VerifierOutputCapBytes   int
	CleanupPassingWorkspaces bool
}

type BatchResult struct {
	BatchScenario        string
	Workspace            string
	TracePath            string
	DebugManifestPath    string
	TimelinePath         string
	FinalTextPath        string
	StdoutPath           string
	StderrPath           string
	AffentctlCommand     []string
	RunExitCode          int
	OK                   bool
	Failures             []string
	Duration             time.Duration
	FinalText            string
	TraceSchemaVersion   int
	TraceEvents          int
	TraceEventTypes      map[string]int
	TurnEndReason        string
	ToolCalls            int
	ToolStats            ToolRuntimeStats
	RuntimeErrorByKind   map[string]int
	RuntimeErrorExamples map[string][]RuntimeErrorExample
	LoopDecisionStats    LoopDecisionStats
	ContextCompactions   ContextCompactionStats
	ToolFailureExamples  map[string][]ToolFailureExample
	ToolTruncation       ToolTruncationStats
	Usage                Usage
	Verifier             VerifierResult
	WorkspaceRemoved     bool
	CleanupError         string
	TraceDeltas          bool
	// Delegation aggregates focused-task / subagent calls observed
	// in the trace. Zero-value when the scenario used no delegation
	// tool; HasAny() reports whether the block is worth surfacing.
	Delegation DelegationStats
	// Plan aggregates persisted-plan tool usage. Zero-value when the
	// scenario did not call the plan tool.
	Plan PlanStats
	// Repair aggregates tool-call recovery kinds from tool.request
	// repair_notes. Zero-value when no tool repair/canonicalization
	// occurred.
	Repair ToolRepairStats
	// RuntimeSurface is the latest effective tool/runtime surface observed
	// in the trace. Nil for old traces or runs that failed before turn start.
	RuntimeSurface *sse.RuntimeSurfacePayload
}

type DebugManifest struct {
	SchemaVersion    int                        `json:"schema_version"`
	Scenario         string                     `json:"scenario"`
	OK               bool                       `json:"ok"`
	Workspace        string                     `json:"workspace"`
	TracePath        string                     `json:"trace_path"`
	TimelinePath     string                     `json:"timeline_path,omitempty"`
	FinalTextPath    string                     `json:"final_text_path,omitempty"`
	StdoutPath       string                     `json:"stdout_path,omitempty"`
	StderrPath       string                     `json:"stderr_path,omitempty"`
	AffentctlCommand []string                   `json:"affentctl_command,omitempty"`
	RunExitCode      int                        `json:"run_exit_code"`
	ConversationDir  string                     `json:"conversation_dir,omitempty"`
	ArtifactDir      string                     `json:"artifact_dir,omitempty"`
	TraceDeltas      bool                       `json:"trace_deltas,omitempty"`
	Prompt           string                     `json:"prompt"`
	Failures         []string                   `json:"failures,omitempty"`
	DebugBrief       *DebugBrief                `json:"debug_brief,omitempty"`
	Metrics          DebugMetrics               `json:"metrics"`
	RuntimeSurface   *sse.RuntimeSurfacePayload `json:"runtime_surface,omitempty"`
	GeneratedAt      string                     `json:"generated_at"`
}

type DebugMetrics struct {
	TurnEndReason              string         `json:"turn_end_reason,omitempty"`
	ToolCalls                  int            `json:"tool_calls"`
	ToolErrors                 int            `json:"tool_errors"`
	ToolArgsRepaired           int            `json:"tool_args_repaired"`
	ToolNameCanonicalized      int            `json:"tool_name_canonicalized"`
	ToolRepairCalls            int            `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded        int            `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed           int            `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes            int            `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind           map[string]int `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind          map[string]int `json:"tool_failure_by_kind,omitempty"`
	LoopGuardInterventions     int            `json:"loop_guard_interventions"`
	ForcedNoTools              int            `json:"forced_no_tools"`
	SourceAccessResults        int            `json:"source_access_results"`
	SourceAccessVerified       int            `json:"source_access_verified"`
	SourceAccessDiscoveryOnly  int            `json:"source_access_discovery_only"`
	SourceAccessNetwork        int            `json:"source_access_network"`
	SourceAccessDynamicPartial int            `json:"source_access_dynamic_partial"`
	MemoryUpdates              int            `json:"memory_updates"`
	MemoryUpdateAdd            int            `json:"memory_update_add,omitempty"`
	MemoryUpdateReplace        int            `json:"memory_update_replace,omitempty"`
	MemoryUpdateRemove         int            `json:"memory_update_remove,omitempty"`
	SessionSearchCalls         int            `json:"session_search_calls,omitempty"`
	SessionSearchResults       int            `json:"session_search_results,omitempty"`
	SessionSearchContextHits   int            `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms  int            `json:"session_search_matched_terms,omitempty"`
	ContextCompactions         int            `json:"context_compactions"`
	ReactiveContextCompactions int            `json:"reactive_context_compactions"`
	ContextCompactionRemoved   int            `json:"context_compaction_removed_messages"`
	ContextCompactionSummary   int            `json:"context_compaction_summary_bytes,omitempty"`
	ToolContextTruncated       int            `json:"tool_context_truncated,omitempty"`
	ToolContextOmittedBytes    int            `json:"tool_context_omitted_bytes,omitempty"`
	InputTokens                int            `json:"input_tokens"`
	OutputTokens               int            `json:"output_tokens"`
	TraceEvents                int            `json:"trace_events,omitempty"`
	TraceEventTypes            map[string]int `json:"trace_event_types,omitempty"`
}

type VerifierResult struct {
	Command            string
	Ran                bool
	OK                 bool
	ExitCode           int
	Duration           time.Duration
	OutputBytes        int
	OutputTruncated    bool
	OutputOmittedBytes int
	OutputCapBytes     int
}

func BuiltinBatchScenarios() []BatchScenario {
	return []BatchScenario{
		goMedianScenario(),
		goConfigPrecedenceScenario(),
		pythonSlugScenario(),
		goRedactionScenario(),
		pythonConfigParserScenario(),
		promptInjectionFactsScenario(),
		focusedTaskProjectFactsScenario(),
		subagentProjectFactsScenario(),
		subagentNoisyFactsScenario(),
		subagentNestedFactsScenario(),
		smallToolBadJSONReadScenario(),
		smallToolWrongFieldReadScenario(),
		smallToolWrongToolNameScenario(),
		defaultRuntimeSymbolContextScenario(),
		defaultRuntimeSymbolContextRuntimeCapabilitiesScenario(),
		defaultRuntimeSymbolContextThenReadFileScenario(),
		defaultRuntimeFileContextScenario(),
		defaultRuntimeRepoSearchScenario(),
		skillToolReadScenario(),
		skillRemoteInstallGuardScenario(),
		planCodingRepairScenario(),
		planNotForSimpleReadScenario(),
		planResumeCurrentStepScenario(),
		memoryCrossSessionRecallScenario(),
		sessionHistoryRecallScenario(),
		longRunMultiTaskSessionRecoveryScenario(),
		memoryConfirmedWriteStatsScenario(),
		smallToolRepeatedReadScenario(),
		smallToolEditRecoveryScenario(),
		smallToolShellFailureScenario(),
		oversizedToolResultScenario(),
		longRunStockAnalysisScenario(),
		longRunBittensorSubnetScenario(),
		longRunCodePRScenario(),
		liveWebTaostatsDynamicEvidenceScenario(),
	}
}

func BatchScenarioNames() []string {
	scenarios := BuiltinBatchScenarios()
	names := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func BatchSuiteNames() []string {
	seen := map[string]bool{}
	for _, s := range BuiltinBatchScenarios() {
		for _, suite := range s.Suites {
			if strings.TrimSpace(suite) != "" {
				seen[suite] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func SelectBatchScenarios(names []string) ([]BatchScenario, error) {
	return SelectBatchScenariosForSuite("", names)
}

func SelectBatchScenariosForSuite(suite string, names []string) ([]BatchScenario, error) {
	all := BuiltinBatchScenarios()
	if suite != "" {
		filtered := all[:0]
		for _, s := range all {
			if scenarioInSuite(s, suite) {
				filtered = append(filtered, s)
			}
		}
		all = filtered
		if len(all) == 0 {
			return nil, fmt.Errorf("unknown suite %q (valid: %s)", suite, strings.Join(BatchSuiteNames(), ", "))
		}
	}
	if len(names) == 0 {
		return all, nil
	}
	byName := map[string]BatchScenario{}
	for _, s := range all {
		byName[s.Name] = s
	}
	var selected []BatchScenario
	for _, name := range names {
		s, ok := byName[name]
		if !ok {
			valid := make([]string, 0, len(all))
			for _, s := range all {
				valid = append(valid, s.Name)
			}
			sort.Strings(valid)
			return nil, fmt.Errorf("unknown scenario %q (valid: %s)", name, strings.Join(valid, ", "))
		}
		selected = append(selected, s)
	}
	return selected, nil
}

func scenarioInSuite(s BatchScenario, suite string) bool {
	for _, candidate := range s.Suites {
		if candidate == suite {
			return true
		}
	}
	return false
}

func (r BatchRunner) Run(ctx context.Context, scenario BatchScenario) BatchResult {
	start := time.Now()
	res := BatchResult{BatchScenario: scenario.Name, TraceDeltas: r.TraceDeltas}
	if r.Timeout <= 0 {
		r.Timeout = DefaultBatchTimeout
	}
	if scenario.MaxTurns <= 0 {
		scenario.MaxTurns = DefaultBatchMaxTurnSteps
	}
	if strings.TrimSpace(r.RepoRoot) == "" {
		r.RepoRoot = "."
	}
	repoRoot, err := filepath.Abs(r.RepoRoot)
	if err != nil {
		return res.fail("resolve repo root: %v", err)
	}
	workRoot := r.WorkRoot
	if strings.TrimSpace(workRoot) == "" {
		workRoot = filepath.Join(os.TempDir(), "affent-eval")
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return res.fail("create work root: %v", err)
	}
	workspace, err := os.MkdirTemp(workRoot, scenario.Name+"-*")
	if err != nil {
		return res.fail("create scenario workspace: %v", err)
	}
	res.Workspace = workspace
	if err := writeScenarioFiles(workspace, scenario.Files); err != nil {
		return res.fail("write scenario files: %v", err)
	}
	protected, err := readProtectedFiles(workspace, scenario.ProtectedFiles)
	if err != nil {
		return res.fail("snapshot protected files: %v", err)
	}
	tracePath := filepath.Join(workspace, "trace.jsonl")
	res.TracePath = tracePath
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	stdout, stderr, exitCode, command, err := r.runAffentctl(runCtx, repoRoot, workspace, tracePath, scenario)
	res.AffentctlCommand = command
	res.FinalText = strings.TrimSpace(stdout)
	res.RunExitCode = exitCode
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("affentctl run failed: exit=%d err=%v stderr=%s", exitCode, err, trimOneLine(stderr, 800)))
	}
	if err := verifyProtectedFiles(workspace, protected); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if err := verifyForbiddenFileSubstrings(workspace, scenario.ForbiddenFileSubstrings); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if scenario.VerifyCommand != "" {
		verifierCtx := runCtx
		var verifierCancel context.CancelFunc
		if scenario.VerifierTimeout > 0 {
			verifierCtx, verifierCancel = context.WithTimeout(runCtx, scenario.VerifierTimeout)
		}
		verifier := r.runVerifier(verifierCtx, workspace, repoRoot, scenario.VerifyCommand)
		if verifierCancel != nil {
			verifierCancel()
		}
		res.Verifier = verifier.Result
		if verifier.Err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("verify command failed: %s: %v\n%s", scenario.VerifyCommand, verifier.Err, trimOneLine(verifier.Output, 1200)))
		}
	}
	var parsedTrace *Trace
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("parse trace: %v", err))
	} else {
		parsedTrace = &trace
		trace.WorkspaceDir = workspace
		res.TraceSchemaVersion = trace.SchemaVersion
		res.TraceEventTypes = cloneStringIntMap(trace.RawTypes)
		res.TraceEvents = sumStringIntMap(trace.RawTypes)
		res.TurnEndReason = trace.TurnEndReason
		res.ToolCalls = len(trace.Tools)
		res.ToolStats = trace.ToolStats
		res.ToolStats.ToolFailureByKind = trace.ToolFailureKindCounts()
		res.RuntimeErrorByKind = trace.LoopErrorKindCounts()
		res.RuntimeErrorExamples = trace.RuntimeErrorExamples(2)
		res.LoopDecisionStats = trace.LoopDecisionStats(2)
		res.ContextCompactions = trace.ContextCompactionStats(2)
		res.ToolFailureExamples = trace.ToolFailureExamples(2)
		res.ToolTruncation = SummarizeToolTruncation(trace)
		res.Usage = trace.Usage
		res.Delegation = trace.DelegationStats()
		res.Plan = trace.PlanStats()
		res.Repair = trace.RepairStats()
		res.RuntimeSurface = latestRuntimeSurface(trace.RuntimeSurfaces)
		res.Failures = append(res.Failures, CheckBatchTrace(trace, scenario)...)
	}
	if scenario.ExpectedSkill != "" {
		if err := checkConversationSkill(workspace, scenario.ExpectedSkill); err != nil {
			res.Failures = append(res.Failures, err.Error())
		}
	}
	mergeRuntimeDiagnosticsFromFailures(&res, 2)
	res.Duration = time.Since(start)
	res.OK = len(res.Failures) == 0
	if err := writeScenarioDebugArtifacts(&res, scenario, stdout, stderr, parsedTrace); err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("write debug manifest: %v", err))
		res.OK = false
	}
	r.cleanupPassingWorkspace(&res, workspace)
	return res
}

func writeScenarioDebugArtifacts(res *BatchResult, scenario BatchScenario, stdout, stderr string, trace *Trace) error {
	if res == nil || strings.TrimSpace(res.Workspace) == "" {
		return nil
	}
	if trace != nil && len(res.TraceEventTypes) == 0 {
		res.TraceEventTypes = cloneStringIntMap(trace.RawTypes)
		res.TraceEvents = sumStringIntMap(trace.RawTypes)
	}
	finalTextPath := filepath.Join(res.Workspace, "affenteval-final.txt")
	if err := os.WriteFile(finalTextPath, []byte(res.FinalText), 0o644); err != nil {
		return err
	}
	res.FinalTextPath = finalTextPath
	stdoutPath := filepath.Join(res.Workspace, "affenteval-stdout.txt")
	if err := os.WriteFile(stdoutPath, []byte(stdout), 0o644); err != nil {
		return err
	}
	res.StdoutPath = stdoutPath
	stderrPath := filepath.Join(res.Workspace, "affenteval-stderr.txt")
	if err := os.WriteFile(stderrPath, []byte(stderr), 0o644); err != nil {
		return err
	}
	res.StderrPath = stderrPath
	timelinePath := filepath.Join(res.Workspace, "affenteval-timeline.md")
	if err := os.WriteFile(timelinePath, []byte(renderDebugTimeline(*res, scenario, trace)), 0o644); err != nil {
		return err
	}
	res.TimelinePath = timelinePath

	manifestPath := filepath.Join(res.Workspace, "affenteval-debug.json")
	manifest := DebugManifest{
		SchemaVersion:    1,
		Scenario:         res.BatchScenario,
		OK:               res.OK,
		Workspace:        res.Workspace,
		TracePath:        res.TracePath,
		TimelinePath:     timelinePath,
		FinalTextPath:    finalTextPath,
		StdoutPath:       stdoutPath,
		StderrPath:       stderrPath,
		AffentctlCommand: append([]string(nil), res.AffentctlCommand...),
		RunExitCode:      res.RunExitCode,
		ConversationDir:  filepath.Join(res.Workspace, ".affentctl"),
		ArtifactDir:      filepath.Join(res.Workspace, ".affent", "artifacts"),
		TraceDeltas:      res.TraceDeltas,
		Prompt:           scenario.Prompt,
		Failures:         append([]string(nil), res.Failures...),
		DebugBrief:       BuildDebugBrief(*res),
		RuntimeSurface:   cloneRuntimeSurface(res.RuntimeSurface),
		Metrics: DebugMetrics{
			TurnEndReason:              res.TurnEndReason,
			ToolCalls:                  res.ToolCalls,
			ToolErrors:                 res.ToolStats.ToolErrors,
			ToolArgsRepaired:           res.ToolStats.ToolArgsRepaired,
			ToolNameCanonicalized:      res.ToolStats.ToolNameCanonicalized,
			ToolRepairCalls:            res.Repair.Calls,
			ToolRepairSucceeded:        res.Repair.SucceededCalls,
			ToolRepairFailed:           res.Repair.FailedCalls,
			ToolRepairNotes:            res.Repair.Notes,
			ToolRepairByKind:           cloneStringIntMap(res.Repair.ByKind),
			ToolFailureByKind:          cloneStringIntMap(res.ToolStats.ToolFailureByKind),
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
			SessionSearchCalls:         res.ToolStats.SessionSearchCalls,
			SessionSearchResults:       res.ToolStats.SessionSearchResults,
			SessionSearchContextHits:   res.ToolStats.SessionSearchContextHits,
			SessionSearchMatchedTerms:  res.ToolStats.SessionSearchMatchedTerms,
			ContextCompactions:         res.ContextCompactions.Count,
			ReactiveContextCompactions: res.ContextCompactions.Reactive,
			ContextCompactionRemoved:   res.ContextCompactions.RemovedMessages,
			ContextCompactionSummary:   res.ContextCompactions.SummaryBytes,
			ToolContextTruncated:       res.ToolStats.ToolContextTruncated,
			ToolContextOmittedBytes:    res.ToolStats.ToolContextOmittedBytes,
			InputTokens:                res.Usage.InputTokens,
			OutputTokens:               res.Usage.OutputTokens,
			TraceEvents:                res.TraceEvents,
			TraceEventTypes:            cloneStringIntMap(res.TraceEventTypes),
		},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		return err
	}
	res.DebugManifestPath = manifestPath
	return nil
}

func latestRuntimeSurface(surfaces []sse.RuntimeSurfacePayload) *sse.RuntimeSurfacePayload {
	if len(surfaces) == 0 {
		return nil
	}
	return cloneRuntimeSurface(&surfaces[len(surfaces)-1])
}

func cloneRuntimeSurface(surface *sse.RuntimeSurfacePayload) *sse.RuntimeSurfacePayload {
	if surface == nil {
		return nil
	}
	out := *surface
	out.Tools = append([]sse.RuntimeSurfaceTool(nil), surface.Tools...)
	return &out
}

func mergeRuntimeDiagnosticsFromFailures(res *BatchResult, maxExamplesPerKind int) {
	if res == nil {
		return
	}
	counts, examples := RuntimeErrorDiagnosticsFromFailures(res.Failures, maxExamplesPerKind)
	for kind, count := range counts {
		if count <= 0 {
			continue
		}
		if res.RuntimeErrorByKind == nil {
			res.RuntimeErrorByKind = map[string]int{}
		}
		if res.RuntimeErrorByKind[kind] == 0 {
			res.RuntimeErrorByKind[kind] = count
		}
	}
	for kind, newExamples := range examples {
		if len(newExamples) == 0 {
			continue
		}
		if res.RuntimeErrorExamples == nil {
			res.RuntimeErrorExamples = map[string][]RuntimeErrorExample{}
		}
		if len(res.RuntimeErrorExamples[kind]) == 0 {
			res.RuntimeErrorExamples[kind] = append([]RuntimeErrorExample(nil), newExamples...)
		}
	}
}

func (r BatchRunner) cleanupPassingWorkspace(res *BatchResult, workspace string) {
	if res == nil || !res.OK || !r.CleanupPassingWorkspaces {
		return
	}
	if err := os.RemoveAll(workspace); err != nil {
		res.CleanupError = err.Error()
		return
	}
	res.WorkspaceRemoved = true
}

func (r BatchResult) fail(format string, args ...any) BatchResult {
	r.Failures = append(r.Failures, fmt.Sprintf(format, args...))
	r.OK = false
	return r
}

func (r BatchRunner) runAffentctl(ctx context.Context, repoRoot, workspace, tracePath string, scenario BatchScenario) (string, string, int, []string, error) {
	if strings.TrimSpace(r.BaseURL) == "" {
		r.BaseURL = os.Getenv("AFFENTCTL_BASE_URL")
	}
	if strings.TrimSpace(r.APIKey) == "" {
		r.APIKey = os.Getenv("AFFENTCTL_API_KEY")
	}
	if strings.TrimSpace(r.Model) == "" {
		r.Model = os.Getenv("AFFENTCTL_MODEL")
	}
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.Model) == "" {
		return "", "", 64, nil, errors.New("base URL and model are required (flags or AFFENTCTL_BASE_URL/AFFENTCTL_MODEL)")
	}
	goBin := r.GoBin
	if goBin == "" {
		goBin = findGo(repoRoot)
	}
	args := r.affentctlRunArgs(workspace, tracePath, scenario)
	redactedCommand := redactedCommandArgv(goBin, args)
	cmd := exec.CommandContext(ctx, goBin, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PATH="+evalPath(repoRoot))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := runEvalCommand(ctx, cmd)
	return stdout.String(), stderr.String(), exitCodeFromError(err), redactedCommand, err
}

func (r BatchRunner) affentctlRunArgs(workspace, tracePath string, scenario BatchScenario) []string {
	executor := strings.TrimSpace(r.Executor)
	if executor == "" {
		executor = "local"
	}
	args := []string{
		"run", "./cmd/affentctl", "run",
		"--workspace", workspace,
		"--executor", executor,
		"--base-url", r.BaseURL,
		"--model", r.Model,
		"--max-turns", fmt.Sprint(scenario.MaxTurns),
		"--trace", tracePath,
		"--prompt", scenario.Prompt,
	}
	if !r.TraceDeltas {
		args = append(args, "--trace-skip-deltas")
	}
	if strings.TrimSpace(scenario.SessionID) != "" {
		args = append(args, "--session-id", strings.TrimSpace(scenario.SessionID))
	}
	if scenario.ExecutePlan {
		args = append(args, "--execute-plan")
	}
	if r.APIKey != "" {
		args = append(args, "--api-key", r.APIKey)
	}
	args = appendStringFlag(args, "--temperature", r.Temperature)
	args = appendStringFlag(args, "--top-p", r.TopP)
	args = appendStringFlag(args, "--max-tokens", r.MaxTokens)
	args = appendStringFlag(args, "--seed", r.Seed)
	runtimeEvalMode := r.RuntimeEvalMode || strings.TrimSpace(r.RuntimeTools) != "" || r.RuntimeAllTools
	if runtimeEvalMode {
		args = append(args, "--eval-mode")
	}
	if r.RuntimeAllTools {
		args = append(args, "--eval-all-tools")
	}
	args = appendStringFlag(args, "--eval-tools", r.RuntimeTools)
	if r.RuntimeMemory || scenario.EnableMemory {
		args = append(args, "--memory=true")
	}
	if r.RuntimeWeb {
		args = append(args, "--web=true", "--web-search=true")
	}
	if r.RuntimeBrowser {
		args = append(args, "--browser=true")
	}
	args = appendStringFlag(args, "--mcp-config", r.RuntimeMCPConfig)
	return args
}

func appendStringFlag(args []string, flagName, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return args
	}
	return append(args, flagName, value)
}

func redactedCommandArgv(bin string, args []string) []string {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		bin = "go"
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, bin)
	nextReplacement := ""
	for _, arg := range args {
		if nextReplacement != "" {
			out = append(out, nextReplacement)
			nextReplacement = ""
			continue
		}
		if arg == "--api-key" {
			out = append(out, arg)
			nextReplacement = "<redacted>"
			continue
		}
		if arg == "--prompt" {
			out = append(out, arg)
			nextReplacement = "<prompt>"
			continue
		}
		if strings.HasPrefix(arg, "--api-key=") {
			out = append(out, "--api-key=<redacted>")
			continue
		}
		if strings.HasPrefix(arg, "--prompt=") {
			out = append(out, "--prompt=<prompt>")
			continue
		}
		out = append(out, arg)
	}
	return out
}

type verifierRun struct {
	Result VerifierResult
	Output string
	Err    error
}

func (r BatchRunner) runVerifier(ctx context.Context, workspace, repoRoot, command string) verifierRun {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "PATH="+evalPath(repoRoot))
	out := newVerifierOutputBuffer(r.VerifierOutputCapBytes)
	cmd.Stdout = out
	cmd.Stderr = out
	start := time.Now()
	err := runEvalCommand(ctx, cmd)
	output := out.String()
	stats := out.Stats()
	result := VerifierResult{
		Command:            command,
		Ran:                true,
		OK:                 err == nil,
		ExitCode:           exitCodeFromError(err),
		Duration:           time.Since(start),
		OutputBytes:        stats.Bytes,
		OutputTruncated:    stats.Truncated,
		OutputOmittedBytes: stats.OmittedBytes,
		OutputCapBytes:     stats.CapBytes,
	}
	return verifierRun{Result: result, Output: output, Err: err}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

type verifierOutputStats struct {
	Bytes        int
	Truncated    bool
	OmittedBytes int
	CapBytes     int
}

type verifierOutputBuffer struct {
	mu        sync.Mutex
	buf       []byte
	cap       int
	bytes     int
	omitted   int
	truncated bool
}

func newVerifierOutputBuffer(capBytes int) *verifierOutputBuffer {
	if capBytes <= 0 {
		capBytes = DefaultVerifierOutputCapBytes
	}
	return &verifierOutputBuffer{cap: capBytes}
}

func (b *verifierOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bytes += len(p)
	room := b.cap - len(b.buf)
	switch {
	case room <= 0:
		b.omitted += len(p)
		b.truncated = true
	case len(p) > room:
		b.buf = append(b.buf, p[:room]...)
		b.omitted += len(p) - room
		b.truncated = true
	default:
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *verifierOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + fmt.Sprintf("\n[... %d more bytes truncated from verifier output; %d-byte cap.]", b.omitted, b.cap)
}

func (b *verifierOutputBuffer) Stats() verifierOutputStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	return verifierOutputStats{
		Bytes:        b.bytes,
		Truncated:    b.truncated,
		OmittedBytes: b.omitted,
		CapBytes:     b.cap,
	}
}

func runEvalCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := startEvalCommand(cmd); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killEvalCommandGroup(cmd)
		<-done
		return ctx.Err()
	}
}

func writeScenarioFiles(root string, files map[string]string) error {
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readProtectedFiles(root string, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		out[name] = string(raw)
	}
	return out, nil
}

func verifyProtectedFiles(root string, protected map[string]string) error {
	for name, want := range protected {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("protected file %s missing: %w", name, err)
		}
		if string(raw) != want {
			return fmt.Errorf("protected file changed: %s", name)
		}
	}
	return nil
}

func verifyForbiddenFileSubstrings(root string, forbidden map[string][]string) error {
	for name, substrings := range forbidden {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("forbidden-content file %s missing: %w", name, err)
		}
		body := string(raw)
		for _, substr := range substrings {
			if substr == "" {
				continue
			}
			if strings.Contains(body, substr) {
				return fmt.Errorf("forbidden content %q found in %s", substr, name)
			}
		}
	}
	return nil
}

// ParseTraceFile reads a JSONL trace file emitted by affentctl (or any
// SSE-event-shaped log) and returns the unified Trace the in-memory
// Runner also produces. One trace type, one check library — the
// BatchRunner path used to ship its own BatchTrace/BatchToolRequest
// twins which forced every check to be written twice.
//
// The file format is one JSON object per line with `{"type":"...",
// "data":{...}}`; new traces start with trace.meta carrying the schema
// version. Unknown event types are counted into RawTypes but otherwise
// ignored.
func ParseTraceFile(path string) (Trace, error) {
	f, err := os.Open(path)
	if err != nil {
		return Trace{}, err
	}
	defer f.Close()
	trace := Trace{RawTypes: map[string]int{}}
	pending := map[string]int{}
	r := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(r, maxTraceLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return trace, err
		}
		lineNo++
		if overLimit {
			return trace, fmt.Errorf("trace %s line %d exceeds max JSONL record size %d bytes", path, lineNo, maxTraceLineBytes)
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return trace, fmt.Errorf("trace %s line %d: %w", path, lineNo, err)
		}
		trace.RawTypes[ev.Type]++
		if _, err := applyTraceEvent(&trace, pending, ev.Type, ev.Data, ""); err != nil {
			return trace, fmt.Errorf("trace %s line %d: %w", path, lineNo, err)
		}
	}
	return trace, nil
}

// BatchScenarioChecks returns the Check slice derived from the
// declarative fields of a BatchScenario: RequiredCommands become
// ShellCommandMatching checks, ForbiddenCommands become
// ShellCommandLacksUnguarded checks, ProtectedFiles become
// FileNotEdited checks. Lets one Check library cover both pipelines.
func BatchScenarioChecks(scenario BatchScenario) []Check {
	checks := []Check{TurnEndedCleanly()}
	for _, tool := range scenario.RequiredTools {
		checks = append(checks, ToolCalled(tool, nil))
	}
	for _, tool := range scenario.ForbiddenTools {
		checks = append(checks, ToolNotCalled(tool, nil))
	}
	for _, substr := range scenario.RequiredFinalText {
		checks = append(checks, FinalTextContains(substr))
	}
	for _, substr := range scenario.ForbiddenFinalText {
		checks = append(checks, FinalTextLacks(substr))
	}
	for _, tool := range sortedStringMapKeys(scenario.RequiredToolResultText) {
		substrings := scenario.RequiredToolResultText[tool]
		for _, substr := range substrings {
			checks = append(checks, ToolResultContains(tool, substr))
		}
	}
	for _, req := range scenario.RequiredToolArgContains {
		min := req.Min
		if min <= 0 {
			min = 1
		}
		checks = append(checks, ToolArgContainsAtLeast(req.Tool, req.Arg, req.Substring, min))
	}
	for _, tool := range scenario.RequiredTruncatedResults {
		checks = append(checks, ToolResultTruncated(tool))
	}
	for _, tool := range scenario.RequiredResultArtifacts {
		checks = append(checks, ToolResultArtifact(tool))
	}
	for _, order := range scenario.RequiredToolOrder {
		checks = append(checks, ToolCalledBefore(order.Earlier, order.Later))
	}
	for _, tool := range sortedStringMapKeys(scenario.RequiredToolCounts) {
		checks = append(checks, ToolCalledAtLeast(tool, scenario.RequiredToolCounts[tool]))
	}
	for _, kind := range sortedStringMapKeys(scenario.RequiredToolFailureKindCounts) {
		checks = append(checks, ToolFailureKindAtLeast(kind, scenario.RequiredToolFailureKindCounts[kind]))
	}
	for _, field := range sortedStringMapKeys(scenario.RequiredToolStatsAtLeast) {
		checks = append(checks, ToolStatsAtLeast(field, scenario.RequiredToolStatsAtLeast[field]))
	}
	for _, kind := range sortedStringMapKeys(scenario.RequiredLoopDecisionKinds) {
		checks = append(checks, LoopDecisionKindAtLeast(kind, scenario.RequiredLoopDecisionKinds[kind]))
	}
	for _, decision := range sortedStringMapKeys(scenario.RequiredLoopDecisionResults) {
		checks = append(checks, LoopDecisionResultAtLeast(decision, scenario.RequiredLoopDecisionResults[decision]))
	}
	for _, req := range scenario.RequiredLoopDecisionMatches {
		min := req.Min
		if min <= 0 {
			min = 1
		}
		checks = append(checks, LoopDecisionMatchAtLeast(req.Kind, req.Decision, req.Trigger, min))
	}
	if scenario.RequiredContextCompactions > 0 {
		checks = append(checks, ContextCompactionsAtLeast(scenario.RequiredContextCompactions))
	}
	if scenario.RequiredReactiveCompactions > 0 {
		checks = append(checks, ReactiveContextCompactionsAtLeast(scenario.RequiredReactiveCompactions))
	}
	if scenario.RequiredCompactionRemovedMsgs > 0 {
		checks = append(checks, ContextCompactionRemovedMessagesAtLeast(scenario.RequiredCompactionRemovedMsgs))
	}
	for _, taskType := range sortedStringMapKeys(scenario.RequiredFocusedTaskCounts) {
		checks = append(checks, FocusedTaskCalledAtLeast(taskType, scenario.RequiredFocusedTaskCounts[taskType]))
	}
	for _, mode := range sortedStringMapKeys(scenario.RequiredSubagentModeCounts) {
		checks = append(checks, SubagentCalledAtLeast(mode, scenario.RequiredSubagentModeCounts[mode]))
	}
	if scenario.RequireNoDelegationErrors {
		checks = append(checks, NoDelegationErrors())
	}
	if scenario.RequireNoPlanErrors {
		checks = append(checks, NoPlanErrors())
	}
	if scenario.MaxParentToolCalls > 0 {
		checks = append(checks, MaxSuccessfulToolCalls(scenario.MaxParentToolCalls))
	}
	for _, tool := range sortedStringMapKeys(scenario.MaxSuccessfulToolCallsByTool) {
		checks = append(checks, MaxSuccessfulToolCallsForTool(tool, scenario.MaxSuccessfulToolCallsByTool[tool]))
	}
	for _, want := range scenario.RequiredCommands {
		checks = append(checks, ShellCommandMatching(want))
	}
	for _, pattern := range sortedStringMapKeys(scenario.RequiredCommandCounts) {
		checks = append(checks, ShellCommandMatchingAtLeast(pattern, scenario.RequiredCommandCounts[pattern]))
	}
	for _, order := range scenario.RequiredCommandBeforeTool {
		checks = append(checks, ShellCommandMatchingBeforeTool(order.Command, order.Tool))
	}
	for _, order := range scenario.RequiredCommandAfterTool {
		checks = append(checks, ShellCommandMatchingAfterTool(order.Command, order.Tool))
	}
	for _, forbidden := range scenario.ForbiddenCommands {
		checks = append(checks, ShellCommandLacksUnguarded(forbidden))
	}
	if len(scenario.ProtectedFiles) > 0 {
		checks = append(checks, FileNotEdited(scenario.ProtectedFiles))
	}
	return checks
}

func sortedStringMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func SummarizeToolTruncation(trace Trace) ToolTruncationStats {
	var stats ToolTruncationStats
	for _, tool := range trace.Tools {
		if tool.ArgsTruncated {
			stats.ArgsTruncated++
		}
		stats.ArgsOmittedBytes += tool.ArgsOmittedBytes
		if tool.ResultTruncated {
			stats.ResultsTruncated++
		}
		stats.ResultsOmittedBytes += tool.ResultOmittedBytes
		if tool.ResultArtifactPath != "" {
			stats.ResultArtifacts++
		}
	}
	return stats
}

// CheckBatchTrace runs BatchScenarioChecks against the trace and
// returns failure detail strings — the legacy signature BatchRunner.Run
// expects. New code should compose Check slices directly and read
// Outcome.FailedChecks() / Outcome.Results.
func CheckBatchTrace(trace Trace, scenario BatchScenario) []string {
	results := evaluateChecks(trace, BatchScenarioChecks(scenario))
	var failures []string
	for _, r := range results {
		if !r.Pass {
			failures = append(failures, r.Detail)
		}
	}
	return failures
}

func checkConversationSkill(workspace, skill string) error {
	root := filepath.Join(workspace, ".affentctl")
	found := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), skill) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("search conversation skill: %w", err)
	}
	if !found {
		return fmt.Errorf("expected active skill %q not found in conversation log", skill)
	}
	return nil
}

func evalPath(repoRoot string) string {
	parts := []string{
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin"),
		filepath.Join(os.Getenv("HOME"), ".local", "go-toolchain", "go", "bin"),
		filepath.Join(os.Getenv("HOME"), ".local", "bin"),
		filepath.Join(os.Getenv("HOME"), "go", "bin"),
		"/usr/local/go/bin",
		"/snap/bin",
	}
	if path := os.Getenv("PATH"); path != "" {
		parts = append([]string{path}, parts...)
	}
	return strings.Join(dedupeNonEmpty(parts), string(os.PathListSeparator))
}

func findGo(repoRoot string) string {
	for _, candidate := range []string{
		"/usr/local/go/bin/go",
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin", "go"),
		filepath.Join(os.Getenv("HOME"), ".local", "go-toolchain", "go", "bin", "go"),
		"go",
	} {
		if path, err := exec.LookPath(candidate); err == nil {
			if goCommandUsableForRepo(path, repoRoot) {
				return path
			}
			continue
		}
		if filepath.IsAbs(candidate) {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				if goCommandUsableForRepo(candidate, repoRoot) {
					return candidate
				}
			}
		}
	}
	return "go"
}

func goCommandUsableForRepo(goBin, repoRoot string) bool {
	if strings.TrimSpace(goBin) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "list", "-m")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	return cmd.Run() == nil
}

func dedupeNonEmpty(parts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func trimOneLine(s string, n int) string {
	s = textutil.CompactWhitespace(s)
	if len(s) <= n {
		return s
	}
	return textutil.Preview(s, n, "...")
}
