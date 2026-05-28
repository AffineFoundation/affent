package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
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
		"max-avg-tool-calls=14.000",
		"max-avg-duration-ms=180000.000",
		"max-avg-total-tokens=120000.000",
		"max-avg-context-removed-messages=120.000",
		"max-avg-context-summary-bytes=24000.000",
		"max-avg-context-injections=8.000",
		"max-avg-context-injection-bytes=24000.000",
		"max-avg-context-injection-estimated-tokens=6000.000",
		"min-loop-protocol-calibration-request-rate=0.050",
		"min-loop-protocol-calibration-rate=0.050",
		"min-trace-event-rate=0.900",
		"min-source-access-verified-rate=0.900",
		"min-expectation-domain-pass-rate=0.800",
		"min-each-expectation-domain-pass-rate=0.500",
		"min-expectation-domain-source-access-verified-rate=web_evidence=0.900",
		"max-focused-task-error-rate=0.100",
		"max-subagent-error-rate=0.100",
		"max-source-dynamic-partial-rate=0.200",
		"max-expectation-domain-avg-total-tokens=web_evidence=120000.000",
		"max-expectation-domain-avg-tool-calls=web_evidence=18.000",
		"max-expectation-domain-avg-runtime-errors=web_evidence=0.200",
		"max-expectation-domain-tool-error-rate=web_evidence=0.100",
		"max-expectation-domain-loop-guard-intervention-rate=web_evidence=0.250",
		"max-debug-brief-tag-rate=browser_network:unread_refs=0.000",
		"max-debug-brief-tag-rate=browser_scroll:stuck_without_network=0.000",
		"max-debug-brief-tag-rate=empty_recall:no_recent_sessions=0.000",
		"max-debug-brief-tag-rate=loop_guard:forced_no_tools=0.000",
		"max-debug-brief-tag-rate=recall:weak_context=0.000",
		"max-debug-brief-tag-rate=source_dynamic_without_network=0.000",
		"max-debug-brief-tag-rate=source_network:missing_response_diagnostics=0.000",
		"max-debug-brief-tag-rate=tool_repair:failed=0.000",
		"max-debug-brief-tag-rate=truncation:missing_artifact=0.000",
		"require-expectation-capability=longrun_recovery,loop_protocol,session_search",
		"require-expectation-capability=browser,delegated_source_evidence,source_access,web",
		"require-expectation-domain=bittensor,code_pr,longrun_recovery,market",
		"require-expectation-domain=web_evidence",
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
	for _, want := range []string{"-require-expectation-capability", "-require-expectation-domain", "-min-expectation-domain-pass-rate", "-min-each-expectation-domain-pass-rate", "-max-expectation-domain-avg-total-tokens", "-min-expectation-domain-source-access-verified-rate"} {
		if !strings.Contains(help, want) {
			t.Fatalf("--help missing expectation coverage gate %q:\n%s", want, help)
		}
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
			name: "min expectation capability pass rate too high",
			args: []string{"--min-expectation-capability-pass-rate=1.1"},
			want: "--min-expectation-capability-pass-rate must be between 0 and 1",
		},
		{
			name: "min each expectation capability pass rate too high",
			args: []string{"--min-each-expectation-capability-pass-rate=1.1"},
			want: "--min-each-expectation-capability-pass-rate must be between 0 and 1",
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
			name: "negative max avg tool calls",
			args: []string{"--max-avg-tool-calls=-2"},
			want: "--max-avg-tool-calls must be disabled with -1 or set to a non-negative value",
		},
		{
			name: "negative max avg duration",
			args: []string{"--max-avg-duration-ms=-2"},
			want: "--max-avg-duration-ms must be disabled with -1 or set to a non-negative value",
		},
		{
			name: "negative max avg reactive context compactions",
			args: []string{"--max-avg-reactive-context-compactions=-2"},
			want: "--max-avg-reactive-context-compactions must be disabled with -1 or set to a non-negative value",
		},
		{
			name: "negative max avg context injection tokens",
			args: []string{"--max-avg-context-injection-estimated-tokens=-2"},
			want: "--max-avg-context-injection-estimated-tokens must be disabled with -1 or set to a non-negative value",
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
			name:   "runtime tools web group satisfies fetch and search",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "web"},
			scenario: agenteval.BatchScenario{
				Name:          "live-web",
				RequiredTools: []string{"web_fetch", "web_search"},
			},
		},
		{
			name:   "runtime browser satisfies rendered page tools",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeBrowser: true},
			scenario: agenteval.BatchScenario{
				Name:          "taostats-scroll",
				RequiredTools: []string{"browser_navigate", "browser_scroll", "browser_network_read"},
				RequiredToolOrder: []agenteval.ToolOrderRequirement{
					{Earlier: "browser_scroll", Later: "browser_network_read"},
				},
			},
		},
		{
			name:   "browser-only live scenario is rejected without browser surface",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "web"},
			scenario: agenteval.BatchScenario{
				Name:          "taostats-scroll",
				RequiredTools: []string{"browser_navigate", "browser_scroll", "browser_network_read"},
				RequiredToolOrder: []agenteval.ToolOrderRequirement{
					{Earlier: "browser_scroll", Later: "browser_network_read"},
				},
			},
			wantErr: "taostats-scroll missing browser_navigate, browser_network_read, browser_scroll",
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
		{
			name:   "required tool counts are validated before run",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "readonly_workspace"},
			scenario: agenteval.BatchScenario{
				Name:               "plan-edit",
				RequiredToolCounts: map[string]int{"plan": 1, "edit_file": 1},
			},
			wantErr: "plan-edit missing edit_file, plan",
		},
		{
			name:   "memory stats imply memory tool",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true},
			scenario: agenteval.BatchScenario{
				Name:                     "memory-stats",
				RequiredToolStatsAtLeast: map[string]int{"memory_updates": 1},
			},
			wantErr: "memory-stats missing memory",
		},
		{
			name:   "runtime memory satisfies memory stats",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeMemory: true},
			scenario: agenteval.BatchScenario{
				Name:                     "memory-stats",
				RequiredToolStatsAtLeast: map[string]int{"memory_update_add": 1},
			},
		},
		{
			name:   "source access stats require a web or browser surface",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "readonly_workspace"},
			scenario: agenteval.BatchScenario{
				Name:                     "source-stats",
				RequiredToolStatsAtLeast: map[string]int{"source_access_verified": 1},
			},
			wantErr: "source-stats missing source_access",
		},
		{
			name:   "source access pseudo tool does not satisfy source stats",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "source_access"},
			scenario: agenteval.BatchScenario{
				Name:                     "source-stats",
				RequiredToolStatsAtLeast: map[string]int{"source_access_verified": 1},
			},
			wantErr: "source-stats missing source_access",
		},
		{
			name:   "individual web tool satisfies source access stats",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "web_fetch"},
			scenario: agenteval.BatchScenario{
				Name:                     "source-stats",
				RequiredToolStatsAtLeast: map[string]int{"source_access_results": 1},
			},
		},
		{
			name:   "tool order and argument requirements are validated before run",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "read_file"},
			scenario: agenteval.BatchScenario{
				Name:              "edit-flow",
				RequiredToolOrder: []agenteval.ToolOrderRequirement{{Earlier: "read_file", Later: "edit_file"}},
				RequiredToolArgContains: []agenteval.ToolArgContainsRequirement{{
					Tool:      "shell",
					Arg:       "command",
					Substring: "go test",
				}},
			},
			wantErr: "edit-flow missing edit_file, shell",
		},
		{
			name:   "session search requirements imply recall tool",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "workspace"},
			scenario: agenteval.BatchScenario{
				Name:                  "history",
				RequiredSessionSearch: []agenteval.SessionSearchRequirement{{SessionID: "market-alpha"}},
			},
			wantErr: "history missing session_search",
		},
		{
			name:   "recent session search requirements imply recall tool",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "workspace"},
			scenario: agenteval.BatchScenario{
				Name:                        "history-recent",
				RequiredRecentSessionSearch: []agenteval.RecentSessionSearchRequirement{{SessionID: "market-alpha"}},
			},
			wantErr: "history-recent missing session_search",
		},
		{
			name:   "command tool order requirements imply ordered tool",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "shell"},
			scenario: agenteval.BatchScenario{
				Name: "verify-edit",
				RequiredCommandBeforeTool: []agenteval.CommandToolOrderRequirement{{
					Command: "go test ./...",
					Tool:    "edit_file",
				}},
			},
			wantErr: "verify-edit missing edit_file",
		},
		{
			name:   "delegation requirements imply child tools",
			runner: BatchRuntimeToolConfig{RuntimeEvalMode: true, RuntimeTools: "workspace"},
			scenario: agenteval.BatchScenario{
				Name:                      "delegation",
				RequiredFocusedTaskCounts: map[string]int{"explore": 1},
				RequiredSubagentModeCounts: map[string]int{
					"review": 1,
				},
				RequiredSubagentSourceCounts: map[string]int{
					"review": 1,
				},
			},
			wantErr: "delegation missing run_task, subagent_run",
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

func TestExpectationRequiredToolNamesIncludesImplicitTools(t *testing.T) {
	got := expectationRequiredToolNames(agenteval.DebugScenarioExpectations{
		RequiredCommands: []string{"go test ./..."},
		RequiredCommandAfterTool: []agenteval.DebugCommandToolOrderRequirement{{
			Command: "go test ./...",
			Tool:    "edit_file",
		}},
		RequiredSessionSearch: []agenteval.DebugSessionSearchRequirement{{
			SessionID: "market-alpha",
		}},
		RequiredRecentSessionSearch: []agenteval.DebugRecentSessionSearchRequirement{{
			SessionID: "market-alpha",
		}},
		RequiredFocusedTaskCounts: map[string]int{"explore": 1},
		RequiredSubagentModeCounts: map[string]int{
			"review": 1,
		},
		RequiredSubagentSourceCounts: map[string]int{
			"review": 1,
		},
	})
	want := []string{"edit_file", agent.FocusedTaskToolName, agent.SessionSearchToolName, "shell", agent.SubagentToolName}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expectationRequiredToolNames = %#v, want %#v", got, want)
	}
}

func TestQualityGateFailures(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	summary := batchSummary{
		Total:                                   2,
		Passed:                                  1,
		EndCompleted:                            1,
		Duration:                                2500 * time.Millisecond,
		ToolCalls:                               5,
		ToolErrors:                              1,
		LoopGuardInterventions:                  2,
		ForcedNoTools:                           1,
		FocusedTaskCalls:                        4,
		FocusedTaskErrors:                       2,
		SubagentCalls:                           2,
		SubagentErrors:                          1,
		PlanCalls:                               4,
		PlanErrors:                              2,
		ToolRepairCalls:                         4,
		ToolRepairSucceeded:                     3,
		VerifierRuns:                            2,
		VerifierPassed:                          1,
		RuntimeErrors:                           3,
		RuntimeSurfaceScenarios:                 1,
		TraceEventScenarios:                     1,
		MemoryUpdates:                           1,
		MemorySearchCalls:                       2,
		MemorySearchMisses:                      1,
		LoopProtocolFeedScenarios:               1,
		LoopProtocolCalibrationRequestScenarios: 1,
		LoopProtocolCalibrationScenarios:        1,
		SourceAccessResults:                     4,
		SourceAccessVerified:                    3,
		SourceAccessDiscoveryOnly:               1,
		SourceAccessNetwork:                     1,
		SourceAccessDynamicPartial:              1,
		SessionSearchCalls:                      1,
		SessionSearchResults:                    2,
		SessionSearchContextHits:                1,
		SessionSearchMatchedTerms:               1,
		ToolContextTruncated:                    4,
		ToolResultsTruncated:                    3,
		InputTokens:                             90,
		OutputTokens:                            20,
		ContextCompactions:                      1,
		ContextCompactionsReactive:              1,
		ContextCompactionRemoved:                32,
		ContextCompactionSummary:                2048,
		ContextCompactionSummaryMissing:         1,
		ContextCompactionSummaryEmpty:           1,
		ContextInjections:                       5,
		ContextInjectionBytes:                   9000,
		ContextInjectionEstimatedTokens:         2250,
		ExpectationCapabilities:                 map[string]int{"browser": 2, "memory": 1, "web": 1},
		ExpectationCapabilityPass:               map[string]int{"browser": 1, "memory": 1},
		ExpectationDomains:                      map[string]int{"bittensor": 1, "market": 2},
		ExpectationDomainPass:                   map[string]int{"market": 1},
		ExpectationDomainRuntime: map[string]*expectationDomainRuntimeTotals{
			"bittensor": {
				Scenarios:                  1,
				Failed:                     1,
				Duration:                   time.Second,
				ToolCalls:                  4,
				ToolErrors:                 2,
				LoopGuardInterventions:     2,
				SourceAccessResults:        2,
				SourceAccessVerified:       1,
				SourceAccessNetwork:        1,
				SourceAccessDiscoveryOnly:  1,
				SourceAccessDynamicPartial: 1,
				RuntimeErrors:              3,
				InputTokens:                600,
				OutputTokens:               200,
			},
			"market": {
				Scenarios:            2,
				Passed:               1,
				Failed:               1,
				Duration:             1500 * time.Millisecond,
				ToolCalls:            6,
				ToolErrors:           1,
				SourceAccessResults:  4,
				SourceAccessVerified: 3,
				SourceAccessNetwork:  3,
				RuntimeErrors:        1,
				InputTokens:          500,
				OutputTokens:         100,
			},
		},
		DebugBriefByTag: map[string]int{"browser_scroll:stuck_without_network": 1, "source_dynamic_without_network": 1, "verifier:not_run": 1},
	}
	failures := qualityGateFailures(summary, qualityGateConfig{
		MinPassRate:                                    ptr(0.75),
		MinCompletionRate:                              ptr(0.75),
		MinExpectationCapabilityPassRate:               ptr(0.75),
		MinEachExpectationCapabilityPassRate:           ptr(0.75),
		MinExpectationDomainPassRate:                   ptr(0.75),
		MinEachExpectationDomainPassRate:               ptr(0.75),
		MinMemoryUpdateRate:                            ptr(0.75),
		MinLoopProtocolFeedRate:                        ptr(0.75),
		MinLoopProtocolCalibrationRequestRate:          ptr(0.75),
		MinLoopProtocolCalibrationRate:                 ptr(0.75),
		MinRuntimeSurfaceRate:                          ptr(0.75),
		MinTraceEventRate:                              ptr(0.75),
		MinSourceNetworkRate:                           ptr(0.5),
		MinSourceAccessVerifiedRate:                    ptr(0.9),
		MinSessionSearchContextHitRate:                 ptr(0.75),
		MinSessionSearchMatchedTermsPerCall:            ptr(2),
		MinToolRepairSuccessRate:                       ptr(0.9),
		MinVerifierPassRate:                            ptr(0.75),
		MaxFocusedTaskErrorRate:                        ptr(0.25),
		MaxForcedNoToolsRate:                           ptr(0.1),
		MaxLoopGuardInterventionRate:                   ptr(0.3),
		MaxPlanErrorRate:                               ptr(0.25),
		MaxMemorySearchMissRate:                        ptr(0.25),
		MaxSourceDiscoveryOnlyRate:                     ptr(0.1),
		MaxSourceDynamicPartialRate:                    ptr(0.1),
		MaxSubagentErrorRate:                           ptr(0.25),
		MaxToolErrorRate:                               ptr(0.1),
		MaxToolContextTruncationRate:                   ptr(0.5),
		MaxToolResultTruncationRate:                    ptr(0.4),
		MaxAvgRuntimeErrors:                            ptr(1.0),
		MaxAvgContextCompactions:                       ptr(0.25),
		MaxAvgReactiveCompactions:                      ptr(0.25),
		MaxAvgContextRemovedMessages:                   ptr(12),
		MaxAvgContextSummaryBytes:                      ptr(512),
		MaxAvgContextSummaryMissing:                    ptr(0.25),
		MaxAvgContextSummaryEmpty:                      ptr(0.25),
		MaxAvgContextInjections:                        ptr(2),
		MaxAvgContextInjectionBytes:                    ptr(4000),
		MaxAvgContextInjectionEstimatedTokens:          ptr(1000),
		MaxAvgToolCalls:                                ptr(2),
		MaxAvgDurationMS:                               ptr(1000),
		MaxAvgTotalTokens:                              ptr(40),
		MaxDebugBriefTagRates:                          map[string]float64{"browser_scroll:stuck_without_network": 0, "source_dynamic_without_network": 0, "verifier:not_run": 0},
		MinExpectationDomainSourceAccessVerifiedRates:  map[string]float64{"bittensor": 0.75, "web_evidence": 0.80},
		MaxExpectationDomainAvgTotalTokens:             map[string]float64{"bittensor": 700, "market": 250},
		MaxExpectationDomainAvgToolCalls:               map[string]float64{"bittensor": 3},
		MaxExpectationDomainAvgRuntimeErrors:           map[string]float64{"bittensor": 2},
		MaxExpectationDomainToolErrorRates:             map[string]float64{"bittensor": 0.25},
		MaxExpectationDomainLoopGuardInterventionRates: map[string]float64{"bittensor": 0.25},
		RequiredExpectationCapabilities:                []string{"browser", "delegated_source_evidence"},
		RequiredExpectationDomains:                     []string{"code_pr", "market"},
	})
	got := strings.Join(failures, "\n")
	for _, want := range []string{
		"avg_context_compactions 0.500 > max 0.250",
		"avg_context_removed_messages 16.000 > max 12.000",
		"avg_context_summary_bytes 1024.000 > max 512.000",
		"avg_context_summary_empty 0.500 > max 0.250",
		"avg_context_summary_missing 0.500 > max 0.250",
		"avg_context_injections 2.500 > max 2.000",
		"avg_context_injection_bytes 4500.000 > max 4000.000",
		"avg_context_injection_estimated_tokens 1125.000 > max 1000.000",
		"avg_duration_ms 1250.000 > max 1000.000",
		"avg_reactive_context_compactions 0.500 > max 0.250",
		"avg_runtime_errors 1.500 > max 1.000",
		"avg_tool_calls 2.500 > max 2.000",
		"avg_total_tokens 55.000 > max 40.000",
		"completion_rate 0.500 < min 0.750",
		"debug_brief_tag_rate[browser_scroll:stuck_without_network] 0.500 > max 0.000",
		"debug_brief_tag_rate[source_dynamic_without_network] 0.500 > max 0.000",
		"debug_brief_tag_rate[verifier:not_run] 0.500 > max 0.000",
		"expectation_capability_pass_rate[browser] 0.500 < min 0.750",
		"expectation_capability_pass_rate[web] 0.000 < min 0.750",
		"expectation_capability_pass_rate 0.500 < min 0.750",
		"expectation_capability[delegated_source_evidence] unavailable, want >= 1 scenario",
		"expectation_domain_pass_rate[bittensor] 0.000 < min 0.750",
		"expectation_domain_pass_rate[market] 0.500 < min 0.750",
		"expectation_domain_pass_rate 0.333 < min 0.750",
		"expectation_domain[code_pr] unavailable, want >= 1 scenario",
		"expectation_domain_avg_runtime_errors[bittensor] 3.000 > max 2.000",
		"expectation_domain_avg_tool_calls[bittensor] 4.000 > max 3.000",
		"expectation_domain_avg_total_tokens[bittensor] 800.000 > max 700.000",
		"expectation_domain_avg_total_tokens[market] 300.000 > max 250.000",
		"expectation_domain_loop_guard_intervention_rate[bittensor] 0.500 > max 0.250",
		"expectation_domain_source_access_verified_rate[bittensor] 0.500 < min 0.750",
		"expectation_domain_source_access_verified_rate[web_evidence] unavailable, want >= 0.800",
		"expectation_domain_tool_error_rate[bittensor] 0.500 > max 0.250",
		"focused_task_error_rate 0.500 > max 0.250",
		"forced_no_tools_rate 0.200 > max 0.100",
		"loop_guard_intervention_rate 0.400 > max 0.300",
		"loop_protocol_feed_rate 0.500 < min 0.750",
		"loop_protocol_calibration_request_rate 0.500 < min 0.750",
		"loop_protocol_calibration_rate 0.500 < min 0.750",
		"memory_update_rate 0.500 < min 0.750",
		"memory_search_miss_rate 0.500 > max 0.250",
		"pass_rate 0.500 < min 0.750",
		"plan_error_rate 0.500 > max 0.250",
		"runtime_surface_rate 0.500 < min 0.750",
		"trace_event_rate 0.500 < min 0.750",
		"session_search_context_hit_rate 0.500 < min 0.750",
		"session_search_matched_terms_per_call 1.000 < min 2.000",
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

func TestDeferredCleanupKeepsPassingWorkspacesWhenGatesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "affenteval-timeline.md"), []byte("trace"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	results := []agenteval.BatchResult{{
		OK:        true,
		Workspace: dir,
		ToolCalls: 5,
	}}

	summary := summarizeBatchResults(results)
	failures := qualityGateFailures(summary, qualityGateConfig{MaxAvgToolCalls: float64Ptr(1)})
	if len(failures) == 0 {
		t.Fatal("quality gate should fail")
	}
	if len(failures) == 0 {
		cleanupPassingBatchResults(results)
	}

	if results[0].WorkspaceRemoved {
		t.Fatal("passing workspace should remain when the batch gate fails")
	}
	if _, err := os.Stat(filepath.Join(dir, "affenteval-timeline.md")); err != nil {
		t.Fatalf("retained workspace marker: %v", err)
	}
}

func TestDeferredCleanupRemovesPassingWorkspacesWhenGatesPass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "affenteval-timeline.md"), []byte("trace"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	results := []agenteval.BatchResult{{
		OK:        true,
		Workspace: dir,
		ToolCalls: 1,
	}}

	summary := summarizeBatchResults(results)
	failures := qualityGateFailures(summary, qualityGateConfig{MaxAvgToolCalls: float64Ptr(2)})
	if len(failures) != 0 {
		t.Fatalf("quality gate should pass: %#v", failures)
	}
	if len(failures) == 0 {
		cleanupPassingBatchResults(results)
	}

	if !results[0].WorkspaceRemoved || results[0].CleanupError != "" {
		t.Fatalf("passing workspace cleanup result = removed:%v err:%q", results[0].WorkspaceRemoved, results[0].CleanupError)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("passing workspace should be removed after gates pass, stat err=%v", err)
	}
}

func TestCleanupPassingBatchResults(t *testing.T) {
	passDir := t.TempDir()
	failDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(passDir, "marker.txt"), []byte("pass"), 0o644); err != nil {
		t.Fatalf("write pass marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(failDir, "marker.txt"), []byte("fail"), 0o644); err != nil {
		t.Fatalf("write fail marker: %v", err)
	}
	results := []agenteval.BatchResult{
		{OK: true, Workspace: passDir},
		{OK: false, Workspace: failDir},
		{OK: true, WorkspaceRemoved: true, Workspace: filepath.Join(t.TempDir(), "already-removed")},
		{OK: true, Workspace: " "},
	}

	cleanupPassingBatchResults(results)

	if !results[0].WorkspaceRemoved || results[0].CleanupError != "" {
		t.Fatalf("passing workspace cleanup result = removed:%v err:%q", results[0].WorkspaceRemoved, results[0].CleanupError)
	}
	if _, err := os.Stat(passDir); !os.IsNotExist(err) {
		t.Fatalf("passing workspace should be removed, stat err=%v", err)
	}
	if results[1].WorkspaceRemoved {
		t.Fatal("failed workspace should not be removed")
	}
	if _, err := os.Stat(filepath.Join(failDir, "marker.txt")); err != nil {
		t.Fatalf("failed workspace marker should remain: %v", err)
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
	if gates.MinMemoryUpdateRate == nil || *gates.MinMemoryUpdateRate != 0.10 {
		t.Fatalf("longrun min memory update rate = %#v, want 0.10", gates.MinMemoryUpdateRate)
	}
	if gates.MinLoopProtocolFeedRate == nil || *gates.MinLoopProtocolFeedRate != 0.05 {
		t.Fatalf("longrun min loop protocol feed rate = %#v, want 0.05", gates.MinLoopProtocolFeedRate)
	}
	if gates.MinLoopProtocolCalibrationRequestRate == nil || *gates.MinLoopProtocolCalibrationRequestRate != 0.05 {
		t.Fatalf("longrun min loop protocol calibration request rate = %#v, want 0.05", gates.MinLoopProtocolCalibrationRequestRate)
	}
	if gates.MinLoopProtocolCalibrationRate == nil || *gates.MinLoopProtocolCalibrationRate != 0.05 {
		t.Fatalf("longrun min loop protocol calibration rate = %#v, want 0.05", gates.MinLoopProtocolCalibrationRate)
	}
	if gates.MinTraceEventRate == nil || *gates.MinTraceEventRate != 0.90 {
		t.Fatalf("longrun min trace event rate = %#v, want 0.90", gates.MinTraceEventRate)
	}
	if gates.MinSessionSearchContextHitRate == nil || *gates.MinSessionSearchContextHitRate != 0.75 {
		t.Fatalf("longrun min session search context hit rate = %#v, want 0.75", gates.MinSessionSearchContextHitRate)
	}
	if gates.MinSessionSearchMatchedTermsPerCall == nil || *gates.MinSessionSearchMatchedTermsPerCall != 1.0 {
		t.Fatalf("longrun min session search matched terms per call = %#v, want 1.0", gates.MinSessionSearchMatchedTermsPerCall)
	}
	if gates.MaxToolErrorRate == nil || *gates.MaxToolErrorRate != 0.33 {
		t.Fatalf("explicit max tool error rate should win, got %#v", gates.MaxToolErrorRate)
	}
	if gates.MaxAvgTotalTokens == nil || *gates.MaxAvgTotalTokens != 120000 {
		t.Fatalf("longrun max avg tokens = %#v, want 120000", gates.MaxAvgTotalTokens)
	}
	if gates.MaxAvgToolCalls == nil || *gates.MaxAvgToolCalls != 14 {
		t.Fatalf("longrun max avg tool calls = %#v, want 14", gates.MaxAvgToolCalls)
	}
	if gates.MaxAvgDurationMS == nil || *gates.MaxAvgDurationMS != 180000 {
		t.Fatalf("longrun max avg duration ms = %#v, want 180000", gates.MaxAvgDurationMS)
	}
	if gates.MaxAvgContextRemovedMessages == nil || *gates.MaxAvgContextRemovedMessages != 120 {
		t.Fatalf("longrun max avg context removed messages = %#v, want 120", gates.MaxAvgContextRemovedMessages)
	}
	if gates.MaxAvgContextSummaryBytes == nil || *gates.MaxAvgContextSummaryBytes != 24000 {
		t.Fatalf("longrun max avg context summary bytes = %#v, want 24000", gates.MaxAvgContextSummaryBytes)
	}
	if gates.MaxAvgContextSummaryMissing == nil || *gates.MaxAvgContextSummaryMissing != 0 {
		t.Fatalf("longrun max avg context summary missing = %#v, want 0", gates.MaxAvgContextSummaryMissing)
	}
	if gates.MaxAvgContextSummaryEmpty == nil || *gates.MaxAvgContextSummaryEmpty != 0 {
		t.Fatalf("longrun max avg context summary empty = %#v, want 0", gates.MaxAvgContextSummaryEmpty)
	}
	if gates.MaxAvgContextInjections == nil || *gates.MaxAvgContextInjections != 8 {
		t.Fatalf("longrun max avg context injections = %#v, want 8", gates.MaxAvgContextInjections)
	}
	if gates.MaxAvgContextInjectionBytes == nil || *gates.MaxAvgContextInjectionBytes != 24000 {
		t.Fatalf("longrun max avg context injection bytes = %#v, want 24000", gates.MaxAvgContextInjectionBytes)
	}
	if gates.MaxAvgContextInjectionEstimatedTokens == nil || *gates.MaxAvgContextInjectionEstimatedTokens != 6000 {
		t.Fatalf("longrun max avg context injection estimated tokens = %#v, want 6000", gates.MaxAvgContextInjectionEstimatedTokens)
	}
	if gates.MinExpectationCapabilityPassRate == nil || *gates.MinExpectationCapabilityPassRate != 0.80 {
		t.Fatalf("longrun min expectation capability pass rate = %#v, want 0.80", gates.MinExpectationCapabilityPassRate)
	}
	if gates.MinEachExpectationCapabilityPassRate == nil || *gates.MinEachExpectationCapabilityPassRate != 0.50 {
		t.Fatalf("longrun min each expectation capability pass rate = %#v, want 0.50", gates.MinEachExpectationCapabilityPassRate)
	}
	if gates.MinExpectationDomainPassRate == nil || *gates.MinExpectationDomainPassRate != 0.80 {
		t.Fatalf("longrun min expectation domain pass rate = %#v, want 0.80", gates.MinExpectationDomainPassRate)
	}
	if gates.MinEachExpectationDomainPassRate == nil || *gates.MinEachExpectationDomainPassRate != 0.50 {
		t.Fatalf("longrun min each expectation domain pass rate = %#v, want 0.50", gates.MinEachExpectationDomainPassRate)
	}
	if !reflect.DeepEqual(gates.RequiredExpectationCapabilities, []string{"longrun_recovery", "loop_protocol", "session_search"}) {
		t.Fatalf("longrun required expectation capabilities = %#v", gates.RequiredExpectationCapabilities)
	}
	if !reflect.DeepEqual(gates.RequiredExpectationDomains, []string{"bittensor", "code_pr", "longrun_recovery", "market"}) {
		t.Fatalf("longrun required expectation domains = %#v", gates.RequiredExpectationDomains)
	}
	if gates.MinSourceAccessVerifiedRate != nil && *gates.MinSourceAccessVerifiedRate >= 0 {
		t.Fatalf("longrun profile should not require source evidence for non-web suites: %#v", gates.MinSourceAccessVerifiedRate)
	}
	if gates.MaxMemorySearchMissRate != nil && *gates.MaxMemorySearchMissRate >= 0 {
		t.Fatalf("longrun profile should not gate memory search miss rate by default: %#v", gates.MaxMemorySearchMissRate)
	}
	if gates.MaxDebugBriefTagRates["truncation:missing_artifact"] != 0 ||
		gates.MaxDebugBriefTagRates["empty_recall:no_recent_sessions"] != 0 ||
		gates.MaxDebugBriefTagRates["loop_protocol:fixture"] != 0 ||
		gates.MaxDebugBriefTagRates["loop_guard:forced_no_tools"] != 0 ||
		gates.MaxDebugBriefTagRates["recall:memory_no_topic_anchors"] != 0 ||
		gates.MaxDebugBriefTagRates["recall:no_context"] != 0 ||
		gates.MaxDebugBriefTagRates["recall:no_matched_terms"] != 0 ||
		gates.MaxDebugBriefTagRates["recall:weak_context"] != 0 ||
		gates.MaxDebugBriefTagRates["recall:weak_matched_terms"] != 0 ||
		gates.MaxDebugBriefTagRates["tool_repair:failed"] != 0 ||
		gates.MaxDebugBriefTagRates["verifier:failed"] != 0 ||
		gates.MaxDebugBriefTagRates["verifier:not_run"] != 0 ||
		gates.MaxDebugBriefTagRates["verifier:abnormal"] != 0 {
		t.Fatalf("longrun debug brief tag gates = %#v, want loop, recall, repair, verifier, and truncation artifact gates", gates.MaxDebugBriefTagRates)
	}

	webGates := qualityGateConfig{MinSourceAccessVerifiedRate: float64Ptr(-1)}
	if err := applyQualityGateProfile(&webGates, "web-evidence", nil); err != nil {
		t.Fatalf("apply web-evidence profile: %v", err)
	}
	if webGates.MinSourceAccessVerifiedRate == nil || *webGates.MinSourceAccessVerifiedRate != 0.90 ||
		webGates.MinSourceNetworkRate == nil || *webGates.MinSourceNetworkRate != 0.50 ||
		webGates.MinTraceEventRate == nil || *webGates.MinTraceEventRate != 0.90 ||
		webGates.MinExpectationCapabilityPassRate == nil || *webGates.MinExpectationCapabilityPassRate != 0.80 ||
		webGates.MinEachExpectationCapabilityPassRate == nil || *webGates.MinEachExpectationCapabilityPassRate != 0.50 ||
		webGates.MinExpectationDomainPassRate == nil || *webGates.MinExpectationDomainPassRate != 0.80 ||
		webGates.MinEachExpectationDomainPassRate == nil || *webGates.MinEachExpectationDomainPassRate != 0.50 ||
		webGates.MaxFocusedTaskErrorRate == nil || *webGates.MaxFocusedTaskErrorRate != 0.10 ||
		webGates.MaxSubagentErrorRate == nil || *webGates.MaxSubagentErrorRate != 0.10 ||
		webGates.MaxAvgContextRemovedMessages == nil || *webGates.MaxAvgContextRemovedMessages != 80 ||
		webGates.MaxAvgContextSummaryBytes == nil || *webGates.MaxAvgContextSummaryBytes != 20000 ||
		webGates.MaxAvgContextSummaryMissing == nil || *webGates.MaxAvgContextSummaryMissing != 0 ||
		webGates.MaxAvgContextSummaryEmpty == nil || *webGates.MaxAvgContextSummaryEmpty != 0 ||
		webGates.MaxAvgContextInjections == nil || *webGates.MaxAvgContextInjections != 6 ||
		webGates.MaxAvgContextInjectionBytes == nil || *webGates.MaxAvgContextInjectionBytes != 18000 ||
		webGates.MaxAvgContextInjectionEstimatedTokens == nil || *webGates.MaxAvgContextInjectionEstimatedTokens != 4500 ||
		webGates.MaxAvgToolCalls == nil || *webGates.MaxAvgToolCalls != 18 ||
		webGates.MaxAvgDurationMS == nil || *webGates.MaxAvgDurationMS != 240000 ||
		(webGates.MaxMemorySearchMissRate != nil && *webGates.MaxMemorySearchMissRate >= 0) ||
		!reflect.DeepEqual(webGates.MinExpectationDomainSourceAccessVerifiedRates, map[string]float64{"web_evidence": 0.90}) ||
		!reflect.DeepEqual(webGates.MaxExpectationDomainAvgTotalTokens, map[string]float64{"web_evidence": 120000}) ||
		!reflect.DeepEqual(webGates.MaxExpectationDomainAvgToolCalls, map[string]float64{"web_evidence": 18}) ||
		!reflect.DeepEqual(webGates.MaxExpectationDomainAvgRuntimeErrors, map[string]float64{"web_evidence": 0.20}) ||
		!reflect.DeepEqual(webGates.MaxExpectationDomainToolErrorRates, map[string]float64{"web_evidence": 0.10}) ||
		!reflect.DeepEqual(webGates.MaxExpectationDomainLoopGuardInterventionRates, map[string]float64{"web_evidence": 0.25}) ||
		webGates.MaxDebugBriefTagRates["browser_network:unread_refs"] != 0 ||
		webGates.MaxDebugBriefTagRates["browser_scroll:stuck_without_network"] != 0 ||
		webGates.MaxSourceDynamicPartialRate == nil || *webGates.MaxSourceDynamicPartialRate != 0.20 ||
		webGates.MaxDebugBriefTagRates["source_dynamic_without_decision"] != 0 ||
		webGates.MaxDebugBriefTagRates["source_dynamic_without_network"] != 0 ||
		webGates.MaxDebugBriefTagRates["source_network:missing_response_diagnostics"] != 0 ||
		webGates.MaxDebugBriefTagRates["source_network:partial_read"] != 0 ||
		webGates.MaxDebugBriefTagRates["source_unverified_all"] != 0 ||
		webGates.MaxDebugBriefTagRates["source_discovery_only_all"] != 0 ||
		webGates.MaxDebugBriefTagRates["research_checkpoint:no_external_evidence"] != 0 ||
		webGates.MaxDebugBriefTagRates["truncation:missing_artifact"] != 0 ||
		!reflect.DeepEqual(webGates.RequiredExpectationCapabilities, []string{"browser", "delegated_source_evidence", "source_access", "web"}) ||
		!reflect.DeepEqual(webGates.RequiredExpectationDomains, []string{"web_evidence"}) {
		t.Fatalf("web-evidence gates not applied: %+v", webGates)
	}
	if err := applyQualityGateProfile(&qualityGateConfig{}, "unknown", nil); err == nil || !strings.Contains(err.Error(), "--quality-profile") {
		t.Fatalf("unknown profile err = %v", err)
	}

	overrideGates := qualityGateConfig{
		MaxDebugBriefTagRates: map[string]float64{
			"source_dynamic_without_network": -1,
			"recall:no_context":              0.25,
		},
		MinExpectationDomainSourceAccessVerifiedRates: map[string]float64{"web_evidence": 0.75},
		RequiredExpectationCapabilities:               []string{"delegated_source_evidence", "web"},
		RequiredExpectationDomains:                    []string{"bittensor"},
	}
	if err := applyQualityGateProfile(&overrideGates, "web-evidence", func(name string) bool {
		return name == "max-debug-brief-tag-rate" ||
			name == "min-expectation-domain-source-access-verified-rate" ||
			name == "require-expectation-capability" ||
			name == "require-expectation-domain"
	}); err != nil {
		t.Fatalf("apply web-evidence profile with debug tag overrides: %v", err)
	}
	if overrideGates.MaxDebugBriefTagRates["source_dynamic_without_network"] != -1 ||
		overrideGates.MaxDebugBriefTagRates["browser_network:unread_refs"] != 0 ||
		overrideGates.MaxDebugBriefTagRates["browser_scroll:stuck_without_network"] != 0 ||
		overrideGates.MaxDebugBriefTagRates["source_unverified_all"] != 0 ||
		overrideGates.MaxDebugBriefTagRates["recall:no_context"] != 0.25 {
		t.Fatalf("debug brief tag gates not merged: %+v", overrideGates.MaxDebugBriefTagRates)
	}
	if !reflect.DeepEqual(overrideGates.RequiredExpectationCapabilities, []string{"browser", "delegated_source_evidence", "source_access", "web"}) {
		t.Fatalf("required expectation capabilities not merged: %#v", overrideGates.RequiredExpectationCapabilities)
	}
	if !reflect.DeepEqual(overrideGates.RequiredExpectationDomains, []string{"bittensor", "web_evidence"}) {
		t.Fatalf("required expectation domains not merged: %#v", overrideGates.RequiredExpectationDomains)
	}
	if !reflect.DeepEqual(overrideGates.MinExpectationDomainSourceAccessVerifiedRates, map[string]float64{"web_evidence": 0.75}) {
		t.Fatalf("domain source access gates not overridden: %#v", overrideGates.MinExpectationDomainSourceAccessVerifiedRates)
	}
	if err := validateQualityGateConfig(overrideGates); err != nil {
		t.Fatalf("validate debug brief tag gate override: %v", err)
	}
}

func TestBatchResultExpectationCapabilityOutcome(t *testing.T) {
	res := agenteval.BatchResult{
		OK: false,
		Expectations: &agenteval.DebugScenarioExpectations{
			RequiredTools: []string{"browser_network_read", "memory"},
			ExecutePlan:   true,
		},
	}
	names := batchResultExpectationCapabilityNames(res)
	wantNames := []string{"browser", "memory", "plan", "source_access"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("capability names = %#v, want %#v", names, wantNames)
	}
	if got := batchResultExpectationCapabilityOutcome(res, names); got != "failed" {
		t.Fatalf("outcome = %q, want failed", got)
	}
	if got := batchResultExpectationCapabilityFailedNames(res, names); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("failed names = %#v, want %#v", got, wantNames)
	}
	if got := batchResultExpectationCapabilityPassedNames(res, names); got != nil {
		t.Fatalf("failed result should not report passed names: %#v", got)
	}
	res.OK = true
	if got := batchResultExpectationCapabilityOutcome(res, names); got != "passed" {
		t.Fatalf("outcome = %q, want passed", got)
	}
	if got := batchResultExpectationCapabilityPassedNames(res, names); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("passed names = %#v, want %#v", got, wantNames)
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
		LoopGuardExamples: []agenteval.LoopGuardExample{{
			Kind:              "loop_guard_repeated_failed_input",
			Category:          "loop_guard",
			ToolIndex:         1,
			CallID:            "guard-print-1",
			Tool:              "web_fetch",
			ArgsSummary:       `url="https://example.com"`,
			GuardSummary:      "repeated failed input",
			SuggestedNextStep: "stop retrying this URL",
			ResultSummary:     "repeated failed input | Next: stop retrying this URL",
			ExitCode:          1,
		}},
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			ToolIndex:     2,
			CallID:        "source-print-1",
			Tool:          "browser_network_read",
			Status:        "network",
			URL:           "https://metrics.example/api.json",
			RequestedURL:  "https://metrics.example/dashboard",
			SourceMethod:  "network_xhr_fetch",
			HTTPStatus:    "200",
			ContentType:   "application/json",
			JSONPath:      "$.price",
			ResultPreview: `JSON_PATH: $.price "12.34"`,
		}},
		BrowserNetworkExamples: []agenteval.BrowserNetworkSearchExample{{
			ToolIndex:         2,
			CallID:            "browser-network-print-1",
			CurrentPageURL:    "https://metrics.example/dashboard",
			Query:             "market_cap",
			Status:            "no_matches",
			EvidenceStatus:    "refs_only_not_citable; read_required=true",
			NotCitable:        true,
			SuggestedNextStep: "adjust query or read available network refs before citing values",
		}},
		MemoryUpdateExamples: []agenteval.MemoryUpdateExample{{
			ToolIndex:       3,
			CallID:          "memory-print-1",
			Action:          "replace",
			Target:          "memory",
			Topic:           "markets",
			Location:        "memory:markets",
			PreviousPreview: "old dashboard rule",
			NextPreview:     "prefer browser_network_read evidence",
		}},
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
		LoopProtocolFeeds: agenteval.LoopProtocolFeedStats{
			Count:  2,
			ByMode: map[string]int{"digest": 1, "full": 1},
			Latest: agenteval.LoopProtocolFeed{LoopID: "longrun", Status: "running", Mode: "digest", FeedNumber: 2, ProtocolFeeds: 2, ProtocolPath: ".affent/loops/longrun/LOOP.md"},
			Examples: []agenteval.LoopProtocolFeed{
				{LoopID: "longrun", Status: "running", Mode: "full", FeedNumber: 1, ProtocolFeeds: 1, ProtocolPath: ".affent/loops/longrun/LOOP.md", CurrentSituation: "current risk: dashboard values need network refs", PlanLabel: "plan:1/3:active", PlanCurrentStepIndex: 2, PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "verify browser evidence", LastTurnID: "turn-prev", LastTurnEndReason: "max_turns", LastTurnMemorySearchCalls: 3, LastTurnMemorySearchMisses: 2, LastTurnSessionSearchCalls: 1},
			},
		},
		LoopProtocolCalibrationRequests: agenteval.LoopProtocolCalibrationStats{
			Count: 1,
			Examples: []agenteval.LoopProtocolCalibration{{
				LoopID:                  "longrun",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				ProtocolPath:            ".affent/loops/longrun/LOOP.md",
				EventSeq:                4,
			}},
		},
		LoopProtocolCalibrations: agenteval.LoopProtocolCalibrationStats{
			Count: 1,
			Examples: []agenteval.LoopProtocolCalibration{{
				LoopID:                  "longrun",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				CalibrationAnswers:      1,
				LastCalibrationAnswer:   "Pause when browser evidence is missing.",
				ProtocolPath:            ".affent/loops/longrun/LOOP.md",
				EventSeq:                5,
			}},
		},
		ContextCompactions: agenteval.ContextCompactionStats{
			Count:           2,
			Reactive:        1,
			Proactive:       1,
			RemovedMessages: 64,
			SummaryBytes:    4096,
			Examples: []agenteval.ContextCompaction{{
				TurnID:              "turn-compact-print",
				BeforeMessages:      72,
				AfterMessages:       24,
				RemovedMessages:     48,
				Reactive:            true,
				Reason:              "context_overflow",
				SummaryPresent:      true,
				SummaryPresentKnown: true,
				SummaryBytes:        2048,
				SummaryPreview:      "USER_CONTEXT: keep browser evidence and recovery anchors.",
				LoopProtocolAnchor:  "LOOP_PROTOCOL: active path=.affent/loops/longrun/LOOP.md mode=full",
			}},
		},
		ContextInjections: agenteval.ContextInjectionStats{
			Count:           1,
			BySource:        map[string]int{"account_access": 1},
			Bytes:           1200,
			EstimatedTokens: 300,
			Examples: []agenteval.ContextInjection{{
				TurnID:          "turn-1",
				Source:          "account_access",
				Title:           "Account access context injected",
				Summary:         "Account-level environment and SSH access hints were made available.",
				Preview:         "Configured environment variables: GITHUB_TOKEN",
				Bytes:           1200,
				EstimatedTokens: 300,
			}},
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:       1,
			ArgsOmittedBytes:    512,
			ResultsTruncated:    1,
			ResultsOmittedBytes: 4096,
			ResultArtifacts:     1,
			ContextTruncated:    3,
			ContextOmittedBytes: 9216,
			ContextArtifacts:    1,
		},
		ToolTruncationExamples: []agenteval.ToolTruncationExample{{
			ToolIndex:              1,
			CallID:                 "trunc-print-1",
			Tool:                   "web_fetch",
			ArgsTruncated:          true,
			ArgsBytes:              70000,
			ArgsOmittedBytes:       512,
			ArgsCapBytes:           65536,
			ResultTruncated:        true,
			ResultSummary:          "large web_fetch output preview",
			ResultBytes:            300000,
			ResultOmittedBytes:     4096,
			ResultCapBytes:         262144,
			ContextBytes:           4096,
			ContextOmittedBytes:    9216,
			ContextEstimatedTokens: 1024,
			ResultArtifactPath:     ".affent/artifacts/tool-results/000001-trunc-print-1.txt",
		}},
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
			FocusedTaskCalls:                2,
			FocusedTaskByType:               map[string]int{"explore": 1, "verify": 1},
			FocusedTaskSourceFindingsByType: map[string]int{"explore": 2},
			FocusedTaskErrors:               1,
			SubagentCalls:                   1,
			SubagentByMode:                  map[string]int{"review": 1},
			SubagentSourceEvidenceByMode:    map[string]int{"review": 3},
			SubagentErrors:                  1,
		},
		Plan: agenteval.PlanStats{
			Calls:    3,
			ByAction: map[string]int{"set": 1, "update": 2},
			Errors:   1,
		},
		PlanExamples: []agenteval.PlanExample{{
			ToolIndex:         3,
			CallID:            "plan-print-1",
			Action:            "update",
			Index:             2,
			Status:            "completed",
			StepText:          "verify browser evidence",
			Evidence:          []string{"go test ./cmd/affenteval"},
			TotalSteps:        3,
			CompletedSteps:    2,
			CurrentStepIndex:  3,
			CurrentStepStatus: "pending",
		}},
		Usage: agenteval.Usage{InputTokens: 100, OutputTokens: 25},
	})
	got := out.String()
	for _, want := range []string{
		"PASS sample (1.234s)",
		"workspace: /tmp/ws (removed)",
		"trace: /tmp/ws/trace.jsonl",
		"command: go run ./cmd/affentctl run --trace /tmp/ws/trace.jsonl",
		"metrics: tools=3 errors=2 repaired=1 canonicalized=1 loop_guard=2 forced_no_tools=1 tool_ms=45 tokens=100/25 trunc=args:1,results:1,artifacts:1,ctx_artifacts:1,missing_artifacts:0 omitted=512/4096 ctx_trunc=3,omitted=9216,artifacts=1,missing_artifacts=0 tool_failure_kinds=invalid_args:1 runtime_error_kinds=llm_timeout:1 loop_decisions=1 loop_decision_kinds=evidence_quality:1 loop_decision_results=defer:1 loop_protocol_feeds=2 loop_protocol_feed_modes=digest:1,full:1 loop_protocol_calibration=requests:1,answers:1 compactions=2,reactive=1,removed=64,summary_bytes=4096,summary_missing=0,summary_empty=0 context_injections=1,bytes=1200,est_tokens=300 context_injection_sources=account_access:1 debug_brief=browser_network,browser_network:no_matches,context_compaction,context_compaction:reactive,context_injection,context_injection:account_access,delegation,delegation:focused_task,delegation:subagent,delegation_error,delegation_error:focused_task,delegation_error:subagent,loop_guard,loop_guard:forced_no_tools,plan,plan:set,plan:update,plan_error,runtime_error,runtime_error:llm_timeout,tool_failure,tool_failure:invalid_args,truncation,truncation:tool_context,verifier,verifier:output_truncated delegation=focused_tasks:2,subagents:1 delegation_errors=focused_tasks:1,subagents:1 focused_task_by_type=explore:1,verify:1 focused_task_sources=explore:2 subagent_by_mode=review:1 subagent_sources=review:3 plan=calls:3,errors:1 plan_by_action=set:1,update:2 end=completed",
		`verifier: pass exit=0 duration=80ms output=1200 truncated omitted=176 cap=1024 command="go test ./..."`,
		"tool_failure_hint[invalid_args]",
		"invalid arguments",
		`tool_failure_example[invalid_args]: tool=web_fetch args=url="https://example.com" exit=1 result=url is required | Next: retry with a full URL`,
		`loop_guard_example[loop_guard_repeated_failed_input]: category=loop_guard tool=web_fetch call_id=guard-print-1 args=url="https://example.com" exit=1 guard=repeated failed input next=stop retrying this URL result=repeated failed input | Next: stop retrying this URL`,
		"source_access_example: status=network tool=browser_network_read call_id=source-print-1 url=https://metrics.example/api.json requested=https://metrics.example/dashboard method=network_xhr_fetch http_status=200 content_type=application/json json_path=$.price preview=\"JSON_PATH: $.price \\\"12.34\\\"\"",
		`browser_network_example: status=no_matches call_id=browser-network-print-1 page=https://metrics.example/dashboard query="market_cap" evidence_status="refs_only_not_citable; read_required=true" not_citable=true next="adjust query or read available network refs before citing values"`,
		`memory_update_example: action=replace target=memory location=memory:markets call_id=memory-print-1 topic=markets previous="old dashboard rule" next="prefer browser_network_read evidence"`,
		"hint[llm_timeout]",
		"runtime_error_example[llm_timeout]: LLM llm_stream timed out after 4m0s",
		"loop_decision_example[evidence_quality]: decision=defer trigger=source_access_dynamic_partial confidence=high reason=dynamic widgets lacked text action=read browser network responses",
		`loop_protocol_feed_example: loop_id=longrun mode=full feed=1 path=.affent/loops/longrun/LOOP.md plan=plan:1/3:active current=2:in_progress step="verify browser evidence" situation="current risk: dashboard values need network refs" last_turn="id=turn-prev reason=max_turns memory_searches=3 memory_misses=2 session_search=1"`,
		`loop_protocol_calibration_request_example: loop_id=longrun status=draft questions=1 answers=0 path=.affent/loops/longrun/LOOP.md event_seq=4 question="What should pause this loop?"`,
		`loop_protocol_calibration_example: loop_id=longrun status=draft questions=1 answers=1 path=.affent/loops/longrun/LOOP.md event_seq=5 question="What should pause this loop?" answer="Pause when browser evidence is missing."`,
		`context_compaction_example: turn=turn-compact-print reactive=true messages=72->24 removed=48 summary_state=present summary_bytes=2048 reason=context_overflow loop_anchor="LOOP_PROTOCOL: active path=.affent/loops/longrun/LOOP.md mode=full" preview="USER_CONTEXT: keep browser evidence and recovery anchors."`,
		`context_injection_example: turn=turn-1 source=account_access bytes=1200 estimated_tokens=300 title="Account access context injected" summary="Account-level environment and SSH access hints were made available." preview="Configured environment variables: GITHUB_TOKEN"`,
		`plan_example: action=update index=2 status=completed progress=2/3 current=3:pending step="verify browser evidence" evidence=go test ./cmd/affenteval`,
		`tool_truncation_example: tool=web_fetch call_id=trunc-print-1 args=truncated:true,bytes:70000,omitted:512,cap:65536 result=truncated:true,bytes:300000,omitted:4096,cap:262144 summary="large web_fetch output preview" context=bytes:4096,omitted:9216,tokens:1024 artifact=.affent/artifacts/tool-results/000001-trunc-print-1.txt`,
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

func TestPrintBatchResultIncludesMemorySearchMissExamples(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario: "sample",
		OK:            true,
		MemorySearchMissExamples: []agenteval.MemorySearchMissExample{{
			ToolIndex:  1,
			CallID:     "memory-search-1",
			Target:     "memory",
			Topic:      "deploy",
			Query:      "helm deployment",
			Message:    "no entries matched. Next: retry with fewer keywords or search a specific topic.",
			TopicCount: 2,
			Topics:     []string{"deploy", "auth"},
		}},
	})

	got := out.String()
	if !strings.Contains(got, `memory_search_miss_example: call_id=memory-search-1 target=memory topic=deploy query="helm deployment" topic_count=2 topics=deploy,auth message="no entries matched. Next: retry with fewer keywords or search a specific topic."`) {
		t.Fatalf("output missing memory search miss example:\n%s", got)
	}
}

func TestPrintBatchResultIncludesBrowserScrollExamples(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario: "scroll",
		Workspace:     "/tmp/ws",
		TracePath:     "/tmp/ws/trace.jsonl",
		Duration:      10 * time.Millisecond,
		BrowserScrollExamples: []agenteval.BrowserScrollExample{{
			ToolIndex:         1,
			CallID:            "scroll-print-1",
			URL:               "https://taostats.io/subnets/120",
			Direction:         "down",
			BeforeY:           "1200",
			AfterY:            "1200",
			MaxY:              "1200",
			Movement:          "none",
			Boundary:          "bottom",
			Status:            "boundary",
			SuggestedNextStep: "use browser_network_read before citing hidden values",
			ResultPreview:     "SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
		}},
	})
	got := out.String()
	if !strings.Contains(got, `browser_scroll_example: status=boundary call_id=scroll-print-1 url=https://taostats.io/subnets/120 direction=down movement=none boundary=bottom y=1200->1200/1200 next="use browser_network_read before citing hidden values" preview="SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom"`) {
		t.Fatalf("output missing browser scroll example:\n%s", got)
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

func TestBatchSummaryAggregatesConversationRepairs(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		BatchScenario: "resume-a",
		ConversationRepairs: []sse.ConversationRepairedPayload{{
			SessionID:          "resume-session-a",
			MissingToolResults: 2,
			FailureKind:        "resume_missing_tool_result",
			Next:               "do not assume the tool succeeded; rerun only if essential",
		}},
	})
	summary.add(agenteval.BatchResult{
		BatchScenario: "resume-b",
		ConversationRepairs: []sse.ConversationRepairedPayload{{
			SessionID:            "resume-session-b",
			MissingToolResults:   1,
			DuplicateToolResults: 1,
			FailureKind:          "resume_missing_tool_result",
			Next:                 "continue from available context",
		}},
	})
	summary.add(agenteval.BatchResult{
		BatchScenario: "resume-c",
		ConversationRepairs: []sse.ConversationRepairedPayload{{
			SessionID:             "resume-session-c",
			MissingToolResults:    1,
			UnexpectedToolResults: 2,
			FailureKind:           "unknown_repair",
		}},
	})

	if summary.ConversationRepairs != 3 ||
		summary.ConversationRepairMissingToolResults != 4 ||
		summary.ConversationRepairDuplicateResults != 1 ||
		summary.ConversationRepairUnexpectedResults != 2 ||
		!reflect.DeepEqual(summary.ConversationRepairByKind, map[string]int{"resume_missing_tool_result": 2, "unknown_repair": 1}) {
		t.Fatalf("conversation repair summary = count:%d missing:%d duplicate:%d unexpected:%d kinds:%#v", summary.ConversationRepairs, summary.ConversationRepairMissingToolResults, summary.ConversationRepairDuplicateResults, summary.ConversationRepairUnexpectedResults, summary.ConversationRepairByKind)
	}
	if len(summary.ConversationRepairExamples) != 2 ||
		summary.ConversationRepairExamples[0].Scenario != "resume-a" ||
		summary.ConversationRepairExamples[1].Scenario != "resume-b" {
		t.Fatalf("ConversationRepairExamples = %#v", summary.ConversationRepairExamples)
	}

	var out bytes.Buffer
	printBatchSummary(&out, summary)
	got := out.String()
	for _, want := range []string{
		"conversation_repairs=3,missing_tool_results=4,duplicate_tool_results=1,unexpected_tool_results=2 conversation_repair_kinds=resume_missing_tool_result:2,unknown_repair:1",
		`conversation_repair_example: scenario=resume-a session=resume-session-a missing_tool_results=2 kind=resume_missing_tool_result next="do not assume the tool succeeded; rerun only if essential"`,
		`conversation_repair_example: scenario=resume-b session=resume-session-b missing_tool_results=1 duplicate_tool_results=1 kind=resume_missing_tool_result next="continue from available context"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary output missing %q:\n%s", want, got)
		}
	}

	var jsonl bytes.Buffer
	printBatchSummaryJSONL(&jsonl, evalJSONLMetadata{SchemaVersion: evalJSONLSchemaVersion, Executor: "local"}, summary, nil)
	var record batchSummaryRecord
	if err := json.Unmarshal(jsonl.Bytes(), &record); err != nil {
		t.Fatalf("decode summary jsonl: %v\n%s", err, jsonl.String())
	}
	if record.ConversationRepairs != 3 ||
		record.ConversationRepairMissingToolResults != 4 ||
		record.ConversationRepairDuplicateResults != 1 ||
		record.ConversationRepairUnexpectedResults != 2 ||
		!reflect.DeepEqual(record.ConversationRepairByKind, map[string]int{"resume_missing_tool_result": 2, "unknown_repair": 1}) ||
		len(record.ConversationRepairExamples) != 2 {
		t.Fatalf("summary jsonl conversation repairs = %#v", record)
	}
}

func TestBatchSummaryAggregatesRuntimeMetrics(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		BatchScenario:      "sample",
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
		Expectations: &agenteval.DebugScenarioExpectations{
			Suites:        []string{"long-run"},
			Domains:       []string{"market"},
			SessionID:     "memory-writer",
			EnableMemory:  true,
			VerifyCommand: "go test ./...",
			RequiredTools: []string{"read_file", "repo_search", "memory"},
			RequiredSourceAccess: []agenteval.DebugSourceAccessRequirement{
				{Status: "network", Tool: "browser_network_read", URLContains: "metrics.example/api.json"},
			},
			RequiredToolStatsAtLeast: map[string]int{
				"memory_updates": 1,
			},
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
			MemorySearchCalls:         2,
			MemorySearchMisses:        1,
			ToolContextTruncated:      1,
			ToolContextOmittedBytes:   1024,
		},
		Repair: agenteval.ToolRepairStats{
			Calls:          2,
			SucceededCalls: 2,
			Notes:          2,
			ByKind:         map[string]int{"tool_name": 1, "alias_rename": 1},
		},
		ToolRepairExamples: []agenteval.ToolRepairExample{{
			Scenario:      "sample",
			ToolIndex:     1,
			CallID:        "repair-1",
			Tool:          "read_file",
			OriginalTool:  "readFile",
			Canonicalized: true,
			ArgsRepaired:  true,
			RepairNotes:   []string{"canonicalized tool readFile to read_file", "renamed field file_path to path"},
			RepairKinds:   []string{"tool_name", "alias_rename"},
			Succeeded:     true,
		}},
		LoopGuardExamples: []agenteval.LoopGuardExample{{
			Scenario:          "sample",
			Kind:              "loop_guard_repeated_failed_input",
			Category:          "loop_guard",
			ToolIndex:         2,
			CallID:            "guard-1",
			Tool:              "web_fetch",
			ArgsSummary:       `url="https://loop.example"`,
			GuardSummary:      "repeated failed input",
			SuggestedNextStep: "use browser_network_read",
			ResultSummary:     "repeated failed input | Next: use browser_network_read",
			ExitCode:          1,
		}},
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
			ResultSummary:          "history fetch preview",
			ContextOmittedBytes:    1024,
			ContextEstimatedTokens: 256,
		}},
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			ToolIndex:     1,
			CallID:        "source-1",
			Tool:          "browser_network_read",
			Status:        "network",
			URL:           "https://metrics.example/api.json",
			SourceMethod:  "network_xhr_fetch",
			HTTPStatus:    "200",
			ContentType:   "application/json",
			JSONPath:      "$.price",
			ResultPreview: `JSON_PATH: $.price "12.34"`,
		}},
		BrowserScrollExamples: []agenteval.BrowserScrollExample{{
			ToolIndex:         2,
			CallID:            "browser-scroll-1",
			URL:               "https://metrics.example/dashboard",
			Direction:         "down",
			BeforeY:           "1200",
			AfterY:            "1200",
			MaxY:              "1200",
			Movement:          "none",
			Boundary:          "bottom",
			Status:            "boundary",
			SuggestedNextStep: "use browser_network_read before citing hidden values",
			ResultPreview:     "SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
		}},
		BrowserNetworkExamples: []agenteval.BrowserNetworkSearchExample{{
			ToolIndex:         2,
			CallID:            "browser-network-1",
			CurrentPageURL:    "https://metrics.example/dashboard",
			Query:             "price",
			Status:            "matches",
			EvidenceStatus:    "refs_only_not_citable; read_required=true",
			Refs:              []string{"n1"},
			Previews:          []string{`{"price":"12.34"}`},
			RequiresRead:      true,
			NotCitable:        true,
			SuggestedNextStep: "call browser_network_read before citing values",
		}},
		MemoryUpdateExamples: []agenteval.MemoryUpdateExample{{
			ToolIndex:   2,
			CallID:      "memory-1",
			Action:      "add",
			Target:      "memory",
			Topic:       "markets",
			Location:    "memory:markets",
			NextPreview: "Prefer browser_network_read evidence for dynamic dashboards.",
		}},
		MemorySearchMissExamples: []agenteval.MemorySearchMissExample{{
			ToolIndex:  2,
			CallID:     "memory-search-1",
			Target:     "memory",
			Topic:      "deploy",
			Query:      "helm deployment",
			Message:    "no entries matched. Next: search a specific topic from topics.",
			TopicCount: 2,
			Topics:     []string{"deploy", "auth"},
		}},
		SessionSearchExamples: []agenteval.SessionSearchExample{{
			ToolIndex:       2,
			CallID:          "search-1",
			Query:           "Alpha Coast",
			Total:           2,
			SessionID:       "market-alpha",
			TurnIdx:         4,
			MessageIdx:      8,
			ModTime:         "2026-05-27T12:00:00Z",
			MatchedTerms:    []string{"alpha", "coast"},
			ContextIncluded: true,
		}},
		Plan: agenteval.PlanStats{
			Calls:    1,
			ByAction: map[string]int{"set": 1},
		},
		PlanExamples: []agenteval.PlanExample{{
			ToolIndex:         2,
			CallID:            "plan-1",
			Action:            "update",
			Index:             2,
			Status:            "completed",
			StepText:          "verify browser evidence",
			Evidence:          []string{"go test ./cmd/affenteval"},
			TotalSteps:        3,
			CompletedSteps:    2,
			CurrentStepIndex:  3,
			CurrentStepStatus: "pending",
		}},
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
		BatchScenario:      "taostats-rendered",
		OK:                 false,
		Duration:           250 * time.Millisecond,
		ToolCalls:          3,
		TraceSchemaVersion: 1,
		TracePath:          "/tmp/affenteval/taostats-rendered/trace.jsonl",
		DebugManifestPath:  "/tmp/affenteval/taostats-rendered/affenteval-debug.json",
		TimelinePath:       "/tmp/affenteval/taostats-rendered/affenteval-timeline.md",
		TurnEndReason:      "max_turns",
		Expectations: &agenteval.DebugScenarioExpectations{
			Suites:      []string{"live-web"},
			Domains:     []string{"longrun_recovery", "web_evidence"},
			SessionID:   "history-reader",
			ExecutePlan: true,
			RequiredTools: []string{
				"web_fetch",
				"browser_network_read",
				"session_search",
				"run_task",
			},
			RequiredSourceAccess: []agenteval.DebugSourceAccessRequirement{
				{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io/api"},
			},
			RequiredFocusedTaskCounts:  map[string]int{"explore": 1},
			RequireNoPlanErrors:        true,
			RequiredContextCompactions: 1,
		},
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
		LoopProtocolFeeds: agenteval.LoopProtocolFeedStats{
			Count:  2,
			ByMode: map[string]int{"digest": 1, "full": 1},
			Latest: agenteval.LoopProtocolFeed{LoopID: "taostats-rendered", Status: "running", Mode: "digest", FeedNumber: 2, ProtocolFeeds: 2, ProtocolPath: ".affent/loops/taostats-rendered/LOOP.md"},
			Examples: []agenteval.LoopProtocolFeed{
				{LoopID: "taostats-rendered", Status: "running", Mode: "full", FeedNumber: 1, ProtocolFeeds: 1, ProtocolPath: ".affent/loops/taostats-rendered/LOOP.md"},
			},
		},
		LoopProtocolCalibrationRequests: agenteval.LoopProtocolCalibrationStats{
			Count: 1,
			Examples: []agenteval.LoopProtocolCalibration{{
				LoopID:                  "taostats-rendered",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				ProtocolPath:            ".affent/loops/taostats-rendered/LOOP.md",
				EventSeq:                2,
			}},
		},
		LoopProtocolCalibrations: agenteval.LoopProtocolCalibrationStats{
			Count: 1,
			Examples: []agenteval.LoopProtocolCalibration{{
				LoopID:                  "taostats-rendered",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				CalibrationAnswers:      1,
				LastCalibrationAnswer:   "Pause if network evidence cannot be read.",
				ProtocolPath:            ".affent/loops/taostats-rendered/LOOP.md",
				EventSeq:                3,
			}},
		},
		ContextCompactions: agenteval.ContextCompactionStats{
			Count:           1,
			Reactive:        1,
			RemovedMessages: 32,
			SummaryBytes:    2048,
			Examples: []agenteval.ContextCompaction{{
				TurnID:              "turn-summary",
				BeforeMessages:      70,
				AfterMessages:       22,
				RemovedMessages:     48,
				Reactive:            true,
				Reason:              "context_overflow",
				SummaryPresent:      true,
				SummaryPresentKnown: true,
				SummaryBytes:        2048,
				SummaryPreview:      "USER_CONTEXT: preserve the market evidence trail.",
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
			ContextTruncated:    2,
			ContextOmittedBytes: 4096,
			ContextArtifacts:    1,
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
	want := "SUMMARY scenarios=2 passed=1 failed=1 duration=350ms avg_duration_ms=175 tools=5 errors=1 repaired=3 canonicalized=2 loop_guard=3 forced_no_tools=1 tool_ms=50 trunc=args:1,results:2,artifacts:1,ctx_artifacts:1,missing_artifacts:1 omitted=128/2048 verifier=run:2,passed:1,failed:1,truncated:1,omitted:2048 tokens=90/20 ends=completed:1,max_turns:1,error:0,cancelled:0,unknown:0 failure_kinds=missing_command:1,turn_end:1 removed_workspaces=1 cleanup_errors=0"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("summary output missing %q:\n%s", want, out.String())
	}
	if !strings.Contains(out.String(), "ctx_trunc=3,omitted=5120,artifacts=1,missing_artifacts=0") {
		t.Fatalf("summary output missing context truncation rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "rates=pass:50.0%,completed:50.0%,memory_update:0.0%,memory_search_miss:50.0%,loop_protocol_feed:50.0%,loop_protocol_calibration_request:50.0%,loop_protocol_calibration:50.0%,runtime_surface:100.0%,tool_error:20.0%,focused_task_error:n/a,subagent_error:n/a,plan_error:33.3%,repair_success:80.0%,verifier_pass:50.0%,evidence_verified:75.0%,source_network:75.0%,source_discovery:0.0%,source_dynamic_partial:0.0% avg_tools=2.5 avg_tokens=45.0/10.0") {
		t.Fatalf("summary output missing normalized rates:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "context_pressure=avg_compactions:0.50,avg_reactive:0.50,avg_removed:16.0,avg_summary_bytes:1024,avg_summary_missing:0.00,avg_summary_empty:0.00,avg_injections:0.00,avg_injection_bytes:0,avg_injection_tokens:0,tool_ctx_trunc:60.0%") {
		t.Fatalf("summary output missing context pressure rates:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "source_access=results:4,verified:3,discovery:0,network:3,dynamic_partial:0") {
		t.Fatalf("summary output missing source access rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "loop_protocol_calibration=scenarios:1/1,requests:1,answers:1") {
		t.Fatalf("summary output missing loop calibration rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "debug_brief=browser_network:1,browser_network:refs:1,browser_scroll:1,browser_scroll:boundary:1,context_compaction:1,context_compaction:reactive:1,loop_guard:2,loop_guard:forced_no_tools:1,memory_search_miss:1,outcome:failed:1,plan:2,plan:set:1,plan:update:1,plan_error:1,recall:1,recall:context:1,recall:memory_topic_anchors:1,recall:weak_context:1,runtime_error:1,runtime_error:context_overflow:1,runtime_error:llm_timeout:1,source_access:2,source_network:2,source_unverified:1,tool_failure:1,tool_failure:invalid_args:1,tool_failure:timeout:1,tool_repair:2,tool_repair:alias_rename:2,tool_repair:failed:1,tool_repair:tool_name:1,tool_repair:type_coercion:1,truncation:2,truncation:missing_artifact:1,truncation:tool_context:2,turn_end:max_turns:1") {
		t.Fatalf("summary output missing debug brief tag rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `failure_example[turn_end]: scenario=taostats-rendered failure="turn ended with reason \"max_turns\" (expected completed)"`) ||
		!strings.Contains(out.String(), "trace=/tmp/affenteval/taostats-rendered/trace.jsonl") ||
		!strings.Contains(out.String(), "timeline=/tmp/affenteval/taostats-rendered/affenteval-timeline.md") {
		t.Fatalf("summary output missing grouped failure example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "debug_brief_example[verifier:failed]: scenario=taostats-rendered failure_kinds=missing_command:1,turn_end:1") ||
		!strings.Contains(out.String(), "debug_manifest=/tmp/affenteval/taostats-rendered/affenteval-debug.json") {
		t.Fatalf("summary output missing debug brief tag example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "expectations=scenarios:2 expectation_capabilities=browser:2,context_compaction:1,delegation:1,memory:1,plan:1,session:2,session_search:1,source_access:2,verifier:1,web:1,workspace:1 expectation_capability_pass=browser:1/2,context_compaction:0/1,delegation:0/1,memory:1/1,plan:0/1,session:1/2,session_search:0/1,source_access:1/2,verifier:1/1,web:0/1,workspace:1/1 expectation_capability_pass_rate=42.9% expectation_tools=browser_network_read:2,memory:1,read_file:1,repo_search:1,run_task:1,session_search:1,web_fetch:1 expectation_source_access=network:2 expectation_suites=live-web:1,long-run:1 expectation_domains=longrun_recovery:1,market:1,web_evidence:1 expectation_domain_pass=longrun_recovery:0/1,market:1/1,web_evidence:0/1 expectation_domain_pass_rate=33.3%") {
		t.Fatalf("summary output missing expectation rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "expectation_domain_failure[web_evidence]: scenario=taostats-rendered failure_kinds=missing_command:1,turn_end:1") {
		t.Fatalf("summary output missing expectation domain failure example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "expectation_capability_failure[browser]: scenario=taostats-rendered failure_kinds=missing_command:1,turn_end:1") ||
		!strings.Contains(out.String(), "trace=/tmp/affenteval/taostats-rendered/trace.jsonl") ||
		!strings.Contains(out.String(), "timeline=/tmp/affenteval/taostats-rendered/affenteval-timeline.md") {
		t.Fatalf("summary output missing expectation capability failure example:\n%s", out.String())
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
	if !strings.Contains(out.String(), "terms_per_call:2.00") {
		t.Fatalf("summary output missing session search matched terms per call:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `session_search_example: scenario=sample query="Alpha Coast" total=2 session=market-alpha turn=4 message=8 mod_time=2026-05-27T12:00:00Z terms=alpha,coast context=true`) {
		t.Fatalf("summary output missing session search example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runtime_surface=scenarios:2 runtime_capabilities=browser:2,web_fetch:2,web_search:1,workspace_partial:1 runtime_tools=browser_find:2,web_fetch:2,web_search:1") {
		t.Fatalf("summary output missing runtime surface rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "loop_decisions=1 loop_decision_kinds=evidence_quality:1 loop_decision_results=defer:1") {
		t.Fatalf("summary output missing loop decision rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "loop_protocol_feed_scenarios=1 loop_protocol_feeds=2 loop_protocol_feed_modes=digest:1,full:1") {
		t.Fatalf("summary output missing loop protocol feed rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "compactions=1,reactive=1,removed=32,summary_bytes=2048,summary_missing=0,summary_empty=0") {
		t.Fatalf("summary output missing context compaction rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `context_compaction_example: scenario=taostats-rendered turn=turn-summary reactive=true messages=70->22 removed=48 summary_state=present summary_bytes=2048 reason=context_overflow preview="USER_CONTEXT: preserve the market evidence trail."`) {
		t.Fatalf("summary output missing context compaction example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "trace_events=7 trace_event_types=message.delta:3,tool.request:2,tool.result:2") {
		t.Fatalf("summary output missing trace event rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "memory_search=calls:2,misses:1") {
		t.Fatalf("summary output missing memory search miss rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "trace_event_scenarios=1,rate=50.0%") {
		t.Fatalf("summary output missing trace event scenario rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "tool_failure_hint[invalid_args]") || !strings.Contains(out.String(), "tool_failure_hint[timeout]") {
		t.Fatalf("summary output missing tool failure hints:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hint[context_overflow]") || !strings.Contains(out.String(), "hint[llm_timeout]") {
		t.Fatalf("summary output missing runtime error hints:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `tool_failure_example[timeout]: scenario=taostats-rendered tool=web_fetch args=url="https://slow.example"`) {
		t.Fatalf("summary output missing tool failure example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `loop_guard_example[loop_guard_repeated_failed_input]: scenario=sample category=loop_guard tool=web_fetch call_id=guard-1 args=url="https://loop.example" exit=1 guard=repeated failed input next=use browser_network_read result=repeated failed input | Next: use browser_network_read`) {
		t.Fatalf("summary output missing loop guard example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "source_access_example: scenario=sample status=network tool=browser_network_read call_id=source-1 url=https://metrics.example/api.json method=network_xhr_fetch http_status=200 content_type=application/json json_path=$.price preview=\"JSON_PATH: $.price \\\"12.34\\\"\"") {
		t.Fatalf("summary output missing source access example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `browser_scroll_example: scenario=sample status=boundary call_id=browser-scroll-1 url=https://metrics.example/dashboard direction=down movement=none boundary=bottom y=1200->1200/1200 next="use browser_network_read before citing hidden values" preview="SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom"`) {
		t.Fatalf("summary output missing browser scroll example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `browser_network_example: scenario=sample status=matches call_id=browser-network-1 page=https://metrics.example/dashboard query="price" evidence_status="refs_only_not_citable; read_required=true" refs=n1 previews="{\"price\":\"12.34\"}" requires_read=true not_citable=true next="call browser_network_read before citing values"`) {
		t.Fatalf("summary output missing browser network example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `memory_update_example: scenario=sample action=add target=memory location=memory:markets call_id=memory-1 topic=markets next="Prefer browser_network_read evidence for dynamic dashboards."`) {
		t.Fatalf("summary output missing memory update example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `memory_search_miss_example: scenario=sample call_id=memory-search-1 target=memory topic=deploy query="helm deployment" topic_count=2 topics=deploy,auth message="no entries matched. Next: search a specific topic from topics."`) {
		t.Fatalf("summary output missing memory search miss example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runtime_error_example[llm_timeout]: scenario=taostats-rendered LLM llm_stream timed out after 4m0s") {
		t.Fatalf("summary output missing runtime error example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "loop_decision_example[evidence_quality]: scenario=taostats-rendered decision=defer trigger=source_access_dynamic_partial") {
		t.Fatalf("summary output missing loop decision example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "repair_calls=5,ok=4,failed=1") {
		t.Fatalf("summary output missing repair outcome rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `tool_repair_example: scenario=sample tool=read_file original=readFile call_id=repair-1 kinds=tool_name,alias_rename canonicalized=true args_repaired=true exit=0 note="canonicalized tool readFile to read_file"`) {
		t.Fatalf("summary output missing tool repair example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "plan=calls:3,errors:1 plan_by_action=set:1,update:2") {
		t.Fatalf("summary output missing plan rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `plan_example: scenario=sample action=update index=2 status=completed progress=2/3 current=3:pending step="verify browser evidence" evidence=go test ./cmd/affenteval`) {
		t.Fatalf("summary output missing plan example:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `tool_truncation_example: scenario=sample tool=web_fetch call_id=trunc-1 args=truncated:true,bytes:0,omitted:128,cap:0 summary="history fetch preview" context=bytes:0,omitted:1024,tokens:256`) {
		t.Fatalf("summary output missing tool truncation example:\n%s", out.String())
	}
	if summary.TraceSchemaVersions[1] != 2 {
		t.Fatalf("TraceSchemaVersions = %#v, want version 1 count 2", summary.TraceSchemaVersions)
	}
	if summary.TraceEventScenarios != 1 {
		t.Fatalf("TraceEventScenarios = %d, want 1", summary.TraceEventScenarios)
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
	if len(summary.ToolRepairExamples) != 1 ||
		summary.ToolRepairExamples[0].CallID != "repair-1" ||
		summary.ToolRepairExamples[0].Scenario != "sample" {
		t.Fatalf("ToolRepairExamples = %#v", summary.ToolRepairExamples)
	}
	if len(summary.LoopGuardExamples) != 1 ||
		summary.LoopGuardExamples[0].CallID != "guard-1" ||
		summary.LoopGuardExamples[0].Scenario != "sample" {
		t.Fatalf("LoopGuardExamples = %#v", summary.LoopGuardExamples)
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
	if len(summary.SourceAccessExamples) != 1 ||
		summary.SourceAccessExamples[0].CallID != "source-1" ||
		summary.SourceAccessExamples[0].Scenario != "sample" {
		t.Fatalf("SourceAccessExamples = %#v", summary.SourceAccessExamples)
	}
	if len(summary.BrowserScrollExamples) != 1 ||
		summary.BrowserScrollExamples[0].CallID != "browser-scroll-1" ||
		summary.BrowserScrollExamples[0].Scenario != "sample" ||
		summary.BrowserScrollExamples[0].Status != "boundary" ||
		summary.BrowserScrollExamples[0].Movement != "none" {
		t.Fatalf("BrowserScrollExamples = %#v", summary.BrowserScrollExamples)
	}
	if len(summary.BrowserNetworkExamples) != 1 ||
		summary.BrowserNetworkExamples[0].CallID != "browser-network-1" ||
		summary.BrowserNetworkExamples[0].Scenario != "sample" ||
		!reflect.DeepEqual(summary.BrowserNetworkExamples[0].Refs, []string{"n1"}) ||
		!reflect.DeepEqual(summary.BrowserNetworkExamples[0].Previews, []string{`{"price":"12.34"}`}) {
		t.Fatalf("BrowserNetworkExamples = %#v", summary.BrowserNetworkExamples)
	}
	if len(summary.MemoryUpdateExamples) != 1 ||
		summary.MemoryUpdateExamples[0].CallID != "memory-1" ||
		summary.MemoryUpdateExamples[0].Scenario != "sample" {
		t.Fatalf("MemoryUpdateExamples = %#v", summary.MemoryUpdateExamples)
	}
	if len(summary.MemorySearchMissExamples) != 1 ||
		summary.MemorySearchMissExamples[0].CallID != "memory-search-1" ||
		summary.MemorySearchMissExamples[0].Scenario != "sample" {
		t.Fatalf("MemorySearchMissExamples = %#v", summary.MemorySearchMissExamples)
	}
	if len(summary.SessionSearchExamples) != 1 ||
		summary.SessionSearchExamples[0].CallID != "search-1" ||
		summary.SessionSearchExamples[0].SessionID != "market-alpha" ||
		summary.SessionSearchExamples[0].Scenario != "sample" {
		t.Fatalf("SessionSearchExamples = %#v", summary.SessionSearchExamples)
	}
	if len(summary.ToolTruncationExamples) != 1 ||
		summary.ToolTruncationExamples[0].CallID != "trunc-1" ||
		summary.ToolTruncationExamples[0].Scenario != "sample" {
		t.Fatalf("ToolTruncationExamples = %#v", summary.ToolTruncationExamples)
	}
	if got := summary.ToolFailureExamples["timeout"]; len(got) != 1 ||
		got[0].Tool != "web_fetch" ||
		got[0].Scenario != "taostats-rendered" {
		t.Fatalf("ToolFailureExamples[timeout] = %#v", got)
	}
	if got := summary.RuntimeErrorExamples["llm_timeout"]; len(got) != 1 ||
		got[0].Scenario != "taostats-rendered" ||
		!strings.Contains(got[0].Message, "llm_stream timed out") {
		t.Fatalf("RuntimeErrorExamples[llm_timeout] = %#v", got)
	}
	if summary.LoopDecisions != 1 || summary.LoopDecisionByKind["evidence_quality"] != 1 || summary.LoopDecisionByDecision["defer"] != 1 {
		t.Fatalf("loop decision summary = count:%d kinds:%#v decisions:%#v", summary.LoopDecisions, summary.LoopDecisionByKind, summary.LoopDecisionByDecision)
	}
	if len(summary.LoopDecisionExamples) != 1 ||
		summary.LoopDecisionExamples[0].Scenario != "taostats-rendered" {
		t.Fatalf("LoopDecisionExamples = %#v", summary.LoopDecisionExamples)
	}
	if summary.LoopProtocolFeeds != 2 || summary.LoopProtocolFeedByMode["digest"] != 1 || summary.LoopProtocolFeedByMode["full"] != 1 {
		t.Fatalf("loop protocol feed summary = count:%d modes:%#v", summary.LoopProtocolFeeds, summary.LoopProtocolFeedByMode)
	}
	if len(summary.LoopProtocolFeedExamples) != 1 ||
		summary.LoopProtocolFeedExamples[0].Scenario != "taostats-rendered" ||
		summary.LoopProtocolFeedExamples[0].Mode != "full" {
		t.Fatalf("LoopProtocolFeedExamples = %#v", summary.LoopProtocolFeedExamples)
	}
	if len(summary.ContextCompactionExamples) != 1 ||
		summary.ContextCompactionExamples[0].TurnID != "turn-summary" ||
		summary.ContextCompactionExamples[0].Scenario != "taostats-rendered" ||
		!strings.Contains(summary.ContextCompactionExamples[0].SummaryPreview, "market evidence") {
		t.Fatalf("ContextCompactionExamples = %#v", summary.ContextCompactionExamples)
	}
	if summary.PlanCalls != 3 || summary.PlanErrors != 1 {
		t.Fatalf("plan counts = calls:%d errors:%d, want 3/1", summary.PlanCalls, summary.PlanErrors)
	}
	if !reflect.DeepEqual(summary.PlanByAction, map[string]int{"set": 1, "update": 2}) {
		t.Fatalf("PlanByAction = %#v", summary.PlanByAction)
	}
	if len(summary.PlanExamples) != 1 ||
		summary.PlanExamples[0].CallID != "plan-1" ||
		summary.PlanExamples[0].StepText != "verify browser evidence" ||
		summary.PlanExamples[0].Scenario != "sample" {
		t.Fatalf("PlanExamples = %#v", summary.PlanExamples)
	}
	if summary.ExpectationScenarios != 2 {
		t.Fatalf("ExpectationScenarios = %d, want 2", summary.ExpectationScenarios)
	}
	if !reflect.DeepEqual(summary.ExpectationSuites, map[string]int{"long-run": 1, "live-web": 1}) {
		t.Fatalf("ExpectationSuites = %#v", summary.ExpectationSuites)
	}
	if !reflect.DeepEqual(summary.ExpectationDomains, map[string]int{"longrun_recovery": 1, "market": 1, "web_evidence": 1}) {
		t.Fatalf("ExpectationDomains = %#v", summary.ExpectationDomains)
	}
	if !reflect.DeepEqual(summary.ExpectationDomainPass, map[string]int{"market": 1}) {
		t.Fatalf("ExpectationDomainPass = %#v", summary.ExpectationDomainPass)
	}
	if !reflect.DeepEqual(summary.ExpectationDomainFail, map[string]int{"longrun_recovery": 1, "web_evidence": 1}) {
		t.Fatalf("ExpectationDomainFail = %#v", summary.ExpectationDomainFail)
	}
	marketRuntime := summary.ExpectationDomainRuntime["market"]
	if marketRuntime == nil ||
		marketRuntime.Scenarios != 1 ||
		marketRuntime.Passed != 1 ||
		marketRuntime.ToolCalls != 2 ||
		marketRuntime.ToolErrors != 0 ||
		marketRuntime.SourceAccessResults != 2 ||
		marketRuntime.SourceAccessVerified != 2 ||
		marketRuntime.SourceAccessNetwork != 2 ||
		marketRuntime.InputTokens != 20 ||
		marketRuntime.OutputTokens != 5 {
		t.Fatalf("market expectation domain runtime = %#v", marketRuntime)
	}
	webRuntime := summary.ExpectationDomainRuntime["web_evidence"]
	if webRuntime == nil ||
		webRuntime.Scenarios != 1 ||
		webRuntime.Failed != 1 ||
		webRuntime.ToolCalls != 3 ||
		webRuntime.ToolErrors != 1 ||
		webRuntime.LoopGuardInterventions != 2 ||
		webRuntime.SourceAccessResults != 2 ||
		webRuntime.SourceAccessVerified != 1 ||
		webRuntime.SourceAccessNetwork != 1 ||
		webRuntime.RuntimeErrors != 3 ||
		webRuntime.InputTokens != 70 ||
		webRuntime.OutputTokens != 15 {
		t.Fatalf("web_evidence expectation domain runtime = %#v", webRuntime)
	}
	if !reflect.DeepEqual(summary.ExpectationSourceAccess, map[string]int{"network": 2}) {
		t.Fatalf("ExpectationSourceAccess = %#v", summary.ExpectationSourceAccess)
	}
	wantExpectationCaps := map[string]int{
		"browser":            2,
		"context_compaction": 1,
		"delegation":         1,
		"memory":             1,
		"plan":               1,
		"session":            2,
		"session_search":     1,
		"source_access":      2,
		"verifier":           1,
		"web":                1,
		"workspace":          1,
	}
	if !reflect.DeepEqual(summary.ExpectationCapabilities, wantExpectationCaps) {
		t.Fatalf("ExpectationCapabilities = %#v, want %#v", summary.ExpectationCapabilities, wantExpectationCaps)
	}
	wantExpectationPass := map[string]int{
		"browser":       1,
		"memory":        1,
		"session":       1,
		"source_access": 1,
		"verifier":      1,
		"workspace":     1,
	}
	if !reflect.DeepEqual(summary.ExpectationCapabilityPass, wantExpectationPass) {
		t.Fatalf("ExpectationCapabilityPass = %#v, want %#v", summary.ExpectationCapabilityPass, wantExpectationPass)
	}
	wantExpectationFail := map[string]int{
		"browser":            1,
		"context_compaction": 1,
		"delegation":         1,
		"plan":               1,
		"session":            1,
		"session_search":     1,
		"source_access":      1,
		"web":                1,
	}
	if !reflect.DeepEqual(summary.ExpectationCapabilityFail, wantExpectationFail) {
		t.Fatalf("ExpectationCapabilityFail = %#v, want %#v", summary.ExpectationCapabilityFail, wantExpectationFail)
	}
	wantExpectationTools := map[string]int{"read_file": 1, "repo_search": 1, "memory": 1, "web_fetch": 1, "browser_network_read": 2, "session_search": 1, "run_task": 1}
	if !reflect.DeepEqual(summary.ExpectationRequiredTools, wantExpectationTools) {
		t.Fatalf("ExpectationRequiredTools = %#v, want %#v", summary.ExpectationRequiredTools, wantExpectationTools)
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

func TestPrintBatchQualityGates(t *testing.T) {
	var out bytes.Buffer
	meta := testEvalJSONLMetadata()
	printBatchQualityGates(&out, meta, batchSummary{}, nil)
	if out.Len() != 0 {
		t.Fatalf("disabled quality gates should not print, got:\n%s", out.String())
	}

	minPassRate := 0.8
	meta.MinPassRate = &minPassRate
	meta.QualityProfile = "longrun"
	printBatchQualityGates(&out, meta, batchSummary{}, []string{"pass_rate 0.500 < min 0.800"})
	got := out.String()
	if !strings.Contains(got, "QUALITY_GATES status=failed profile=longrun failures=1") ||
		!strings.Contains(got, "gate_failure: pass_rate 0.500 < min 0.800") {
		t.Fatalf("failed quality gates output missing status or failure:\n%s", got)
	}

	out.Reset()
	printBatchQualityGates(&out, meta, batchSummary{
		DebugBriefTagExamples: map[string][]batchDebugBriefTagExample{
			"verifier:failed": {{
				Scenario:          "code-pr",
				FailureKinds:      map[string]int{"verify_command": 1},
				TracePath:         "/tmp/affenteval/code-pr/trace.jsonl",
				TimelinePath:      "/tmp/affenteval/code-pr/affenteval-timeline.md",
				DebugManifestPath: "/tmp/affenteval/code-pr/affenteval-debug.json",
			}},
		},
	}, []string{"debug_brief_tag_rate[verifier:failed] 0.500 > max 0.000"})
	got = out.String()
	if !strings.Contains(got, "gate_failure: debug_brief_tag_rate[verifier:failed] 0.500 > max 0.000") ||
		!strings.Contains(got, "debug_brief_example[verifier:failed]: scenario=code-pr failure_kinds=verify_command:1") ||
		!strings.Contains(got, "timeline=/tmp/affenteval/code-pr/affenteval-timeline.md") {
		t.Fatalf("debug brief tag quality gate output missing example:\n%s", got)
	}

	out.Reset()
	printBatchQualityGates(&out, meta, batchSummary{}, nil)
	if strings.TrimSpace(out.String()) != "QUALITY_GATES status=passed profile=longrun" {
		t.Fatalf("passed quality gates output = %q", out.String())
	}

	out.Reset()
	meta = testEvalJSONLMetadata()
	meta.MaxDebugBriefTagRates = map[string]float64{"source_dynamic_without_network": 0}
	printBatchQualityGates(&out, meta, batchSummary{}, nil)
	if strings.TrimSpace(out.String()) != "QUALITY_GATES status=passed" {
		t.Fatalf("debug brief tag quality gates output = %q", out.String())
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
			MemorySearchCalls:         2,
			MemorySearchMisses:        1,
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
		LoopGuardExamples: []agenteval.LoopGuardExample{{
			Kind:              "tool_policy_forced_no_tools",
			Category:          "tool_policy",
			ToolIndex:         1,
			CallID:            "guard-jsonl-1",
			Tool:              "web_fetch",
			ArgsSummary:       `url="https://blocked.example/metrics"`,
			GuardSummary:      "forced no-tools after repeated failures",
			SuggestedNextStep: "answer from gathered evidence",
			ResultSummary:     "forced no-tools after repeated failures",
			ExitCode:          1,
		}},
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			ToolIndex:     2,
			CallID:        "net-1",
			Tool:          "browser_network_read",
			Status:        "network",
			URL:           "https://metrics.example/api.json",
			URLField:      "browser_network_url",
			SourceMethod:  "network_xhr_fetch",
			HTTPStatus:    "200",
			ContentType:   "application/json",
			JSONPath:      "$.price",
			ResultPreview: `JSON_PATH: $.price "12.34"`,
		}},
		BrowserNetworkExamples: []agenteval.BrowserNetworkSearchExample{{
			ToolIndex:         3,
			CallID:            "network-jsonl-1",
			CurrentPageURL:    "https://taostats.io/subnets/120",
			Query:             "market_cap",
			Status:            "matches",
			EvidenceStatus:    "refs_only_not_citable; read_required=true",
			Refs:              []string{"n1"},
			Previews:          []string{`{"market_cap":"201.04K T"}`},
			RequiresRead:      true,
			NotCitable:        true,
			SuggestedNextStep: "call browser_network_read before citing values",
		}},
		BrowserScrollExamples: []agenteval.BrowserScrollExample{{
			ToolIndex:         2,
			CallID:            "scroll-jsonl-1",
			URL:               "https://taostats.io/subnets/120",
			Direction:         "down",
			BeforeY:           "1200",
			AfterY:            "1200",
			MaxY:              "1200",
			Movement:          "none",
			Boundary:          "bottom",
			Status:            "boundary",
			SuggestedNextStep: "use browser_network_read before citing hidden values",
			ResultPreview:     "SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
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
		MemorySearchMissExamples: []agenteval.MemorySearchMissExample{{
			ToolIndex:  4,
			CallID:     "mem-search-jsonl-1",
			Target:     "memory",
			Topic:      "deploy",
			Query:      "helm deployment",
			Message:    "no entries matched. Next: search a specific topic from topics.",
			TopicCount: 2,
			Topics:     []string{"deploy", "auth"},
		}},
		SessionSearchExamples: []agenteval.SessionSearchExample{{
			ToolIndex:       4,
			CallID:          "search-jsonl-1",
			Query:           "Alpha Coast",
			Total:           2,
			SessionID:       "market-alpha",
			TurnIdx:         4,
			MessageIdx:      8,
			Role:            "assistant",
			Score:           2.5,
			ModTime:         "2026-05-27T12:00:00Z",
			MatchedTerms:    []string{"alpha", "coast"},
			ContextIncluded: true,
			SnippetPreview:  "history marker ALPHA-COAST risk label elevated",
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
		LoopProtocolFeeds: agenteval.LoopProtocolFeedStats{
			Count:  2,
			ByMode: map[string]int{"digest": 1, "full": 1},
			Latest: agenteval.LoopProtocolFeed{LoopID: "sample-loop", Status: "running", Mode: "digest", FeedNumber: 2, ProtocolFeeds: 2, ProtocolPath: ".affent/loops/sample-loop/LOOP.md"},
			Examples: []agenteval.LoopProtocolFeed{
				{LoopID: "sample-loop", Status: "running", Mode: "full", FeedNumber: 1, ProtocolFeeds: 1, ProtocolPath: ".affent/loops/sample-loop/LOOP.md", PlanLabel: "plan:1/2:active", PlanCurrentStepIndex: 2, PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "finish implementation"},
			},
		},
		LoopProtocolCalibrationRequests: agenteval.LoopProtocolCalibrationStats{
			Count: 1,
			Examples: []agenteval.LoopProtocolCalibration{{
				LoopID:                  "sample-loop",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				ProtocolPath:            ".affent/loops/sample-loop/LOOP.md",
				EventSeq:                8,
			}},
		},
		LoopProtocolCalibrations: agenteval.LoopProtocolCalibrationStats{
			Count: 1,
			Examples: []agenteval.LoopProtocolCalibration{{
				LoopID:                  "sample-loop",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				CalibrationAnswers:      1,
				LastCalibrationAnswer:   "Pause when implementation evidence is missing.",
				ProtocolPath:            ".affent/loops/sample-loop/LOOP.md",
				EventSeq:                9,
			}},
		},
		ContextCompactions: agenteval.ContextCompactionStats{
			Count:           3,
			Reactive:        1,
			RemovedMessages: 48,
			SummaryBytes:    3072,
			SummaryMissing:  1,
			SummaryEmpty:    1,
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
		ContextInjections: agenteval.ContextInjectionStats{
			Count:           2,
			BySource:        map[string]int{"account_access": 1, "loop_protocol": 1},
			Bytes:           3200,
			EstimatedTokens: 800,
			Examples: []agenteval.ContextInjection{{
				TurnID:          "turn-jsonl",
				Source:          "account_access",
				Title:           "Account access context injected",
				Summary:         "Account-level access hints were made available.",
				Preview:         "Configured environment variables: GITHUB_TOKEN",
				Bytes:           1200,
				EstimatedTokens: 300,
			}},
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:           2,
			ArgsOmittedBytes:        1024,
			ResultsTruncated:        1,
			ResultsOmittedBytes:     8192,
			ResultArtifacts:         1,
			ContextTruncated:        2,
			ContextOmittedBytes:     6144,
			ContextArtifacts:        1,
			ContextMissingArtifacts: 1,
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
		ToolRepairExamples: []agenteval.ToolRepairExample{{
			ToolIndex:     1,
			CallID:        "repair-jsonl-1",
			Tool:          "read_file",
			OriginalTool:  "readFile",
			Canonicalized: true,
			ArgsRepaired:  true,
			RepairNotes:   []string{"canonicalized tool readFile to read_file", "renamed field file_path to path"},
			RepairKinds:   []string{"tool_name", "alias_rename"},
			Succeeded:     true,
		}},
		Plan: agenteval.PlanStats{
			Calls:    2,
			ByAction: map[string]int{"set": 1, "update": 1},
			Errors:   1,
		},
		PlanExamples: []agenteval.PlanExample{{
			ToolIndex:         4,
			CallID:            "plan-jsonl-1",
			Action:            "update",
			Index:             2,
			Status:            "completed",
			StepText:          "verify browser evidence",
			Evidence:          []string{"go test ./cmd/affenteval"},
			TotalSteps:        3,
			CompletedSteps:    2,
			CurrentStepIndex:  3,
			CurrentStepStatus: "pending",
		}},
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
		"memory_search_calls":                 float64(2),
		"memory_search_misses":                float64(1),
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
		"tool_context_artifacts":              float64(1),
		"tool_context_missing_artifacts":      float64(1),
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
		"loop_protocol_feeds":                 float64(2),
		"loop_protocol_calibration_requests":  float64(1),
		"loop_protocol_calibrations":          float64(1),
		"context_compactions":                 float64(3),
		"context_compactions_reactive":        float64(1),
		"context_compaction_removed_messages": float64(48),
		"context_compaction_summary_bytes":    float64(3072),
		"context_compaction_summary_missing":  float64(1),
		"context_compaction_summary_empty":    float64(1),
		"context_injections":                  float64(2),
		"context_injection_bytes":             float64(3200),
		"context_injection_estimated_tokens":  float64(800),
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
	if got["expectation_capability_outcome"] != "passed" {
		t.Fatalf("expectation_capability_outcome = %#v\njson=%s", got["expectation_capability_outcome"], out.String())
	}
	for _, cap := range []string{"browser", "source_access", "web"} {
		if !jsonArrayContainsString(got["expectation_capability_names"], cap) {
			t.Fatalf("expectation_capability_names missing %q: %#v\njson=%s", cap, got["expectation_capability_names"], out.String())
		}
		if !jsonArrayContainsString(got["expectation_capability_passed_names"], cap) {
			t.Fatalf("expectation_capability_passed_names missing %q: %#v\njson=%s", cap, got["expectation_capability_passed_names"], out.String())
		}
	}
	if _, ok := got["expectation_capability_failed_names"]; ok {
		t.Fatalf("passing result should omit expectation_capability_failed_names, got %#v", got["expectation_capability_failed_names"])
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
	loopGuardExamples, ok := got["loop_guard_examples"].([]any)
	if !ok || len(loopGuardExamples) != 1 {
		t.Fatalf("loop_guard_examples = %#v\njson=%s", got["loop_guard_examples"], out.String())
	}
	loopGuardExample, ok := loopGuardExamples[0].(map[string]any)
	if !ok ||
		loopGuardExample["call_id"] != "guard-jsonl-1" ||
		loopGuardExample["kind"] != "tool_policy_forced_no_tools" ||
		loopGuardExample["category"] != "tool_policy" ||
		loopGuardExample["tool"] != "web_fetch" ||
		!strings.Contains(fmt.Sprint(loopGuardExample["args_summary"]), "blocked.example") ||
		!strings.Contains(fmt.Sprint(loopGuardExample["guard_summary"]), "forced no-tools") ||
		!strings.Contains(fmt.Sprint(loopGuardExample["suggested_next_step"]), "gathered evidence") {
		t.Fatalf("loop_guard_example = %#v\njson=%s", loopGuardExamples[0], out.String())
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
		!jsonArrayContainsString(debugBrief["tags"], "browser_scroll:boundary") ||
		!jsonArrayContainsString(debugBrief["tags"], "browser_network:refs") ||
		!jsonArrayContainsString(debugBrief["tags"], "memory_update:add") ||
		!jsonArrayContainsString(debugBrief["tags"], "memory_search_miss") ||
		!jsonArrayContainsString(debugBrief["tags"], "recall:memory_topic_anchors") ||
		!jsonArrayContainsString(debugBrief["tags"], "recall") ||
		!jsonArrayContainsString(debugBrief["tags"], "context_compaction:reactive") ||
		!jsonArrayContainsString(debugBrief["tags"], "context_injection:account_access") ||
		!jsonArrayContainsString(debugBrief["tags"], "context_injection:loop_protocol") ||
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
		sourceAccessExample["http_status"] != "200" ||
		sourceAccessExample["content_type"] != "application/json" ||
		sourceAccessExample["result_preview"] != `JSON_PATH: $.price "12.34"` ||
		!strings.Contains(fmt.Sprint(sourceAccessExample["url"]), "metrics.example") {
		t.Fatalf("source_access_example = %#v\njson=%s", sourceAccessExamples[0], out.String())
	}
	browserNetworkExamples, ok := got["browser_network_examples"].([]any)
	if !ok || len(browserNetworkExamples) != 1 {
		t.Fatalf("browser_network_examples = %#v\njson=%s", got["browser_network_examples"], out.String())
	}
	browserNetworkExample, ok := browserNetworkExamples[0].(map[string]any)
	if !ok ||
		browserNetworkExample["call_id"] != "network-jsonl-1" ||
		browserNetworkExample["current_page_url"] != "https://taostats.io/subnets/120" ||
		browserNetworkExample["query"] != "market_cap" ||
		browserNetworkExample["status"] != "matches" ||
		browserNetworkExample["evidence_status"] != "refs_only_not_citable; read_required=true" ||
		browserNetworkExample["requires_read"] != true ||
		browserNetworkExample["not_citable"] != true ||
		!jsonArrayContainsString(browserNetworkExample["refs"], "n1") ||
		!jsonArrayContainsString(browserNetworkExample["previews"], `{"market_cap":"201.04K T"}`) ||
		!strings.Contains(fmt.Sprint(browserNetworkExample["suggested_next_step"]), "browser_network_read") {
		t.Fatalf("browser_network_example = %#v\njson=%s", browserNetworkExamples[0], out.String())
	}
	browserScrollExamples, ok := got["browser_scroll_examples"].([]any)
	if !ok || len(browserScrollExamples) != 1 {
		t.Fatalf("browser_scroll_examples = %#v\njson=%s", got["browser_scroll_examples"], out.String())
	}
	browserScrollExample, ok := browserScrollExamples[0].(map[string]any)
	if !ok ||
		browserScrollExample["call_id"] != "scroll-jsonl-1" ||
		browserScrollExample["url"] != "https://taostats.io/subnets/120" ||
		browserScrollExample["direction"] != "down" ||
		browserScrollExample["before_y"] != "1200" ||
		browserScrollExample["after_y"] != "1200" ||
		browserScrollExample["max_y"] != "1200" ||
		browserScrollExample["movement"] != "none" ||
		browserScrollExample["boundary"] != "bottom" ||
		browserScrollExample["status"] != "boundary" ||
		!strings.Contains(fmt.Sprint(browserScrollExample["suggested_next_step"]), "browser_network_read") {
		t.Fatalf("browser_scroll_example = %#v\njson=%s", browserScrollExamples[0], out.String())
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
	memorySearchMissExamples, ok := got["memory_search_miss_examples"].([]any)
	if !ok || len(memorySearchMissExamples) != 1 {
		t.Fatalf("memory_search_miss_examples = %#v\njson=%s", got["memory_search_miss_examples"], out.String())
	}
	memorySearchMissExample, ok := memorySearchMissExamples[0].(map[string]any)
	if !ok ||
		memorySearchMissExample["call_id"] != "mem-search-jsonl-1" ||
		memorySearchMissExample["target"] != "memory" ||
		memorySearchMissExample["topic"] != "deploy" ||
		memorySearchMissExample["query"] != "helm deployment" ||
		memorySearchMissExample["topic_count"] != float64(2) ||
		!jsonArrayContainsString(memorySearchMissExample["topics"], "auth") ||
		!strings.Contains(fmt.Sprint(memorySearchMissExample["message"]), "no entries matched") {
		t.Fatalf("memory_search_miss_example = %#v\njson=%s", memorySearchMissExamples[0], out.String())
	}
	sessionSearchExamples, ok := got["session_search_examples"].([]any)
	if !ok || len(sessionSearchExamples) != 1 {
		t.Fatalf("session_search_examples = %#v\njson=%s", got["session_search_examples"], out.String())
	}
	sessionSearchExample, ok := sessionSearchExamples[0].(map[string]any)
	if !ok ||
		sessionSearchExample["call_id"] != "search-jsonl-1" ||
		sessionSearchExample["query"] != "Alpha Coast" ||
		sessionSearchExample["session_id"] != "market-alpha" ||
		sessionSearchExample["turn_idx"] != float64(4) ||
		sessionSearchExample["message_idx"] != float64(8) ||
		sessionSearchExample["mod_time"] != "2026-05-27T12:00:00Z" ||
		sessionSearchExample["context_included"] != true ||
		!jsonArrayContainsString(sessionSearchExample["matched_terms"], "coast") ||
		!strings.Contains(fmt.Sprint(sessionSearchExample["snippet_preview"]), "risk label") {
		t.Fatalf("session_search_example = %#v\njson=%s", sessionSearchExamples[0], out.String())
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
	loopProtocolFeedByMode, ok := got["loop_protocol_feed_by_mode"].(map[string]any)
	if !ok || loopProtocolFeedByMode["digest"] != float64(1) || loopProtocolFeedByMode["full"] != float64(1) {
		t.Fatalf("loop_protocol_feed_by_mode = %#v\njson=%s", got["loop_protocol_feed_by_mode"], out.String())
	}
	loopProtocolFeedExamples, ok := got["loop_protocol_feed_examples"].([]any)
	if !ok || len(loopProtocolFeedExamples) != 1 {
		t.Fatalf("loop_protocol_feed_examples = %#v\njson=%s", got["loop_protocol_feed_examples"], out.String())
	}
	loopProtocolFeedExample, ok := loopProtocolFeedExamples[0].(map[string]any)
	if !ok ||
		loopProtocolFeedExample["loop_id"] != "sample-loop" ||
		loopProtocolFeedExample["mode"] != "full" ||
		loopProtocolFeedExample["feed_number"] != float64(1) ||
		loopProtocolFeedExample["plan_label"] != "plan:1/2:active" ||
		loopProtocolFeedExample["plan_current_step_index"] != float64(2) ||
		loopProtocolFeedExample["plan_current_step_status"] != "in_progress" ||
		loopProtocolFeedExample["plan_current_step"] != "finish implementation" ||
		loopProtocolFeedExample["protocol_path"] != ".affent/loops/sample-loop/LOOP.md" {
		t.Fatalf("loop_protocol_feed_example = %#v\njson=%s", loopProtocolFeedExamples[0], out.String())
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
	contextInjectionBySource, ok := got["context_injection_by_source"].(map[string]any)
	if !ok || contextInjectionBySource["account_access"] != float64(1) || contextInjectionBySource["loop_protocol"] != float64(1) {
		t.Fatalf("context_injection_by_source = %#v\njson=%s", got["context_injection_by_source"], out.String())
	}
	contextInjectionExamples, ok := got["context_injection_examples"].([]any)
	if !ok || len(contextInjectionExamples) != 1 {
		t.Fatalf("context_injection_examples = %#v\njson=%s", got["context_injection_examples"], out.String())
	}
	contextInjectionExample, ok := contextInjectionExamples[0].(map[string]any)
	if !ok ||
		contextInjectionExample["turn_id"] != "turn-jsonl" ||
		contextInjectionExample["source"] != "account_access" ||
		contextInjectionExample["bytes"] != float64(1200) ||
		contextInjectionExample["estimated_tokens"] != float64(300) ||
		!strings.Contains(fmt.Sprint(contextInjectionExample["preview"]), "GITHUB_TOKEN") {
		t.Fatalf("context_injection_example = %#v\njson=%s", contextInjectionExamples[0], out.String())
	}
	repairKinds, ok := got["tool_repair_by_kind"].(map[string]any)
	if !ok {
		t.Fatalf("tool_repair_by_kind missing or wrong type: %#v\njson=%s", got["tool_repair_by_kind"], out.String())
	}
	if repairKinds["alias_rename"] != float64(2) || repairKinds["type_coercion"] != float64(1) {
		t.Fatalf("tool_repair_by_kind = %#v", repairKinds)
	}
	toolRepairExamples, ok := got["tool_repair_examples"].([]any)
	if !ok || len(toolRepairExamples) != 1 {
		t.Fatalf("tool_repair_examples = %#v\njson=%s", got["tool_repair_examples"], out.String())
	}
	toolRepairExample, ok := toolRepairExamples[0].(map[string]any)
	if !ok ||
		toolRepairExample["call_id"] != "repair-jsonl-1" ||
		toolRepairExample["tool"] != "read_file" ||
		toolRepairExample["original_tool"] != "readFile" ||
		toolRepairExample["canonicalized"] != true ||
		!jsonArrayContainsString(toolRepairExample["repair_kinds"], "alias_rename") {
		t.Fatalf("tool_repair_example = %#v\njson=%s", toolRepairExamples[0], out.String())
	}
	planByAction, ok := got["plan_by_action"].(map[string]any)
	if !ok {
		t.Fatalf("plan_by_action missing or wrong type: %#v\njson=%s", got["plan_by_action"], out.String())
	}
	if planByAction["set"] != float64(1) || planByAction["update"] != float64(1) {
		t.Fatalf("plan_by_action = %#v", planByAction)
	}
	planExamples, ok := got["plan_examples"].([]any)
	if !ok || len(planExamples) != 1 {
		t.Fatalf("plan_examples = %#v\njson=%s", got["plan_examples"], out.String())
	}
	planExample, ok := planExamples[0].(map[string]any)
	if !ok ||
		planExample["call_id"] != "plan-jsonl-1" ||
		planExample["action"] != "update" ||
		planExample["index"] != float64(2) ||
		planExample["status"] != "completed" ||
		planExample["step_text"] != "verify browser evidence" ||
		!jsonArrayContainsString(planExample["evidence"], "go test ./cmd/affenteval") {
		t.Fatalf("plan_example = %#v\njson=%s", planExamples[0], out.String())
	}
}

func TestEvalJSONLContractDocumentsBrowserDiagnostics(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "eval-jsonl-contract.md"))
	if err != nil {
		t.Fatalf("read eval JSONL contract: %v", err)
	}
	doc := string(raw)
	for _, want := range []string{
		"`browser_scroll_examples`",
		"`browser_scroll:boundary`",
		"`browser_scroll:stuck_without_network`",
		"page-position diagnostics",
		"`browser_network_examples`",
		"not citable factual evidence",
		"`browser_network_read`",
		"`browser_network:unread_refs`",
		"`browser_network:refs`",
		"`source_access_examples`, `browser_scroll_examples`",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("eval JSONL contract missing %q", want)
		}
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
			`affentctl run failed: exit=1 err=launch chromium: executable file not found
Failure: kind=browser_launch_failed`,
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
		"hint[browser_launch_failed]",
		"Chromium could not start",
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
		!strings.Contains(fmt.Sprint(hints["context_overflow"]), "context window") ||
		!strings.Contains(fmt.Sprint(hints["browser_launch_failed"]), "Chromium could not start") {
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
	summary.add(agenteval.BatchResult{
		BatchScenario:     "code-pr",
		OK:                false,
		TracePath:         "/tmp/affenteval/code-pr/trace.jsonl",
		TimelinePath:      "/tmp/affenteval/code-pr/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affenteval/code-pr/affenteval-debug.json",
		Failures:          []string{"verify command failed: go test ./..."},
		Verifier: agenteval.VerifierResult{
			Command:  "go test ./...",
			Ran:      true,
			OK:       false,
			ExitCode: -1,
		},
	})

	if summary.DebugBriefByTag["outcome:failed"] != 2 ||
		summary.DebugBriefByTag["turn_end:max_turns"] != 1 ||
		summary.DebugBriefByTag["runtime_error:llm_timeout"] != 1 ||
		summary.DebugBriefByTag["empty_recall"] != 1 ||
		summary.DebugBriefByTag["verifier:failed"] != 1 ||
		summary.DebugBriefByTag["verifier:abnormal"] != 1 {
		t.Fatalf("DebugBriefByTag = %#v", summary.DebugBriefByTag)
	}
	examples := summary.DebugBriefTagExamples["verifier:failed"]
	if len(examples) != 1 ||
		examples[0].Scenario != "code-pr" ||
		examples[0].FailureKinds["verify_command"] != 1 ||
		examples[0].TracePath != "/tmp/affenteval/code-pr/trace.jsonl" ||
		examples[0].TimelinePath != "/tmp/affenteval/code-pr/affenteval-timeline.md" ||
		examples[0].DebugManifestPath != "/tmp/affenteval/code-pr/affenteval-debug.json" {
		t.Fatalf("DebugBriefTagExamples[verifier:failed] = %#v", examples)
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
			FocusedTaskCalls:                2,
			FocusedTaskByType:               map[string]int{"recall": 2},
			FocusedTaskSourceFindingsByType: map[string]int{"recall": 1},
			FocusedTaskErrors:               1,
			FocusedTaskIncomplete:           1,
		},
	})
	summary.add(agenteval.BatchResult{
		OK:                 true,
		Duration:           150 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		Delegation: agenteval.DelegationStats{
			FocusedTaskCalls:                2,
			FocusedTaskByType:               map[string]int{"recall": 1, "explore": 1},
			FocusedTaskSourceFindingsByType: map[string]int{"recall": 2, "explore": 1},
			SubagentCalls:                   1,
			SubagentByMode:                  map[string]int{"review": 1},
			SubagentSourceEvidenceByMode:    map[string]int{"review": 3},
			SubagentErrors:                  1,
			SubagentIncomplete:              1,
		},
	})

	if summary.FocusedTaskCalls != 4 {
		t.Errorf("FocusedTaskCalls = %d, want 4", summary.FocusedTaskCalls)
	}
	if summary.FocusedTaskByType["recall"] != 3 || summary.FocusedTaskByType["explore"] != 1 {
		t.Errorf("merged FocusedTaskByType = %#v", summary.FocusedTaskByType)
	}
	if summary.FocusedTaskSources["recall"] != 3 || summary.FocusedTaskSources["explore"] != 1 {
		t.Errorf("merged FocusedTaskSources = %#v", summary.FocusedTaskSources)
	}
	if summary.SubagentCalls != 1 || summary.SubagentByMode["review"] != 1 {
		t.Errorf("subagent aggregates = %d, %#v", summary.SubagentCalls, summary.SubagentByMode)
	}
	if summary.SubagentSources["review"] != 3 {
		t.Errorf("merged SubagentSources = %#v", summary.SubagentSources)
	}
	if summary.FocusedTaskErrors != 1 || summary.SubagentErrors != 1 {
		t.Errorf("delegation error aggregates = focused:%d subagent:%d, want 1/1", summary.FocusedTaskErrors, summary.SubagentErrors)
	}
	if summary.FocusedTaskIncomplete != 1 || summary.SubagentIncomplete != 1 {
		t.Errorf("delegation incomplete aggregates = focused:%d subagent:%d, want 1/1", summary.FocusedTaskIncomplete, summary.SubagentIncomplete)
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
	if got["focused_task_incomplete"] != float64(1) {
		t.Errorf("summary.focused_task_incomplete = %#v, want 1", got["focused_task_incomplete"])
	}
	if got["subagent_error_rate"] != float64(1) {
		t.Errorf("summary.subagent_error_rate = %#v, want 1", got["subagent_error_rate"])
	}
	if got["subagent_incomplete"] != float64(1) {
		t.Errorf("summary.subagent_incomplete = %#v, want 1", got["subagent_incomplete"])
	}
	byType, ok := got["focused_task_by_type"].(map[string]any)
	if !ok || byType["recall"] != float64(3) || byType["explore"] != float64(1) {
		t.Errorf("summary.focused_task_by_type = %#v", byType)
	}
	sources, ok := got["focused_task_sources"].(map[string]any)
	if !ok || sources["recall"] != float64(3) || sources["explore"] != float64(1) {
		t.Errorf("summary.focused_task_sources = %#v", sources)
	}
	subSources, ok := got["subagent_sources"].(map[string]any)
	if !ok || subSources["review"] != float64(3) {
		t.Errorf("summary.subagent_sources = %#v", subSources)
	}

	var textOut bytes.Buffer
	printBatchSummary(&textOut, summary)
	for _, want := range []string{
		"delegation=focused_tasks:4,subagents:1",
		"delegation_errors=focused_tasks:1,subagents:1",
		"delegation_incomplete=focused_tasks:1,subagents:1",
		"focused_task_error:25.0%,subagent_error:100.0%",
		"focused_task_by_type=explore:1,recall:3",
		"focused_task_sources=explore:1,recall:3",
		"subagent_by_mode=review:1",
		"subagent_sources=review:3",
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
			FocusedTaskCalls:                3,
			FocusedTaskByType:               map[string]int{"recall": 2, "explore": 1},
			FocusedTaskSourceFindingsByType: map[string]int{"recall": 2},
			FocusedTaskErrors:               1,
			SubagentCalls:                   1,
			SubagentByMode:                  map[string]int{"test": 1},
			SubagentSourceEvidenceByMode:    map[string]int{"test": 2},
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
	sources, ok := got["focused_task_sources"].(map[string]any)
	if !ok || sources["recall"] != float64(2) {
		t.Errorf("focused_task_sources = %#v", sources)
	}
	if got["subagent_calls"] != float64(1) {
		t.Errorf("subagent_calls = %#v, want 1", got["subagent_calls"])
	}
	byMode, ok := got["subagent_by_mode"].(map[string]any)
	if !ok || byMode["test"] != float64(1) {
		t.Errorf("subagent_by_mode = %#v", byMode)
	}
	subSources, ok := got["subagent_sources"].(map[string]any)
	if !ok || subSources["test"] != float64(2) {
		t.Errorf("subagent_sources = %#v", subSources)
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
		`"focused_task_sources"`,
		`"focused_task_errors"`,
		`"subagent_calls"`,
		`"subagent_by_mode"`,
		`"subagent_sources"`,
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
		ToolRepairExamples: []agenteval.ToolRepairExample{{
			Scenario:      "taostats-rendered",
			ToolIndex:     1,
			CallID:        "summary-repair-1",
			Tool:          "read_file",
			OriginalTool:  "readFile",
			Canonicalized: true,
			ArgsRepaired:  true,
			RepairNotes:   []string{"canonicalized tool readFile to read_file"},
			RepairKinds:   []string{"tool_name"},
			Succeeded:     true,
		}},
		ToolFailureByKind: map[string]int{"blocked": 1},
		ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
			"blocked": {
				{Scenario: "taostats-rendered", Kind: "blocked", Tool: "web_fetch", ArgsSummary: `url="https://blocked.example"`, ResultSummary: "blocked | Next: use another source", ExitCode: 1},
			},
		},
		LoopGuardExamples: []agenteval.LoopGuardExample{{
			Scenario:          "taostats-rendered",
			Kind:              "loop_guard_repeated_failed_input",
			Category:          "loop_guard",
			ToolIndex:         1,
			CallID:            "summary-guard-1",
			Tool:              "web_fetch",
			ArgsSummary:       `url="https://blocked.example"`,
			GuardSummary:      "repeated failed input",
			SuggestedNextStep: "use another source",
			ResultSummary:     "repeated failed input",
			ExitCode:          1,
		}},
		RuntimeErrors:      1,
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_timeout": {
				{Scenario: "taostats-rendered", Kind: "llm_timeout", Message: "LLM llm_stream timed out after 4m0s"},
			},
		},
		RuntimeSurfaceScenarios:    2,
		RuntimeSurfaceTools:        map[string]int{"web_fetch": 2, "browser_find": 1},
		RuntimeSurfaceCapabilities: map[string]int{"web_fetch": 2, "browser": 1},
		LoopDecisions:              1,
		LoopDecisionByKind:         map[string]int{"evidence_quality": 1},
		LoopDecisionByDecision:     map[string]int{"defer": 1},
		LoopDecisionExamples: []agenteval.LoopDecision{
			{Scenario: "taostats-rendered", Kind: "evidence_quality", Decision: "defer", RequiredAction: "read browser network responses"},
		},
		LoopProtocolFeedScenarios: 1,
		LoopProtocolFeeds:         2,
		LoopProtocolFeedByMode:    map[string]int{"digest": 1, "full": 1},
		LoopProtocolFeedExamples: []agenteval.LoopProtocolFeed{{
			Scenario:              "taostats-rendered",
			LoopID:                "taostats-rendered",
			Status:                "running",
			Mode:                  "digest",
			FeedNumber:            4,
			ProtocolFeeds:         4,
			ProtocolPath:          ".affent/loops/taostats-rendered/LOOP.md",
			PlanLabel:             "plan:2/5:active",
			PlanCurrentStepIndex:  3,
			PlanCurrentStepStatus: "in_progress",
			PlanCurrentStep:       "read taostats network evidence",
		}},
		LoopProtocolCalibrationRequestScenarios: 1,
		LoopProtocolCalibrationRequests:         1,
		LoopProtocolCalibrationRequestExamples: []agenteval.LoopProtocolCalibration{{
			Scenario:                "taostats-rendered",
			LoopID:                  "taostats-rendered",
			Status:                  "draft",
			CalibrationQuestions:    1,
			LastCalibrationQuestion: "What should pause this loop?",
			ProtocolPath:            ".affent/loops/taostats-rendered/LOOP.md",
			EventSeq:                2,
		}},
		LoopProtocolCalibrationScenarios: 1,
		LoopProtocolCalibrations:         1,
		LoopProtocolCalibrationExamples: []agenteval.LoopProtocolCalibration{{
			Scenario:                "taostats-rendered",
			LoopID:                  "taostats-rendered",
			Status:                  "draft",
			CalibrationQuestions:    1,
			LastCalibrationQuestion: "What should pause this loop?",
			CalibrationAnswers:      1,
			LastCalibrationAnswer:   "Pause if network evidence cannot be read.",
			ProtocolPath:            ".affent/loops/taostats-rendered/LOOP.md",
			EventSeq:                3,
		}},
		ContextCompactions:              1,
		ContextCompactionsReactive:      1,
		ContextCompactionRemoved:        32,
		ContextCompactionSummary:        2048,
		ContextCompactionSummaryMissing: 1,
		ContextCompactionSummaryEmpty:   1,
		ContextCompactionExamples: []agenteval.ContextCompaction{{
			Scenario:        "taostats-rendered",
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
		ContextInjections:               3,
		ContextInjectionBySource:        map[string]int{"account_access": 1, "loop_protocol": 2},
		ContextInjectionBytes:           3600,
		ContextInjectionEstimatedTokens: 900,
		ContextInjectionExamples: []agenteval.ContextInjection{{
			Scenario:        "taostats-rendered",
			TurnID:          "turn-summary-jsonl",
			Source:          "loop_protocol",
			Title:           "Loop protocol context injected",
			Summary:         "Loop protocol digest was fed into the turn.",
			Preview:         "North Star: keep evidence quality high.",
			Bytes:           1600,
			EstimatedTokens: 400,
		}},
		LoopGuardInterventions:     3,
		ForcedNoTools:              1,
		SourceAccessResults:        4,
		SourceAccessVerified:       3,
		SourceAccessDiscoveryOnly:  1,
		SourceAccessNetwork:        2,
		SourceAccessDynamicPartial: 1,
		SourceAccessExamples: []agenteval.SourceAccessExample{{
			Scenario:      "taostats-rendered",
			ToolIndex:     2,
			CallID:        "summary-source-1",
			Tool:          "browser_network_read",
			Status:        "network",
			URL:           "https://metrics.example/api.json",
			HTTPStatus:    "200",
			ContentType:   "application/json",
			JSONPath:      "$.price",
			ResultPreview: `JSON_PATH: $.price "12.34"`,
		}},
		BrowserScrollExamples: []agenteval.BrowserScrollExample{{
			Scenario:          "taostats-rendered",
			ToolIndex:         2,
			CallID:            "summary-scroll-1",
			URL:               "https://taostats.io/subnets/120",
			Direction:         "down",
			BeforeY:           "1200",
			AfterY:            "1200",
			MaxY:              "1200",
			Movement:          "none",
			Boundary:          "bottom",
			Status:            "boundary",
			SuggestedNextStep: "use browser_network_read before citing hidden values",
			ResultPreview:     "SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
		}},
		BrowserNetworkExamples: []agenteval.BrowserNetworkSearchExample{{
			Scenario:          "taostats-rendered",
			ToolIndex:         3,
			CallID:            "summary-network-1",
			CurrentPageURL:    "https://taostats.io/subnets/120",
			Query:             "market_cap",
			Status:            "matches",
			EvidenceStatus:    "refs_only_not_citable; read_required=true",
			Refs:              []string{"n1"},
			Previews:          []string{`{"market_cap":"201.04K T"}`},
			RequiresRead:      true,
			NotCitable:        true,
			SuggestedNextStep: "call browser_network_read before citing values",
		}},
		MemoryUpdates:      1,
		MemoryUpdateAdd:    1,
		MemorySearchCalls:  2,
		MemorySearchMisses: 1,
		MemoryUpdateExamples: []agenteval.MemoryUpdateExample{{
			Scenario:    "taostats-rendered",
			ToolIndex:   2,
			CallID:      "summary-memory-1",
			Action:      "add",
			Target:      "memory",
			Topic:       "markets",
			Location:    "memory:markets",
			NextPreview: "Prefer browser_network_read evidence.",
		}},
		SessionSearchCalls:        1,
		SessionSearchResults:      2,
		SessionSearchContextHits:  1,
		SessionSearchMatchedTerms: 2,
		SessionSearchExamples: []agenteval.SessionSearchExample{{
			Scenario:        "taostats-rendered",
			ToolIndex:       3,
			CallID:          "summary-search-1",
			Query:           "Alpha Coast",
			Total:           2,
			SessionID:       "market-alpha",
			TurnIdx:         4,
			MessageIdx:      8,
			ModTime:         "2026-05-27T12:00:00Z",
			MatchedTerms:    []string{"alpha", "coast"},
			ContextIncluded: true,
		}},
		ToolDurationMS:              120,
		ToolContextTruncated:        4,
		ToolContextOmittedBytes:     12288,
		ToolArgsTruncated:           1,
		ToolArgsOmittedBytes:        256,
		ToolResultsTruncated:        2,
		ToolResultsOmittedBytes:     4096,
		ToolResultArtifacts:         2,
		ToolContextArtifacts:        1,
		ToolContextMissingArtifacts: 1,
		ToolTruncationExamples: []agenteval.ToolTruncationExample{{
			Scenario:           "taostats-rendered",
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
		TraceEventScenarios:        2,
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
		FailureExamples: map[string][]batchFailureExample{
			"turn_end": {{
				Scenario:     "taostats-rendered",
				Failure:      `turn ended with reason "max_turns" (expected completed)`,
				TracePath:    "/tmp/affenteval/taostats-rendered/trace.jsonl",
				TimelinePath: "/tmp/affenteval/taostats-rendered/affenteval-timeline.md",
			}},
		},
		DebugBriefByTag: map[string]int{"outcome:failed": 1, "tool_failure:blocked": 1, "runtime_error:llm_timeout": 1},
		DebugBriefTagExamples: map[string][]batchDebugBriefTagExample{
			"tool_failure:blocked": {{
				Scenario:          "taostats-rendered",
				FailureKinds:      map[string]int{"turn_end": 1},
				TracePath:         "/tmp/affenteval/taostats-rendered/trace.jsonl",
				TimelinePath:      "/tmp/affenteval/taostats-rendered/affenteval-timeline.md",
				DebugManifestPath: "/tmp/affenteval/taostats-rendered/affenteval-debug.json",
			}},
		},
		ExpectationScenarios:  2,
		ExpectationSuites:     map[string]int{"long-run": 1, "live-web": 1},
		ExpectationDomains:    map[string]int{"bittensor": 1, "web_evidence": 1},
		ExpectationDomainPass: map[string]int{"bittensor": 1},
		ExpectationDomainFail: map[string]int{"web_evidence": 1},
		ExpectationDomainFailureExamples: map[string][]expectationDomainFailureExample{
			"web_evidence": {{
				Domain:         "web_evidence",
				Scenario:       "taostats-rendered",
				FailureKinds:   map[string]int{"turn_end": 1},
				DebugBriefTags: []string{"outcome:failed", "turn_end:max_turns"},
				TracePath:      "/tmp/affenteval/taostats-rendered/trace.jsonl",
				TimelinePath:   "/tmp/affenteval/taostats-rendered/affenteval-timeline.md",
			}},
		},
		ExpectationDomainRuntime: map[string]*expectationDomainRuntimeTotals{
			"bittensor": {
				Scenarios:              1,
				Passed:                 1,
				Duration:               100 * time.Millisecond,
				ToolCalls:              2,
				SourceAccessResults:    2,
				SourceAccessVerified:   2,
				SourceAccessNetwork:    2,
				LoopGuardInterventions: 1,
				InputTokens:            20,
				OutputTokens:           5,
			},
			"web_evidence": {
				Scenarios:              1,
				Failed:                 1,
				Duration:               250 * time.Millisecond,
				ToolCalls:              3,
				ToolErrors:             1,
				LoopGuardInterventions: 2,
				SourceAccessResults:    2,
				SourceAccessVerified:   1,
				SourceAccessNetwork:    1,
				RuntimeErrors:          3,
				InputTokens:            70,
				OutputTokens:           15,
			},
		},
		ExpectationCapabilities:   map[string]int{"browser": 2, "source_access": 2, "web": 1},
		ExpectationCapabilityPass: map[string]int{"browser": 1, "source_access": 1},
		ExpectationCapabilityFail: map[string]int{"browser": 1, "source_access": 1, "web": 1},
		ExpectationCapabilityFailureExamples: map[string][]expectationCapabilityFailureExample{
			"browser": {{
				Capability:     "browser",
				Scenario:       "taostats-rendered",
				FailureKinds:   map[string]int{"turn_end": 1},
				DebugBriefTags: []string{"outcome:failed", "turn_end:max_turns"},
				TracePath:      "/tmp/affenteval/taostats-rendered/trace.jsonl",
				TimelinePath:   "/tmp/affenteval/taostats-rendered/affenteval-timeline.md",
			}},
		},
		ExpectationRequiredTools: map[string]int{"web_fetch": 1, "browser_network_read": 1},
		ExpectationSourceAccess:  map[string]int{"network": 2},
		RemovedWorkspaces:        1,
		FocusedTaskCalls:         4,
		FocusedTaskErrors:        1,
		SubagentCalls:            2,
		SubagentErrors:           1,
		PlanCalls:                3,
		PlanByAction:             map[string]int{"set": 1, "update": 2},
		PlanErrors:               1,
		PlanExamples: []agenteval.PlanExample{{
			Scenario:          "taostats-rendered",
			ToolIndex:         4,
			CallID:            "summary-plan-1",
			Action:            "update",
			Index:             2,
			Status:            "completed",
			StepText:          "verify browser evidence",
			Evidence:          []string{"go test ./cmd/affenteval"},
			TotalSteps:        3,
			CompletedSteps:    2,
			CurrentStepIndex:  3,
			CurrentStepStatus: "pending",
		}},
	}, nil)

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl summary did not decode: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{
		"schema_version":                              float64(1),
		"type":                                        "summary",
		"suite":                                       "small-model-tools",
		"model":                                       "eval-model",
		"provider_label":                              "eval-provider",
		"executor":                                    "docker:affent-eval",
		"temperature":                                 "0.2",
		"top_p":                                       "0.9",
		"max_tokens":                                  "512",
		"seed":                                        "42",
		"runtime_web":                                 true,
		"runtime_browser":                             true,
		"timeout_ms":                                  float64(300000),
		"scenarios":                                   float64(2),
		"passed":                                      float64(1),
		"failed":                                      float64(1),
		"pass_rate":                                   float64(0.5),
		"completion_rate":                             float64(0.5),
		"memory_update_rate":                          float64(0.5),
		"memory_search_miss_rate":                     float64(0.5),
		"loop_protocol_feed_rate":                     float64(0.5),
		"loop_protocol_calibration_request_rate":      float64(0.5),
		"loop_protocol_calibration_rate":              float64(0.5),
		"tool_error_rate":                             float64(0.2),
		"focused_task_error_rate":                     float64(0.25),
		"subagent_error_rate":                         float64(0.5),
		"forced_no_tools_rate":                        float64(0.2),
		"loop_guard_intervention_rate":                float64(0.6),
		"plan_error_rate":                             float64(1.0 / 3.0),
		"tool_repair_success_rate":                    float64(0.75),
		"verifier_pass_rate":                          float64(0.5),
		"source_access_verified_rate":                 float64(0.75),
		"source_network_rate":                         float64(0.5),
		"source_discovery_only_rate":                  float64(0.25),
		"source_dynamic_partial_rate":                 float64(0.25),
		"session_search_context_hit_rate":             float64(0.5),
		"session_search_matched_terms_per_call":       float64(2),
		"avg_runtime_errors":                          float64(0.5),
		"avg_context_compactions":                     float64(0.5),
		"avg_context_removed_messages":                float64(16),
		"avg_context_summary_bytes":                   float64(1024),
		"avg_context_summary_missing":                 float64(0.5),
		"avg_context_summary_empty":                   float64(0.5),
		"avg_context_injections":                      float64(1.5),
		"avg_context_injection_bytes":                 float64(1800),
		"avg_context_injection_estimated_tokens":      float64(450),
		"avg_tool_calls":                              float64(2.5),
		"tool_context_truncation_rate":                float64(0.8),
		"tool_result_truncation_rate":                 float64(0.4),
		"duration_ms":                                 float64(2500),
		"avg_duration_ms":                             float64(1250),
		"tool_calls":                                  float64(5),
		"tool_errors":                                 float64(1),
		"tool_repaired":                               float64(3),
		"tool_name_canonicalized":                     float64(2),
		"tool_repair_calls":                           float64(4),
		"tool_repair_succeeded":                       float64(3),
		"tool_repair_failed":                          float64(1),
		"tool_repair_notes":                           float64(4),
		"loop_guard_interventions":                    float64(3),
		"forced_no_tools":                             float64(1),
		"source_access_results":                       float64(4),
		"source_access_verified":                      float64(3),
		"source_access_network":                       float64(2),
		"source_access_dynamic_partial":               float64(1),
		"memory_updates":                              float64(1),
		"memory_update_add":                           float64(1),
		"memory_search_calls":                         float64(2),
		"memory_search_misses":                        float64(1),
		"session_search_calls":                        float64(1),
		"session_search_results":                      float64(2),
		"session_search_context_hits":                 float64(1),
		"session_search_matched_terms":                float64(2),
		"tool_duration_ms":                            float64(120),
		"tool_context_truncated":                      float64(4),
		"tool_context_omitted_bytes":                  float64(12288),
		"tool_args_truncated":                         float64(1),
		"tool_args_omitted_bytes":                     float64(256),
		"tool_results_truncated":                      float64(2),
		"tool_results_omitted_bytes":                  float64(4096),
		"tool_result_artifacts":                       float64(2),
		"tool_context_artifacts":                      float64(1),
		"tool_context_missing_artifacts":              float64(1),
		"context_compaction_summary_missing":          float64(1),
		"context_compaction_summary_empty":            float64(1),
		"context_injections":                          float64(3),
		"context_injection_bytes":                     float64(3600),
		"context_injection_estimated_tokens":          float64(900),
		"verifier_runs":                               float64(2),
		"verifier_passed":                             float64(1),
		"verifier_failed":                             float64(1),
		"verifier_output_truncated":                   float64(1),
		"verifier_output_omitted_bytes":               float64(1024),
		"trace_event_rate":                            float64(1),
		"trace_event_scenarios":                       float64(2),
		"trace_events":                                float64(12),
		"input_tokens":                                float64(90),
		"output_tokens":                               float64(20),
		"avg_input_tokens":                            float64(45),
		"avg_output_tokens":                           float64(10),
		"avg_total_tokens":                            float64(55),
		"end_completed":                               float64(1),
		"end_max_turns":                               float64(1),
		"end_errors":                                  float64(0),
		"end_cancelled":                               float64(0),
		"end_unknown":                                 float64(0),
		"expectation_scenarios":                       float64(2),
		"removed_workspaces":                          float64(1),
		"cleanup_errors":                              float64(0),
		"focused_task_calls":                          float64(4),
		"focused_task_errors":                         float64(1),
		"subagent_calls":                              float64(2),
		"subagent_errors":                             float64(1),
		"plan_calls":                                  float64(3),
		"plan_errors":                                 float64(1),
		"loop_decisions":                              float64(1),
		"loop_protocol_feed_scenarios":                float64(1),
		"loop_protocol_feeds":                         float64(2),
		"loop_protocol_calibration_request_scenarios": float64(1),
		"loop_protocol_calibration_requests":          float64(1),
		"loop_protocol_calibration_scenarios":         float64(1),
		"loop_protocol_calibrations":                  float64(1),
		"runtime_surface_rate":                        float64(1),
		"runtime_surface_scenarios":                   float64(2),
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
	toolRepairExamples, ok := got["tool_repair_examples"].([]any)
	if !ok || len(toolRepairExamples) != 1 {
		t.Fatalf("tool_repair_examples = %#v\njson=%s", got["tool_repair_examples"], out.String())
	}
	toolRepairExample, ok := toolRepairExamples[0].(map[string]any)
	if !ok ||
		toolRepairExample["scenario"] != "taostats-rendered" ||
		toolRepairExample["call_id"] != "summary-repair-1" ||
		toolRepairExample["tool"] != "read_file" ||
		toolRepairExample["original_tool"] != "readFile" ||
		!jsonArrayContainsString(toolRepairExample["repair_kinds"], "tool_name") {
		t.Fatalf("tool_repair_example = %#v\njson=%s", toolRepairExamples[0], out.String())
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
	blockedExamples, ok := toolFailureExamples["blocked"].([]any)
	if !ok || len(blockedExamples) != 1 {
		t.Fatalf("blocked tool_failure_examples = %#v\njson=%s", toolFailureExamples["blocked"], out.String())
	}
	blockedExample, ok := blockedExamples[0].(map[string]any)
	if !ok || blockedExample["scenario"] != "taostats-rendered" {
		t.Fatalf("blocked tool_failure_example = %#v\njson=%s", blockedExamples[0], out.String())
	}
	loopGuardExamples, ok := got["loop_guard_examples"].([]any)
	if !ok || len(loopGuardExamples) != 1 {
		t.Fatalf("loop_guard_examples = %#v\njson=%s", got["loop_guard_examples"], out.String())
	}
	loopGuardExample, ok := loopGuardExamples[0].(map[string]any)
	if !ok ||
		loopGuardExample["scenario"] != "taostats-rendered" ||
		loopGuardExample["call_id"] != "summary-guard-1" ||
		loopGuardExample["kind"] != "loop_guard_repeated_failed_input" ||
		loopGuardExample["category"] != "loop_guard" ||
		!strings.Contains(fmt.Sprint(loopGuardExample["args_summary"]), "blocked.example") ||
		!strings.Contains(fmt.Sprint(loopGuardExample["guard_summary"]), "repeated failed input") ||
		!strings.Contains(fmt.Sprint(loopGuardExample["suggested_next_step"]), "another source") {
		t.Fatalf("loop_guard_example = %#v\njson=%s", loopGuardExamples[0], out.String())
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
	timeoutExamples, ok := runtimeErrorExamples["llm_timeout"].([]any)
	if !ok || len(timeoutExamples) != 1 {
		t.Fatalf("llm_timeout runtime_error_examples = %#v\njson=%s", runtimeErrorExamples["llm_timeout"], out.String())
	}
	timeoutExample, ok := timeoutExamples[0].(map[string]any)
	if !ok || timeoutExample["scenario"] != "taostats-rendered" {
		t.Fatalf("llm_timeout runtime_error_example = %#v\njson=%s", timeoutExamples[0], out.String())
	}
	failureExamples, ok := got["failure_examples"].(map[string]any)
	if !ok {
		t.Fatalf("failure_examples = %#v\njson=%s", got["failure_examples"], out.String())
	}
	turnEndExamples, ok := failureExamples["turn_end"].([]any)
	if !ok || len(turnEndExamples) != 1 {
		t.Fatalf("turn_end failure_examples = %#v\njson=%s", failureExamples["turn_end"], out.String())
	}
	turnEndExample, ok := turnEndExamples[0].(map[string]any)
	if !ok ||
		turnEndExample["scenario"] != "taostats-rendered" ||
		turnEndExample["failure"] != `turn ended with reason "max_turns" (expected completed)` ||
		turnEndExample["trace_path"] != "/tmp/affenteval/taostats-rendered/trace.jsonl" {
		t.Fatalf("turn_end failure example = %#v\njson=%s", turnEndExamples[0], out.String())
	}
	sourceAccessExamples, ok := got["source_access_examples"].([]any)
	if !ok || len(sourceAccessExamples) != 1 {
		t.Fatalf("source_access_examples = %#v\njson=%s", got["source_access_examples"], out.String())
	}
	sourceAccessExample, ok := sourceAccessExamples[0].(map[string]any)
	if !ok ||
		sourceAccessExample["scenario"] != "taostats-rendered" ||
		sourceAccessExample["call_id"] != "summary-source-1" ||
		sourceAccessExample["status"] != "network" ||
		sourceAccessExample["http_status"] != "200" ||
		sourceAccessExample["content_type"] != "application/json" ||
		sourceAccessExample["json_path"] != "$.price" ||
		sourceAccessExample["result_preview"] != `JSON_PATH: $.price "12.34"` {
		t.Fatalf("source_access_example = %#v\njson=%s", sourceAccessExamples[0], out.String())
	}
	browserScrollExamples, ok := got["browser_scroll_examples"].([]any)
	if !ok || len(browserScrollExamples) != 1 {
		t.Fatalf("browser_scroll_examples = %#v\njson=%s", got["browser_scroll_examples"], out.String())
	}
	browserScrollExample, ok := browserScrollExamples[0].(map[string]any)
	if !ok ||
		browserScrollExample["scenario"] != "taostats-rendered" ||
		browserScrollExample["call_id"] != "summary-scroll-1" ||
		browserScrollExample["url"] != "https://taostats.io/subnets/120" ||
		browserScrollExample["status"] != "boundary" ||
		browserScrollExample["movement"] != "none" ||
		browserScrollExample["boundary"] != "bottom" ||
		!strings.Contains(fmt.Sprint(browserScrollExample["suggested_next_step"]), "browser_network_read") {
		t.Fatalf("browser_scroll_example = %#v\njson=%s", browserScrollExamples[0], out.String())
	}
	browserNetworkExamples, ok := got["browser_network_examples"].([]any)
	if !ok || len(browserNetworkExamples) != 1 {
		t.Fatalf("browser_network_examples = %#v\njson=%s", got["browser_network_examples"], out.String())
	}
	browserNetworkExample, ok := browserNetworkExamples[0].(map[string]any)
	if !ok ||
		browserNetworkExample["scenario"] != "taostats-rendered" ||
		browserNetworkExample["call_id"] != "summary-network-1" ||
		browserNetworkExample["current_page_url"] != "https://taostats.io/subnets/120" ||
		browserNetworkExample["status"] != "matches" ||
		browserNetworkExample["requires_read"] != true ||
		!jsonArrayContainsString(browserNetworkExample["refs"], "n1") ||
		!jsonArrayContainsString(browserNetworkExample["previews"], `{"market_cap":"201.04K T"}`) {
		t.Fatalf("browser_network_example = %#v\njson=%s", browserNetworkExamples[0], out.String())
	}
	memoryUpdateExamples, ok := got["memory_update_examples"].([]any)
	if !ok || len(memoryUpdateExamples) != 1 {
		t.Fatalf("memory_update_examples = %#v\njson=%s", got["memory_update_examples"], out.String())
	}
	memoryUpdateExample, ok := memoryUpdateExamples[0].(map[string]any)
	if !ok ||
		memoryUpdateExample["scenario"] != "taostats-rendered" ||
		memoryUpdateExample["call_id"] != "summary-memory-1" ||
		memoryUpdateExample["action"] != "add" ||
		memoryUpdateExample["location"] != "memory:markets" ||
		!strings.Contains(fmt.Sprint(memoryUpdateExample["next_preview"]), "browser_network_read") {
		t.Fatalf("memory_update_example = %#v\njson=%s", memoryUpdateExamples[0], out.String())
	}
	sessionSearchExamples, ok := got["session_search_examples"].([]any)
	if !ok || len(sessionSearchExamples) != 1 {
		t.Fatalf("session_search_examples = %#v\njson=%s", got["session_search_examples"], out.String())
	}
	sessionSearchExample, ok := sessionSearchExamples[0].(map[string]any)
	if !ok ||
		sessionSearchExample["scenario"] != "taostats-rendered" ||
		sessionSearchExample["call_id"] != "summary-search-1" ||
		sessionSearchExample["query"] != "Alpha Coast" ||
		sessionSearchExample["session_id"] != "market-alpha" ||
		sessionSearchExample["message_idx"] != float64(8) ||
		sessionSearchExample["mod_time"] != "2026-05-27T12:00:00Z" ||
		sessionSearchExample["context_included"] != true ||
		!jsonArrayContainsString(sessionSearchExample["matched_terms"], "coast") {
		t.Fatalf("session_search_example = %#v\njson=%s", sessionSearchExamples[0], out.String())
	}
	toolTruncationExamples, ok := got["tool_truncation_examples"].([]any)
	if !ok || len(toolTruncationExamples) != 1 {
		t.Fatalf("tool_truncation_examples = %#v\njson=%s", got["tool_truncation_examples"], out.String())
	}
	toolTruncationExample, ok := toolTruncationExamples[0].(map[string]any)
	if !ok ||
		toolTruncationExample["scenario"] != "taostats-rendered" ||
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
	debugBriefTagExamples, ok := got["debug_brief_tag_examples"].(map[string]any)
	if !ok {
		t.Fatalf("debug_brief_tag_examples = %#v\njson=%s", got["debug_brief_tag_examples"], out.String())
	}
	blockedTagExamples, ok := debugBriefTagExamples["tool_failure:blocked"].([]any)
	if !ok || len(blockedTagExamples) != 1 {
		t.Fatalf("blocked debug_brief_tag_examples = %#v\njson=%s", debugBriefTagExamples["tool_failure:blocked"], out.String())
	}
	blockedTagExample, ok := blockedTagExamples[0].(map[string]any)
	if !ok ||
		blockedTagExample["scenario"] != "taostats-rendered" ||
		blockedTagExample["trace_path"] != "/tmp/affenteval/taostats-rendered/trace.jsonl" ||
		blockedTagExample["timeline_path"] != "/tmp/affenteval/taostats-rendered/affenteval-timeline.md" ||
		blockedTagExample["debug_manifest_path"] != "/tmp/affenteval/taostats-rendered/affenteval-debug.json" {
		t.Fatalf("blocked debug_brief_tag_example = %#v\njson=%s", blockedTagExamples[0], out.String())
	}
	expectationCapabilities, ok := got["expectation_capabilities"].(map[string]any)
	if !ok ||
		expectationCapabilities["browser"] != float64(2) ||
		expectationCapabilities["source_access"] != float64(2) ||
		expectationCapabilities["web"] != float64(1) {
		t.Fatalf("expectation_capabilities = %#v\njson=%s", got["expectation_capabilities"], out.String())
	}
	expectationCapabilityPassed, ok := got["expectation_capability_passed"].(map[string]any)
	if !ok ||
		expectationCapabilityPassed["browser"] != float64(1) ||
		expectationCapabilityPassed["source_access"] != float64(1) {
		t.Fatalf("expectation_capability_passed = %#v\njson=%s", got["expectation_capability_passed"], out.String())
	}
	expectationCapabilityFailed, ok := got["expectation_capability_failed"].(map[string]any)
	if !ok ||
		expectationCapabilityFailed["browser"] != float64(1) ||
		expectationCapabilityFailed["source_access"] != float64(1) ||
		expectationCapabilityFailed["web"] != float64(1) {
		t.Fatalf("expectation_capability_failed = %#v\njson=%s", got["expectation_capability_failed"], out.String())
	}
	expectationCapabilityRate, ok := got["expectation_capability_pass_rate"].(map[string]any)
	if !ok ||
		expectationCapabilityRate["browser"] != float64(0.5) ||
		expectationCapabilityRate["source_access"] != float64(0.5) ||
		expectationCapabilityRate["web"] != float64(0) {
		t.Fatalf("expectation_capability_pass_rate = %#v\njson=%s", got["expectation_capability_pass_rate"], out.String())
	}
	if got["expectation_capability_total"] != float64(5) ||
		got["expectation_capability_passed_total"] != float64(2) ||
		got["expectation_capability_failed_total"] != float64(3) ||
		got["expectation_capability_pass_rate_total"] != float64(0.4) {
		t.Fatalf("expectation capability totals not preserved: total=%#v passed=%#v failed=%#v rate=%#v\njson=%s",
			got["expectation_capability_total"],
			got["expectation_capability_passed_total"],
			got["expectation_capability_failed_total"],
			got["expectation_capability_pass_rate_total"],
			out.String(),
		)
	}
	expectationFailureExamples, ok := got["expectation_capability_failure_examples"].(map[string]any)
	if !ok {
		t.Fatalf("expectation_capability_failure_examples = %#v\njson=%s", got["expectation_capability_failure_examples"], out.String())
	}
	browserExamples, ok := expectationFailureExamples["browser"].([]any)
	if !ok || len(browserExamples) != 1 {
		t.Fatalf("browser expectation failure examples = %#v\njson=%s", expectationFailureExamples["browser"], out.String())
	}
	browserExample, ok := browserExamples[0].(map[string]any)
	if !ok ||
		browserExample["scenario"] != "taostats-rendered" ||
		browserExample["trace_path"] != "/tmp/affenteval/taostats-rendered/trace.jsonl" ||
		browserExample["timeline_path"] != "/tmp/affenteval/taostats-rendered/affenteval-timeline.md" {
		t.Fatalf("browser expectation failure example = %#v\njson=%s", browserExamples[0], out.String())
	}
	expectationTools, ok := got["expectation_required_tools"].(map[string]any)
	if !ok ||
		expectationTools["web_fetch"] != float64(1) ||
		expectationTools["browser_network_read"] != float64(1) {
		t.Fatalf("expectation_required_tools = %#v\njson=%s", got["expectation_required_tools"], out.String())
	}
	expectationSourceAccess, ok := got["expectation_source_access"].(map[string]any)
	if !ok || expectationSourceAccess["network"] != float64(2) {
		t.Fatalf("expectation_source_access = %#v\njson=%s", got["expectation_source_access"], out.String())
	}
	expectationSuites, ok := got["expectation_suites"].(map[string]any)
	if !ok || expectationSuites["long-run"] != float64(1) || expectationSuites["live-web"] != float64(1) {
		t.Fatalf("expectation_suites = %#v\njson=%s", got["expectation_suites"], out.String())
	}
	expectationDomains, ok := got["expectation_domains"].(map[string]any)
	if !ok || expectationDomains["bittensor"] != float64(1) || expectationDomains["web_evidence"] != float64(1) {
		t.Fatalf("expectation_domains = %#v\njson=%s", got["expectation_domains"], out.String())
	}
	expectationDomainPassed, ok := got["expectation_domain_passed"].(map[string]any)
	if !ok || expectationDomainPassed["bittensor"] != float64(1) {
		t.Fatalf("expectation_domain_passed = %#v\njson=%s", got["expectation_domain_passed"], out.String())
	}
	expectationDomainFailed, ok := got["expectation_domain_failed"].(map[string]any)
	if !ok || expectationDomainFailed["web_evidence"] != float64(1) {
		t.Fatalf("expectation_domain_failed = %#v\njson=%s", got["expectation_domain_failed"], out.String())
	}
	expectationDomainRate, ok := got["expectation_domain_pass_rate"].(map[string]any)
	if !ok ||
		expectationDomainRate["bittensor"] != float64(1) ||
		expectationDomainRate["web_evidence"] != float64(0) {
		t.Fatalf("expectation_domain_pass_rate = %#v\njson=%s", got["expectation_domain_pass_rate"], out.String())
	}
	if got["expectation_domain_total"] != float64(2) ||
		got["expectation_domain_passed_total"] != float64(1) ||
		got["expectation_domain_failed_total"] != float64(1) ||
		got["expectation_domain_pass_rate_total"] != float64(0.5) {
		t.Fatalf("expectation domain totals not preserved: total=%#v passed=%#v failed=%#v rate=%#v\njson=%s",
			got["expectation_domain_total"],
			got["expectation_domain_passed_total"],
			got["expectation_domain_failed_total"],
			got["expectation_domain_pass_rate_total"],
			out.String(),
		)
	}
	expectationDomainMetrics, ok := got["expectation_domain_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expectation_domain_metrics = %#v\njson=%s", got["expectation_domain_metrics"], out.String())
	}
	bittensorMetrics, ok := expectationDomainMetrics["bittensor"].(map[string]any)
	if !ok ||
		bittensorMetrics["scenarios"] != float64(1) ||
		bittensorMetrics["passed"] != float64(1) ||
		bittensorMetrics["pass_rate"] != float64(1) ||
		bittensorMetrics["avg_tool_calls"] != float64(2) ||
		bittensorMetrics["avg_total_tokens"] != float64(25) ||
		bittensorMetrics["source_access_verified_rate"] != float64(1) ||
		bittensorMetrics["source_network_rate"] != float64(1) ||
		bittensorMetrics["loop_guard_intervention_rate"] != float64(0.5) {
		t.Fatalf("bittensor expectation domain metrics = %#v\njson=%s", expectationDomainMetrics["bittensor"], out.String())
	}
	webEvidenceMetrics, ok := expectationDomainMetrics["web_evidence"].(map[string]any)
	if !ok ||
		webEvidenceMetrics["scenarios"] != float64(1) ||
		webEvidenceMetrics["failed"] != float64(1) ||
		webEvidenceMetrics["pass_rate"] != float64(0) ||
		webEvidenceMetrics["avg_runtime_errors"] != float64(3) ||
		webEvidenceMetrics["avg_total_tokens"] != float64(85) ||
		webEvidenceMetrics["tool_error_rate"] != float64(1.0/3.0) ||
		webEvidenceMetrics["source_access_verified_rate"] != float64(0.5) ||
		webEvidenceMetrics["source_network_rate"] != float64(0.5) {
		t.Fatalf("web_evidence expectation domain metrics = %#v\njson=%s", expectationDomainMetrics["web_evidence"], out.String())
	}
	expectationDomainFailureExamples, ok := got["expectation_domain_failure_examples"].(map[string]any)
	if !ok {
		t.Fatalf("expectation_domain_failure_examples = %#v\njson=%s", got["expectation_domain_failure_examples"], out.String())
	}
	webEvidenceExamples, ok := expectationDomainFailureExamples["web_evidence"].([]any)
	if !ok || len(webEvidenceExamples) != 1 {
		t.Fatalf("web_evidence expectation failure examples = %#v\njson=%s", expectationDomainFailureExamples["web_evidence"], out.String())
	}
	webEvidenceExample, ok := webEvidenceExamples[0].(map[string]any)
	if !ok ||
		webEvidenceExample["scenario"] != "taostats-rendered" ||
		webEvidenceExample["trace_path"] != "/tmp/affenteval/taostats-rendered/trace.jsonl" ||
		webEvidenceExample["timeline_path"] != "/tmp/affenteval/taostats-rendered/affenteval-timeline.md" {
		t.Fatalf("web_evidence expectation failure example = %#v\njson=%s", webEvidenceExamples[0], out.String())
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
	loopDecisionExample, ok := loopDecisionExamples[0].(map[string]any)
	if !ok || loopDecisionExample["scenario"] != "taostats-rendered" {
		t.Fatalf("loop_decision_example = %#v\njson=%s", loopDecisionExamples[0], out.String())
	}
	loopProtocolFeedByMode, ok := got["loop_protocol_feed_by_mode"].(map[string]any)
	if !ok || loopProtocolFeedByMode["digest"] != float64(1) || loopProtocolFeedByMode["full"] != float64(1) {
		t.Fatalf("loop_protocol_feed_by_mode = %#v\njson=%s", got["loop_protocol_feed_by_mode"], out.String())
	}
	loopProtocolFeedExamples, ok := got["loop_protocol_feed_examples"].([]any)
	if !ok || len(loopProtocolFeedExamples) != 1 {
		t.Fatalf("loop_protocol_feed_examples = %#v\njson=%s", got["loop_protocol_feed_examples"], out.String())
	}
	loopProtocolFeedExample, ok := loopProtocolFeedExamples[0].(map[string]any)
	if !ok ||
		loopProtocolFeedExample["scenario"] != "taostats-rendered" ||
		loopProtocolFeedExample["loop_id"] != "taostats-rendered" ||
		loopProtocolFeedExample["mode"] != "digest" ||
		loopProtocolFeedExample["feed_number"] != float64(4) ||
		loopProtocolFeedExample["plan_label"] != "plan:2/5:active" ||
		loopProtocolFeedExample["plan_current_step_index"] != float64(3) ||
		loopProtocolFeedExample["plan_current_step_status"] != "in_progress" ||
		loopProtocolFeedExample["plan_current_step"] != "read taostats network evidence" {
		t.Fatalf("loop_protocol_feed_example = %#v\njson=%s", loopProtocolFeedExamples[0], out.String())
	}
	contextCompactionExamples, ok := got["context_compaction_examples"].([]any)
	if !ok || len(contextCompactionExamples) != 1 {
		t.Fatalf("context_compaction_examples = %#v\njson=%s", got["context_compaction_examples"], out.String())
	}
	contextCompactionExample, ok := contextCompactionExamples[0].(map[string]any)
	if !ok ||
		contextCompactionExample["scenario"] != "taostats-rendered" ||
		contextCompactionExample["turn_id"] != "turn-summary-jsonl" ||
		contextCompactionExample["removed_messages"] != float64(44) ||
		contextCompactionExample["reason"] != "context_overflow" ||
		!strings.Contains(fmt.Sprint(contextCompactionExample["summary_preview"]), "JSONL summary evidence") {
		t.Fatalf("context_compaction_example = %#v\njson=%s", contextCompactionExamples[0], out.String())
	}
	contextInjectionBySource, ok := got["context_injection_by_source"].(map[string]any)
	if !ok || contextInjectionBySource["account_access"] != float64(1) || contextInjectionBySource["loop_protocol"] != float64(2) {
		t.Fatalf("context_injection_by_source = %#v\njson=%s", got["context_injection_by_source"], out.String())
	}
	contextInjectionExamples, ok := got["context_injection_examples"].([]any)
	if !ok || len(contextInjectionExamples) != 1 {
		t.Fatalf("context_injection_examples = %#v\njson=%s", got["context_injection_examples"], out.String())
	}
	contextInjectionExample, ok := contextInjectionExamples[0].(map[string]any)
	if !ok ||
		contextInjectionExample["scenario"] != "taostats-rendered" ||
		contextInjectionExample["turn_id"] != "turn-summary-jsonl" ||
		contextInjectionExample["source"] != "loop_protocol" ||
		contextInjectionExample["bytes"] != float64(1600) ||
		contextInjectionExample["estimated_tokens"] != float64(400) ||
		!strings.Contains(fmt.Sprint(contextInjectionExample["preview"]), "North Star") {
		t.Fatalf("context_injection_example = %#v\njson=%s", contextInjectionExamples[0], out.String())
	}
	planByAction, ok := got["plan_by_action"].(map[string]any)
	if !ok {
		t.Fatalf("plan_by_action missing or wrong type: %#v\njson=%s", got["plan_by_action"], out.String())
	}
	if planByAction["set"] != float64(1) || planByAction["update"] != float64(2) {
		t.Fatalf("plan_by_action = %#v", planByAction)
	}
	planExamples, ok := got["plan_examples"].([]any)
	if !ok || len(planExamples) != 1 {
		t.Fatalf("plan_examples = %#v\njson=%s", got["plan_examples"], out.String())
	}
	planExample, ok := planExamples[0].(map[string]any)
	if !ok ||
		planExample["scenario"] != "taostats-rendered" ||
		planExample["call_id"] != "summary-plan-1" ||
		planExample["action"] != "update" ||
		planExample["step_text"] != "verify browser evidence" ||
		!jsonArrayContainsString(planExample["evidence"], "go test ./cmd/affenteval") {
		t.Fatalf("plan_example = %#v\njson=%s", planExamples[0], out.String())
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
	minLoopProtocolFeedRate := 0.3
	minLoopProtocolCalibrationRequestRate := 0.4
	minLoopProtocolCalibrationRate := 0.5
	minRuntimeSurfaceRate := 0.9
	minTraceEventRate := 0.95
	minSourceNetworkRate := 0.5
	minSourceRate := 0.9
	minExpectationCapabilityPassRate := 0.7
	minEachExpectationCapabilityPassRate := 0.6
	minExpectationDomainPassRate := 0.65
	minEachExpectationDomainPassRate := 0.55
	minSessionSearchContextHitRate := 0.75
	minSessionSearchMatchedTermsPerCall := 1.25
	minToolRepairSuccessRate := 0.85
	minVerifierPassRate := 0.9
	maxFocusedTaskErrorRate := 0.07
	maxForcedNoToolsRate := 0.1
	maxLoopGuardInterventionRate := 0.15
	maxPlanErrorRate := 0.05
	maxMemorySearchMissRate := 0.35
	maxSourceDiscoveryOnlyRate := 0.1
	maxSourceDynamicPartialRate := 0.1
	maxSubagentErrorRate := 0.08
	maxToolErrorRate := 0.05
	maxToolResultTruncationRate := 0.2
	maxAvgRuntimeErrors := 0.05
	maxAvgContextCompactions := 0.1
	maxAvgReactiveContextCompactions := 0.2
	maxAvgContextRemovedMessages := 40.0
	maxAvgContextSummaryBytes := 16000.0
	maxAvgContextSummaryMissing := 0.0
	maxAvgContextSummaryEmpty := 0.0
	maxAvgContextInjections := 4.0
	maxAvgContextInjectionBytes := 12000.0
	maxAvgContextInjectionEstimatedTokens := 3000.0
	maxAvgToolCalls := 12.0
	maxAvgDurationMS := 90000.0
	maxAvgTotalTokens := 120000.0
	maxDebugBriefTagRates := map[string]float64{"source_dynamic_without_network": 0}
	minExpectationDomainVerifiedRates := map[string]float64{"web_evidence": 0.9}
	maxExpectationDomainAvgTotalTokens := map[string]float64{"bittensor": 90000}
	maxExpectationDomainAvgToolCalls := map[string]float64{"bittensor": 14}
	maxExpectationDomainAvgRuntimeErrors := map[string]float64{"web_evidence": 0.2}
	maxExpectationDomainToolErrorRates := map[string]float64{"code_pr": 0.05}
	maxExpectationDomainLoopGuardRates := map[string]float64{"market": 0.1}
	requiredExpectationCapabilities := []string{"delegated_source_evidence", "source_access"}
	requiredExpectationDomains := []string{"bittensor", "market"}
	meta = evalJSONLMetadataFromConfig(" custom ", " flag-model ", " flag-provider ", " sandbox ", " 0.4 ", " 0.9 ", " 512 ", " 42 ", true, " readonly_workspace,web ", true, true, true, true, true, " /tmp/mcp.json ", time.Second, " Web-Evidence ", qualityGateConfig{
		MinPassRate:                                    &minPassRate,
		MinMemoryUpdateRate:                            &minMemoryUpdateRate,
		MinLoopProtocolFeedRate:                        &minLoopProtocolFeedRate,
		MinLoopProtocolCalibrationRequestRate:          &minLoopProtocolCalibrationRequestRate,
		MinLoopProtocolCalibrationRate:                 &minLoopProtocolCalibrationRate,
		MinRuntimeSurfaceRate:                          &minRuntimeSurfaceRate,
		MinTraceEventRate:                              &minTraceEventRate,
		MinSourceNetworkRate:                           &minSourceNetworkRate,
		MinSourceAccessVerifiedRate:                    &minSourceRate,
		MinExpectationCapabilityPassRate:               &minExpectationCapabilityPassRate,
		MinEachExpectationCapabilityPassRate:           &minEachExpectationCapabilityPassRate,
		MinExpectationDomainPassRate:                   &minExpectationDomainPassRate,
		MinEachExpectationDomainPassRate:               &minEachExpectationDomainPassRate,
		MinSessionSearchContextHitRate:                 &minSessionSearchContextHitRate,
		MinSessionSearchMatchedTermsPerCall:            &minSessionSearchMatchedTermsPerCall,
		MinToolRepairSuccessRate:                       &minToolRepairSuccessRate,
		MinVerifierPassRate:                            &minVerifierPassRate,
		MaxFocusedTaskErrorRate:                        &maxFocusedTaskErrorRate,
		MaxForcedNoToolsRate:                           &maxForcedNoToolsRate,
		MaxLoopGuardInterventionRate:                   &maxLoopGuardInterventionRate,
		MaxPlanErrorRate:                               &maxPlanErrorRate,
		MaxMemorySearchMissRate:                        &maxMemorySearchMissRate,
		MaxSourceDiscoveryOnlyRate:                     &maxSourceDiscoveryOnlyRate,
		MaxSourceDynamicPartialRate:                    &maxSourceDynamicPartialRate,
		MaxSubagentErrorRate:                           &maxSubagentErrorRate,
		MaxToolErrorRate:                               &maxToolErrorRate,
		MaxToolResultTruncationRate:                    &maxToolResultTruncationRate,
		MaxAvgRuntimeErrors:                            &maxAvgRuntimeErrors,
		MaxAvgContextCompactions:                       &maxAvgContextCompactions,
		MaxAvgReactiveCompactions:                      &maxAvgReactiveContextCompactions,
		MaxAvgContextRemovedMessages:                   &maxAvgContextRemovedMessages,
		MaxAvgContextSummaryBytes:                      &maxAvgContextSummaryBytes,
		MaxAvgContextSummaryMissing:                    &maxAvgContextSummaryMissing,
		MaxAvgContextSummaryEmpty:                      &maxAvgContextSummaryEmpty,
		MaxAvgContextInjections:                        &maxAvgContextInjections,
		MaxAvgContextInjectionBytes:                    &maxAvgContextInjectionBytes,
		MaxAvgContextInjectionEstimatedTokens:          &maxAvgContextInjectionEstimatedTokens,
		MaxAvgToolCalls:                                &maxAvgToolCalls,
		MaxAvgDurationMS:                               &maxAvgDurationMS,
		MaxAvgTotalTokens:                              &maxAvgTotalTokens,
		MaxDebugBriefTagRates:                          maxDebugBriefTagRates,
		MinExpectationDomainSourceAccessVerifiedRates:  minExpectationDomainVerifiedRates,
		MaxExpectationDomainAvgTotalTokens:             maxExpectationDomainAvgTotalTokens,
		MaxExpectationDomainAvgToolCalls:               maxExpectationDomainAvgToolCalls,
		MaxExpectationDomainAvgRuntimeErrors:           maxExpectationDomainAvgRuntimeErrors,
		MaxExpectationDomainToolErrorRates:             maxExpectationDomainToolErrorRates,
		MaxExpectationDomainLoopGuardInterventionRates: maxExpectationDomainLoopGuardRates,
		RequiredExpectationCapabilities:                requiredExpectationCapabilities,
		RequiredExpectationDomains:                     requiredExpectationDomains,
	})
	if meta.Model != "flag-model" || meta.ProviderLabel != "flag-provider" || meta.Executor != "sandbox" || meta.Temperature != "0.4" || meta.TopP != "0.9" || meta.MaxTokens != "512" || meta.Seed != "42" || meta.Suite != "custom" || !meta.RuntimeEvalMode || meta.RuntimeTools != "readonly_workspace,web" || !meta.RuntimeAllTools || !meta.RuntimeMemory || !meta.RuntimeWeb || !meta.RuntimeBrowser || !meta.TraceDeltas || !meta.RuntimeMCP || meta.TimeoutMS != 1000 || meta.QualityProfile != "web-evidence" {
		t.Fatalf("flag metadata not normalized: %+v", meta)
	}
	if meta.MinPassRate == nil || *meta.MinPassRate != 0.8 || meta.MinMemoryUpdateRate == nil || *meta.MinMemoryUpdateRate != 0.2 || meta.MinLoopProtocolFeedRate == nil || *meta.MinLoopProtocolFeedRate != 0.3 || meta.MinLoopProtocolCalibrationRequestRate == nil || *meta.MinLoopProtocolCalibrationRequestRate != 0.4 || meta.MinLoopProtocolCalibrationRate == nil || *meta.MinLoopProtocolCalibrationRate != 0.5 || meta.MinRuntimeSurfaceRate == nil || *meta.MinRuntimeSurfaceRate != 0.9 || meta.MinTraceEventRate == nil || *meta.MinTraceEventRate != 0.95 || meta.MinSourceNetworkRate == nil || *meta.MinSourceNetworkRate != 0.5 || meta.MinSourceAccessVerifiedRate == nil || *meta.MinSourceAccessVerifiedRate != 0.9 || meta.MinExpectationCapabilityPassRate == nil || *meta.MinExpectationCapabilityPassRate != 0.7 || meta.MinEachExpectationCapabilityPassRate == nil || *meta.MinEachExpectationCapabilityPassRate != 0.6 || meta.MinExpectationDomainPassRate == nil || *meta.MinExpectationDomainPassRate != 0.65 || meta.MinEachExpectationDomainPassRate == nil || *meta.MinEachExpectationDomainPassRate != 0.55 || meta.MinSessionSearchContextHitRate == nil || *meta.MinSessionSearchContextHitRate != 0.75 || meta.MinSessionSearchMatchedTermsPerCall == nil || *meta.MinSessionSearchMatchedTermsPerCall != 1.25 || meta.MinToolRepairSuccessRate == nil || *meta.MinToolRepairSuccessRate != 0.85 || meta.MinVerifierPassRate == nil || *meta.MinVerifierPassRate != 0.9 || meta.MaxFocusedTaskErrorRate == nil || *meta.MaxFocusedTaskErrorRate != 0.07 || meta.MaxForcedNoToolsRate == nil || *meta.MaxForcedNoToolsRate != 0.1 || meta.MaxLoopGuardInterventionRate == nil || *meta.MaxLoopGuardInterventionRate != 0.15 || meta.MaxPlanErrorRate == nil || *meta.MaxPlanErrorRate != 0.05 || meta.MaxMemorySearchMissRate == nil || *meta.MaxMemorySearchMissRate != 0.35 || meta.MaxSourceDiscoveryOnlyRate == nil || *meta.MaxSourceDiscoveryOnlyRate != 0.1 || meta.MaxSourceDynamicPartialRate == nil || *meta.MaxSourceDynamicPartialRate != 0.1 || meta.MaxSubagentErrorRate == nil || *meta.MaxSubagentErrorRate != 0.08 || meta.MaxToolErrorRate == nil || *meta.MaxToolErrorRate != 0.05 || meta.MaxToolResultTruncationRate == nil || *meta.MaxToolResultTruncationRate != 0.2 || meta.MaxAvgRuntimeErrors == nil || *meta.MaxAvgRuntimeErrors != 0.05 || meta.MaxAvgContextCompactions == nil || *meta.MaxAvgContextCompactions != 0.1 || meta.MaxAvgReactiveCompactions == nil || *meta.MaxAvgReactiveCompactions != 0.2 || meta.MaxAvgContextRemovedMessages == nil || *meta.MaxAvgContextRemovedMessages != 40 || meta.MaxAvgContextSummaryBytes == nil || *meta.MaxAvgContextSummaryBytes != 16000 || meta.MaxAvgContextSummaryMissing == nil || *meta.MaxAvgContextSummaryMissing != 0 || meta.MaxAvgContextSummaryEmpty == nil || *meta.MaxAvgContextSummaryEmpty != 0 || meta.MaxAvgContextInjections == nil || *meta.MaxAvgContextInjections != 4 || meta.MaxAvgContextInjectionBytes == nil || *meta.MaxAvgContextInjectionBytes != 12000 || meta.MaxAvgContextInjectionEstimatedTokens == nil || *meta.MaxAvgContextInjectionEstimatedTokens != 3000 || meta.MaxAvgToolCalls == nil || *meta.MaxAvgToolCalls != 12 || meta.MaxAvgDurationMS == nil || *meta.MaxAvgDurationMS != 90000 || meta.MaxAvgTotalTokens == nil || *meta.MaxAvgTotalTokens != 120000 {
		t.Fatalf("quality gate metadata not preserved: %+v", meta)
	}
	if !reflect.DeepEqual(meta.MaxDebugBriefTagRates, maxDebugBriefTagRates) {
		t.Fatalf("debug brief tag gate metadata = %#v, want %#v", meta.MaxDebugBriefTagRates, maxDebugBriefTagRates)
	}
	if !reflect.DeepEqual(meta.MinExpectationDomainSourceAccessVerifiedRates, minExpectationDomainVerifiedRates) ||
		!reflect.DeepEqual(meta.MaxExpectationDomainAvgTotalTokens, maxExpectationDomainAvgTotalTokens) ||
		!reflect.DeepEqual(meta.MaxExpectationDomainAvgToolCalls, maxExpectationDomainAvgToolCalls) ||
		!reflect.DeepEqual(meta.MaxExpectationDomainAvgRuntimeErrors, maxExpectationDomainAvgRuntimeErrors) ||
		!reflect.DeepEqual(meta.MaxExpectationDomainToolErrorRates, maxExpectationDomainToolErrorRates) ||
		!reflect.DeepEqual(meta.MaxExpectationDomainLoopGuardInterventionRates, maxExpectationDomainLoopGuardRates) {
		t.Fatalf("expectation domain metric gate metadata not preserved: %+v", meta)
	}
	if !reflect.DeepEqual(meta.RequiredExpectationCapabilities, requiredExpectationCapabilities) {
		t.Fatalf("required expectation capability metadata = %#v, want %#v", meta.RequiredExpectationCapabilities, requiredExpectationCapabilities)
	}
	if !reflect.DeepEqual(meta.RequiredExpectationDomains, requiredExpectationDomains) {
		t.Fatalf("required expectation domain metadata = %#v, want %#v", meta.RequiredExpectationDomains, requiredExpectationDomains)
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
		`scenario "loop-draft" requires loop protocol feeds but active protocol file .affent/loops/loop-draft/LOOP.md has status "draft", want running`,
	})
	if got["turn_end"] != 1 ||
		got["missing_command"] != 2 ||
		got["delegation_error"] != 1 ||
		got["missing_focused_task"] != 1 ||
		got["missing_subagent"] != 1 ||
		got["skill_install_guard"] != 1 ||
		got["llm_timeout"] != 2 ||
		got["llm_incomplete_stream"] != 1 ||
		got["context_overflow"] != 1 ||
		got["loop_protocol_fixture"] != 1 {
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
		{kind: "loop_protocol_activation_status", want: "metadata status was running"},
		{kind: "loop_protocol_activation_invalid", want: "keep Current Situation compact"},
		{kind: "loop_protocol_activation_unready", want: "no user calibration answer"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			if got := toolFailureKindHint(c.kind); !strings.Contains(got, c.want) {
				t.Fatalf("toolFailureKindHint(%q) = %q, want contains %q", c.kind, got, c.want)
			}
		})
	}
}

func TestFailureKindHintIncludesLoopProtocolFixtureRecovery(t *testing.T) {
	got := failureKindHint("loop_protocol_fixture")
	for _, want := range []string{"LOOP.md fixture", "missing", "non-running", "unreadable state"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failureKindHint(loop_protocol_fixture) = %q, want %q", got, want)
		}
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
		{`affentctl run failed: exit=1 err=launch chromium: /chrome: error while loading shared libraries: libglib-2.0.so.0: cannot open shared object file
Failure: kind=browser_launch_failed`, "browser_launch_failed"},
		{`scenario "loop-missing" requires loop protocol feeds but active protocol file .affent/loops/loop-missing/LOOP.md is missing`, "loop_protocol_fixture"},
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
