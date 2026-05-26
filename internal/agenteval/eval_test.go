package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrimOneLine_CompactsWhitespaceAndTruncates(t *testing.T) {
	got := trimOneLine("  hello \n\t world  "+strings.Repeat("界", 200), 12)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("trimOneLine should compact whitespace, got %q", got)
	}
	if !strings.HasPrefix(got, "hello world") {
		t.Fatalf("trimOneLine lost leading content: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("trimOneLine should append ellipsis when truncated, got %q", got)
	}
}

func TestCheckTraceFlagsProcessRegressions(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "shell", Args: map[string]any{"command": "python -m pytest 2>&1 | head -80"}},
		{Tool: "edit_file", Args: map[string]any{"path": "test_slug.py"}},
	}}
	scenario := BatchScenario{
		RequiredCommands:  []string{`python(3)? -m pytest`},
		ForbiddenCommands: []string{"| head"},
		ProtectedFiles:    []string{"test_slug.py"},
	}
	failures := CheckBatchTrace(trace, scenario)
	joined := strings.Join(failures, "\n")
	for _, want := range []string{"forbidden command substring", "modified protected file"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in failures:\n%s", want, joined)
		}
	}
}

func TestCheckTraceIgnoresGuardRejectedForbiddenCommand(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			Tool:     "shell",
			Args:     map[string]any{"command": "python -m pytest 2>&1 | head -80"},
			ExitCode: 1,
			IsErr:    true,
			Result:   "Error: shell command masks a test/build exit code",
		},
		{
			Tool: "shell",
			Args: map[string]any{"command": "python -m pytest"},
		},
	}, TurnEndReason: "completed"}
	scenario := BatchScenario{
		RequiredCommands:  []string{`python(3)? -m pytest`},
		ForbiddenCommands: []string{"| head"},
	}
	if failures := CheckBatchTrace(trace, scenario); len(failures) != 0 {
		t.Fatalf("guard-rejected command should not fail batch eval: %v", failures)
	}
}

func TestParseTraceFileReadsToolRequestsAndFinalText(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	body := strings.Join([]string{
		`{"type":"trace.meta","data":{"schema_version":1}}`,
		`{"type":"tool.request","data":{"call_id":"c1","tool":"shell","args":{"command":"go test ./..."},"args_truncated":true,"args_bytes":70000,"args_omitted_bytes":512,"args_cap_bytes":65536,"original_tool":"Shell","original_args_summary":"{\"cmd\":\"go test ./...\"}","canonicalized":true,"args_repaired":true,"repair_notes":["renamed tool","renamed field"]}}`,
		`{"type":"tool.result","data":{"call_id":"c1","result":"ok","exit_code":0,"duration_ms":17,"result_truncated":true,"result_bytes":300000,"result_omitted_bytes":4096,"result_cap_bytes":262144,"result_artifact_path":".affent/artifacts/tool-results/000001-c1.txt"}}`,
		`{"type":"tool.result","data":{"call_id":"guarded","result":"blocked\nFailure: kind=invalid_args","exit_code":1}}`,
		`{"type":"usage","data":{"input_tokens":11,"output_tokens":7}}`,
		`{"type":"error","data":{"message":"transient stream warning","failure_kind":"llm_timeout"}}`,
		`{"type":"message.done","data":{"text":"Conclusion: green","finish_reason":"stop"}}`,
		`{"type":"turn.end","data":{"reason":"completed","tool_stats":{"tool_requests":2,"tool_name_canonicalized":1,"tool_args_repaired":1,"tool_repair_calls":1,"tool_repair_succeeded":1,"tool_repair_failed":0,"tool_repair_notes":2,"tool_repair_by_kind":{"tool_name":1,"alias_rename":1},"tool_failure_by_kind":{"invalid_args":1},"tool_errors":1,"tool_duration_ms":17,"loop_guard_interventions":1,"forced_no_tools":1,"source_access_dynamic_partial":1,"memory_updates":2,"memory_update_add":1,"memory_update_replace":1,"tool_context_truncated":2,"tool_context_omitted_bytes":8192}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if trace.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", trace.SchemaVersion)
	}
	if len(trace.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(trace.Tools))
	}
	tc := trace.Tools[0]
	if tc.Tool != "shell" || tc.Args["command"] != "go test ./..." {
		t.Fatalf("first tool call wrong: %+v", tc)
	}
	if !tc.Canonicalized || !tc.ArgsRepaired || tc.OriginalTool != "Shell" || !strings.Contains(tc.OriginalArgsSummary, "cmd") || len(tc.RepairNotes) != 2 {
		t.Fatalf("tool repair metadata missing: %+v", tc)
	}
	if !tc.ArgsTruncated || tc.ArgsBytes != 70000 || tc.ArgsOmittedBytes != 512 || tc.ArgsCapBytes != 65536 {
		t.Fatalf("tool request truncation metadata not parsed: %+v", tc)
	}
	if tc.Result != "ok" || tc.ExitCode != 0 || tc.IsErr {
		t.Fatalf("tool result not stitched into request: %+v", tc)
	}
	if tc.DurationMS != 17 {
		t.Fatalf("tool duration not parsed: %+v", tc)
	}
	if !tc.ResultTruncated || tc.ResultBytes != 300000 || tc.ResultOmittedBytes != 4096 || tc.ResultCapBytes != 262144 {
		t.Fatalf("tool result truncation metadata not parsed: %+v", tc)
	}
	if tc.ResultArtifactPath != ".affent/artifacts/tool-results/000001-c1.txt" {
		t.Fatalf("ResultArtifactPath = %q", tc.ResultArtifactPath)
	}
	if stats := SummarizeToolTruncation(trace); stats.ArgsTruncated != 1 || stats.ArgsOmittedBytes != 512 || stats.ResultsTruncated != 1 || stats.ResultsOmittedBytes != 4096 || stats.ResultArtifacts != 1 {
		t.Fatalf("ToolTruncationStats = %+v", stats)
	}
	if guarded := trace.Tools[1]; guarded.CallID != "guarded" || !guarded.IsErr || guarded.ExitCode != 1 {
		t.Fatalf("unmatched error tool result not recorded: %+v", guarded)
	}
	if trace.Tools[1].FailureKind != "invalid_args" {
		t.Fatalf("unmatched error FailureKind = %q, want invalid_args", trace.Tools[1].FailureKind)
	}
	if trace.Usage.InputTokens != 11 || trace.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", trace.Usage)
	}
	if len(trace.LoopErrors) != 1 || !strings.Contains(trace.LoopErrors[0], "transient stream warning") {
		t.Fatalf("LoopErrors = %+v", trace.LoopErrors)
	}
	if len(trace.LoopErrorKinds) != 1 || trace.LoopErrorKinds[0] != "llm_timeout" {
		t.Fatalf("LoopErrorKinds = %+v", trace.LoopErrorKinds)
	}
	if got := trace.LoopErrorKindCounts(); got["llm_timeout"] != 1 {
		t.Fatalf("LoopErrorKindCounts = %+v", got)
	}
	if examples := trace.RuntimeErrorExamples(1); len(examples["llm_timeout"]) != 1 || !strings.Contains(examples["llm_timeout"][0].Message, "transient stream warning") {
		t.Fatalf("RuntimeErrorExamples = %+v", examples)
	}
	if trace.FinalText != "Conclusion: green" {
		t.Fatalf("FinalText = %q", trace.FinalText)
	}
	if trace.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q", trace.FinishReason)
	}
	if trace.TurnEndReason != "completed" {
		t.Fatalf("TurnEndReason = %q", trace.TurnEndReason)
	}
	if trace.ToolStats.ToolRequests != 2 || trace.ToolStats.ToolArgsRepaired != 1 || trace.ToolStats.ToolErrors != 1 || trace.ToolStats.ToolDurationMS != 17 || trace.ToolStats.ForcedNoTools != 1 {
		t.Fatalf("ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.ToolContextTruncated != 2 || trace.ToolStats.ToolContextOmittedBytes != 8192 {
		t.Fatalf("context ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.ToolRepairNotes != 2 || trace.ToolStats.ToolRepairByKind["tool_name"] != 1 || trace.ToolStats.ToolRepairByKind["alias_rename"] != 1 {
		t.Fatalf("repair ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.ToolRepairCalls != 1 || trace.ToolStats.ToolRepairSucceeded != 1 || trace.ToolStats.ToolRepairFailed != 0 {
		t.Fatalf("repair outcome ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.ToolFailureByKind["invalid_args"] != 1 {
		t.Fatalf("failure ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.SourceAccessDynamicPartial != 1 {
		t.Fatalf("source access ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.MemoryUpdates != 2 || trace.ToolStats.MemoryUpdateAdd != 1 || trace.ToolStats.MemoryUpdateReplace != 1 || trace.ToolStats.MemoryUpdateRemove != 0 {
		t.Fatalf("memory ToolStats = %+v", trace.ToolStats)
	}
	if got := trace.RawTypes["trace.meta"]; got != 1 {
		t.Fatalf("RawTypes[trace.meta] = %d", got)
	}
	if got := trace.RawTypes["tool.request"]; got != 1 {
		t.Fatalf("RawTypes[tool.request] = %d", got)
	}
}

func TestParseTraceFileDerivesToolFailureExamples(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	body := strings.Join([]string{
		`{"type":"trace.meta","data":{"schema_version":1}}`,
		`{"type":"tool.request","data":{"call_id":"fetch1","tool":"web_fetch","args":{"url":"https://dashboard.example/helio","timeout":10}}}`,
		`{"type":"tool.result","data":{"call_id":"fetch1","result":"[dynamic page shell: URL=https://dashboard.example/helio]\nFailure: kind=dynamic_shell\nNext: use a text/API/source page.","exit_code":0}}`,
		`{"type":"tool.request","data":{"call_id":"search1","tool":"web_search","args":{"query":"rare subnet official metrics"}}}`,
		`{"type":"tool.result","data":{"call_id":"search1","result":"(no results)\nFailure: kind=no_results\nNext: retry with official domains.","exit_code":0}}`,
		`{"type":"turn.end","data":{"reason":"completed"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	examples := trace.ToolFailureExamples(1)
	dynamic := examples["dynamic_shell"]
	if len(dynamic) != 1 {
		t.Fatalf("dynamic_shell examples = %#v", dynamic)
	}
	if dynamic[0].Tool != "web_fetch" ||
		!strings.Contains(dynamic[0].ArgsSummary, "dashboard.example") ||
		!strings.Contains(dynamic[0].ResultSummary, "dynamic page shell") ||
		!strings.Contains(dynamic[0].ResultSummary, "Next:") {
		t.Fatalf("dynamic_shell example missing replayed diagnostics: %#v", dynamic[0])
	}
	search := examples["no_results"]
	if len(search) != 1 || search[0].Tool != "web_search" || !strings.Contains(search[0].ArgsSummary, "rare subnet") {
		t.Fatalf("no_results example missing replayed query context: %#v", search)
	}
}

func TestMergeRuntimeDiagnosticsFromFailures(t *testing.T) {
	res := BatchResult{Failures: []string{
		`affentctl run failed: exit=1 err=LLM llm_stream timed out after 4m0s while waiting for chat completion (model="qwen" endpoint="https://llm.example/v1/chat/completions" max-call-timeout/per-call-timeout=4m0s): context deadline exceeded`,
		`affentctl run failed: exit=1 err=stream ended without finish`,
	}}
	mergeRuntimeDiagnosticsFromFailures(&res, 1)
	if res.RuntimeErrorByKind["llm_timeout"] != 1 || res.RuntimeErrorByKind["llm_incomplete_stream"] != 1 {
		t.Fatalf("RuntimeErrorByKind = %#v", res.RuntimeErrorByKind)
	}
	timeout := res.RuntimeErrorExamples["llm_timeout"]
	if len(timeout) != 1 || !strings.Contains(timeout[0].Message, "max-call-timeout") || !strings.Contains(timeout[0].Message, "llm.example") {
		t.Fatalf("llm_timeout RuntimeErrorExamples = %#v", timeout)
	}
	incomplete := res.RuntimeErrorExamples["llm_incomplete_stream"]
	if len(incomplete) != 1 ||
		!strings.Contains(incomplete[0].Message, "terminal finish_reason") ||
		!strings.Contains(incomplete[0].Message, "OOM kill") ||
		!strings.Contains(incomplete[0].Message, "Original error:") ||
		!strings.Contains(incomplete[0].Message, "stream ended without finish") {
		t.Fatalf("llm_incomplete_stream RuntimeErrorExamples = %#v", incomplete)
	}
}

func TestRuntimeErrorDiagnosticsFromFailuresAddsActionableLegacyMessages(t *testing.T) {
	failures := []string{
		`affentctl run failed: exit=1 err=context deadline exceeded max-call-timeout/per-call-timeout=4m0s`,
		`affentctl run failed: exit=1 err=stream ended without finish`,
	}
	counts, examples := RuntimeErrorDiagnosticsFromFailures(failures, 2)
	if counts["llm_timeout"] != 1 || counts["llm_incomplete_stream"] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
	timeout := examples["llm_timeout"]
	if len(timeout) != 1 ||
		!strings.Contains(timeout[0].Message, "per-call wall-clock timeout") ||
		!strings.Contains(timeout[0].Message, "first-token latency") ||
		!strings.Contains(timeout[0].Message, "Original error:") {
		t.Fatalf("llm_timeout examples = %#v", timeout)
	}
	incomplete := examples["llm_incomplete_stream"]
	if len(incomplete) != 1 ||
		!strings.Contains(incomplete[0].Message, "terminal finish_reason") ||
		!strings.Contains(incomplete[0].Message, "sglang/vLLM") ||
		!strings.Contains(incomplete[0].Message, "reverse-proxy reset") ||
		!strings.Contains(incomplete[0].Message, "Original error:") {
		t.Fatalf("llm_incomplete_stream examples = %#v", incomplete)
	}
}

func TestParseTraceFileRejectsOversizedLineWithLineNumber(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	body := `{"type":"trace.meta","data":{"schema_version":1}}` + "\n" +
		`{"type":"message.done","data":{"text":"` + strings.Repeat("x", maxTraceLineBytes+1) + `"}}` + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseTraceFile(tracePath)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "exceeds max JSONL record size") {
		t.Fatalf("ParseTraceFile err = %v, want oversized line 2 error", err)
	}
}

func TestParseTraceFileReportsInvalidJSONLineNumber(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	body := `{"type":"trace.meta","data":{"schema_version":1}}` + "\n" +
		`{bad json` + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseTraceFile(tracePath)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("ParseTraceFile err = %v, want invalid JSON line 2 error", err)
	}
}

func TestParseTraceFileRejectsUnsupportedSchemaVersion(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	body := `{"type":"trace.meta","data":{"schema_version":999}}` + "\n"
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseTraceFile(tracePath)
	if err == nil || !strings.Contains(err.Error(), "unsupported trace schema_version 999") {
		t.Fatalf("ParseTraceFile err = %v, want unsupported schema version", err)
	}
}

func TestRunVerifierCapsOutputAndRecordsStats(t *testing.T) {
	runner := BatchRunner{VerifierOutputCapBytes: 8}
	got := runner.runVerifier(context.Background(), t.TempDir(), t.TempDir(), "printf 1234567890; exit 7")
	if got.Err == nil {
		t.Fatal("runVerifier err = nil, want failing exit")
	}
	if got.Result.Command != "printf 1234567890; exit 7" || !got.Result.Ran || got.Result.OK {
		t.Fatalf("verifier result state wrong: %+v", got.Result)
	}
	if got.Result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", got.Result.ExitCode)
	}
	if got.Result.OutputBytes != 10 || !got.Result.OutputTruncated || got.Result.OutputOmittedBytes != 2 || got.Result.OutputCapBytes != 8 {
		t.Fatalf("output stats = %+v, want bytes=10 truncated omitted=2 cap=8", got.Result)
	}
	if !strings.Contains(got.Output, "12345678") || !strings.Contains(got.Output, "2 more bytes truncated from verifier output") {
		t.Fatalf("capped output missing prefix or marker: %q", got.Output)
	}
}

func TestRunVerifierRecordsSuccess(t *testing.T) {
	got := (BatchRunner{}).runVerifier(context.Background(), t.TempDir(), t.TempDir(), "printf ok")
	if got.Err != nil {
		t.Fatalf("runVerifier err = %v", got.Err)
	}
	if !got.Result.Ran || !got.Result.OK || got.Result.ExitCode != 0 {
		t.Fatalf("verifier result state wrong: %+v", got.Result)
	}
	if got.Result.OutputBytes != 2 || got.Result.OutputTruncated || got.Result.OutputCapBytes != DefaultVerifierOutputCapBytes {
		t.Fatalf("output stats = %+v", got.Result)
	}
	if got.Output != "ok" {
		t.Fatalf("Output = %q, want ok", got.Output)
	}
}

func TestCheckBatchTraceRequiresCleanTurnEnd(t *testing.T) {
	failures := CheckBatchTrace(Trace{TurnEndReason: "max_turns"}, BatchScenario{})
	if len(failures) != 1 || !strings.Contains(failures[0], "turn ended with reason") {
		t.Fatalf("failures = %+v, want turn-end failure", failures)
	}
}

// TestBatchScenarioChecks_UsesSharedCheckLibrary pins the unification:
// a BatchScenario's declarative fields map to the same Check builders
// the in-process Runner uses, so adding a check happens once. A
// regression that grows a parallel check pipeline back into eval.go
// fires this test by leaving one of the asserted check names off the
// list.
func TestBatchScenarioChecks_UsesSharedCheckLibrary(t *testing.T) {
	scenario := BatchScenario{
		RequiredTools:     []string{"read_file"},
		ForbiddenTools:    []string{"write_file"},
		RequiredFinalText: []string{"done"},
		RequiredToolResultText: map[string][]string{
			"subagent_run": {"report"},
			"skill":        {"AFFENT ACTIVE SKILL"},
		},
		RequiredToolArgContains: []ToolArgContainsRequirement{
			{Tool: "web_search", Arg: "query", Substring: "Bittensor", Min: 2},
		},
		RequiredTruncatedResults: []string{"shell"},
		RequiredResultArtifacts:  []string{"shell"},
		RequiredToolOrder: []ToolOrderRequirement{
			{Earlier: "read_file", Later: "edit_file"},
		},
		RequiredToolCounts: map[string]int{
			"plan": 2,
		},
		RequiredToolFailureKindCounts: map[string]int{
			"invalid_args": 1,
		},
		RequiredToolStatsAtLeast: map[string]int{
			"memory_updates": 1,
		},
		RequiredFocusedTaskCounts: map[string]int{
			"explore": 1,
		},
		RequiredSubagentModeCounts: map[string]int{
			"review": 1,
		},
		RequireNoDelegationErrors: true,
		RequireNoPlanErrors:       true,
		MaxSuccessfulToolCallsByTool: map[string]int{
			"read_file": 1,
		},
		RequiredCommands: []string{`go test`, `gofmt`},
		RequiredCommandCounts: map[string]int{
			`go test`: 2,
		},
		RequiredCommandBeforeTool: []CommandToolOrderRequirement{
			{Command: `go test`, Tool: "edit_file"},
		},
		RequiredCommandAfterTool: []CommandToolOrderRequirement{
			{Command: `go test`, Tool: "edit_file"},
		},
		ForbiddenCommands: []string{"| head", "|| true"},
		ProtectedFiles:    []string{"main_test.go", "doc_test.go"},
	}
	checks := BatchScenarioChecks(scenario)

	wantPrefixes := []string{
		"turn_ended_cleanly",
		"tool_called:read_file",
		"tool_not_called:write_file",
		"final_text_contains:done",
		"tool_result_contains:skill:AFFENT ACTIVE SKILL",
		"tool_result_contains:subagent_run:report",
		"tool_arg_contains_at_least:web_search:query:Bittensor:2",
		"tool_result_truncated:shell",
		"tool_result_artifact:shell",
		"tool_called_before:read_file->edit_file",
		"tool_called_at_least:plan:2",
		"tool_failure_kind_at_least:invalid_args:1",
		"tool_stats_at_least:memory_updates:1",
		"focused_task_called_at_least:explore:1",
		"subagent_called_at_least:review:1",
		"no_delegation_errors",
		"no_plan_errors",
		"max_successful_tool_calls:read_file:1",
		"shell_command_matching:go test",
		"shell_command_matching:gofmt",
		"shell_command_matching_at_least:go test:2",
		"shell_command_before_tool:go test->edit_file",
		"shell_command_after_tool:go test->edit_file",
		"shell_command_lacks_unguarded:| head",
		"shell_command_lacks_unguarded:|| true",
		"file_not_edited:",
	}
	if len(checks) != len(wantPrefixes) {
		t.Fatalf("checks count = %d, want %d (%v)", len(checks), len(wantPrefixes), checks)
	}
	for i, want := range wantPrefixes {
		if !strings.HasPrefix(checks[i].Name, want) {
			t.Errorf("check[%d].Name = %q, want prefix %q", i, checks[i].Name, want)
		}
	}
}

func TestBatchScenarioChecks_ToolArgContainsDefaultsToOne(t *testing.T) {
	checks := BatchScenarioChecks(BatchScenario{
		RequiredToolArgContains: []ToolArgContainsRequirement{
			{Tool: "web_search", Arg: "query", Substring: "subnet 88"},
		},
	})
	if len(checks) != 2 {
		t.Fatalf("checks count = %d, want turn-end + arg check: %+v", len(checks), checks)
	}
	if !strings.HasPrefix(checks[1].Name, "tool_arg_contains_at_least:web_search:query:subnet 88:1") {
		t.Fatalf("default min check name = %q", checks[1].Name)
	}
}

func TestSelectBatchScenariosForSuite(t *testing.T) {
	scenarios, err := SelectBatchScenariosForSuite("small-model-tools", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) < 8 {
		t.Fatalf("small-model-tools suite size = %d, want at least 8", len(scenarios))
	}
	foundOversized := false
	foundRepeatedRead := false
	foundEditRecovery := false
	foundSkillInstallGuard := false
	foundPlanRepair := false
	foundPlanSkip := false
	foundPlanResume := false
	foundMemoryRecall := false
	foundMemoryWriteStats := false
	foundSymbolContext := false
	foundSymbolContextRuntimeCapabilities := false
	foundSymbolContextThenReadFile := false
	foundFileContext := false
	foundRepoSearch := false
	for _, scenario := range scenarios {
		if !scenarioInSuite(scenario, "small-model-tools") {
			t.Fatalf("scenario %s missing suite marker", scenario.Name)
		}
		if scenario.Name == "runtime-oversized-tool-result" {
			foundOversized = true
		}
		if scenario.Name == "small-tools-repeated-read" {
			foundRepeatedRead = true
			if scenario.MaxSuccessfulToolCallsByTool["read_file"] != 1 {
				t.Fatalf("small-tools-repeated-read read_file cap = %#v, want 1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "small-tools-edit-recovery" {
			foundEditRecovery = true
			if len(scenario.RequiredToolOrder) != 1 || scenario.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "read_file", Later: "edit_file"}) {
				t.Fatalf("small-tools-edit-recovery order = %#v, want read_file before edit_file", scenario.RequiredToolOrder)
			}
		}
		if scenario.Name == "skill-remote-install-guard" {
			foundSkillInstallGuard = true
			if !stringSliceContains(scenario.RequiredTools, "skill") {
				t.Fatalf("skill-remote-install-guard RequiredTools = %#v, want skill", scenario.RequiredTools)
			}
			if len(scenario.RequiredToolArgContains) != 1 ||
				scenario.RequiredToolArgContains[0] != (ToolArgContainsRequirement{Tool: "skill", Arg: "source", Substring: "https://github.com/example/skills/remote_guard_demo/SKILL.md"}) {
				t.Fatalf("skill-remote-install-guard RequiredToolArgContains = %#v", scenario.RequiredToolArgContains)
			}
			required := strings.Join(scenario.RequiredToolResultText["skill"], "\n")
			for _, want := range []string{"direct install cannot use a remote source URL", "action=propose_install", "proposal_id"} {
				if !strings.Contains(required, want) {
					t.Fatalf("skill-remote-install-guard RequiredToolResultText = %#v, want %q", scenario.RequiredToolResultText, want)
				}
			}
		}
		if scenario.Name == "plan-coding-repair" {
			foundPlanRepair = true
			if !stringSliceContains(scenario.RequiredTools, "plan") {
				t.Fatalf("plan-coding-repair RequiredTools = %#v, want plan", scenario.RequiredTools)
			}
			if scenario.RequiredToolCounts["plan"] != 2 {
				t.Fatalf("plan-coding-repair RequiredToolCounts = %#v, want plan=2", scenario.RequiredToolCounts)
			}
			if len(scenario.RequiredToolOrder) != 1 || scenario.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "plan", Later: "edit_file"}) {
				t.Fatalf("plan-coding-repair order = %#v, want plan before edit_file", scenario.RequiredToolOrder)
			}
			if !scenario.RequireNoPlanErrors {
				t.Fatal("plan-coding-repair should require clean plan usage")
			}
		}
		if scenario.Name == "plan-not-for-simple-read" {
			foundPlanSkip = true
			if !stringSliceContains(scenario.ForbiddenTools, "plan") {
				t.Fatalf("plan-not-for-simple-read ForbiddenTools = %#v, want plan", scenario.ForbiddenTools)
			}
		}
		if scenario.Name == "plan-resume-current-step" {
			foundPlanResume = true
			if !scenario.ExecutePlan || scenario.SessionID != "plan-resume" {
				t.Fatalf("plan-resume-current-step execution fields = execute_plan:%v session:%q", scenario.ExecutePlan, scenario.SessionID)
			}
			if !scenario.RequireNoPlanErrors {
				t.Fatal("plan-resume-current-step should require clean plan usage")
			}
			if scenario.RequiredToolCounts["plan"] != 1 {
				t.Fatalf("plan-resume-current-step RequiredToolCounts = %#v, want plan=1", scenario.RequiredToolCounts)
			}
			if scenario.MaxSuccessfulToolCallsByTool["read_file"] != 1 {
				t.Fatalf("plan-resume-current-step read_file cap = %#v, want 1", scenario.MaxSuccessfulToolCallsByTool)
			}
			if len(scenario.RequiredToolArgContains) != 3 {
				t.Fatalf("plan-resume-current-step RequiredToolArgContains = %#v, want current read and step 2 update constraints", scenario.RequiredToolArgContains)
			}
		}
		if scenario.Name == "memory-cross-session-recall" {
			foundMemoryRecall = true
			if !scenario.EnableMemory || scenario.SessionID != "memory-reader" {
				t.Fatalf("memory-cross-session-recall memory/session fields = memory:%v session:%q", scenario.EnableMemory, scenario.SessionID)
			}
			if !stringSliceContains(scenario.RequiredTools, "memory") {
				t.Fatalf("memory-cross-session-recall RequiredTools = %#v, want memory", scenario.RequiredTools)
			}
			if scenario.RequiredToolCounts["memory"] != 1 || scenario.MaxSuccessfulToolCallsByTool["memory"] != 1 {
				t.Fatalf("memory-cross-session-recall tool counts = required:%#v max:%#v", scenario.RequiredToolCounts, scenario.MaxSuccessfulToolCallsByTool)
			}
			if len(scenario.RequiredToolArgContains) != 2 {
				t.Fatalf("memory-cross-session-recall RequiredToolArgContains = %#v, want action/query constraints", scenario.RequiredToolArgContains)
			}
		}
		if scenario.Name == "memory-confirmed-write-stats" {
			foundMemoryWriteStats = true
			if !scenario.EnableMemory || scenario.SessionID != "memory-writer" {
				t.Fatalf("memory-confirmed-write-stats memory/session fields = memory:%v session:%q", scenario.EnableMemory, scenario.SessionID)
			}
			if scenario.RequiredToolStatsAtLeast["memory_updates"] != 1 || scenario.RequiredToolStatsAtLeast["memory_update_add"] != 1 {
				t.Fatalf("memory-confirmed-write-stats stats = %#v, want memory update/add requirements", scenario.RequiredToolStatsAtLeast)
			}
			if scenario.RequiredToolCounts["memory"] != 1 || scenario.MaxSuccessfulToolCallsByTool["memory"] != 1 {
				t.Fatalf("memory-confirmed-write-stats tool counts = required:%#v max:%#v", scenario.RequiredToolCounts, scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "default-runtime-repo-search" {
			foundRepoSearch = true
			if !stringSliceContains(scenario.RequiredTools, "repo_search") {
				t.Fatalf("default-runtime-repo-search RequiredTools = %#v, want repo_search", scenario.RequiredTools)
			}
			if len(scenario.RequiredToolArgContains) != 2 {
				t.Fatalf("default-runtime-repo-search RequiredToolArgContains = %#v, want query/path constraints", scenario.RequiredToolArgContains)
			}
			if scenario.MaxParentToolCalls != 1 {
				t.Fatalf("default-runtime-repo-search MaxParentToolCalls = %d, want 1", scenario.MaxParentToolCalls)
			}
			if scenario.MaxSuccessfulToolCallsByTool["repo_search"] != 1 {
				t.Fatalf("default-runtime-repo-search MaxSuccessfulToolCallsByTool = %#v, want repo_search=1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "default-runtime-symbol-context" {
			foundSymbolContext = true
			if !stringSliceContains(scenario.RequiredTools, "symbol_context") {
				t.Fatalf("default-runtime-symbol-context RequiredTools = %#v, want symbol_context", scenario.RequiredTools)
			}
			if len(scenario.RequiredToolArgContains) != 2 {
				t.Fatalf("default-runtime-symbol-context RequiredToolArgContains = %#v, want query/path constraints", scenario.RequiredToolArgContains)
			}
			if scenario.MaxParentToolCalls != 1 {
				t.Fatalf("default-runtime-symbol-context MaxParentToolCalls = %d, want 1", scenario.MaxParentToolCalls)
			}
			if scenario.MaxSuccessfulToolCallsByTool["symbol_context"] != 1 {
				t.Fatalf("default-runtime-symbol-context MaxSuccessfulToolCallsByTool = %#v, want symbol_context=1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "default-runtime-symbol-context-runtime-capabilities" {
			foundSymbolContextRuntimeCapabilities = true
			if !stringSliceContains(scenario.RequiredTools, "symbol_context") {
				t.Fatalf("default-runtime-symbol-context-runtime-capabilities RequiredTools = %#v, want symbol_context", scenario.RequiredTools)
			}
			if len(scenario.RequiredToolArgContains) != 1 {
				t.Fatalf("default-runtime-symbol-context-runtime-capabilities RequiredToolArgContains = %#v, want query-only constraint", scenario.RequiredToolArgContains)
			}
			if scenario.MaxParentToolCalls != 1 {
				t.Fatalf("default-runtime-symbol-context-runtime-capabilities MaxParentToolCalls = %d, want 1", scenario.MaxParentToolCalls)
			}
			if scenario.MaxSuccessfulToolCallsByTool["symbol_context"] != 1 {
				t.Fatalf("default-runtime-symbol-context-runtime-capabilities MaxSuccessfulToolCallsByTool = %#v, want symbol_context=1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "default-runtime-symbol-context-then-read-file" {
			foundSymbolContextThenReadFile = true
			if !stringSliceContains(scenario.RequiredTools, "symbol_context") || !stringSliceContains(scenario.RequiredTools, "read_file") {
				t.Fatalf("default-runtime-symbol-context-then-read-file RequiredTools = %#v, want symbol_context and read_file", scenario.RequiredTools)
			}
			if len(scenario.RequiredToolArgContains) != 2 {
				t.Fatalf("default-runtime-symbol-context-then-read-file RequiredToolArgContains = %#v, want query/path constraints", scenario.RequiredToolArgContains)
			}
			if len(scenario.RequiredToolOrder) != 1 || scenario.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "symbol_context", Later: "read_file"}) {
				t.Fatalf("default-runtime-symbol-context-then-read-file RequiredToolOrder = %#v, want symbol_context before read_file", scenario.RequiredToolOrder)
			}
			if scenario.MaxParentToolCalls != 2 {
				t.Fatalf("default-runtime-symbol-context-then-read-file MaxParentToolCalls = %d, want 2", scenario.MaxParentToolCalls)
			}
			if scenario.MaxSuccessfulToolCallsByTool["symbol_context"] != 1 || scenario.MaxSuccessfulToolCallsByTool["read_file"] != 1 {
				t.Fatalf("default-runtime-symbol-context-then-read-file MaxSuccessfulToolCallsByTool = %#v, want symbol_context=1 read_file=1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
		if scenario.Name == "default-runtime-file-context" {
			foundFileContext = true
			if !stringSliceContains(scenario.RequiredTools, "file_context") {
				t.Fatalf("default-runtime-file-context RequiredTools = %#v, want file_context", scenario.RequiredTools)
			}
			if len(scenario.RequiredToolArgContains) != 2 {
				t.Fatalf("default-runtime-file-context RequiredToolArgContains = %#v, want query/path constraints", scenario.RequiredToolArgContains)
			}
			if scenario.MaxParentToolCalls != 1 {
				t.Fatalf("default-runtime-file-context MaxParentToolCalls = %d, want 1", scenario.MaxParentToolCalls)
			}
			if scenario.MaxSuccessfulToolCallsByTool["file_context"] != 1 {
				t.Fatalf("default-runtime-file-context MaxSuccessfulToolCallsByTool = %#v, want file_context=1", scenario.MaxSuccessfulToolCallsByTool)
			}
		}
	}
	if !foundOversized {
		t.Fatalf("small-model-tools suite missing runtime-oversized-tool-result")
	}
	if !foundRepeatedRead {
		t.Fatalf("small-model-tools suite missing small-tools-repeated-read")
	}
	if !foundEditRecovery {
		t.Fatalf("small-model-tools suite missing small-tools-edit-recovery")
	}
	if !foundSkillInstallGuard {
		t.Fatalf("small-model-tools suite missing skill-remote-install-guard")
	}
	if !foundPlanRepair {
		t.Fatalf("small-model-tools suite missing plan-coding-repair")
	}
	if !foundPlanSkip {
		t.Fatalf("small-model-tools suite missing plan-not-for-simple-read")
	}
	if !foundPlanResume {
		t.Fatalf("small-model-tools suite missing plan-resume-current-step")
	}
	if !foundMemoryRecall {
		t.Fatalf("small-model-tools suite missing memory-cross-session-recall")
	}
	if !foundMemoryWriteStats {
		t.Fatalf("small-model-tools suite missing memory-confirmed-write-stats")
	}
	if !foundRepoSearch {
		t.Fatalf("small-model-tools suite missing default-runtime-repo-search")
	}
	if !foundSymbolContext {
		t.Fatalf("small-model-tools suite missing default-runtime-symbol-context")
	}
	if !foundSymbolContextRuntimeCapabilities {
		t.Fatalf("small-model-tools suite missing default-runtime-symbol-context-runtime-capabilities")
	}
	if !foundSymbolContextThenReadFile {
		t.Fatalf("small-model-tools suite missing default-runtime-symbol-context-then-read-file")
	}
	if !foundFileContext {
		t.Fatalf("small-model-tools suite missing default-runtime-file-context")
	}
	one, err := SelectBatchScenariosForSuite("small-model-tools", []string{"small-tools-wrong-field-read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Name != "small-tools-wrong-field-read" {
		t.Fatalf("filtered suite result = %+v", one)
	}
}

func TestSelectLongRunSuite(t *testing.T) {
	scenarios, err := SelectBatchScenariosForSuite("long-run", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 6 {
		t.Fatalf("long-run suite size = %d, want 6", len(scenarios))
	}
	seen := map[string]BatchScenario{}
	for _, scenario := range scenarios {
		if !scenarioInSuite(scenario, "long-run") {
			t.Fatalf("scenario %s missing long-run suite marker", scenario.Name)
		}
		seen[scenario.Name] = scenario
	}

	stock, ok := seen["longrun-stock-analysis-synthesis"]
	if !ok {
		t.Fatalf("long-run suite missing stock analysis scenario")
	}
	if !stringSliceContains(stock.RequiredTools, "repo_search") || !stringSliceContains(stock.RequiredTools, "read_file") {
		t.Fatalf("stock scenario RequiredTools = %#v, want repo_search/read_file", stock.RequiredTools)
	}
	if len(stock.RequiredToolOrder) != 1 || stock.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "repo_search", Later: "read_file"}) {
		t.Fatalf("stock scenario RequiredToolOrder = %#v, want repo_search before read_file", stock.RequiredToolOrder)
	}
	if !stringSliceContains(stock.ForbiddenTools, "shell") {
		t.Fatalf("stock scenario ForbiddenTools = %#v, want shell", stock.ForbiddenTools)
	}

	subnet, ok := seen["longrun-bittensor-subnet-synthesis"]
	if !ok {
		t.Fatalf("long-run suite missing Bittensor subnet scenario")
	}
	for _, want := range []string{"0.06342 T", "201.04K T", "metrics/tao-app-snapshot.txt"} {
		if !stringSliceContains(subnet.RequiredFinalText, want) {
			t.Fatalf("Bittensor scenario RequiredFinalText = %#v, want %q", subnet.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(subnet.ForbiddenFinalText, "subnet price $277.32") {
		t.Fatalf("Bittensor scenario ForbiddenFinalText = %#v, want TAO/subnet price conflation guard", subnet.ForbiddenFinalText)
	}

	pr, ok := seen["longrun-code-implementation-pr-summary"]
	if !ok {
		t.Fatalf("long-run suite missing code PR scenario")
	}
	if !stringSliceContains(pr.RequiredTools, "edit_file") {
		t.Fatalf("code PR scenario RequiredTools = %#v, want edit_file", pr.RequiredTools)
	}
	if pr.RequiredCommandCounts[`go test`] != 2 {
		t.Fatalf("code PR scenario RequiredCommandCounts = %#v, want go test=2", pr.RequiredCommandCounts)
	}
	if !stringSliceContains(pr.RequiredFinalText, "PR Summary") || !stringSliceContains(pr.RequiredFinalText, "Tests") {
		t.Fatalf("code PR scenario RequiredFinalText = %#v, want PR Summary and Tests", pr.RequiredFinalText)
	}

	planResume, ok := seen["plan-resume-current-step"]
	if !ok {
		t.Fatalf("long-run suite missing plan resume scenario")
	}
	if !planResume.ExecutePlan || planResume.SessionID != "plan-resume" {
		t.Fatalf("plan resume execution fields = execute_plan:%v session:%q", planResume.ExecutePlan, planResume.SessionID)
	}
	if !stringSliceContains(planResume.RequiredFinalText, "RESUME-CURRENT-42") || !stringSliceContains(planResume.ForbiddenFinalText, "STALE-PLAN-99") {
		t.Fatalf("plan resume final text constraints = required:%#v forbidden:%#v", planResume.RequiredFinalText, planResume.ForbiddenFinalText)
	}
	if planResume.RequiredToolCounts["plan"] != 1 || planResume.MaxSuccessfulToolCallsByTool["read_file"] != 1 {
		t.Fatalf("plan resume tool constraints = counts:%#v max:%#v", planResume.RequiredToolCounts, planResume.MaxSuccessfulToolCallsByTool)
	}

	memoryRecall, ok := seen["memory-cross-session-recall"]
	if !ok {
		t.Fatalf("long-run suite missing memory recall scenario")
	}
	if !memoryRecall.EnableMemory || memoryRecall.SessionID != "memory-reader" {
		t.Fatalf("memory recall fields = memory:%v session:%q", memoryRecall.EnableMemory, memoryRecall.SessionID)
	}
	if memoryRecall.RequiredToolCounts["memory"] != 1 || memoryRecall.MaxSuccessfulToolCallsByTool["memory"] != 1 {
		t.Fatalf("memory recall tool constraints = counts:%#v max:%#v", memoryRecall.RequiredToolCounts, memoryRecall.MaxSuccessfulToolCallsByTool)
	}

	memoryWrite, ok := seen["memory-confirmed-write-stats"]
	if !ok {
		t.Fatalf("long-run suite missing memory write stats scenario")
	}
	if !memoryWrite.EnableMemory || memoryWrite.SessionID != "memory-writer" {
		t.Fatalf("memory write fields = memory:%v session:%q", memoryWrite.EnableMemory, memoryWrite.SessionID)
	}
	if memoryWrite.RequiredToolStatsAtLeast["memory_updates"] != 1 || memoryWrite.RequiredToolStatsAtLeast["memory_update_add"] != 1 {
		t.Fatalf("memory write stats constraints = %#v", memoryWrite.RequiredToolStatsAtLeast)
	}
}

func TestFocusedTaskScenarioRequiresExploreTask(t *testing.T) {
	for _, scenario := range BuiltinBatchScenarios() {
		if scenario.Name != "focused-task-project-facts" {
			continue
		}
		if scenario.RequiredFocusedTaskCounts["explore"] != 1 {
			t.Fatalf("focused-task-project-facts RequiredFocusedTaskCounts = %#v, want explore=1", scenario.RequiredFocusedTaskCounts)
		}
		if !scenario.RequireNoDelegationErrors {
			t.Fatal("focused-task-project-facts should require clean delegation")
		}
		return
	}
	t.Fatal("builtin scenarios missing focused-task-project-facts")
}

func TestSubagentScenarioRequiresExploreMode(t *testing.T) {
	for _, scenario := range BuiltinBatchScenarios() {
		if scenario.Name != "subagent-project-facts" {
			continue
		}
		if scenario.RequiredSubagentModeCounts["explore"] != 1 {
			t.Fatalf("subagent-project-facts RequiredSubagentModeCounts = %#v, want explore=1", scenario.RequiredSubagentModeCounts)
		}
		if !scenario.RequireNoDelegationErrors {
			t.Fatal("subagent-project-facts should require clean delegation")
		}
		return
	}
	t.Fatal("builtin scenarios missing subagent-project-facts")
}

func TestRepairScenariosRequireRepeatedVerification(t *testing.T) {
	want := map[string]map[string]int{
		"coding-go-median":            {`go test`: 2},
		"coding-go-config-precedence": {`go test`: 2},
		"coding-python-slug":          {`python(3)? -m pytest`: 2},
		"coding-go-redaction-overlap": {`go test`: 2},
		"coding-python-config-parser": {`python(3)? -m pytest`: 2},
		"plan-coding-repair":          {`go test`: 2},
	}
	seen := map[string]bool{}
	for _, scenario := range BuiltinBatchScenarios() {
		counts, ok := want[scenario.Name]
		if !ok {
			continue
		}
		seen[scenario.Name] = true
		for pattern, min := range counts {
			if scenario.RequiredCommandCounts[pattern] != min {
				t.Fatalf("%s RequiredCommandCounts[%q] = %d, want %d; all counts=%#v", scenario.Name, pattern, scenario.RequiredCommandCounts[pattern], min, scenario.RequiredCommandCounts)
			}
			if !stringSliceContains(scenario.RequiredTools, "edit_file") {
				t.Fatalf("%s RequiredTools = %#v, want edit_file", scenario.Name, scenario.RequiredTools)
			}
			wantBefore := CommandToolOrderRequirement{Command: pattern, Tool: "edit_file"}
			if !commandToolOrderContains(scenario.RequiredCommandBeforeTool, wantBefore) {
				t.Fatalf("%s RequiredCommandBeforeTool = %#v, want %#v", scenario.Name, scenario.RequiredCommandBeforeTool, wantBefore)
			}
			if !commandToolOrderContains(scenario.RequiredCommandAfterTool, wantBefore) {
				t.Fatalf("%s RequiredCommandAfterTool = %#v, want %#v", scenario.Name, scenario.RequiredCommandAfterTool, wantBefore)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("missing repair scenario %s", name)
		}
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func commandToolOrderContains(values []CommandToolOrderRequirement, want CommandToolOrderRequirement) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProtectedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := writeScenarioFiles(dir, map[string]string{"test.py": "original"}); err != nil {
		t.Fatal(err)
	}
	snap, err := readProtectedFiles(dir, []string{"test.py"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.py"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyProtectedFiles(dir, snap); err == nil {
		t.Fatal("expected protected file change to be detected")
	}
}

func TestBatchRunnerCleanupPassingWorkspace(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "passing")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	res := BatchResult{OK: true}
	BatchRunner{CleanupPassingWorkspaces: true}.cleanupPassingWorkspace(&res, workspace)
	if !res.WorkspaceRemoved || res.CleanupError != "" {
		t.Fatalf("cleanup result = %+v, want removed without error", res)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace should be removed, stat err=%v", err)
	}
}

func TestBatchRunnerKeepsFailingWorkspace(t *testing.T) {
	workspace := t.TempDir()
	res := BatchResult{OK: false}
	BatchRunner{CleanupPassingWorkspaces: true}.cleanupPassingWorkspace(&res, workspace)
	if res.WorkspaceRemoved || res.CleanupError != "" {
		t.Fatalf("cleanup result = %+v, want untouched failure", res)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("failing workspace should remain: %v", err)
	}
}

func TestBatchRunnerRunVerifierHonorsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := (BatchRunner{}).runVerifier(ctx, t.TempDir(), ".", "sleep 1")
	if got.Err == nil {
		t.Fatal("expected verifier to be killed by context timeout")
	}
	if got.Result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1 on timeout", got.Result.ExitCode)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("verifier ignored context timeout; elapsed=%s err=%v", elapsed, got.Err)
	}
}

func TestBatchRunnerAffentctlRunArgsForwardsExecutor(t *testing.T) {
	args := (BatchRunner{
		BaseURL:          "https://llm.example/v1",
		Model:            "model-a",
		APIKey:           "secret",
		Temperature:      " 0 ",
		TopP:             " 0.9 ",
		MaxTokens:        " 512 ",
		Seed:             " 42 ",
		Executor:         "docker:affent-eval",
		RuntimeEvalMode:  true,
		RuntimeMCPConfig: " /tmp/eval-mcp.json ",
	}).affentctlRunArgs("/tmp/ws", "/tmp/ws/trace.jsonl", BatchScenario{
		Prompt:       "fix it",
		SessionID:    "planned",
		ExecutePlan:  true,
		EnableMemory: true,
		MaxTurns:     3,
	})
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"--executor\x00docker:affent-eval",
		"--workspace\x00/tmp/ws",
		"--session-id\x00planned",
		"--execute-plan",
		"--trace\x00/tmp/ws/trace.jsonl",
		"--max-turns\x003",
		"--temperature\x000",
		"--top-p\x000.9",
		"--max-tokens\x00512",
		"--seed\x0042",
		"--api-key\x00secret",
		"--eval-mode",
		"--memory=true",
		"--mcp-config\x00/tmp/eval-mcp.json",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q:\n%q", want, args)
		}
	}
	for _, unsupported := range []string{"--web=true", "--browser=true"} {
		if strings.Contains(joined, unsupported) {
			t.Fatalf("args should not include unsupported runtime flag %q:\n%q", unsupported, args)
		}
	}
}

func TestBatchRunnerRunRejectsUnsupportedExternalRuntimeFlags(t *testing.T) {
	cases := []struct {
		name   string
		runner BatchRunner
	}{
		{
			name:   "runtime web",
			runner: BatchRunner{RuntimeWeb: true},
		},
		{
			name:   "runtime browser",
			runner: BatchRunner{RuntimeBrowser: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.runner.Run(context.Background(), BatchScenario{Name: "unsupported-runtime", MaxTurns: 1})
			if res.OK {
				t.Fatalf("Run OK = true, want unsupported runtime failure")
			}
			if res.Workspace != "" {
				t.Fatalf("Workspace = %q, want early rejection before workspace creation", res.Workspace)
			}
			if len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "runtime web/browser tools are not supported") {
				t.Fatalf("Failures = %#v", res.Failures)
			}
		})
	}
}

func TestBatchRunnerRunHonorsScenarioVerifierTimeout(t *testing.T) {
	repoRoot := t.TempDir()
	goBin := filepath.Join(t.TempDir(), "go")
	traceBody := strings.Join([]string{
		`{"type":"trace.meta","data":{"schema_version":1}}`,
		`{"type":"turn.end","data":{"reason":"completed"}}`,
	}, "\n") + "\n"
	script := "#!/bin/sh\nset -eu\ntrace=\"\"\nprev=\"\"\nfor arg in \"$@\"; do\n  if [ \"$prev\" = \"--trace\" ]; then\n    trace=\"$arg\"\n  fi\n  prev=\"$arg\"\ndone\ncase \"${1:-}\" in\n  run)\n    : ;;\n  *)\n    echo \"unexpected args: $*\" >&2\n    exit 1\n    ;;\nesac\nmkdir -p \"$(dirname \"$trace\")\"\ncat >\"$trace\" <<'EOF'\n" + traceBody + "EOF\nexit 0\n"
	if err := os.WriteFile(goBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := BatchRunner{
		RepoRoot: repoRoot,
		BaseURL:  "http://127.0.0.1:0",
		APIKey:   "test",
		Model:    "fake-model",
		GoBin:    goBin,
		Timeout:  5 * time.Second,
	}
	res := runner.Run(context.Background(), BatchScenario{
		Name:            "verifier-timeout",
		Prompt:          "answer briefly",
		VerifyCommand:   "sleep 2",
		VerifierTimeout: 100 * time.Millisecond,
		MaxTurns:        1,
	})
	if res.OK {
		t.Fatalf("expected verifier timeout run to fail, got OK: %+v", res)
	}
	if !res.Verifier.Ran || res.Verifier.OK {
		t.Fatalf("verifier result should record a failed run: %+v", res.Verifier)
	}
	if res.Verifier.ExitCode != -1 {
		t.Fatalf("verifier exit code = %d, want -1 on timeout", res.Verifier.ExitCode)
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), "verify command failed: sleep 2") {
		t.Fatalf("failures should mention verifier failure, got: %+v", res.Failures)
	}
}
