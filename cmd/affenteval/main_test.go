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
)

func TestRunListSuites(t *testing.T) {
	out, code := captureStdout(t, func() int {
		return run([]string{"--list-suites"})
	})
	if code != 0 {
		t.Fatalf("run --list-suites exit = %d", code)
	}
	for _, want := range []string{"hard-agent", "small-model-tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--list-suites output missing %q:\n%s", want, out)
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

func TestPrintBatchResultIncludesTraceMetrics(t *testing.T) {
	var out bytes.Buffer
	printBatchResult(&out, agenteval.BatchResult{
		BatchScenario:    "sample",
		Workspace:        "/tmp/ws",
		TracePath:        "/tmp/ws/trace.jsonl",
		OK:               true,
		Duration:         1234 * time.Millisecond,
		TurnEndReason:    "completed",
		ToolCalls:        3,
		WorkspaceRemoved: true,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:       1,
			ToolNameCanonicalized:  1,
			ToolErrors:             2,
			ToolFailureByKind:      map[string]int{"invalid_args": 1},
			ToolDurationMS:         45,
			LoopGuardInterventions: 2,
			ForcedNoTools:          1,
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
		"metrics: tools=3 errors=2 repaired=1 canonicalized=1 loop_guard=2 forced_no_tools=1 tool_ms=45 tokens=100/25 trunc=args:1,results:1,artifacts:1 omitted=512/4096 tool_failure_kinds=invalid_args:1 runtime_error_kinds=llm_timeout:1 delegation=focused_tasks:2,subagents:1 delegation_errors=focused_tasks:1,subagents:1 focused_task_by_type=explore:1,verify:1 subagent_by_mode=review:1 plan=calls:3,errors:1 plan_by_action=set:1,update:2 end=completed",
		`verifier: pass exit=0 duration=80ms output=1200 truncated omitted=176 cap=1024 command="go test ./..."`,
		"tool_failure_hint[invalid_args]",
		"invalid arguments",
		`tool_failure_example[invalid_args]: tool=web_fetch args=url="https://example.com" exit=1 result=url is required | Next: retry with a full URL`,
		"hint[llm_timeout]",
		"runtime_error_example[llm_timeout]: LLM llm_stream timed out after 4m0s",
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

func TestBatchSummaryAggregatesRuntimeMetrics(t *testing.T) {
	var summary batchSummary
	summary.add(agenteval.BatchResult{
		OK:                 true,
		Duration:           100 * time.Millisecond,
		ToolCalls:          2,
		WorkspaceRemoved:   true,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:       1,
			ToolNameCanonicalized:  1,
			ToolErrors:             0,
			ToolDurationMS:         10,
			LoopGuardInterventions: 1,
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
		Plan: agenteval.PlanStats{
			Calls:    1,
			ByAction: map[string]int{"set": 1},
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
			ToolArgsRepaired:       2,
			ToolNameCanonicalized:  1,
			ToolErrors:             1,
			ToolFailureByKind:      map[string]int{"invalid_args": 1, "timeout": 2},
			ToolDurationMS:         40,
			LoopGuardInterventions: 2,
			ForcedNoTools:          1,
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
	if !strings.Contains(out.String(), "repair_kinds=alias_rename:2,tool_name:1,type_coercion:2") {
		t.Fatalf("summary output missing repair kind rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "tool_failure_kinds=invalid_args:1,timeout:2") {
		t.Fatalf("summary output missing tool failure kind rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "runtime_error_kinds=context_overflow:1,llm_timeout:2") {
		t.Fatalf("summary output missing runtime error kind rollup:\n%s", out.String())
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
	if !strings.Contains(out.String(), "repair_calls=5,ok=4,failed=1") {
		t.Fatalf("summary output missing repair outcome rollup:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "plan=calls:3,errors:1 plan_by_action=set:1,update:2") {
		t.Fatalf("summary output missing plan rollup:\n%s", out.String())
	}
	if summary.TraceSchemaVersions[1] != 2 {
		t.Fatalf("TraceSchemaVersions = %#v, want version 1 count 2", summary.TraceSchemaVersions)
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
	if got := summary.ToolFailureExamples["timeout"]; len(got) != 1 || got[0].Tool != "web_fetch" {
		t.Fatalf("ToolFailureExamples[timeout] = %#v", got)
	}
	if got := summary.RuntimeErrorExamples["llm_timeout"]; len(got) != 1 || !strings.Contains(got[0].Message, "llm_stream timed out") {
		t.Fatalf("RuntimeErrorExamples[llm_timeout] = %#v", got)
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
	printBatchResultJSONL(&out, testEvalJSONLMetadata(), agenteval.BatchResult{
		BatchScenario:      "sample",
		Workspace:          "/tmp/ws",
		TracePath:          "/tmp/ws/trace.jsonl",
		OK:                 true,
		Duration:           1500 * time.Millisecond,
		TraceSchemaVersion: 1,
		TurnEndReason:      "completed",
		ToolCalls:          4,
		WorkspaceRemoved:   true,
		ToolStats: agenteval.ToolRuntimeStats{
			ToolArgsRepaired:       2,
			ToolNameCanonicalized:  1,
			ToolErrors:             1,
			ToolFailureByKind:      map[string]int{"blocked": 1},
			ToolDurationMS:         75,
			LoopGuardInterventions: 3,
			ForcedNoTools:          1,
		},
		ToolFailureExamples: map[string][]agenteval.ToolFailureExample{
			"blocked": {
				{Kind: "blocked", Tool: "web_fetch", ArgsSummary: `url="https://blocked.example/metrics"`, ResultSummary: "HTTP 403 | Next: use another source", ExitCode: 1},
			},
		},
		RuntimeErrorByKind: map[string]int{"llm_incomplete_stream": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_incomplete_stream": {
				{Kind: "llm_incomplete_stream", Message: "LLM llm_stream ended with an incomplete SSE stream before finish_reason"},
			},
		},
		ToolTruncation: agenteval.ToolTruncationStats{
			ArgsTruncated:       2,
			ArgsOmittedBytes:    1024,
			ResultsTruncated:    1,
			ResultsOmittedBytes: 8192,
			ResultArtifacts:     1,
		},
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
		"schema_version":                float64(1),
		"type":                          "scenario",
		"suite":                         "small-model-tools",
		"model":                         "eval-model",
		"provider_label":                "eval-provider",
		"executor":                      "docker:affent-eval",
		"temperature":                   "0.2",
		"top_p":                         "0.9",
		"max_tokens":                    "512",
		"seed":                          "42",
		"timeout_ms":                    float64(300000),
		"scenario":                      "sample",
		"ok":                            true,
		"duration_ms":                   float64(1500),
		"trace_schema_version":          float64(1),
		"turn_end_reason":               "completed",
		"tool_calls":                    float64(4),
		"tool_errors":                   float64(1),
		"tool_repaired":                 float64(2),
		"tool_name_canonicalized":       float64(1),
		"tool_repair_calls":             float64(2),
		"tool_repair_succeeded":         float64(1),
		"tool_repair_failed":            float64(1),
		"tool_repair_notes":             float64(3),
		"loop_guard_interventions":      float64(3),
		"forced_no_tools":               float64(1),
		"tool_duration_ms":              float64(75),
		"tool_args_truncated":           float64(2),
		"tool_args_omitted_bytes":       float64(1024),
		"tool_results_truncated":        float64(1),
		"tool_results_omitted_bytes":    float64(8192),
		"tool_result_artifacts":         float64(1),
		"verifier_command":              "go test ./...",
		"verifier_ran":                  true,
		"verifier_ok":                   false,
		"verifier_exit_code":            float64(1),
		"verifier_duration_ms":          float64(25),
		"verifier_output_bytes":         float64(2048),
		"verifier_output_truncated":     true,
		"verifier_output_omitted_bytes": float64(1024),
		"verifier_output_cap_bytes":     float64(1024),
		"input_tokens":                  float64(200),
		"output_tokens":                 float64(50),
		"workspace_removed":             true,
		"plan_calls":                    float64(2),
		"plan_errors":                   float64(1),
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
	toolFailureKinds, ok := got["tool_failure_by_kind"].(map[string]any)
	if !ok || toolFailureKinds["blocked"] != float64(1) {
		t.Fatalf("tool_failure_by_kind = %#v\njson=%s", got["tool_failure_by_kind"], out.String())
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
	printBatchSummaryJSONL(&out, testEvalJSONLMetadata(), summary)
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, out.String())
	}
	if got["focused_task_calls"] != float64(4) {
		t.Errorf("summary.focused_task_calls = %#v, want 4", got["focused_task_calls"])
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
	printBatchSummaryJSONL(&out, testEvalJSONLMetadata(), batchSummary{
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
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
		RuntimeErrorExamples: map[string][]agenteval.RuntimeErrorExample{
			"llm_timeout": {
				{Kind: "llm_timeout", Message: "LLM llm_stream timed out after 4m0s"},
			},
		},
		LoopGuardInterventions:     3,
		ForcedNoTools:              1,
		ToolDurationMS:             120,
		ToolArgsTruncated:          1,
		ToolArgsOmittedBytes:       256,
		ToolResultsTruncated:       2,
		ToolResultsOmittedBytes:    4096,
		ToolResultArtifacts:        2,
		VerifierRuns:               2,
		VerifierPassed:             1,
		VerifierFailed:             1,
		VerifierOutputTruncated:    1,
		VerifierOutputOmittedBytes: 1024,
		TraceSchemaVersions:        map[int]int{1: 2},
		InputTokens:                90,
		OutputTokens:               20,
		EndCompleted:               1,
		EndMaxTurns:                1,
		EndErrors:                  0,
		EndCancelled:               0,
		EndUnknown:                 0,
		FailureKinds:               map[string]int{"missing_command": 1, "turn_end": 1},
		RemovedWorkspaces:          1,
		PlanCalls:                  3,
		PlanByAction:               map[string]int{"set": 1, "update": 2},
		PlanErrors:                 1,
	})

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("jsonl summary did not decode: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{
		"schema_version":                float64(1),
		"type":                          "summary",
		"suite":                         "small-model-tools",
		"model":                         "eval-model",
		"provider_label":                "eval-provider",
		"executor":                      "docker:affent-eval",
		"temperature":                   "0.2",
		"top_p":                         "0.9",
		"max_tokens":                    "512",
		"seed":                          "42",
		"timeout_ms":                    float64(300000),
		"scenarios":                     float64(2),
		"passed":                        float64(1),
		"failed":                        float64(1),
		"duration_ms":                   float64(2500),
		"tool_calls":                    float64(5),
		"tool_errors":                   float64(1),
		"tool_repaired":                 float64(3),
		"tool_name_canonicalized":       float64(2),
		"tool_repair_calls":             float64(4),
		"tool_repair_succeeded":         float64(3),
		"tool_repair_failed":            float64(1),
		"tool_repair_notes":             float64(4),
		"loop_guard_interventions":      float64(3),
		"forced_no_tools":               float64(1),
		"tool_duration_ms":              float64(120),
		"tool_args_truncated":           float64(1),
		"tool_args_omitted_bytes":       float64(256),
		"tool_results_truncated":        float64(2),
		"tool_results_omitted_bytes":    float64(4096),
		"tool_result_artifacts":         float64(2),
		"verifier_runs":                 float64(2),
		"verifier_passed":               float64(1),
		"verifier_failed":               float64(1),
		"verifier_output_truncated":     float64(1),
		"verifier_output_omitted_bytes": float64(1024),
		"input_tokens":                  float64(90),
		"output_tokens":                 float64(20),
		"end_completed":                 float64(1),
		"end_max_turns":                 float64(1),
		"end_errors":                    float64(0),
		"end_cancelled":                 float64(0),
		"end_unknown":                   float64(0),
		"removed_workspaces":            float64(1),
		"cleanup_errors":                float64(0),
		"plan_calls":                    float64(3),
		"plan_errors":                   float64(1),
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %v\njson=%s", key, got[key], want, out.String())
		}
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
	planByAction, ok := got["plan_by_action"].(map[string]any)
	if !ok {
		t.Fatalf("plan_by_action missing or wrong type: %#v\njson=%s", got["plan_by_action"], out.String())
	}
	if planByAction["set"] != float64(1) || planByAction["update"] != float64(2) {
		t.Fatalf("plan_by_action = %#v", planByAction)
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
	meta := evalJSONLMetadataFromConfig("small-model-tools", "", "", "", "0", "", "", "", false, false, "", 5*time.Minute)
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

	meta = evalJSONLMetadataFromConfig(" custom ", " flag-model ", " flag-provider ", " sandbox ", " 0.4 ", " 0.9 ", " 512 ", " 42 ", true, true, " /tmp/mcp.json ", time.Second)
	if meta.Model != "flag-model" || meta.ProviderLabel != "flag-provider" || meta.Executor != "sandbox" || meta.Temperature != "0.4" || meta.TopP != "0.9" || meta.MaxTokens != "512" || meta.Seed != "42" || meta.Suite != "custom" || !meta.RuntimeEvalMode || !meta.RuntimeMemory || !meta.RuntimeMCP || meta.TimeoutMS != 1000 {
		t.Fatalf("flag metadata not normalized: %+v", meta)
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
		{kind: "loop_guard_repeated_call", want: "repeated identical tool arguments"},
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
