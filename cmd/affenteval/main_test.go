package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agenteval"
	"github.com/affinefoundation/affent/internal/sse"
)

func TestRunListSuites(t *testing.T) {
	out, code := captureStdout(t, func() int {
		return run([]string{"--list-suites"})
	})
	if code != 0 {
		t.Fatalf("run --list-suites exit = %d", code)
	}
	for _, want := range []string{"hard-agent", "live-web", "long-run", "small-model-tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--list-suites output missing %q:\n%s", want, out)
		}
	}
}

func TestRunListQualityProfiles(t *testing.T) {
	out, code := captureStdout(t, func() int {
		return run([]string{"--list-quality-profiles"})
	})
	if code != 0 {
		t.Fatalf("run --list-quality-profiles exit = %d", code)
	}
	for _, want := range []string{
		"longrun",
		"web-evidence",
		"min-pass-rate=0.800",
		"max-avg-total-tokens=120000.000",
		"min-source-access-verified-rate=0.900",
		"max-source-dynamic-partial-rate=0.200",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--list-quality-profiles output missing %q:\n%s", want, out)
		}
	}
}

func TestRunListSuiteScenarios(t *testing.T) {
	out, code := captureStdout(t, func() int {
		return run([]string{"--list", "--suite", "small-model-tools"})
	})
	if code != 0 {
		t.Fatalf("run --list --suite exit = %d", code)
	}
	if !strings.Contains(out, "small-tools-wrong-field-read") {
		t.Fatalf("--list --suite output missing expected scenario:\n%s", out)
	}
	if strings.Contains(out, "coding-go-median") {
		t.Fatalf("--list --suite leaked non-suite scenario:\n%s", out)
	}
}

func TestRunHelpDoesNotLeakEnvSecrets(t *testing.T) {
	t.Setenv("AFFENTCTL_BASE_URL", "https://sentinel-base.example")
	t.Setenv("AFFENTCTL_API_KEY", "sk-sentinel-secret")
	t.Setenv("AFFENTCTL_MODEL", "sentinel-model")
	t.Setenv("AFFENTEVAL_PROVIDER_LABEL", "sentinel-provider")

	help, code := captureStderr(t, func() int {
		return run([]string{"--help"})
	})
	if code != 0 {
		t.Fatalf("run --help exit = %d", code)
	}
	for _, secret := range []string{"https://sentinel-base.example", "sk-sentinel-secret", "sentinel-model", "sentinel-provider"} {
		if strings.Contains(help, secret) {
			t.Fatalf("--help leaked env value %q:\n%s", secret, help)
		}
	}
	for _, want := range []string{"AFFENTCTL_BASE_URL", "AFFENTCTL_API_KEY", "AFFENTCTL_MODEL", "AFFENTEVAL_PROVIDER_LABEL"} {
		if !strings.Contains(help, want) {
			t.Fatalf("--help missing env hint %q:\n%s", want, help)
		}
	}
	for _, want := range []string{"-runtime-web", "-runtime-browser", "-trace-deltas"} {
		if !strings.Contains(help, want) {
			t.Fatalf("--help missing runtime eval flag %q:\n%s", want, help)
		}
	}
	if !strings.Contains(help, "-quality-profile") || !strings.Contains(help, "-list-quality-profiles") || !strings.Contains(help, "web-evidence") {
		t.Fatalf("--help missing quality profile flag:\n%s", help)
	}
}

func TestRunRejectsInvalidConfigBeforeScenarios(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "zero timeout",
			args: []string{"--timeout=0"},
			want: "--timeout must be a positive duration",
		},
		{
			name: "negative timeout",
			args: []string{"--timeout=-1s"},
			want: "--timeout must be a positive duration",
		},
		{
			name: "temperature NaN",
			args: []string{"--temperature=NaN"},
			want: "--temperature must be between 0 and 2",
		},
		{
			name: "temperature too high",
			args: []string{"--temperature=2.1"},
			want: "--temperature must be between 0 and 2",
		},
		{
			name: "temperature not number",
			args: []string{"--temperature=warm"},
			want: "--temperature",
		},
		{
			name: "top-p too high",
			args: []string{"--top-p=1.1"},
			want: "--top-p must be between 0 and 1",
		},
		{
			name: "top-p not number",
			args: []string{"--top-p=wide"},
			want: "--top-p",
		},
		{
			name: "max-tokens zero",
			args: []string{"--max-tokens=0"},
			want: "--max-tokens must be a positive integer",
		},
		{
			name: "max-tokens not number",
			args: []string{"--max-tokens=many"},
			want: "--max-tokens",
		},
		{
			name: "seed not number",
			args: []string{"--seed=random"},
			want: "--seed",
		},
		{
			name: "unknown executor",
			args: []string{"--executor=remote"},
			want: "unknown --executor",
		},
		{
			name: "zero verifier output cap",
			args: []string{"--verifier-output-cap=0"},
			want: "--verifier-output-cap must be positive",
		},
		{
			name: "min pass rate too high",
			args: []string{"--min-pass-rate=1.1"},
			want: "--min-pass-rate must be between 0 and 1",
		},
		{
			name: "max plan error rate too high",
			args: []string{"--max-plan-error-rate=1.1"},
			want: "--max-plan-error-rate must be between 0 and 1",
		},
		{
			name: "max focused task error rate too high",
			args: []string{"--max-focused-task-error-rate=1.1"},
			want: "--max-focused-task-error-rate must be between 0 and 1",
		},
		{
			name: "max subagent error rate too high",
			args: []string{"--max-subagent-error-rate=1.1"},
			want: "--max-subagent-error-rate must be between 0 and 1",
		},
		{
			name: "negative max avg tokens",
			args: []string{"--max-avg-total-tokens=-2"},
			want: "--max-avg-total-tokens must be disabled with -1 or set to a non-negative value",
		},
		{
			name: "negative max avg reactive context compactions",
			args: []string{"--max-avg-reactive-context-compactions=-2"},
			want: "--max-avg-reactive-context-compactions must be disabled with -1 or set to a non-negative value",
		},
		{
			name: "unknown quality profile",
			args: []string{"--quality-profile=experimental"},
			want: "--quality-profile must be one of",
		},
		{
			name: "empty docker executor",
			args: []string{"--executor=docker:"},
			want: "requires a container name",
		},
		{
			name: "docker executor requires work root",
			args: []string{"--executor=docker:affent-eval"},
			want: "requires explicit --work-root",
		},
		{
			name: "docker executor requires absolute work root",
			args: []string{"--executor=docker:affent-eval", "--work-root=relative-eval"},
			want: "--work-root must be an absolute path",
		},
		{
			name: "sandbox suite rejected",
			args: []string{"--executor=sandbox", "--suite=small-model-tools"},
			want: "--executor sandbox is only supported for one selected scenario",
		},
		{
			name: "runtime eval mode requires explicit scenario tools",
			args: []string{"--scenario=coding-python-slug"},
			want: "coding-python-slug missing edit_file, shell",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, code := captureStderr(t, func() int {
				return run(tc.args)
			})
			if code != 64 {
				t.Fatalf("exit = %d, want 64; stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr missing %q:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestValidateRuntimeToolSurface(t *testing.T) {
	cases := []struct {
		name     string
		runner   BatchRuntimeToolConfig
		scenario agenteval.BatchScenario
		wantErr  string
	}{
		{
			name:   "workspace satisfies shell and edit",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "workspace"},
			scenario: agenteval.BatchScenario{
				Name:             "coding",
				RequiredCommands: []string{"go test"},
				RequiredTools:    []string{"edit_file"},
			},
		},
		{
			name:   "readonly workspace does not satisfy shell or edit",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "readonly_workspace"},
			scenario: agenteval.BatchScenario{
				Name:             "coding",
				RequiredCommands: []string{"go test"},
				RequiredTools:    []string{"edit_file"},
			},
			wantErr: "coding missing edit_file, shell",
		},
		{
			name:   "runtime web satisfies web search",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeWeb: true},
			scenario: agenteval.BatchScenario{
				Name:          "live-web",
				RequiredTools: []string{"web_fetch", "web_search"},
			},
		},
		{
			name:   "scenario memory flag satisfies memory",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true},
			scenario: agenteval.BatchScenario{
				Name:          "memory",
				EnableMemory:  true,
				RequiredTools: []string{"memory"},
			},
		},
		{
			name:   "recall group satisfies memory and session search",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "recall"},
			scenario: agenteval.BatchScenario{
				Name:          "recall",
				RequiredTools: []string{"memory", "session_search"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRuntimeToolSurface(
				[]agenteval.BatchScenario{tc.scenario},
				tc.runner.RuntimeEvalMode,
				tc.runner.RuntimeTools,
				tc.runner.RuntimeAllTools,
				tc.runner.RuntimeMemory,
				tc.runner.RuntimeWeb,
				tc.runner.RuntimeBrowser,
				tc.runner.RuntimeMCPConfig,
			)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRuntimeToolSurface err=%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateRuntimeToolSurface err=%v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestQualityGateFailures(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	summary := batchSummary{
		Total:                      2,
		Passed:                     1,
		EndCompleted:               1,
		ToolCalls:                  5,
		ToolErrors:                 1,
		LoopGuardInterventions:     2,
		ForcedNoTools:              1,
		FocusedTaskCalls:           4,
		FocusedTaskErrors:          2,
		SubagentCalls:              2,
		SubagentErrors:             1,
		PlanCalls:                  4,
		PlanErrors:                 2,
		ToolRepairCalls:            4,
		ToolRepairSucceeded:        3,
		VerifierRuns:               2,
		VerifierPassed:             1,
		RuntimeErrors:              3,
		RuntimeSurfaceScenarios:    1,
		MemoryUpdates:              1,
		SourceAccessResults:        4,
		SourceAccessVerified:       3,
		SourceAccessDiscoveryOnly:  1,
		SourceAccessNetwork:        1,
		SourceAccessDynamicPartial: 1,
		SessionSearchResults:       2,
		SessionSearchContextHits:   1,
		ToolContextTruncated:       4,
		ToolResultsTruncated:       3,
		InputTokens:                90,
		OutputTokens:               20,
		ContextCompactions:         1,
		ContextCompactionsReactive: 1,
		ContextCompactionRemoved:   32,
	}
	failures := qualityGateFailures(summary, qualityGateConfig{
		MinPassRate:                    ptr(0.75),
		MinCompletionRate:              ptr(0.75),
		MinMemoryUpdateRate:            ptr(0.75),
		MinRuntimeSurfaceRate:          ptr(0.75),
		MinSourceNetworkRate:           ptr(0.5),
		MinSourceAccessVerifiedRate:    ptr(0.9),
		MinSessionSearchContextHitRate: ptr(0.75),
		MinToolRepairSuccessRate:       ptr(0.9),
		MinVerifierPassRate:            ptr(0.75),
		MaxFocusedTaskErrorRate:        ptr(0.25),
		MaxForcedNoToolsRate:           ptr(0.1),
		MaxLoopGuardInterventionRate:   ptr(0.3),
		MaxPlanErrorRate:               ptr(0.25),
		MaxSourceDiscoveryOnlyRate:     ptr(0.1),
		MaxSourceDynamicPartialRate:    ptr(0.1),
		MaxSubagentErrorRate:           ptr(0.25),
		MaxToolErrorRate:               ptr(0.1),
		MaxToolContextTruncationRate:   ptr(0.5),
		MaxToolResultTruncationRate:    ptr(0.4),
		MaxAvgRuntimeErrors:            ptr(1.0),
		MaxAvgContextCompactions:       ptr(0.25),
		MaxAvgReactiveCompactions:      ptr(0.25),
		MaxAvgTotalTokens:              ptr(40),
	})
	got := strings.Join(failures, "\n")
	for _, want := range []string{
		"avg_context_compactions 0.500 > max 0.250",
		"avg_reactive_context_compactions 0.500 > max 0.250",
		"avg_runtime_errors 1.500 > max 1.000",
		"avg_total_tokens 55.000 > max 40.000",
		"completion_rate 0.500 < min 0.750",
		"focused_task_error_rate 0.500 > max 0.250",
		"forced_no_tools_rate 0.200 > max 0.100",
		"loop_guard_intervention_rate 0.400 > max 0.300",
		"memory_update_rate 0.500 < min 0.750",
		"pass_rate 0.500 < min 0.750",
		"plan_error_rate 0.500 > max 0.250",
		"runtime_surface_rate 0.500 < min 0.750",
		"session_search_context_hit_rate 0.500 < min 0.750",
		"source_discovery_only_rate 0.250 > max 0.100",
		"source_dynamic_partial_rate 0.250 > max 0.100",
		"source_network_rate 0.250 < min 0.500",
		"source_access_verified_rate 0.750 < min 0.900",
		"subagent_error_rate 0.500 > max 0.250",
		"tool_context_truncation_rate 0.800 > max 0.500",
		"tool_error_rate 0.200 > max 0.100",
		"tool_repair_success_rate 0.750 < min 0.900",
		"tool_result_truncation_rate 0.600 > max 0.400",
		"verifier_pass_rate 0.500 < min 0.750",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("quality gate failures missing %q:\n%s", want, got)
		}
	}
	if failures := qualityGateFailures(summary, qualityGateConfig{}); len(failures) != 0 {
		t.Fatalf("disabled gates should pass, got %#v", failures)
	}
	unavailable := qualityGateFailures(batchSummary{Total: 1, Passed: 1, EndCompleted: 1}, qualityGateConfig{
		MinSourceAccessVerifiedRate: ptr(0.8),
	})
	if len(unavailable) != 1 || !strings.Contains(unavailable[0], "source_access_verified_rate unavailable") {
		t.Fatalf("unavailable source gate failures = %#v", unavailable)
	}
}

func TestApplyQualityGateProfile(t *testing.T) {
	gates := qualityGateConfig{
		MinPassRate:       float64Ptr(-1),
		MaxToolErrorRate:  float64Ptr(0.33),
		MaxAvgTotalTokens: float64Ptr(-1),
	}
	err := applyQualityGateProfile(&gates, "longrun", func(name string) bool {
		return name == "max-tool-error-rate"
	})
	if err != nil {
		t.Fatalf("applyQualityGateProfile: %v", err)
	}
	if gates.MinPassRate == nil || *gates.MinPassRate != 0.80 {
		t.Fatalf("longrun min pass rate = %#v, want 0.80", gates.MinPassRate)
	}
	if gates.MaxToolErrorRate == nil || *gates.MaxToolErrorRate != 0.33 {
		t.Fatalf("explicit max tool error rate should win, got %#v", gates.MaxToolErrorRate)
	}
	if gates.MaxAvgTotalTokens == nil || *gates.MaxAvgTotalTokens != 120000 {
		t.Fatalf("longrun max avg tokens = %#v, want 120000", gates.MaxAvgTotalTokens)
	}
	if gates.MinSourceAccessVerifiedRate != nil && *gates.MinSourceAccessVerifiedRate >= 0 {
		t.Fatalf("longrun profile should not require source evidence for non-web suites: %#v", gates.MinSourceAccessVerifiedRate)
	}

	webGates := qualityGateConfig{MinSourceAccessVerifiedRate: float64Ptr(-1)}
	if err := applyQualityGateProfile(&webGates, "web-evidence", nil); err != nil {
		t.Fatalf("apply web-evidence profile: %v", err)
	}
	if webGates.MinSourceAccessVerifiedRate == nil || *webGates.MinSourceAccessVerifiedRate != 0.90 ||
		webGates.MinSourceNetworkRate == nil || *webGates.MinSourceNetworkRate != 0.50 ||
		webGates.MaxSourceDynamicPartialRate == nil || *webGates.MaxSourceDynamicPartialRate != 0.20 {
		t.Fatalf("web-evidence gates not applied: %+v", webGates)
	}
	if err := applyQualityGateProfile(&qualityGateConfig{}, "unknown", nil); err == nil || !strings.Contains(err.Error(), "--quality-profile") {
		t.Fatalf("unknown profile err = %v", err)
	}
}

type BatchRuntimeToolConfig struct {
	RuntimeEvalMode  bool
	RuntimeTools     string
	RuntimeAllTools  bool
	RuntimeMemory    bool
	RuntimeWeb       bool
	RuntimeBrowser   bool
	RuntimeMCPConfig string
}

func TestSelectedEvalScenariosBuildsAdHocPromptScenario(t *testing.T) {
	promptFile := writeTempFile(t, "Investigate a complex task.\n")
	scenarios, err := selectedEvalScenarios("", "", "", promptFile, "market-debug", "sess-1", 12, "test -f trace.jsonl")
	if err != nil {
		t.Fatalf("selectedEvalScenarios: %v", err)
	}
	if len(scenarios) != 1 {
		t.Fatalf("scenario count = %d, want 1", len(scenarios))
	}
	got := scenarios[0]
	if got.Name != "market-debug" ||
		got.Prompt != "Investigate a complex task.\n" ||
		got.SessionID != "sess-1" ||
		got.MaxTurns != 12 ||
		got.VerifyCommand != "test -f trace.jsonl" {
		t.Fatalf("ad-hoc scenario = %+v", got)
	}
}

func TestSelectedEvalScenariosRejectsMixedAdHocAndBuiltins(t *testing.T) {
	_, err := selectedEvalScenarios("long-run", "", "debug this", "", "adhoc", "", 4, "")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("mixed ad-hoc error = %v", err)
	}
}

func TestPrintBatchResultIncludesTraceMetrics(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario:    "sample",
		Workspace:        "/tmp/ws",
		TracePath:        "/tmp/ws/trace.jsonl",
		AffentctlCommand: []string{"go", "run", "./cmd/affentctl", "run", "--trace", "/tmp/ws/trace.jsonl"},
		OK:               true,
		Duration:         1234 * time.Millisecond,
		TurnEndReason:    "completed",
		ToolCalls:        3,
		WorkspaceRemoved: true,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:        1,
			ToolNameCanonicalized:   1,
			ToolErrors:              2,
			ToolFailureByKind:       map[string]int{"invalid_args": 1},
			ToolDurationMS:          45,
			LoopGuardInterventions:  2,
			ForcedNoTools:           1,
			ToolContextTruncated:    3,
			ToolContextOmittedBytes: 9216,
		},
		ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
			"invalid_args": {
				{Kind: "invalid_args", Tool: "web_fetch", ArgsSummary: `url="https://example.com"`, ResultSummary: "url is required | Next: retry with a full URL", ExitCode: 1},
			},
		},
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_timeout": {
				{Kind: "llm_timeout", Message: "LLM llm_stream timed out after 4m0s while waiting for chat completion (max-call-timeout/per-call-timeout=4m0s): context deadline exceeded"},
			},
		},
		LoopDecisionStats: agenteval.LoopDecisionStats{
			Count:      1,
			ByKind:     map[string]int{"evidence_quality": 1},
			ByDecision: map[string]int{"defer": 1},
			Examples: []agenteval.LoopDecision{
				{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial", Confidence: "high", Reason: "dynamic widgets lacked text", RequiredAction: "read browser network responses"},
			},
		},
		ContextCompactions: agenteval.ContextCompactionStats{
			Count:           2,
			Reactive:        1,
			Proactive:       1,
			RemovedMessages: 64,
			SummaryBytes:    4096,
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:       1,
			ArgsOmittedBytes:    512,
			ResultsTruncated:    1,
			ResultsOmittedBytes: 4096,
			ResultArtifacts:     1,
		},
		Verifier: agenteval.VerifierResult{
			Command:            "go test ./...",
			Ran:                true,
			OK:                 true,
			ExitCode:           0,
			Duration:           80 * time.Millisecond,
			OutputBytes:        1200,
			OutputTruncated:    true,
			OutputOmittedBytes: 176,
			OutputCapBytes:     1024,
		},
		Delegation: agenteval.DelegationStats{
			FocusedTaskCalls:  2,
			FocusedTaskByType: map[string]int{"explore": 1, "verify": 1},
			FocusedTaskErrors: 1,
			SubagentCalls:     1,
			SubagentByMode:    map[string]int{"review": 1},
			SubagentErrors:    1,
		},
		Plan: agenteval.PlanStats{
			Calls:    3,
			ByAction: map[string]int{"set": 1, "update": 2},
			Errors:   1,
		},
		Usage: agenteval.Usage{InputTokens: 100, OutputTokens: 25},
	})
	got := out.String()
	for _, want := range []string{
		"PASS sample (1.234s)",
		"workspace: /tmp/ws (removed)",
		"trace: /tmp/ws/trace.jsonl",
		"command: go run ./cmd/affentctl run --trace /tmp/ws/trace.jsonl",
		"metrics: tools=3 errors=2 repaired=1 canonicalized=1 loop_guard=2 forced_no_tools=1 tool_ms=45 tokens=100/25 trunc=args:1,results:1,artifacts:1 omitted=512/4096 ctx_trunc=3,omitted=9216 tool_failure_kinds=invalid_args:1 runtime_error_kinds=llm_timeout:1 loop_decisions=1 loop_decision_kinds=evidence_quality:1 loop_decision_results=defer:1 compactions=2,reactive=1,removed=64,summary_bytes=4096 debug_brief=context_compaction,context_compaction:reactive,delegation,delegation:focused_task,delegation:subagent,delegation_error,delegation_error:focused_task,delegation_error:subagent,loop_guard,plan,plan:set,plan:update,plan_error,runtime_error,runtime_error:llm_timeout,tool_failure,tool_failure:invalid_args,truncation delegation=focused_tasks:2,subagents:1 delegation_errors=focused_tasks:1,subagents:1 focused_task_by_type=explore:1,verify:1 subagent_by_mode=review:1 plan=calls:3,errors:1 plan_by_action=set:1,update:2 end=completed",
		`verifier: pass exit=0 duration=80ms output=1200 truncated omitted=176 cap=1024 command="go test ./..."`,
		"tool_failure_hint[invalid_args]",
		"invalid arguments",
		`tool_failure_example[invalid_args]: tool=web_fetch args=url="https://example.com" exit=1 result=url is required | Next: retry with a full URL`,
		"hint[llm_timeout]",
		"runtime_error_example[llm_timeout]: LLM llm_stream timed out after 4m0s",
		"loop_decision_example[evidence_quality]: decision=defer trigger=source_access_dynamic_partial confidence=high reason=dynamic widgets lacked text action=read browser network responses",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPrintBatchResultIncludesRepairOutcomesWithoutKinds(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario: "repair-outcome-only",
		Workspace:     "/tmp/ws",
		TracePath:     "/tmp/ws/trace.jsonl",
		Duration:      10 * time.Millisecond,
		Repair: agenteval.ToolRepairStats{
			Calls:          2,
			SucceededCalls: 1,
			FailedCalls:    1,
		},
	})
	got := out.String()
	if !strings.Contains(got, "repair_calls=2,ok=1,failed=1") {
		t.Fatalf("output missing repair outcome-only stats:\n%s", got)
	}
	if strings.Contains(got, "repair_kinds=") {
		t.Fatalf("output should omit empty repair kinds:\n%s", got)
	}
}

func TestPrintBatchResultIncludesDebugPathsForRetainedWorkspace(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario:     "debuggable",
		Workspace:         "/tmp/ws",
		TracePath:         "/tmp/ws/trace.jsonl",
		DebugManifestPath: "/tmp/ws/affenteval-debug.json",
		TimelinePath:      "/tmp/ws/affenteval-timeline.md",
		FinalTextPath:     "/tmp/ws/affenteval-final.txt",
		StdoutPath:        "/tmp/ws/affenteval-stdout.txt",
		StderrPath:        "/tmp/ws/affenteval-stderr.txt",
		RunExitCode:       3,
		Duration:          10 * time.Millisecond,
	})
	got := out.String()
	for _, want := range []string{
		"debug: /tmp/ws/affenteval-debug.json",
		"timeline: /tmp/ws/affenteval-timeline.md",
		"final: /tmp/ws/affenteval-final.txt",
		"stdout: /tmp/ws/affenteval-stdout.txt",
		"stderr: /tmp/ws/affenteval-stderr.txt",
		"run_exit: 3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPrintBatchResultOmitsDebugPathsForRemovedWorkspace(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario:     "cleaned",
		Workspace:         "/tmp/ws",
		TracePath:         "/tmp/ws/trace.jsonl",
		DebugManifestPath: "/tmp/ws/affenteval-debug.json",
		TimelinePath:      "/tmp/ws/affenteval-timeline.md",
		FinalTextPath:     "/tmp/ws/affenteval-final.txt",
		StdoutPath:        "/tmp/ws/affenteval-stdout.txt",
		StderrPath:        "/tmp/ws/affenteval-stderr.txt",
		WorkspaceRemoved:  true,
		Duration:          10 * time.Millisecond,
	})
	got := out.String()
	if strings.Contains(got, "affenteval-debug.json") ||
		strings.Contains(got, "affenteval-timeline.md") ||
		strings.Contains(got, "affenteval-final.txt") ||
		strings.Contains(got, "affenteval-stdout.txt") ||
		strings.Contains(got, "affenteval-stderr.txt") {
		t.Fatalf("removed workspace should not advertise stale debug paths:\n%s", got)
	}
}

func TestBatchSummaryAggregatesRuntimeMetrics(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		OK:                 true,
		Duration:           100 * time.Millisecond,
		ToolCalls:          2,
		WorkspaceRemoved:   true,
		TraceSchemaVersion: 1,
		TraceEvents:        7,
		TraceEventTypes: map[string]int{
			"message.delta": 3,
			"tool.request":  2,
			"tool.result":   2,
		},
		TurnEndReason: "completed",
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:          1,
			ToolNameCanonicalized:     1,
			ToolErrors:                0,
			ToolDurationMS:            10,
			LoopGuardInterventions:    1,
			SourceAccessResults:       2,
			SourceAccessVerified:      2,
			SourceAccessNetwork:       2,
			SessionSearchCalls:        1,
			SessionSearchResults:      2,
			SessionSearchContextHits:  1,
			SessionSearchMatchedTerms: 2,
			ToolContextTruncated:      1,
			ToolContextOmittedBytes:   1024,
		},
		Repair: agenteval.ToolRepairStats{
			Calls:          2,
			SucceededCalls: 2,
			Notes:          2,
			ByKind:         map[string]int{"tool_name": 1, "alias_rename": 1},
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:    1,
			ArgsOmittedBytes: 128,
		},
		ToolTruncationExamples: []agenteval.ToolTruncationExample{{
			ToolIndex:              2,
			CallID:                 "trunc-1",
			Tool:                   "web_fetch",
			ArgsTruncated:          true,
			ArgsOmittedBytes:       128,
			ContextOmittedBytes:    1024,
			ContextEstimatedTokens: 256,
		}},
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			ToolIndex: 1,
			CallID:    "source-1",
			Tool:      "browser_network_read",
			Status:    "network",
			URL:       "https://metrics.example/api.json",
			JSONPath:  "$.price",
		}},
		Plan: agenteval.PlanStats{
			Calls:    1,
			ByAction: map[string]int{"set": 1},
		},
		RuntimeSurface: &sse.RuntimeSurfacePayload{
			ToolCount: 2,
			Tools: []sse.RuntimeSurfaceTool{
				{Name: "web_fetch"},
				{Name: "browser_find"},
			},
			Capabilities: sse.RuntimeCapabilities{WorkspaceTools: []string{"read_file"}, WebFetch: true, Browser: true},
		},
		Verifier: agenteval.VerifierResult{Ran: true, OK: true, ExitCode: 0, OutputBytes: 64, OutputCapBytes: 1024},
		Usage:    agenteval.Usage{InputTokens: 20, OutputTokens: 5},
	})
	summary.add(agenteval.BatchResult{
		OK:                 false,
		Duration:           250 * time.Millisecond,
		ToolCalls:          3,
		TraceSchemaVersion: 1,
		TurnEndReason:      "max_turns",
		Failures: []string{
			`turn ended with reason "max_turns" (expected completed)`,
			`missing required command match "go test"; commands=[]`,
		},
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:        2,
			ToolNameCanonicalized:   1,
			ToolErrors:              1,
			ToolFailureByKind:       map[string]int{"invalid_args": 1, "timeout": 2},
			ToolDurationMS:          40,
			LoopGuardInterventions:  2,
			ForcedNoTools:           1,
			SourceAccessResults:     2,
			SourceAccessVerified:    1,
			SourceAccessNetwork:     1,
			ToolContextTruncated:    2,
			ToolContextOmittedBytes: 4096,
		},
		ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
			"timeout": {
				{Kind: "timeout", Tool: "web_fetch", ArgsSummary: `url="https://slow.example"`, ResultSummary: "timed out | Next: switch source", ExitCode: 1},
			},
		},
		RuntimeErrorByKind: map[string]int{"llm_timeout": 2, "context_overflow": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_timeout": {
				{Kind: "llm_timeout", Message: "LLM llm_stream timed out after 4m0s (endpoint=https://llm.example/v1/chat/completions)"},
			},
		},
		LoopDecisionStats: agenteval.LoopDecisionStats{
			Count:      1,
			ByKind:     map[string]int{"evidence_quality": 1},
			ByDecision: map[string]int{"defer": 1},
			Examples: []agenteval.LoopDecision{
				{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial", RequiredAction: "read browser network responses"},
			},
		},
		ContextCompactions: agenteval.ContextCompactionStats{
			Count:           1,
			Reactive:        1,
			RemovedMessages: 32,
			SummaryBytes:    2048,
			Examples: []agenteval.ContextCompaction{{
				TurnID:          "turn-summary",
				BeforeMessages:  70,
				AfterMessages:   22,
				RemovedMessages: 48,
				Reactive:        true,
				Reason:          "context_overflow",
				SummaryPresent:  true,
				SummaryBytes:    2048,
				SummaryPreview:  "USER_CONTEXT: preserve the market evidence trail.",
			}},
		},
		Repair: agenteval.ToolRepairStats{
			Calls:          3,
			SucceededCalls: 2,
			FailedCalls:    1,
			Notes:          3,
			ByKind:         map[string]int{"alias_rename": 1, "type_coercion": 2},
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ResultsTruncated:    2,
			ResultsOmittedBytes: 2048,
			ResultArtifacts:     1,
		},
		Plan: agenteval.PlanStats{
			Calls:    2,
			ByAction: map[string]int{"update": 2},
			Errors:   1,
		},
		RuntimeSurface: &sse.RuntimeSurfacePayload{
			ToolCount: 3,
			Tools: []sse.RuntimeSurfaceTool{
				{Name: "web_fetch"},
				{Name: "web_search"},
				{Name: "browser_find"},
			},
			Capabilities: sse.RuntimeCapabilities{WebFetch: true, WebSearch: true, Browser: true},
		},
		Verifier: agenteval.VerifierResult{
			Ran:                true,
			OK:                 false,
			ExitCode:           1,
			OutputBytes:        4096,
			OutputTruncated:    true,
			OutputOmittedBytes: 2048,
			OutputCapBytes:     2048,
		},
		Usage: agenteval.Usage{InputTokens: 70, OutputTokens: 15},
	})

	var out bytes.Buffer
	printBatchSummary(&out, summary)
	want := "SUMMARY scenarios=2 passed=1 failed=1 duration=350ms tools=5 errors=1 repaired=3 canonicalized=2 loop_guard=3 forced_no_tools=1 tool_ms=50 trunc=args:1,results:2,artifacts:1 omitted=128/2048 verifier=run:2,passed:1,failed:1,truncated:1,omitted:2048 tokens=90/20 ends=completed:1,max_turns:1,error:0,cancelled:0,unknown:0 failure_kinds=missing_command:1,turn_end:1 removed_workspaces=1 cleanup_errors=0"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("summary output missing %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "ctx_trunc=3,omitted=5120") {
		t.Fatalf("summary output missing context truncation rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "rates=pass:50.0%,completed:50.0%,memory_update:0.0%,runtime_surface:100.0%,tool_error:20.0%,focused_task_error:n/a,subagent_error:n/a,plan_error:33.3%,repair_success:80.0%,verifier_pass:50.0%,evidence_verified:75.0%,source_network:75.0%,source_discovery:0.0%,source_dynamic_partial:0.0% avg_tokens=45.0/10.0") {
		t.Fatalf("summary output missing normalized rates:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "context_pressure=avg_compactions:0.50,avg_reactive:0.50,avg_removed:16.0,tool_ctx_trunc:60.0%") {
		t.Fatalf("summary output missing context pressure rates:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "source_access=results:4,verified:3,discovery:0,network:3,dynamic_partial:0") {
		t.Fatalf("summary output missing source access rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "debug_brief=context_compaction:1,context_compaction:reactive:1,loop_guard:2,outcome:failed:1,plan:2,plan:set:1,plan:update:1,plan_error:1,recall:1,runtime_error:1,runtime_error:context_overflow:1,runtime_error:llm_timeout:1,source_access:2,source_network:2,source_unverified:1,tool_failure:1,tool_failure:invalid_args:1,tool_failure:timeout:1,truncation:2,turn_end:max_turns:1") {
		t.Fatalf("summary output missing debug brief tag rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "repair_kinds=alias_rename:2,tool_name:1,type_coercion:2") {
		t.Fatalf("summary output missing repair kind rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "tool_failure_kinds=invalid_args:1,timeout:2") {
		t.Fatalf("summary output missing tool failure kind rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runtime_error_kinds=context_overflow:1,llm_timeout:2") {
		t.Fatalf("summary output missing runtime error kind rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "session_search=calls:1,results:2,context:1,terms:2") {
		t.Fatalf("summary output missing session search rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runtime_surface=scenarios:2 runtime_capabilities=browser:2,web_fetch:2,web_search:1,workspace_partial:1 runtime_tools=browser_find:2,web_fetch:2,web_search:1") {
		t.Fatalf("summary output missing runtime surface rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "loop_decisions=1 loop_decision_kinds=evidence_quality:1 loop_decision_results=defer:1") {
		t.Fatalf("summary output missing loop decision rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "compactions=1,reactive=1,removed=32,summary_bytes=2048") {
		t.Fatalf("summary output missing context compaction rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "trace_events=7 trace_event_types=message.delta:3,tool.request:2,tool.result:2") {
		t.Fatalf("summary output missing trace event rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "tool_failure_hint[invalid_args]") || !strings.Contains(out.String(), "tool_failure_hint[timeout]") {
		t.Fatalf("summary output missing tool failure hints:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hint[context_overflow]") || !strings.Contains(out.String(), "hint[llm_timeout]") {
		t.Fatalf("summary output missing runtime error hints:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `tool_failure_example[timeout]: tool=web_fetch args=url="https://slow.example"`) {
		t.Fatalf("summary output missing tool failure example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runtime_error_example[llm_timeout]: LLM llm_stream timed out after 4m0s") {
		t.Fatalf("summary output missing runtime error example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "loop_decision_example[evidence_quality]: decision=defer trigger=source_access_dynamic_partial") {
		t.Fatalf("summary output missing loop decision example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "repair_calls=5,ok=4,failed=1") {
		t.Fatalf("summary output missing repair outcome rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "plan=calls:3,errors:1 plan_by_action=set:1,update:2") {
		t.Fatalf("summary output missing plan rollup:\n%s", out.String())
	}
	if summary.TraceSchemaVersions[1] != 2 {
		t.Fatalf("TraceSchemaVersions = %#v, want version 1 count 2", summary.TraceSchemaVersions)
	}
	if summary.TraceEvents != 7 || !reflect.DeepEqual(summary.TraceEventTypes, map[string]int{"message.delta": 3, "tool.request": 2, "tool.result": 2}) {
		t.Fatalf("trace events = %d %#v", summary.TraceEvents, summary.TraceEventTypes)
	}
	if summary.ToolRepairNotes != 5 {
		t.Fatalf("ToolRepairNotes = %d, want 5", summary.ToolRepairNotes)
	}
	if summary.ToolRepairCalls != 5 || summary.ToolRepairSucceeded != 4 || summary.ToolRepairFailed != 1 {
		t.Fatalf("repair outcomes = calls:%d ok:%d failed:%d, want 5/4/1", summary.ToolRepairCalls, summary.ToolRepairSucceeded, summary.ToolRepairFailed)
	}
	wantRepairKinds := map[string]int{"tool_name": 1, "alias_rename": 2, "type_coercion": 2}
	if !reflect.DeepEqual(summary.ToolRepairByKind, wantRepairKinds) {
		t.Fatalf("ToolRepairByKind = %#v, want %#v", summary.ToolRepairByKind, wantRepairKinds)
	}
	if !reflect.DeepEqual(summary.ToolFailureByKind, map[string]int{"invalid_args": 1, "timeout": 2}) {
		t.Fatalf("ToolFailureByKind = %#v", summary.ToolFailureByKind)
	}
	if !reflect.DeepEqual(summary.RuntimeErrorByKind, map[string]int{"llm_timeout": 2, "context_overflow": 1}) {
		t.Fatalf("RuntimeErrorByKind = %#v", summary.RuntimeErrorByKind)
	}
	if summary.RuntimeErrors != 3 {
		t.Fatalf("RuntimeErrors = %d, want 3", summary.RuntimeErrors)
	}
	if summary.SessionSearchCalls != 1 || summary.SessionSearchResults != 2 || summary.SessionSearchContextHits != 1 || summary.SessionSearchMatchedTerms != 2 {
		t.Fatalf("session search summary = calls:%d results:%d context:%d terms:%d", summary.SessionSearchCalls, summary.SessionSearchResults, summary.SessionSearchContextHits, summary.SessionSearchMatchedTerms)
	}
	if summary.RuntimeSurfaceScenarios != 2 {
		t.Fatalf("RuntimeSurfaceScenarios = %d, want 2", summary.RuntimeSurfaceScenarios)
	}
	if !reflect.DeepEqual(summary.RuntimeSurfaceCapabilities, map[string]int{"web_fetch": 2, "web_search": 1, "browser": 2, "workspace_partial": 1}) {
		t.Fatalf("RuntimeSurfaceCapabilities = %#v", summary.RuntimeSurfaceCapabilities)
	}
	if !reflect.DeepEqual(summary.RuntimeSurfaceTools, map[string]int{"web_fetch": 2, "web_search": 1, "browser_find": 2}) {
		t.Fatalf("RuntimeSurfaceTools = %#v", summary.RuntimeSurfaceTools)
	}
	if len(summary.SourceAccessExamples) != 1 || summary.SourceAccessExamples[0].CallID != "source-1" {
		t.Fatalf("SourceAccessExamples = %#v", summary.SourceAccessExamples)
	}
	if len(summary.ToolTruncationExamples) != 1 || summary.ToolTruncationExamples[0].CallID != "trunc-1" {
		t.Fatalf("ToolTruncationExamples = %#v", summary.ToolTruncationExamples)
	}
	if got := summary.ToolFailureExamples["timeout"]; len(got) != 1 || got[0].Tool != "web_fetch" {
		t.Fatalf("ToolFailureExamples[timeout] = %#v", got)
	}
	if got := summary.RuntimeErrorExamples["llm_timeout"]; len(got) != 1 || !strings.Contains(got[0].Message, "llm_stream timed out") {
		t.Fatalf("RuntimeErrorExamples[llm_timeout] = %#v", got)
	}
	if summary.LoopDecisions != 1 || summary.LoopDecisionByKind["evidence_quality"] != 1 || summary.LoopDecisionByDecision["defer"] != 1 {
		t.Fatalf("loop decision summary = count:%d kinds:%#v decisions:%#v", summary.LoopDecisions, summary.LoopDecisionByKind, summary.LoopDecisionByDecision)
	}
	if len(summary.ContextCompactionExamples) != 1 ||
		summary.ContextCompactionExamples[0].TurnID != "turn-summary" ||
		!strings.Contains(summary.ContextCompactionExamples[0].SummaryPreview, "market evidence") {
		t.Fatalf("ContextCompactionExamples = %#v", summary.ContextCompactionExamples)
	}
	if summary.PlanCalls != 3 || summary.PlanErrors != 1 {
		t.Fatalf("plan counts = calls:%d errors:%d, want 3/1", summary.PlanCalls, summary.PlanErrors)
	}
	if !reflect.DeepEqual(summary.PlanByAction, map[string]int{"set": 1, "update": 2}) {
		t.Fatalf("PlanByAction = %#v", summary.PlanByAction)
	}
}

func TestPrintBatchSummaryIncludesRepairOutcomesWithoutKinds(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		Repair: agenteval.ToolRepairStats{
			Calls:          2,
			SucceededCalls: 1,
			FailedCalls:    1,
		},
	})

	var out bytes.Buffer
	printBatchSummary(&out, summary)
	got := out.String()
	if !strings.Contains(got, "repair_calls=2,ok=1,failed=1") {
		t.Fatalf("summary missing repair outcome-only stats:\n%s", got)
	}
	if strings.Contains(got, "repair_kinds=") {
		t.Fatalf("summary should omit empty repair kinds:\n%s", got)
	}
}

func TestPrintBatchResultJSONL(t *testing.T) {
	var out bytes.Buffer
	meta := testEvalJSONLMetadata()
	meta.RuntimeWeb = true
	meta.RuntimeBrowser = true
	printBatchResultJSONL(&out, meta, agenteval.BatchResult{
		BatchScenario:    "sample",
		Workspace:        "/tmp/ws",
		TracePath:        "/tmp/ws/trace.jsonl",
		OK:               true,
		Duration:         1500 * time.Millisecond,
		AffentctlCommand: []string{"go", "run", "./cmd/affentctl", "run", "--api-key", "<redacted>"},
		Expectations: &agenteval.DebugScenarioExpectations{
			Suites:        []string{"long-run", "live-web"},
			RequiredTools: []string{"web_fetch", "browser_network_read"},
			RequiredSourceAccess: []agenteval.DebugSourceAccessRequirement{
				{Status: "network", Tool: "browser_network_read", URLContains: "metrics.example", SourceMethod: "network_xhr_fetch", JSONPath: "$.price"},
			},
			RequiredToolStatsAtLeast: map[string]int{
				"source_access_network": 1,
			},
		},
		TraceSchemaVersion: 1,
		TraceEvents:        7,
		TraceEventTypes: map[string]int{
			"message.delta": 3,
			"tool.request":  2,
			"tool.result":   2,
		},
		TurnEndReason:    "completed",
		ToolCalls:        4,
		WorkspaceRemoved: true,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:          2,
			ToolNameCanonicalized:     1,
			ToolErrors:                1,
			ToolFailureByKind:         map[string]int{"blocked": 1},
			ToolDurationMS:            75,
			LoopGuardInterventions:    3,
			ForcedNoTools:             1,
			SourceAccessResults:       1,
			SourceAccessVerified:      1,
			SourceAccessNetwork:       1,
			MemoryUpdates:             1,
			MemoryUpdateAdd:           1,
			SessionSearchCalls:        1,
			SessionSearchResults:      2,
			SessionSearchContextHits:  1,
			SessionSearchMatchedTerms: 2,
			ToolContextTruncated:      2,
			ToolContextOmittedBytes:   6144,
		},
		ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
			"blocked": {
				{Kind: "blocked", Tool: "web_fetch", ArgsSummary: `url="https://blocked.example/metrics"`, ResultSummary: "HTTP 403 | Next: use another source", ExitCode: 1},
			},
		},
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			ToolIndex:    2,
			CallID:       "net-1",
			Tool:         "browser_network_read",
			Status:       "network",
			URL:          "https://metrics.example/api.json",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			JSONPath:     "$.price",
		}},
		MemoryUpdateExamples: []agenteval.MemoryUpdateExample{{
			ToolIndex: 3,
			CallID:    "mem-1",
			Action:    "add",
			Target:    "memory",
			Topic:     "markets",
			Location:  "memory:markets",
			Preview:   "Prefer browser_network_read evidence for dynamic market pages.",
		}},
		RuntimeErrorByKind: map[string]int{"llm_incomplete_stream": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_incomplete_stream": {
				{Kind: "llm_incomplete_stream", Message: "LLM llm_stream ended with an incomplete SSE stream before finish_reason"},
			},
		},
		LoopDecisionStats: agenteval.LoopDecisionStats{
			Count:      1,
			ByKind:     map[string]int{"evidence_quality": 1},
			ByDecision: map[string]int{"defer": 1},
			Examples: []agenteval.LoopDecision{
				{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial", RequiredAction: "read browser network responses"},
			},
		},
		ContextCompactions: agenteval.ContextCompactionStats{
			Count:           1,
			Reactive:        1,
			RemovedMessages: 48,
			SummaryBytes:    3072,
			Examples: []agenteval.ContextCompaction{{
				TurnID:          "turn-jsonl",
				BeforeMessages:  80,
				AfterMessages:   24,
				RemovedMessages: 56,
				Reactive:        true,
				Reason:          "context_overflow",
				SummaryPresent:  true,
				SummaryBytes:    3072,
				SummaryPreview:  "USER_CONTEXT: keep browser network evidence in summary.",
			}},
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:       2,
			ArgsOmittedBytes:    1024,
			ResultsTruncated:    1,
			ResultsOmittedBytes: 8192,
			ResultArtifacts:     1,
		},
		ToolTruncationExamples: []agenteval.ToolTruncationExample{{
			ToolIndex:           1,
			CallID:              "trunc-jsonl-1",
			Tool:                "web_fetch",
			ArgsTruncated:       true,
			ArgsOmittedBytes:    1024,
			ResultTruncated:     true,
			ResultOmittedBytes:  8192,
			ResultArtifactPath:  ".affent/artifacts/tool-results/000001-trunc-jsonl-1.txt",
			ContextOmittedBytes: 6144,
		}},
		Repair: agenteval.ToolRepairStats{
			Calls:          2,
			SucceededCalls: 1,
			FailedCalls:    1,
			Notes:          3,
			ByKind:         map[string]int{"alias_rename": 2, "type_coercion": 1},
		},
		Plan: agenteval.PlanStats{
			Calls:    2,
			ByAction: map[string]int{"set": 1, "update": 1},
			Errors:   1,
		},
		Verifier: agenteval.VerifierResult{
			Command:            "go test ./...",
			Ran:                true,
			OK:                 false,
			ExitCode:           1,
			Duration:           25 * time.Millisecond,
			OutputBytes:        2048,
			OutputTruncated:    true,
			OutputOmittedBytes: 1024,
			OutputCapBytes:     1024,
		},
		Usage: agenteval.Usage{InputTokens: 200, OutputTokens: 50},
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl result did not decode: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{
		"schema_version":                      float64(1),
		"type":                                "scenario",
		"suite":                               "small-model-tools",
		"model":                               "eval-model",
		"provider_label":                      "eval-provider",
		"executor":                            "docker:affent-eval",
		"temperature":                         "0.2",
		"top_p":                               "0.9",
		"max_tokens":                          "512",
		"seed":                                "42",
		"runtime_web":                         true,
		"runtime_browser":                     true,
		"timeout_ms":                          float64(300000),
		"scenario":                            "sample",
		"ok":                                  true,
		"duration_ms":                         float64(1500),
		"trace_schema_version":                float64(1),
		"trace_events":                        float64(7),
		"turn_end_reason":                     "completed",
		"tool_calls":                          float64(4),
		"tool_errors":                         float64(1),
		"tool_repaired":                       float64(2),
		"tool_name_canonicalized":             float64(1),
		"tool_repair_calls":                   float64(2),
		"tool_repair_succeeded":               float64(1),
		"tool_repair_failed":                  float64(1),
		"tool_repair_notes":                   float64(3),
		"loop_guard_interventions":            float64(3),
		"forced_no_tools":                     float64(1),
		"source_access_results":               float64(1),
		"source_access_verified":              float64(1),
		"source_access_network":               float64(1),
		"memory_updates":                      float64(1),
		"memory_update_add":                   float64(1),
		"session_search_calls":                float64(1),
		"session_search_results":              float64(2),
		"session_search_context_hits":         float64(1),
		"session_search_matched_terms":        float64(2),
		"tool_duration_ms":                    float64(75),
		"tool_context_truncated":              float64(2),
		"tool_context_omitted_bytes":          float64(6144),
		"tool_args_truncated":                 float64(2),
		"tool_args_omitted_bytes":             float64(1024),
		"tool_results_truncated":              float64(1),
		"tool_results_omitted_bytes":          float64(8192),
		"tool_result_artifacts":               float64(1),
		"verifier_command":                    "go test ./...",
		"verifier_ran":                        true,
		"verifier_ok":                         false,
		"verifier_exit_code":                  float64(1),
		"verifier_duration_ms":                float64(25),
		"verifier_output_bytes":               float64(2048),
		"verifier_output_truncated":           true,
		"verifier_output_omitted_bytes":       float64(1024),
		"verifier_output_cap_bytes":           float64(1024),
		"input_tokens":                        float64(200),
		"output_tokens":                       float64(50),
		"workspace_removed":                   true,
		"plan_calls":                          float64(2),
		"plan_errors":                         float64(1),
		"loop_decisions":                      float64(1),
		"context_compactions":                 float64(1),
		"context_compactions_reactive":        float64(1),
		"context_compaction_removed_messages": float64(48),
		"context_compaction_summary_bytes":    float64(3072),
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %v\njson=%s", key, got[key], want, out.String())
		}
	}
	if _, ok := got["failures"]; ok {
		t.Fatalf("passing result should omit failures, got %#v", got["failures"])
	}
	if _, ok := got["failure_kinds"]; ok {
		t.Fatalf("passing result should omit failure_kinds, got %#v", got["failure_kinds"])
	}
	expectations, ok := got["expectations"].(map[string]any)
	if !ok {
		t.Fatalf("expectations missing or wrong type: %#v\njson=%s", got["expectations"], out.String())
	}
	if !jsonArrayContainsString(expectations["required_tools"], "browser_network_read") {
		t.Fatalf("expectations.required_tools = %#v\njson=%s", expectations["required_tools"], out.String())
	}
	stats, ok := expectations["required_tool_stats_at_least"].(map[string]any)
	if !ok || stats["source_access_network"] != float64(1) {
		t.Fatalf("expectations.required_tool_stats_at_least = %#v\njson=%s", expectations["required_tool_stats_at_least"], out.String())
	}
	sourceReqs, ok := expectations["required_source_access"].([]any)
	if !ok || len(sourceReqs) != 1 {
		t.Fatalf("expectations.required_source_access = %#v\njson=%s", expectations["required_source_access"], out.String())
	}
	command, ok := got["affentctl_command"].([]any)
	if !ok || len(command) != 6 || command[0] != "go" || command[5] != "<redacted>" {
		t.Fatalf("affentctl_command = %#v\njson=%s", got["affentctl_command"], out.String())
	}
	toolFailureKinds, ok := got["tool_failure_by_kind"].(map[string]any)
	if !ok || toolFailureKinds["blocked"] != float64(1) {
		t.Fatalf("tool_failure_by_kind = %#v\njson=%s", got["tool_failure_by_kind"], out.String())
	}
	traceEventTypes, ok := got["trace_event_types"].(map[string]any)
	if !ok || traceEventTypes["message.delta"] != float64(3) || traceEventTypes["tool.request"] != float64(2) {
		t.Fatalf("trace_event_types = %#v\njson=%s", got["trace_event_types"], out.String())
	}
	toolFailureHints, ok := got["tool_failure_hints"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(toolFailureHints["blocked"]), "direct web_fetch") {
		t.Fatalf("tool_failure_hints = %#v\njson=%s", got["tool_failure_hints"], out.String())
	}
	toolFailureExamples, ok := got["tool_failure_examples"].(map[string]any)
	if !ok {
		t.Fatalf("tool_failure_examples missing or wrong type: %#v\njson=%s", got["tool_failure_examples"], out.String())
	}
	blockedExamples, ok := toolFailureExamples["blocked"].([]any)
	if !ok || len(blockedExamples) != 1 {
		t.Fatalf("blocked tool_failure_examples = %#v\njson=%s", toolFailureExamples["blocked"], out.String())
	}
	blockedExample, ok := blockedExamples[0].(map[string]any)
	if !ok ||
		blockedExample["tool"] != "web_fetch" ||
		!strings.Contains(fmt.Sprint(blockedExample["args_summary"]), "blocked.example") ||
		!strings.Contains(fmt.Sprint(blockedExample["result_summary"]), "Next:") {
		t.Fatalf("blocked tool_failure_example = %#v\njson=%s", blockedExamples[0], out.String())
	}
	runtimeErrorKinds, ok := got["runtime_error_by_kind"].(map[string]any)
	if !ok || runtimeErrorKinds["llm_incomplete_stream"] != float64(1) {
		t.Fatalf("runtime_error_by_kind = %#v\njson=%s", got["runtime_error_by_kind"], out.String())
	}
	runtimeErrorHints, ok := got["runtime_error_hints"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(runtimeErrorHints["llm_incomplete_stream"]), "SSE stream") {
		t.Fatalf("runtime_error_hints = %#v\njson=%s", got["runtime_error_hints"], out.String())
	}
	runtimeErrorExamples, ok := got["runtime_error_examples"].(map[string]any)
	if !ok {
		t.Fatalf("runtime_error_examples missing or wrong type: %#v\njson=%s", got["runtime_error_examples"], out.String())
	}
	incompleteExamples, ok := runtimeErrorExamples["llm_incomplete_stream"].([]any)
	if !ok || len(incompleteExamples) != 1 {
		t.Fatalf("llm_incomplete_stream runtime_error_examples = %#v\njson=%s", runtimeErrorExamples["llm_incomplete_stream"], out.String())
	}
	incompleteExample, ok := incompleteExamples[0].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(incompleteExample["message"]), "incomplete SSE stream") {
		t.Fatalf("llm_incomplete_stream runtime_error_example = %#v\njson=%s", incompleteExamples[0], out.String())
	}
	debugBrief, ok := got["debug_brief"].(map[string]any)
	if !ok {
		t.Fatalf("debug_brief missing or wrong type: %#v\njson=%s", got["debug_brief"], out.String())
	}
	if !jsonArrayContainsString(debugBrief["tags"], "tool_failure:blocked") ||
		!jsonArrayContainsString(debugBrief["tags"], "runtime_error:llm_incomplete_stream") ||
		!jsonArrayContainsString(debugBrief["tags"], "loop_guard") ||
		!jsonArrayContainsString(debugBrief["tags"], "source_network") ||
		!jsonArrayContainsString(debugBrief["tags"], "memory_update:add") ||
		!jsonArrayContainsString(debugBrief["tags"], "recall") ||
		!jsonArrayContainsString(debugBrief["tags"], "context_compaction:reactive") ||
		!jsonArrayContainsString(debugBrief["tags"], "truncation") {
		t.Fatalf("debug_brief tags = %#v\njson=%s", debugBrief["tags"], out.String())
	}
	items, ok := debugBrief["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("debug_brief items = %#v\njson=%s", debugBrief["items"], out.String())
	}
	sourceAccessExamples, ok := got["source_access_examples"].([]any)
	if !ok || len(sourceAccessExamples) != 1 {
		t.Fatalf("source_access_examples = %#v\njson=%s", got["source_access_examples"], out.String())
	}
	sourceAccessExample, ok := sourceAccessExamples[0].(map[string]any)
	if !ok ||
		sourceAccessExample["tool"] != "browser_network_read" ||
		sourceAccessExample["status"] != "network" ||
		sourceAccessExample["json_path"] != "$.price" ||
		!strings.Contains(fmt.Sprint(sourceAccessExample["url"]), "metrics.example") {
		t.Fatalf("source_access_example = %#v\njson=%s", sourceAccessExamples[0], out.String())
	}
	memoryUpdateExamples, ok := got["memory_update_examples"].([]any)
	if !ok || len(memoryUpdateExamples) != 1 {
		t.Fatalf("memory_update_examples = %#v\njson=%s", got["memory_update_examples"], out.String())
	}
	memoryUpdateExample, ok := memoryUpdateExamples[0].(map[string]any)
	if !ok ||
		memoryUpdateExample["action"] != "add" ||
		memoryUpdateExample["location"] != "memory:markets" ||
		!strings.Contains(fmt.Sprint(memoryUpdateExample["preview"]), "browser_network_read") {
		t.Fatalf("memory_update_example = %#v\njson=%s", memoryUpdateExamples[0], out.String())
	}
	toolTruncationExamples, ok := got["tool_truncation_examples"].([]any)
	if !ok || len(toolTruncationExamples) != 1 {
		t.Fatalf("tool_truncation_examples = %#v\njson=%s", got["tool_truncation_examples"], out.String())
	}
	toolTruncationExample, ok := toolTruncationExamples[0].(map[string]any)
	if !ok ||
		toolTruncationExample["call_id"] != "trunc-jsonl-1" ||
		toolTruncationExample["tool"] != "web_fetch" ||
		toolTruncationExample["args_truncated"] != true ||
		toolTruncationExample["result_truncated"] != true ||
		toolTruncationExample["context_omitted_bytes"] != float64(6144) ||
		!strings.Contains(fmt.Sprint(toolTruncationExample["result_artifact_path"]), "trunc-jsonl-1") {
		t.Fatalf("tool_truncation_example = %#v\njson=%s", toolTruncationExamples[0], out.String())
	}
	loopDecisionByKind, ok := got["loop_decision_by_kind"].(map[string]any)
	if !ok || loopDecisionByKind["evidence_quality"] != float64(1) {
		t.Fatalf("loop_decision_by_kind = %#v\njson=%s", got["loop_decision_by_kind"], out.String())
	}
	loopDecisionByDecision, ok := got["loop_decision_by_decision"].(map[string]any)
	if !ok || loopDecisionByDecision["defer"] != float64(1) {
		t.Fatalf("loop_decision_by_decision = %#v\njson=%s", got["loop_decision_by_decision"], out.String())
	}
	loopDecisionExamples, ok := got["loop_decision_examples"].([]any)
	if !ok || len(loopDecisionExamples) != 1 {
		t.Fatalf("loop_decision_examples = %#v\njson=%s", got["loop_decision_examples"], out.String())
	}
	contextCompactionExamples, ok := got["context_compaction_examples"].([]any)
	if !ok || len(contextCompactionExamples) != 1 {
		t.Fatalf("context_compaction_examples = %#v\njson=%s", got["context_compaction_examples"], out.String())
	}
	contextCompactionExample, ok := contextCompactionExamples[0].(map[string]any)
	if !ok ||
		contextCompactionExample["turn_id"] != "turn-jsonl" ||
		contextCompactionExample["reactive"] != true ||
		contextCompactionExample["removed_messages"] != float64(56) ||
		contextCompactionExample["reason"] != "context_overflow" ||
		!strings.Contains(fmt.Sprint(contextCompactionExample["summary_preview"]), "browser network evidence") {
		t.Fatalf("context_compaction_example = %#v\njson=%s", contextCompactionExamples[0], out.String())
	}
	repairKinds, ok := got["tool_repair_by_kind"].(map[string]any)
	if !ok {
		t.Fatalf("tool_repair_by_kind missing or wrong type: %#v\njson=%s", got["tool_repair_by_kind"], out.String())
	}
	if repairKinds["alias_rename"] != float64(2) || repairKinds["type_coercion"] != float64(1) {
		t.Fatalf("tool_repair_by_kind = %#v", repairKinds)
	}
	planByAction, ok := got["plan_by_action"].(map[string]any)
	if !ok {
		t.Fatalf("plan_by_action missing or wrong type: %#v\njson=%s", got["plan_by_action"], out.String())
	}
	if planByAction["set"] != float64(1) || planByAction["update"] != float64(1) {
		t.Fatalf("plan_by_action = %#v", planByAction)
	}
}

func jsonArrayContainsString(raw any, want string) bool {
	values, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestPrintBatchResultJSONLIncludesDebugPathsForRetainedWorkspace(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:     "debuggable",
		Workspace:         "/tmp/ws",
		TracePath:         "/tmp/ws/trace.jsonl",
		DebugManifestPath: "/tmp/ws/affenteval-debug.json",
		TimelinePath:      "/tmp/ws/affenteval-timeline.md",
		FinalTextPath:     "/tmp/ws/affenteval-final.txt",
		StdoutPath:        "/tmp/ws/affenteval-stdout.txt",
		StderrPath:        "/tmp/ws/affenteval-stderr.txt",
		RunExitCode:       2,
		RuntimeSurface: &sse.RuntimeSurfacePayload{
			ToolCount: 3,
			Tools: []sse.RuntimeSurfaceTool{
				{Name: "web_fetch"},
				{Name: "browser_find"},
				{Name: "web_fetch"},
			},
			Capabilities:                 sse.RuntimeCapabilities{WorkspaceTools: []string{"read_file", "repo_search"}, WebFetch: true, Browser: true},
			MaxTurnSteps:                 12,
			MaxToolCalls:                 40,
			ToolResultEventCapBytes:      8192,
			ToolResultContextMaxBytes:    4096,
			ToolResultContextBudgetBytes: 32768,
			ToolResultArtifactPrefix:     ".affent/artifacts/tool-results",
		},
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl result did not decode: %v\n%s", err, out.String())
	}
	if got["debug_manifest_path"] != "/tmp/ws/affenteval-debug.json" {
		t.Fatalf("debug_manifest_path = %#v\njson=%s", got["debug_manifest_path"], out.String())
	}
	if got["timeline_path"] != "/tmp/ws/affenteval-timeline.md" {
		t.Fatalf("timeline_path = %#v\njson=%s", got["timeline_path"], out.String())
	}
	if got["final_text_path"] != "/tmp/ws/affenteval-final.txt" {
		t.Fatalf("final_text_path = %#v\njson=%s", got["final_text_path"], out.String())
	}
	if got["stdout_path"] != "/tmp/ws/affenteval-stdout.txt" {
		t.Fatalf("stdout_path = %#v\njson=%s", got["stdout_path"], out.String())
	}
	if got["stderr_path"] != "/tmp/ws/affenteval-stderr.txt" {
		t.Fatalf("stderr_path = %#v\njson=%s", got["stderr_path"], out.String())
	}
	if got["run_exit_code"] != float64(2) {
		t.Fatalf("run_exit_code = %#v\njson=%s", got["run_exit_code"], out.String())
	}
	surface, ok := got["runtime_surface"].(map[string]any)
	if !ok {
		t.Fatalf("runtime_surface missing or wrong type: %#v\njson=%s", got["runtime_surface"], out.String())
	}
	if surface["tool_count"] != float64(3) ||
		surface["max_turn_steps"] != float64(12) ||
		surface["max_tool_calls"] != float64(40) ||
		surface["tool_result_event_cap_bytes"] != float64(8192) ||
		surface["tool_result_artifact_prefix"] != ".affent/artifacts/tool-results" {
		t.Fatalf("runtime_surface limits = %#v\njson=%s", surface, out.String())
	}
	tools, ok := surface["tools"].([]any)
	if !ok || len(tools) != 2 || tools[0] != "browser_find" || tools[1] != "web_fetch" {
		t.Fatalf("runtime_surface tools = %#v\njson=%s", surface["tools"], out.String())
	}
	caps, ok := surface["capabilities"].(map[string]any)
	if !ok || caps["web_fetch"] != true || caps["browser"] != true {
		t.Fatalf("runtime_surface capabilities = %#v\njson=%s", surface["capabilities"], out.String())
	}
	workspaceTools, ok := caps["workspace_tools"].([]any)
	if !ok || len(workspaceTools) != 2 || workspaceTools[0] != "read_file" || workspaceTools[1] != "repo_search" {
		t.Fatalf("runtime_surface workspace tools = %#v\njson=%s", caps["workspace_tools"], out.String())
	}
}

func TestPrintBatchResultJSONLIncludesFailureKinds(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "failing",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 false,
		Duration:           500 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "max_turns",
		Failures: []string{
			`turn ended with reason "max_turns" (expected completed)`,
			`missing required command match "go test"; commands=[]`,
			`missing required command match "pytest"; commands=[]`,
		},
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl result did not decode: %v\n%s", err, out.String())
	}
	failures, ok := got["failures"].([]any)
	if !ok || len(failures) != 3 {
		t.Fatalf("failures = %#v, want 3 entries\njson=%s", got["failures"], out.String())
	}
	failureKinds, ok := got["failure_kinds"].(map[string]any)
	if !ok {
		t.Fatalf("failure_kinds missing or wrong type: %#v\njson=%s", got["failure_kinds"], out.String())
	}
	if failureKinds["turn_end"] != float64(1) || failureKinds["missing_command"] != float64(2) {
		t.Fatalf("failure_kinds = %#v", failureKinds)
	}
	if got["trace_schema_version"] != float64(1) {
		t.Fatalf("trace_schema_version = %#v, want 1", got["trace_schema_version"])
	}
}

func TestPrintBatchResultIncludesLLMFailureHints(t *testing.T) {
	res := agenteval.BatchResult{
		BatchScenario: "llm-failing",
		Workspace:     "/tmp/ws",
		TracePath:     "/tmp/ws/trace.jsonl",
		Failures: []string{
			`affentctl run failed: exit=1 err=LLM llm_stream timed out after 4m0s while waiting for chat completion (max-call-timeout/per-call-timeout=4m0s): context deadline exceeded`,
			`affentctl run failed: exit=1 err=stream ended without finish`,
			`affentctl run failed: exit=1 err=LLM llm_request failed (model="qwen" endpoint="https://llm.example/v1/chat/completions"): maximum context length is 4096 tokens`,
		},
	}

	var text bytes.Buffer
	printBatchResult(&text, res)
	for _, want := range []string{
		"hint[llm_timeout]",
		"upstream LLM streaming stalled",
		"hint[llm_incomplete_stream]",
		"before finish_reason",
		"hint[context_overflow]",
		"context window",
	} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text result missing %q:\n%s", want, text.String())
		}
	}

	var jsonl bytes.Buffer
	printBatchResultJSONL(&jsonl, testEvalJSONLMetadata(), res)
	var got map[string]any
	if err := json.Unmarshal(jsonl.Bytes(), &got); err != nil {
		t.Fatalf("jsonl result did not decode: %v\n%s", err, jsonl.String())
	}
	hints, ok := got["failure_hints"].(map[string]any)
	if !ok {
		t.Fatalf("failure_hints missing or wrong type: %#v\njson=%s", got["failure_hints"], jsonl.String())
	}
	if !strings.Contains(fmt.Sprint(hints["llm_timeout"]), "per-call timeout") ||
		!strings.Contains(fmt.Sprint(hints["llm_incomplete_stream"]), "SSE stream") ||
		!strings.Contains(fmt.Sprint(hints["context_overflow"]), "context window") {
		t.Fatalf("failure_hints = %#v", hints)
	}
}

func TestBatchSummaryFailureExamplesAreBounded(t *testing.T) {
	var summary batchSummary
	for i := 1; i <= 3; i++ {
		summary.add(agenteval.BatchResult{
			ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
				"timeout": {
					{Kind: "timeout", Tool: "web_fetch", ArgsSummary: fmt.Sprintf(`url="https://slow.example/%d"`, i), ExitCode: 1},
				},
			},
			RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
				"llm_timeout": {
					{Kind: "llm_timeout", Message: fmt.Sprintf("timeout example %d", i)},
				},
			},
		})
	}
	if got := summary.ToolFailureExamples["timeout"]; len(got) != batchSummaryExamplesPerKind {
		t.Fatalf("ToolFailureExamples cap = %d, want %d: %#v", len(got), batchSummaryExamplesPerKind, got)
	}
	if strings.Contains(summary.ToolFailureExamples["timeout"][1].ArgsSummary, "/3") {
		t.Fatalf("tool failure examples should keep earliest bounded samples: %#v", summary.ToolFailureExamples["timeout"])
	}
	if got := summary.RuntimeErrorExamples["llm_timeout"]; len(got) != batchSummaryExamplesPerKind {
		t.Fatalf("RuntimeErrorExamples cap = %d, want %d: %#v", len(got), batchSummaryExamplesPerKind, got)
	}
	if strings.Contains(summary.RuntimeErrorExamples["llm_timeout"][1].Message, "3") {
		t.Fatalf("runtime error examples should keep earliest bounded samples: %#v", summary.RuntimeErrorExamples["llm_timeout"])
	}
}

func TestBatchSummaryAggregatesDebugBriefTags(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		OK:                 false,
		TurnEndReason:      "max_turns",
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
	})
	summary.add(agenteval.BatchResult{
		OK: true,
		ToolStats: agenteval.ToolRuntimeStats{
			SessionSearchCalls:   1,
			SessionSearchResults: 0,
		},
	})

	if summary.DebugBriefByTag["outcome:failed"] != 1 ||
		summary.DebugBriefByTag["turn_end:max_turns"] != 1 ||
		summary.DebugBriefByTag["runtime_error:llm_timeout"] != 1 ||
		summary.DebugBriefByTag["empty_recall"] != 1 {
		t.Fatalf("DebugBriefByTag = %#v", summary.DebugBriefByTag)
	}
}

// TestBatchSummaryAggregatesDelegationAcrossScenarios pins the
// batch-level aggregation for delegation usage.
func TestBatchSummaryAggregatesDelegationAcrossScenarios(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		OK:                 true,
		Duration:           100 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		Delegation: agenteval.DelegationStats{
			FocusedTaskCalls:  2,
			FocusedTaskByType: map[string]int{"recall": 2},
			FocusedTaskErrors: 1,
		},
	})
	summary.add(agenteval.BatchResult{
		OK:                 true,
		Duration:           150 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		Delegation: agenteval.DelegationStats{
			FocusedTaskCalls:  2,
			FocusedTaskByType: map[string]int{"recall": 1, "explore": 1},
			SubagentCalls:     1,
			SubagentByMode:    map[string]int{"review": 1},
			SubagentErrors:    1,
		},
	})

	if summary.FocusedTaskCalls != 4 {
		t.Errorf("FocusedTaskCalls = %d, want 4", summary.FocusedTaskCalls)
	}
	if summary.FocusedTaskByType["recall"] != 3 || summary.FocusedTaskByType["explore"] != 1 {
		t.Errorf("merged FocusedTaskByType = %#v", summary.FocusedTaskByType)
	}
	if summary.SubagentCalls != 1 || summary.SubagentByMode["review"] != 1 {
		t.Errorf("subagent aggregates = %d, %#v", summary.SubagentCalls, summary.SubagentByMode)
	}
	if summary.FocusedTaskErrors != 1 || summary.SubagentErrors != 1 {
		t.Errorf("delegation error aggregates = focused:%d subagent:%d, want 1/1", summary.FocusedTaskErrors, summary.SubagentErrors)
	}

	// Wire-format check: consumers expect one merged object per batch.
	var out bytes.Buffer
	printBatchSummaryJSONL(&out, testEvalJSONLMetadata(), summary, nil)
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, out.String())
	}
	if got["focused_task_calls"] != float64(4) {
		t.Errorf("summary.focused_task_calls = %#v, want 4", got["focused_task_calls"])
	}
	if got["focused_task_error_rate"] != float64(0.25) {
		t.Errorf("summary.focused_task_error_rate = %#v, want 0.25", got["focused_task_error_rate"])
	}
	if got["subagent_error_rate"] != float64(1) {
		t.Errorf("summary.subagent_error_rate = %#v, want 1", got["subagent_error_rate"])
	}
	byType, ok := got["focused_task_by_type"].(map[string]any)
	if !ok || byType["recall"] != float64(3) || byType["explore"] != float64(1) {
		t.Errorf("summary.focused_task_by_type = %#v", byType)
	}

	var textOut bytes.Buffer
	printBatchSummary(&textOut, summary)
	for _, want := range []string{
		"delegation=focused_tasks:4,subagents:1",
		"delegation_errors=focused_tasks:1,subagents:1",
		"focused_task_error:25.0%,subagent_error:100.0%",
		"focused_task_by_type=explore:1,recall:3",
		"subagent_by_mode=review:1",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("summary text missing %q:\n%s", want, textOut.String())
		}
	}
}

// TestPrintBatchResultJSONL_IncludesDelegation pins the per-scenario
// delegation breakdown in the JSONL record.
func TestPrintBatchResultJSONL_IncludesDelegation(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "delegating",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 true,
		Duration:           1 * time.Second,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		ToolCalls:          4,
		Delegation: agenteval.DelegationStats{
			FocusedTaskCalls:  3,
			FocusedTaskByType: map[string]int{"recall": 2, "explore": 1},
			FocusedTaskErrors: 1,
			SubagentCalls:     1,
			SubagentByMode:    map[string]int{"test": 1},
		},
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if got["focused_task_calls"] != float64(3) {
		t.Errorf("focused_task_calls = %#v, want 3", got["focused_task_calls"])
	}
	if got["focused_task_errors"] != float64(1) {
		t.Errorf("focused_task_errors = %#v, want 1", got["focused_task_errors"])
	}
	byType, ok := got["focused_task_by_type"].(map[string]any)
	if !ok {
		t.Fatalf("focused_task_by_type missing or wrong type: %#v", got["focused_task_by_type"])
	}
	if byType["recall"] != float64(2) || byType["explore"] != float64(1) {
		t.Errorf("focused_task_by_type = %#v", byType)
	}
	if got["subagent_calls"] != float64(1) {
		t.Errorf("subagent_calls = %#v, want 1", got["subagent_calls"])
	}
	byMode, ok := got["subagent_by_mode"].(map[string]any)
	if !ok || byMode["test"] != float64(1) {
		t.Errorf("subagent_by_mode = %#v", byMode)
	}
}

// TestPrintBatchResultJSONL_OmitsDelegationForNonDelegating ensures
// scenarios that used no delegation tool produce a clean JSONL record
// without any focused_task_* / subagent_* fields. omitempty on every
// added field; if a regression flips one to a non-empty default, this
// test catches it.
func TestPrintBatchResultJSONL_OmitsDelegationForNonDelegating(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "plain",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 true,
		Duration:           1 * time.Second,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		// no Delegation
	})
	got := out.String()
	for _, field := range []string{
		`"focused_task_calls"`,
		`"focused_task_by_type"`,
		`"focused_task_errors"`,
		`"subagent_calls"`,
		`"subagent_by_mode"`,
		`"subagent_errors"`,
	} {
		if bytes.Contains([]byte(got), []byte(field)) {
			t.Errorf("delegation-free scenario record must not include %s\n%s", field, got)
		}
	}
}

func TestPrintBatchResultJSONL_OmitsPlanForNoPlanCalls(t *testing.T) {
	var out bytes.Buffer
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "plain",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 true,
		Duration:           1 * time.Second,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
	})
	got := out.String()
	for _, field := range []string{
		`"plan_calls"`,
		`"plan_by_action"`,
		`"plan_errors"`,
	} {
		if strings.Contains(got, field) {
			t.Errorf("no-plan scenario record must not include %s\n%s", field, got)
		}
	}
}

func TestPrintBatchSummaryJSONL(t *testing.T) {
	var out bytes.Buffer
	meta := testEvalJSONLMetadata()
	meta.RuntimeWeb = true
	meta.RuntimeBrowser = true
	printBatchSummaryJSONL(&out, meta, batchSummary{
		Total:                 2,
		Passed:                1,
		Failed:                1,
		Duration:              2500 * time.Millisecond,
		ToolCalls:             5,
		ToolErrors:            1,
		ToolRepaired:          3,
		ToolNameCanonicalized: 2,
		ToolRepairCalls:       4,
		ToolRepairSucceeded:   3,
		ToolRepairFailed:      1,
		ToolRepairNotes:       4,
		ToolRepairByKind:      map[string]int{"tool_name": 2, "malformed_json": 1, "type_coercion": 1},
		ToolFailureByKind:     map[string]int{"blocked": 1},
		ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
			"blocked": {
				{Kind: "blocked", Tool: "web_fetch", ArgsSummary: `url="https://blocked.example"`, ResultSummary: "blocked | Next: use another source", ExitCode: 1},
			},
		},
		RuntimeErrors:      1,
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_timeout": {
				{Kind: "llm_timeout", Message: "LLM llm_stream timed out after 4m0s"},
			},
		},
		RuntimeSurfaceScenarios:    2,
		RuntimeSurfaceTools:        map[string]int{"web_fetch": 2, "browser_find": 1},
		RuntimeSurfaceCapabilities: map[string]int{"web_fetch": 2, "browser": 1},
		LoopDecisions:              1,
		LoopDecisionByKind:         map[string]int{"evidence_quality": 1},
		LoopDecisionByDecision:     map[string]int{"defer": 1},
		LoopDecisionExamples: []agenteval.LoopDecision{
			{Kind: "evidence_quality", Decision: "defer", RequiredAction: "read browser network responses"},
		},
		ContextCompactions:         1,
		ContextCompactionsReactive: 1,
		ContextCompactionRemoved:   32,
		ContextCompactionExamples: []agenteval.ContextCompaction{{
			TurnID:          "turn-summary-jsonl",
			BeforeMessages:  64,
			AfterMessages:   20,
			RemovedMessages: 44,
			Reactive:        true,
			Reason:          "context_overflow",
			SummaryPresent:  true,
			SummaryBytes:    2048,
			SummaryPreview:  "USER_CONTEXT: preserve JSONL summary evidence.",
		}},
		LoopGuardInterventions:     3,
		ForcedNoTools:              1,
		SourceAccessResults:        4,
		SourceAccessVerified:       3,
		SourceAccessDiscoveryOnly:  1,
		SourceAccessNetwork:        2,
		SourceAccessDynamicPartial: 1,
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			ToolIndex: 2,
			CallID:    "summary-source-1",
			Tool:      "browser_network_read",
			Status:    "network",
			URL:       "https://metrics.example/api.json",
			JSONPath:  "$.price",
		}},
		MemoryUpdates:             1,
		MemoryUpdateAdd:           1,
		SessionSearchCalls:        1,
		SessionSearchResults:      2,
		SessionSearchContextHits:  1,
		SessionSearchMatchedTerms: 2,
		ToolDurationMS:            120,
		ToolContextTruncated:      4,
		ToolContextOmittedBytes:   12288,
		ToolArgsTruncated:         1,
		ToolArgsOmittedBytes:      256,
		ToolResultsTruncated:      2,
		ToolResultsOmittedBytes:   4096,
		ToolResultArtifacts:       2,
		ToolTruncationExamples: []agenteval.ToolTruncationExample{{
			ToolIndex:          4,
			CallID:             "summary-trunc-1",
			Tool:               "browser_snapshot",
			ResultTruncated:    true,
			ResultOmittedBytes: 4096,
			ResultArtifactPath: ".affent/artifacts/tool-results/000004-summary-trunc-1.txt",
		}},
		VerifierRuns:               2,
		VerifierPassed:             1,
		VerifierFailed:             1,
		VerifierOutputTruncated:    1,
		VerifierOutputOmittedBytes: 1024,
		TraceSchemaVersions:        map[int]int{1: 2},
		TraceEvents:                12,
		TraceEventTypes:            map[string]int{"message.delta": 4, "tool.request": 4, "tool.result": 4},
		InputTokens:                90,
		OutputTokens:               20,
		EndCompleted:               1,
		EndMaxTurns:                1,
		EndErrors:                  0,
		EndCancelled:               0,
		EndUnknown:                 0,
		FailureKinds:               map[string]int{"missing_command": 1, "turn_end": 1},
		DebugBriefByTag:            map[string]int{"outcome:failed": 1, "tool_failure:blocked": 1, "runtime_error:llm_timeout": 1},
		RemovedWorkspaces:          1,
		FocusedTaskCalls:           4,
		FocusedTaskErrors:          1,
		SubagentCalls:              2,
		SubagentErrors:             1,
		PlanCalls:                  3,
		PlanByAction:               map[string]int{"set": 1, "update": 2},
		PlanErrors:                 1,
	}, nil)

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl summary did not decode: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{
		"schema_version":                  float64(1),
		"type":                            "summary",
		"suite":                           "small-model-tools",
		"model":                           "eval-model",
		"provider_label":                  "eval-provider",
		"executor":                        "docker:affent-eval",
		"temperature":                     "0.2",
		"top_p":                           "0.9",
		"max_tokens":                      "512",
		"seed":                            "42",
		"runtime_web":                     true,
		"runtime_browser":                 true,
		"timeout_ms":                      float64(300000),
		"scenarios":                       float64(2),
		"passed":                          float64(1),
		"failed":                          float64(1),
		"pass_rate":                       float64(0.5),
		"completion_rate":                 float64(0.5),
		"memory_update_rate":              float64(0.5),
		"tool_error_rate":                 float64(0.2),
		"focused_task_error_rate":         float64(0.25),
		"subagent_error_rate":             float64(0.5),
		"forced_no_tools_rate":            float64(0.2),
		"loop_guard_intervention_rate":    float64(0.6),
		"plan_error_rate":                 float64(1.0 / 3.0),
		"tool_repair_success_rate":        float64(0.75),
		"verifier_pass_rate":              float64(0.5),
		"source_access_verified_rate":     float64(0.75),
		"source_network_rate":             float64(0.5),
		"source_discovery_only_rate":      float64(0.25),
		"source_dynamic_partial_rate":     float64(0.25),
		"session_search_context_hit_rate": float64(0.5),
		"avg_runtime_errors":              float64(0.5),
		"avg_context_compactions":         float64(0.5),
		"avg_context_removed_messages":    float64(16),
		"tool_context_truncation_rate":    float64(0.8),
		"tool_result_truncation_rate":     float64(0.4),
		"duration_ms":                     float64(2500),
		"tool_calls":                      float64(5),
		"tool_errors":                     float64(1),
		"tool_repaired":                   float64(3),
		"tool_name_canonicalized":         float64(2),
		"tool_repair_calls":               float64(4),
		"tool_repair_succeeded":           float64(3),
		"tool_repair_failed":              float64(1),
		"tool_repair_notes":               float64(4),
		"loop_guard_interventions":        float64(3),
		"forced_no_tools":                 float64(1),
		"source_access_results":           float64(4),
		"source_access_verified":          float64(3),
		"source_access_network":           float64(2),
		"source_access_dynamic_partial":   float64(1),
		"memory_updates":                  float64(1),
		"memory_update_add":               float64(1),
		"session_search_calls":            float64(1),
		"session_search_results":          float64(2),
		"session_search_context_hits":     float64(1),
		"session_search_matched_terms":    float64(2),
		"tool_duration_ms":                float64(120),
		"tool_context_truncated":          float64(4),
		"tool_context_omitted_bytes":      float64(12288),
		"tool_args_truncated":             float64(1),
		"tool_args_omitted_bytes":         float64(256),
		"tool_results_truncated":          float64(2),
		"tool_results_omitted_bytes":      float64(4096),
		"tool_result_artifacts":           float64(2),
		"verifier_runs":                   float64(2),
		"verifier_passed":                 float64(1),
		"verifier_failed":                 float64(1),
		"verifier_output_truncated":       float64(1),
		"verifier_output_omitted_bytes":   float64(1024),
		"trace_events":                    float64(12),
		"input_tokens":                    float64(90),
		"output_tokens":                   float64(20),
		"avg_input_tokens":                float64(45),
		"avg_output_tokens":               float64(10),
		"avg_total_tokens":                float64(55),
		"end_completed":                   float64(1),
		"end_max_turns":                   float64(1),
		"end_errors":                      float64(0),
		"end_cancelled":                   float64(0),
		"end_unknown":                     float64(0),
		"removed_workspaces":              float64(1),
		"cleanup_errors":                  float64(0),
		"focused_task_calls":              float64(4),
		"focused_task_errors":             float64(1),
		"subagent_calls":                  float64(2),
		"subagent_errors":                 float64(1),
		"plan_calls":                      float64(3),
		"plan_errors":                     float64(1),
		"loop_decisions":                  float64(1),
		"runtime_surface_rate":            float64(1),
		"runtime_surface_scenarios":       float64(2),
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %v\njson=%s", key, got[key], want, out.String())
		}
	}
	if got["avg_reactive_context_compactions"] != float64(0.5) {
		t.Fatalf("avg_reactive_context_compactions = %v, want 0.5\njson=%s", got["avg_reactive_context_compactions"], out.String())
	}
	failureKinds, ok := got["failure_kinds"].(map[string]any)
	if !ok {
		t.Fatalf("failure_kinds missing or wrong type: %#v\njson=%s", got["failure_kinds"], out.String())
	}
	if failureKinds["missing_command"] != float64(1) || failureKinds["turn_end"] != float64(1) {
		t.Fatalf("failure_kinds = %#v", failureKinds)
	}
	traceSchemaVersions, ok := got["trace_schema_versions"].(map[string]any)
	if !ok {
		t.Fatalf("trace_schema_versions missing or wrong type: %#v\njson=%s", got["trace_schema_versions"], out.String())
	}
	if traceSchemaVersions["1"] != float64(2) {
		t.Fatalf("trace_schema_versions = %#v", traceSchemaVersions)
	}
	traceEventTypes, ok := got["trace_event_types"].(map[string]any)
	if !ok || traceEventTypes["message.delta"] != float64(4) || traceEventTypes["tool.result"] != float64(4) {
		t.Fatalf("trace_event_types = %#v\njson=%s", got["trace_event_types"], out.String())
	}
	repairKinds, ok := got["tool_repair_by_kind"].(map[string]any)
	if !ok {
		t.Fatalf("tool_repair_by_kind missing or wrong type: %#v\njson=%s", got["tool_repair_by_kind"], out.String())
	}
	if repairKinds["tool_name"] != float64(2) || repairKinds["malformed_json"] != float64(1) || repairKinds["type_coercion"] != float64(1) {
		t.Fatalf("tool_repair_by_kind = %#v", repairKinds)
	}
	toolFailureKinds, ok := got["tool_failure_by_kind"].(map[string]any)
	if !ok || toolFailureKinds["blocked"] != float64(1) {
		t.Fatalf("tool_failure_by_kind = %#v\njson=%s", got["tool_failure_by_kind"], out.String())
	}
	toolFailureHints, ok := got["tool_failure_hints"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(toolFailureHints["blocked"]), "direct web_fetch") {
		t.Fatalf("tool_failure_hints = %#v\njson=%s", got["tool_failure_hints"], out.String())
	}
	toolFailureExamples, ok := got["tool_failure_examples"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(toolFailureExamples["blocked"]), "blocked.example") {
		t.Fatalf("tool_failure_examples = %#v\njson=%s", got["tool_failure_examples"], out.String())
	}
	runtimeErrorKinds, ok := got["runtime_error_by_kind"].(map[string]any)
	if !ok || runtimeErrorKinds["llm_timeout"] != float64(1) {
		t.Fatalf("runtime_error_by_kind = %#v\njson=%s", got["runtime_error_by_kind"], out.String())
	}
	runtimeErrorHints, ok := got["runtime_error_hints"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(runtimeErrorHints["llm_timeout"]), "per-call timeout") {
		t.Fatalf("runtime_error_hints = %#v\njson=%s", got["runtime_error_hints"], out.String())
	}
	runtimeErrorExamples, ok := got["runtime_error_examples"].(map[string]any)
	if !ok || !strings.Contains(fmt.Sprint(runtimeErrorExamples["llm_timeout"]), "timed out") {
		t.Fatalf("runtime_error_examples = %#v\njson=%s", got["runtime_error_examples"], out.String())
	}
	sourceAccessExamples, ok := got["source_access_examples"].([]any)
	if !ok || len(sourceAccessExamples) != 1 {
		t.Fatalf("source_access_examples = %#v\njson=%s", got["source_access_examples"], out.String())
	}
	sourceAccessExample, ok := sourceAccessExamples[0].(map[string]any)
	if !ok ||
		sourceAccessExample["call_id"] != "summary-source-1" ||
		sourceAccessExample["status"] != "network" ||
		sourceAccessExample["json_path"] != "$.price" {
		t.Fatalf("source_access_example = %#v\njson=%s", sourceAccessExamples[0], out.String())
	}
	toolTruncationExamples, ok := got["tool_truncation_examples"].([]any)
	if !ok || len(toolTruncationExamples) != 1 {
		t.Fatalf("tool_truncation_examples = %#v\njson=%s", got["tool_truncation_examples"], out.String())
	}
	toolTruncationExample, ok := toolTruncationExamples[0].(map[string]any)
	if !ok ||
		toolTruncationExample["call_id"] != "summary-trunc-1" ||
		toolTruncationExample["tool"] != "browser_snapshot" ||
		toolTruncationExample["result_truncated"] != true ||
		toolTruncationExample["result_omitted_bytes"] != float64(4096) {
		t.Fatalf("tool_truncation_example = %#v\njson=%s", toolTruncationExamples[0], out.String())
	}
	debugBriefByTag, ok := got["debug_brief_by_tag"].(map[string]any)
	if !ok ||
		debugBriefByTag["outcome:failed"] != float64(1) ||
		debugBriefByTag["tool_failure:blocked"] != float64(1) ||
		debugBriefByTag["runtime_error:llm_timeout"] != float64(1) {
		t.Fatalf("debug_brief_by_tag = %#v\njson=%s", got["debug_brief_by_tag"], out.String())
	}
	runtimeSurfaceTools, ok := got["runtime_surface_tools"].(map[string]any)
	if !ok || runtimeSurfaceTools["web_fetch"] != float64(2) || runtimeSurfaceTools["browser_find"] != float64(1) {
		t.Fatalf("runtime_surface_tools = %#v\njson=%s", got["runtime_surface_tools"], out.String())
	}
	runtimeSurfaceCapabilities, ok := got["runtime_surface_capabilities"].(map[string]any)
	if !ok || runtimeSurfaceCapabilities["web_fetch"] != float64(2) || runtimeSurfaceCapabilities["browser"] != float64(1) {
		t.Fatalf("runtime_surface_capabilities = %#v\njson=%s", got["runtime_surface_capabilities"], out.String())
	}
	loopDecisionByKind, ok := got["loop_decision_by_kind"].(map[string]any)
	if !ok || loopDecisionByKind["evidence_quality"] != float64(1) {
		t.Fatalf("loop_decision_by_kind = %#v\njson=%s", got["loop_decision_by_kind"], out.String())
	}
	loopDecisionByDecision, ok := got["loop_decision_by_decision"].(map[string]any)
	if !ok || loopDecisionByDecision["defer"] != float64(1) {
		t.Fatalf("loop_decision_by_decision = %#v\njson=%s", got["loop_decision_by_decision"], out.String())
	}
	loopDecisionExamples, ok := got["loop_decision_examples"].([]any)
	if !ok || len(loopDecisionExamples) != 1 {
		t.Fatalf("loop_decision_examples = %#v\njson=%s", got["loop_decision_examples"], out.String())
	}
	contextCompactionExamples, ok := got["context_compaction_examples"].([]any)
	if !ok || len(contextCompactionExamples) != 1 {
		t.Fatalf("context_compaction_examples = %#v\njson=%s", got["context_compaction_examples"], out.String())
	}
	contextCompactionExample, ok := contextCompactionExamples[0].(map[string]any)
	if !ok ||
		contextCompactionExample["turn_id"] != "turn-summary-jsonl" ||
		contextCompactionExample["removed_messages"] != float64(44) ||
		contextCompactionExample["reason"] != "context_overflow" ||
		!strings.Contains(fmt.Sprint(contextCompactionExample["summary_preview"]), "JSONL summary evidence") {
		t.Fatalf("context_compaction_example = %#v\njson=%s", contextCompactionExamples[0], out.String())
	}
	planByAction, ok := got["plan_by_action"].(map[string]any)
	if !ok {
		t.Fatalf("plan_by_action missing or wrong type: %#v\njson=%s", got["plan_by_action"], out.String())
	}
	if planByAction["set"] != float64(1) || planByAction["update"] != float64(2) {
		t.Fatalf("plan_by_action = %#v", planByAction)
	}
}

func TestPrintBatchSummaryJSONLIncludesQualityGateResult(t *testing.T) {
	var out bytes.Buffer
	minPassRate := 0.8
	meta := testEvalJSONLMetadata()
	meta.MinPassRate = &minPassRate

	printBatchSummaryJSONL(&out, meta, batchSummary{
		Total:        2,
		Passed:       1,
		EndCompleted: 2,
	}, []string{"pass_rate 0.500 < min 0.800"})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl summary did not decode: %v\n%s", err, out.String())
	}
	if got["min_pass_rate"] != float64(0.8) {
		t.Fatalf("min_pass_rate = %#v\njson=%s", got["min_pass_rate"], out.String())
	}
	if got["quality_gates_passed"] != false {
		t.Fatalf("quality_gates_passed = %#v\njson=%s", got["quality_gates_passed"], out.String())
	}
	failures, ok := got["quality_gate_failures"].([]any)
	if !ok || len(failures) != 1 || failures[0] != "pass_rate 0.500 < min 0.800" {
		t.Fatalf("quality_gate_failures = %#v\njson=%s", got["quality_gate_failures"], out.String())
	}
}

func TestCloneTraceSchemaVersions(t *testing.T) {
	if got := cloneTraceSchemaVersions(nil); got != nil {
		t.Fatalf("nil trace schema versions should produce nil map, got %#v", got)
	}
	in := map[int]int{1: 2}
	got := cloneTraceSchemaVersions(in)
	if got[1] != 2 {
		t.Fatalf("cloneTraceSchemaVersions = %#v, want version 1 count 2", got)
	}
	got[1] = 3
	if in[1] != 2 {
		t.Fatalf("cloneTraceSchemaVersions should not alias input, input = %#v", in)
	}
}

func TestEvalJSONLMetadataFromConfig(t *testing.T) {
	t.Setenv("AFFENTCTL_MODEL", "env-model")
	t.Setenv("AFFENTEVAL_PROVIDER_LABEL", "env-provider")
	meta := evalJSONLMetadataFromConfig("small-model-tools", "", "", "", "0", "", "", "", false, "", false, false, false, false, false, "", 5*time.Minute, "", qualityGateConfig{})
	if meta.SchemaVersion != evalJSONLSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", meta.SchemaVersion, evalJSONLSchemaVersion)
	}
	if meta.Model != "env-model" || meta.ProviderLabel != "env-provider" {
		t.Fatalf("env metadata not used: %+v", meta)
	}
	if meta.Executor != "local" {
		t.Fatalf("Executor = %q, want local", meta.Executor)
	}
	if meta.Suite != "small-model-tools" || meta.Temperature != "0" || meta.TimeoutMS != int64(300000) {
		t.Fatalf("metadata = %+v", meta)
	}

	minPassRate := 0.8
	minMemoryUpdateRate := 0.2
	minRuntimeSurfaceRate := 0.9
	minSourceNetworkRate := 0.5
	minSourceRate := 0.9
	minSessionSearchContextHitRate := 0.75
	minToolRepairSuccessRate := 0.85
	minVerifierPassRate := 0.9
	maxFocusedTaskErrorRate := 0.07
	maxForcedNoToolsRate := 0.1
	maxLoopGuardInterventionRate := 0.15
	maxPlanErrorRate := 0.05
	maxSourceDiscoveryOnlyRate := 0.1
	maxSourceDynamicPartialRate := 0.1
	maxSubagentErrorRate := 0.08
	maxToolErrorRate := 0.05
	maxToolResultTruncationRate := 0.2
	maxAvgRuntimeErrors := 0.05
	maxAvgContextCompactions := 0.1
	maxAvgReactiveContextCompactions := 0.2
	maxAvgTotalTokens := 120000.0
	meta = evalJSONLMetadataFromConfig(" custom ", " flag-model ", " flag-provider ", " sandbox ", " 0.4 ", " 0.9 ", " 512 ", " 42 ", true, " readonly_workspace,web ", true, true, true, true, true, " /tmp/mcp.json ", time.Second, " Web-Evidence ", qualityGateConfig{
		MinPassRate:                    &minPassRate,
		MinMemoryUpdateRate:            &minMemoryUpdateRate,
		MinRuntimeSurfaceRate:          &minRuntimeSurfaceRate,
		MinSourceNetworkRate:           &minSourceNetworkRate,
		MinSourceAccessVerifiedRate:    &minSourceRate,
		MinSessionSearchContextHitRate: &minSessionSearchContextHitRate,
		MinToolRepairSuccessRate:       &minToolRepairSuccessRate,
		MinVerifierPassRate:            &minVerifierPassRate,
		MaxFocusedTaskErrorRate:        &maxFocusedTaskErrorRate,
		MaxForcedNoToolsRate:           &maxForcedNoToolsRate,
		MaxLoopGuardInterventionRate:   &maxLoopGuardInterventionRate,
		MaxPlanErrorRate:               &maxPlanErrorRate,
		MaxSourceDiscoveryOnlyRate:     &maxSourceDiscoveryOnlyRate,
		MaxSourceDynamicPartialRate:    &maxSourceDynamicPartialRate,
		MaxSubagentErrorRate:           &maxSubagentErrorRate,
		MaxToolErrorRate:               &maxToolErrorRate,
		MaxToolResultTruncationRate:    &maxToolResultTruncationRate,
		MaxAvgRuntimeErrors:            &maxAvgRuntimeErrors,
		MaxAvgContextCompactions:       &maxAvgContextCompactions,
		MaxAvgReactiveCompactions:      &maxAvgReactiveContextCompactions,
		MaxAvgTotalTokens:              &maxAvgTotalTokens,
	})
	if meta.Model != "flag-model" || meta.ProviderLabel != "flag-provider" || meta.Executor != "sandbox" || meta.Temperature != "0.4" || meta.TopP != "0.9" || meta.MaxTokens != "512" || meta.Seed != "42" || meta.Suite != "custom" || !meta.RuntimeEvalMode || meta.RuntimeTools != "readonly_workspace,web" || !meta.RuntimeAllTools || !meta.RuntimeMemory || !meta.RuntimeWeb || !meta.RuntimeBrowser || !meta.TraceDeltas || !meta.RuntimeMCP || meta.TimeoutMS != 1000 || meta.QualityProfile != "web-evidence" {
		t.Fatalf("flag metadata not normalized: %+v", meta)
	}
	if meta.MinPassRate == nil || *meta.MinPassRate != 0.8 || meta.MinMemoryUpdateRate == nil || *meta.MinMemoryUpdateRate != 0.2 || meta.MinRuntimeSurfaceRate == nil || *meta.MinRuntimeSurfaceRate != 0.9 || meta.MinSourceNetworkRate == nil || *meta.MinSourceNetworkRate != 0.5 || meta.MinSourceAccessVerifiedRate == nil || *meta.MinSourceAccessVerifiedRate != 0.9 || meta.MinSessionSearchContextHitRate == nil || *meta.MinSessionSearchContextHitRate != 0.75 || meta.MinToolRepairSuccessRate == nil || *meta.MinToolRepairSuccessRate != 0.85 || meta.MinVerifierPassRate == nil || *meta.MinVerifierPassRate != 0.9 || meta.MaxFocusedTaskErrorRate == nil || *meta.MaxFocusedTaskErrorRate != 0.07 || meta.MaxForcedNoToolsRate == nil || *meta.MaxForcedNoToolsRate != 0.1 || meta.MaxLoopGuardInterventionRate == nil || *meta.MaxLoopGuardInterventionRate != 0.15 || meta.MaxPlanErrorRate == nil || *meta.MaxPlanErrorRate != 0.05 || meta.MaxSourceDiscoveryOnlyRate == nil || *meta.MaxSourceDiscoveryOnlyRate != 0.1 || meta.MaxSourceDynamicPartialRate == nil || *meta.MaxSourceDynamicPartialRate != 0.1 || meta.MaxSubagentErrorRate == nil || *meta.MaxSubagentErrorRate != 0.08 || meta.MaxToolErrorRate == nil || *meta.MaxToolErrorRate != 0.05 || meta.MaxToolResultTruncationRate == nil || *meta.MaxToolResultTruncationRate != 0.2 || meta.MaxAvgRuntimeErrors == nil || *meta.MaxAvgRuntimeErrors != 0.05 || meta.MaxAvgContextCompactions == nil || *meta.MaxAvgContextCompactions != 0.1 || meta.MaxAvgReactiveCompactions == nil || *meta.MaxAvgReactiveCompactions != 0.2 || meta.MaxAvgTotalTokens == nil || *meta.MaxAvgTotalTokens != 120000 {
		t.Fatalf("quality gate metadata not preserved: %+v", meta)
	}
	if meta.MinCompletionRate != nil || meta.MaxToolContextTruncationRate != nil {
		t.Fatalf("disabled quality gate metadata should be omitted: %+v", meta)
	}
}

func TestFailureKindsForResult(t *testing.T) {
	if got := failureKindsForResult(nil); got != nil {
		t.Fatalf("nil failures should produce nil map, got %#v", got)
	}
	got := failureKindsForResult([]string{
		`turn ended with reason "max_turns" (expected completed)`,
		`missing required command match "go test"; commands=[]`,
		`missing required command match "pytest"; commands=[]`,
		`focused_task_errors=1 subagent_errors=0`,
		`verify=0, want >= 1; focused_tasks=map[explore:1]`,
		`test=0, want >= 1; subagents=map[review:1]`,
		`expected "skill" result to contain "direct install cannot use a remote source URL"; tools=skill`,
		`affentctl run failed: exit=1 err=LLM llm_stream timed out after 4m0s while waiting for chat completion (model="qwen" endpoint="https://llm.example/v1/chat/completions" max-call-timeout/per-call-timeout=4m0s): context deadline exceeded`,
		`affentctl run failed: exit=1 err=LLM llm_stream stream idle timeout (model="qwen" endpoint="https://llm.example/v1/chat/completions" stream-idle-timeout=1m0s max-call-timeout/per-call-timeout=4m0s): stream idle timeout`,
		`affentctl run failed: exit=1 err=LLM llm_stream ended with an incomplete SSE stream (model="qwen" endpoint="https://llm.example/v1/chat/completions"). HTTP streaming started, but the upstream closed the connection before sending any terminal finish_reason chunk: stream ended without finish`,
		`affentctl run failed: exit=1 err=LLM llm_request failed (model="qwen" endpoint="https://llm.example/v1/chat/completions"): prompt is too long`,
	})
	if got["turn_end"] != 1 ||
		got["missing_command"] != 2 ||
		got["delegation_error"] != 1 ||
		got["missing_focused_task"] != 1 ||
		got["missing_subagent"] != 1 ||
		got["skill_install_guard"] != 1 ||
		got["llm_timeout"] != 2 ||
		got["llm_incomplete_stream"] != 1 ||
		got["context_overflow"] != 1 {
		t.Fatalf("failureKindsForResult = %#v", got)
	}
}

func TestToolFailureKindHintIncludesWebSearchRecovery(t *testing.T) {
	for _, c := range []struct {
		kind string
		want string
	}{
		{kind: "no_results", want: "refine with distinctive entities"},
		{kind: "search_error", want: "web_search backend failed"},
		{kind: "dynamic_shell", want: "client-rendered loading/app shell"},
		{kind: "stale_ref", want: "browser_snapshot"},
		{kind: "not_interactable", want: "hidden, disabled, or covered"},
		{kind: "loop_guard_repeated_failed_input", want: "same failed URL/query"},
		{kind: "loop_guard_direct_reader_warning", want: "direct-reader trap"},
		{kind: "loop_guard_repeated_call", want: "repeated identical tool arguments"},
		{kind: "loop_guard_repeated_failures", want: "soft warning"},
		{kind: "tool_policy_first_tool", want: "required first tool"},
		{kind: "tool_policy_repeat", want: "prior result"},
		{kind: "tool_policy_active", want: "structured evidence"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			if got := toolFailureKindHint(c.kind); !strings.Contains(got, c.want) {
				t.Fatalf("toolFailureKindHint(%q) = %q, want contains %q", c.kind, got, c.want)
			}
		})
	}
}

func testEvalJSONLMetadata() evalJSONLMetadata {
	return evalJSONLMetadata{
		SchemaVersion: evalJSONLSchemaVersion,
		Suite:         "small-model-tools",
		Model:         "eval-model",
		ProviderLabel: "eval-provider",
		Executor:      "docker:affent-eval",
		Temperature:   "0.2",
		TopP:          "0.9",
		MaxTokens:     "512",
		Seed:          "42",
		TimeoutMS:     int64(300000),
	}
}

func TestFailureKind(t *testing.T) {
	cases := []struct {
		failure string
		want    string
	}{
		{"affentctl run failed: exit=1", "affentctl_run"},
		{"verify command failed: go test: exit status 1", "verify_command"},
		{"parse trace: open trace.jsonl: no such file", "parse_trace"},
		{`turn ended with reason "max_turns" (expected completed)`, "turn_end"},
		{`missing required command match "go test"; commands=[]`, "missing_command"},
		{`forbidden command substring "| head" in "go test | head"`, "forbidden_command"},
		{`protected file changed: app_test.go`, "protected_file"},
		{`forbidden content "bad" found in config.py`, "forbidden_content"},
		{`final text did not contain "done"; got ""`, "final_text_missing"},
		{`final text leaked 1 forbidden substring(s): [ignore me]`, "final_text_forbidden"},
		{`expected at least one "read_file" invocation, got 0 tool calls`, "missing_tool"},
		{`found forbidden "write_file" call (call_id=c1 args=map[])`, "forbidden_tool"},
		{`expected "skill" result to contain "direct install cannot use a remote source URL"; tools=skill`, "skill_install_guard"},
		{`expected "shell" result to contain "ok"; tools=shell`, "tool_result_missing"},
		{`affentctl run failed: exit=1 err=LLM llm_stream timed out after 4m0s while waiting for chat completion (max-call-timeout/per-call-timeout=4m0s): context deadline exceeded`, "llm_timeout"},
		{`affentctl run failed: exit=1 err=LLM llm_stream stream idle timeout (stream-idle-timeout=1m0s max-call-timeout/per-call-timeout=4m0s)`, "llm_timeout"},
		{`affentctl run failed: exit=1 err=context deadline exceeded while waiting for chat completion`, "llm_timeout"},
		{`affentctl run failed: exit=1 err=LLM llm_stream ended with an incomplete SSE stream before finish_reason`, "llm_incomplete_stream"},
		{`affentctl run failed: exit=1 err=stream ended without finish`, "llm_incomplete_stream"},
		{`something else`, "other"},
	}
	for _, tc := range cases {
		if got := failureKind(tc.failure); got != tc.want {
			t.Fatalf("failureKind(%q) = %q, want %q", tc.failure, got, tc.want)
		}
	}
}

func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		done <- err
	}()
	code := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), code
}

func writeTempFile(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/prompt.txt"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(&buf, r)
		done <- err
	}()
	code := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), code
}
