package agenteval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
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
		`{"type":"runtime.surface","data":{"turn_id":"t1","tool_count":2,"tools":[{"name":"web_fetch","group":"Web"},{"name":"web_search","group":"Web"}],"capabilities":{"web_fetch":true,"web_search":true},"max_turn_steps":12,"max_tool_calls":7,"tool_result_event_cap_bytes":262144,"tool_result_context_max_bytes":5120,"tool_result_context_budget_bytes":32768,"tool_result_artifact_prefix":".affent/artifacts/tool-results"}}`,
		`{"type":"tool.request","data":{"call_id":"c1","tool":"shell","args":{"command":"go test ./..."},"args_truncated":true,"args_bytes":70000,"args_omitted_bytes":512,"args_cap_bytes":65536,"original_tool":"Shell","original_args_summary":"{\"cmd\":\"go test ./...\"}","canonicalized":true,"args_repaired":true,"repair_notes":["renamed tool","renamed field"]}}`,
		`{"type":"tool.result","data":{"call_id":"c1","result":"ok","exit_code":0,"duration_ms":17,"result_truncated":true,"result_bytes":300000,"result_omitted_bytes":4096,"result_cap_bytes":262144,"result_artifact_path":".affent/artifacts/tool-results/000001-c1.txt"}}`,
		`{"type":"tool.result","data":{"call_id":"guarded","result":"blocked\nFailure: kind=invalid_args","exit_code":1}}`,
		`{"type":"usage","data":{"input_tokens":11,"output_tokens":7}}`,
		`{"type":"error","data":{"message":"transient stream warning","failure_kind":"llm_timeout"}}`,
		`{"type":"loop.decision","data":{"turn_id":"t1","decision_id":"d1","kind":"evidence_quality","trigger":"source_access_dynamic_partial","decision":"defer","confidence":"high","reason":"Dynamic widgets had no text values.","required_action":"Read browser network responses before citing metrics.","visible_in_ui":true}}`,
		`{"type":"context.compacted","data":{"turn_id":"t1","before_messages":50,"after_messages":18,"removed_messages":32,"reactive":true,"reason":"context_overflow","summary_present":true,"summary_bytes":2048}}`,
		`{"type":"message.done","data":{"text":"Conclusion: green","finish_reason":"stop"}}`,
		`{"type":"turn.end","data":{"reason":"completed","tool_stats":{"tool_requests":2,"tool_name_canonicalized":1,"tool_args_repaired":1,"tool_repair_calls":1,"tool_repair_succeeded":1,"tool_repair_failed":0,"tool_repair_notes":2,"tool_repair_by_kind":{"tool_name":1,"alias_rename":1},"tool_failure_by_kind":{"invalid_args":1},"tool_errors":1,"tool_duration_ms":17,"loop_guard_interventions":1,"forced_no_tools":1,"source_access_dynamic_partial":1,"memory_updates":2,"memory_update_add":1,"memory_update_replace":1,"session_search_calls":1,"session_search_results":2,"session_search_context_hits":1,"session_search_matched_terms":2,"tool_context_truncated":2,"tool_context_omitted_bytes":8192}}}`,
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
	if len(trace.RuntimeSurfaces) != 1 ||
		trace.RuntimeSurfaces[0].ToolCount != 2 ||
		!trace.RuntimeSurfaces[0].Capabilities.WebFetch ||
		!trace.RuntimeSurfaces[0].Capabilities.WebSearch ||
		trace.RuntimeSurfaces[0].Tools[0].Name != "web_fetch" ||
		trace.RuntimeSurfaces[0].MaxTurnSteps != 12 {
		t.Fatalf("RuntimeSurfaces = %+v", trace.RuntimeSurfaces)
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
	loopDecisions := trace.LoopDecisionStats(1)
	if loopDecisions.Count != 1 || loopDecisions.ByKind["evidence_quality"] != 1 || loopDecisions.ByDecision["defer"] != 1 {
		t.Fatalf("LoopDecisionStats = %+v", loopDecisions)
	}
	if len(loopDecisions.Examples) != 1 ||
		loopDecisions.Examples[0].Trigger != "source_access_dynamic_partial" ||
		!strings.Contains(loopDecisions.Examples[0].RequiredAction, "browser network") {
		t.Fatalf("LoopDecisionStats examples = %+v", loopDecisions.Examples)
	}
	compactions := trace.ContextCompactionStats(1)
	if compactions.Count != 1 || compactions.Reactive != 1 || compactions.Proactive != 0 || compactions.RemovedMessages != 32 || compactions.SummaryBytes != 2048 {
		t.Fatalf("ContextCompactionStats = %+v", compactions)
	}
	if len(compactions.Examples) != 1 || compactions.Examples[0].Reason != "context_overflow" || !compactions.Examples[0].SummaryPresent {
		t.Fatalf("ContextCompactionStats examples = %+v", compactions.Examples)
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
	if trace.ToolStats.SessionSearchCalls != 1 || trace.ToolStats.SessionSearchResults != 2 || trace.ToolStats.SessionSearchContextHits != 1 || trace.ToolStats.SessionSearchMatchedTerms != 2 {
		t.Fatalf("session search ToolStats = %+v", trace.ToolStats)
	}
	if got := trace.RawTypes["trace.meta"]; got != 1 {
		t.Fatalf("RawTypes[trace.meta] = %d", got)
	}
	if got := trace.RawTypes["tool.request"]; got != 1 {
		t.Fatalf("RawTypes[tool.request] = %d", got)
	}
	if got := trace.RawTypes["loop.decision"]; got != 1 {
		t.Fatalf("RawTypes[loop.decision] = %d", got)
	}
	if got := trace.RawTypes["runtime.surface"]; got != 1 {
		t.Fatalf("RawTypes[runtime.surface] = %d", got)
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
		RequiredLoopDecisionKinds: map[string]int{
			"evidence_quality": 1,
		},
		RequiredLoopDecisionResults: map[string]int{
			"defer": 1,
		},
		RequiredLoopDecisionMatches: []LoopDecisionRequirement{
			{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial"},
		},
		RequiredContextCompactions:    1,
		RequiredReactiveCompactions:   1,
		RequiredCompactionRemovedMsgs: 20,
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
		"loop_decision_kind_at_least:evidence_quality:1",
		"loop_decision_result_at_least:defer:1",
		"loop_decision_match_at_least:evidence_quality:defer:source_access_dynamic_partial:1",
		"context_compactions_at_least:1",
		"reactive_context_compactions_at_least:1",
		"context_compaction_removed_messages_at_least:20",
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

func TestBatchScenarioChecks_LoopDecisionMatchDefaultsToOne(t *testing.T) {
	checks := BatchScenarioChecks(BatchScenario{
		RequiredLoopDecisionMatches: []LoopDecisionRequirement{
			{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial"},
		},
	})
	if len(checks) != 2 {
		t.Fatalf("checks count = %d, want turn-end + loop decision match: %+v", len(checks), checks)
	}
	if !strings.HasPrefix(checks[1].Name, "loop_decision_match_at_least:evidence_quality:defer:source_access_dynamic_partial:1") {
		t.Fatalf("default min check name = %q", checks[1].Name)
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
	foundSessionHistoryRecall := false
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
		if scenario.Name == "session-history-cross-session-recall" {
			foundSessionHistoryRecall = true
			if scenario.SessionID != "history-reader" {
				t.Fatalf("session-history-cross-session-recall SessionID = %q, want history-reader", scenario.SessionID)
			}
			if !stringSliceContains(scenario.RequiredTools, "session_search") {
				t.Fatalf("session-history-cross-session-recall RequiredTools = %#v, want session_search", scenario.RequiredTools)
			}
			if scenario.RequiredToolCounts["session_search"] != 1 || scenario.MaxSuccessfulToolCallsByTool["session_search"] != 1 {
				t.Fatalf("session-history-cross-session-recall tool counts = required:%#v max:%#v", scenario.RequiredToolCounts, scenario.MaxSuccessfulToolCallsByTool)
			}
			if len(scenario.RequiredToolArgContains) != 1 {
				t.Fatalf("session-history-cross-session-recall RequiredToolArgContains = %#v, want query constraint", scenario.RequiredToolArgContains)
			}
			assertSessionSearchDiagnosticsRequired(t, scenario)
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
	if !foundSessionHistoryRecall {
		t.Fatalf("small-model-tools suite missing session-history-cross-session-recall")
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
	if len(scenarios) != 7 {
		t.Fatalf("long-run suite size = %d, want 7", len(scenarios))
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

	sessionHistory, ok := seen["session-history-cross-session-recall"]
	if !ok {
		t.Fatalf("long-run suite missing session history recall scenario")
	}
	if sessionHistory.SessionID != "history-reader" {
		t.Fatalf("session history fields = session:%q", sessionHistory.SessionID)
	}
	if sessionHistory.RequiredToolCounts["session_search"] != 1 || sessionHistory.MaxSuccessfulToolCallsByTool["session_search"] != 1 {
		t.Fatalf("session history tool constraints = counts:%#v max:%#v", sessionHistory.RequiredToolCounts, sessionHistory.MaxSuccessfulToolCallsByTool)
	}
	if !stringSliceContains(sessionHistory.ForbiddenFinalText, "HIST-OLD-00") {
		t.Fatalf("session history final text constraints = forbidden:%#v", sessionHistory.ForbiddenFinalText)
	}
	assertSessionSearchDiagnosticsRequired(t, sessionHistory)

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

func TestSelectLiveWebSuite(t *testing.T) {
	scenarios, err := SelectBatchScenariosForSuite("live-web", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 {
		t.Fatalf("live-web suite size = %d, want 1", len(scenarios))
	}
	scenario := scenarios[0]
	if scenario.Name != "live-web-taostats-sn120-dynamic-evidence" {
		t.Fatalf("live-web scenario name = %q", scenario.Name)
	}
	for _, want := range []string{"browser_navigate", "browser_network", "browser_network_read"} {
		if !stringSliceContains(scenario.RequiredTools, want) {
			t.Fatalf("live-web RequiredTools = %#v, want %q", scenario.RequiredTools, want)
		}
	}
	if scenario.RequiredToolStatsAtLeast["source_access_network"] != 1 {
		t.Fatalf("live-web source access requirements = %#v, want source_access_network=1", scenario.RequiredToolStatsAtLeast)
	}
	if !stringSliceContains(scenario.ForbiddenTools, "shell") {
		t.Fatalf("live-web ForbiddenTools = %#v, want shell", scenario.ForbiddenTools)
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

func assertSessionSearchDiagnosticsRequired(t *testing.T, scenario BatchScenario) {
	t.Helper()
	required := strings.Join(scenario.RequiredToolResultText["session_search"], "\n")
	for _, want := range []string{`"context_included":true`, `"matched_terms"`, `"alpha"`, `"coast"`} {
		if !strings.Contains(required, want) {
			t.Fatalf("%s RequiredToolResultText session_search = %#v, want %q", scenario.Name, scenario.RequiredToolResultText, want)
		}
	}
	for field, min := range map[string]int{
		"session_search_calls":         1,
		"session_search_results":       1,
		"session_search_context_hits":  1,
		"session_search_matched_terms": 2,
	} {
		if scenario.RequiredToolStatsAtLeast[field] != min {
			t.Fatalf("%s RequiredToolStatsAtLeast[%q] = %d, want %d", scenario.Name, field, scenario.RequiredToolStatsAtLeast[field], min)
		}
	}
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

func TestWriteScenarioDebugArtifactsIndexesTraceAndFinalText(t *testing.T) {
	workspace := t.TempDir()
	tracePath := filepath.Join(workspace, "trace.jsonl")
	if err := os.WriteFile(tracePath, []byte(`{"type":"trace.meta","data":{"schema_version":1}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := BatchResult{
		BatchScenario:    "debug-case",
		Workspace:        workspace,
		TracePath:        tracePath,
		OK:               false,
		Failures:         []string{"missing required evidence"},
		FinalText:        "partial answer",
		AffentctlCommand: []string{"go", "run", "./cmd/affentctl", "run", "--api-key", "<redacted>", "--prompt", "<prompt>"},
		RunExitCode:      3,
		TraceDeltas:      true,
		TurnEndReason:    "completed",
		ToolCalls:        5,
		Repair:           ToolRepairStats{Calls: 1, SucceededCalls: 1, Notes: 2, ByKind: map[string]int{"tool_name": 1, "alias_rename": 1}},
		ToolStats: ToolRuntimeStats{
			ToolErrors:                 1,
			ToolFailureByKind:          map[string]int{"dynamic_shell": 1},
			LoopGuardInterventions:     1,
			SourceAccessResults:        2,
			SourceAccessVerified:       1,
			SourceAccessDiscoveryOnly:  1,
			SourceAccessNetwork:        1,
			SourceAccessDynamicPartial: 1,
			MemoryUpdates:              2,
			MemoryUpdateAdd:            1,
			MemoryUpdateReplace:        1,
			SessionSearchCalls:         1,
			SessionSearchResults:       2,
			SessionSearchContextHits:   1,
			SessionSearchMatchedTerms:  2,
			ToolContextTruncated:       2,
			ToolContextOmittedBytes:    8192,
		},
		ContextCompactions: ContextCompactionStats{Count: 1, Reactive: 1, RemovedMessages: 12, SummaryBytes: 512},
		Usage:              Usage{InputTokens: 100, OutputTokens: 20},
		RuntimeSurface: &sse.RuntimeSurfacePayload{
			TurnID:    "turn-debug",
			ToolCount: 2,
			Tools: []sse.RuntimeSurfaceTool{
				{Name: "web_fetch", Group: "Web"},
				{Name: "web_search", Group: "Web"},
			},
			Capabilities: sse.RuntimeCapabilities{WebFetch: true, WebSearch: true},
		},
	}
	trace := Trace{
		RawTypes: map[string]int{
			"message.delta":   2,
			"runtime.surface": 1,
			"tool.request":    1,
			"tool.result":     1,
		},
		RuntimeSurfaces: []sse.RuntimeSurfacePayload{*res.RuntimeSurface},
		Tools: []ToolCall{{
			TurnID:       "turn-debug",
			CallID:       "call-1",
			Tool:         "web_fetch",
			Args:         map[string]any{"url": "https://example.test/report"},
			Result:       "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence\nPAGE DIAGNOSTICS:\n- empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value\nPAGE TEXT:\nAffine SN120",
			FailureKinds: []string{"dynamic_shell"},
			ExitCode:     1,
			DurationMS:   42,
		}, {
			TurnID:     "turn-debug",
			CallID:     "call-2",
			Tool:       "browser_network_read",
			Args:       map[string]any{"ref": "n1", "json_path": "$.price"},
			Result:     "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; source_method=network_xhr_fetch\nJSON_PATH: $.price\n\"0.06342 T\"",
			ExitCode:   0,
			DurationMS: 12,
		}, {
			TurnID:   "turn-debug",
			CallID:   "call-3",
			Tool:     "browser_navigate",
			Args:     map[string]any{"url": "https://search.example/?q=affine"},
			Result:   "SourceAccess: browser_rendered_url=https://search.example/?q=affine; page_text_below=search_results_discovery_only\nPAGE TEXT:\nAffine result",
			ExitCode: 0,
		}, {
			TurnID: "turn-debug",
			CallID: "call-4",
			Tool:   "memory",
			Args: map[string]any{
				"action":   "replace",
				"target":   "memory",
				"topic":    "markets",
				"old_text": "Use direct price labels from dynamic dashboards.",
				"content":  "Use browser_network_read json_path before citing dynamic dashboard metrics.",
			},
			Result:   `{"ok":true,"target":"memory","topic":"markets","message":"replaced"}`,
			ExitCode: 0,
		}, {
			TurnID: "turn-debug",
			CallID: "call-5",
			Tool:   "memory",
			Args: map[string]any{
				"action":  "add",
				"target":  "memory",
				"topic":   "research",
				"content": "Record network evidence gaps explicitly.",
			},
			Result:   `{"ok":true,"target":"memory","topic":"research","message":"added"}`,
			ExitCode: 0,
		}},
		LoopDecisions: []LoopDecision{{
			Kind:     "evidence_quality",
			Decision: "defer",
			Reason:   "need browser network evidence",
		}},
		ContextCompactions: []ContextCompaction{{
			TurnID:          "turn-debug",
			BeforeMessages:  30,
			AfterMessages:   12,
			RemovedMessages: 18,
			Reactive:        true,
			Reason:          "context_overflow",
			SummaryBytes:    512,
		}},
		FinalText:    "partial answer",
		FinishReason: "stop",
	}
	err := writeScenarioDebugArtifacts(&res, BatchScenario{Prompt: "research with evidence"}, "partial answer\n", "runtime log\n", &trace)
	if err != nil {
		t.Fatalf("writeScenarioDebugArtifacts: %v", err)
	}
	if res.DebugManifestPath == "" || res.TimelinePath == "" || res.FinalTextPath == "" || res.StdoutPath == "" || res.StderrPath == "" {
		t.Fatalf("debug paths not populated: %+v", res)
	}
	if raw, err := os.ReadFile(res.FinalTextPath); err != nil || string(raw) != "partial answer" {
		t.Fatalf("final text file = %q err=%v", string(raw), err)
	}
	if raw, err := os.ReadFile(res.StdoutPath); err != nil || string(raw) != "partial answer\n" {
		t.Fatalf("stdout file = %q err=%v", string(raw), err)
	}
	if raw, err := os.ReadFile(res.StderrPath); err != nil || string(raw) != "runtime log\n" {
		t.Fatalf("stderr file = %q err=%v", string(raw), err)
	}
	var manifest DebugManifest
	raw, err := os.ReadFile(res.DebugManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v\n%s", err, string(raw))
	}
	if manifest.Scenario != "debug-case" || manifest.OK || manifest.Prompt != "research with evidence" {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	if manifest.TracePath != tracePath ||
		manifest.TimelinePath != res.TimelinePath ||
		manifest.FinalTextPath != res.FinalTextPath ||
		manifest.StdoutPath != res.StdoutPath ||
		manifest.StderrPath != res.StderrPath ||
		!reflect.DeepEqual(manifest.AffentctlCommand, res.AffentctlCommand) ||
		manifest.RunExitCode != 3 ||
		!manifest.TraceDeltas {
		t.Fatalf("manifest paths = %+v", manifest)
	}
	if len(manifest.Failures) != 1 || manifest.Failures[0] != "missing required evidence" {
		t.Fatalf("manifest failures = %+v", manifest.Failures)
	}
	if manifest.RuntimeSurface == nil ||
		manifest.RuntimeSurface.ToolCount != 2 ||
		!manifest.RuntimeSurface.Capabilities.WebFetch ||
		!manifest.RuntimeSurface.Capabilities.WebSearch ||
		manifest.RuntimeSurface.Tools[0].Name != "web_fetch" {
		t.Fatalf("manifest runtime surface = %+v", manifest.RuntimeSurface)
	}
	if manifest.Metrics.ToolCalls != 5 ||
		manifest.Metrics.ToolErrors != 1 ||
		manifest.Metrics.LoopGuardInterventions != 1 ||
		manifest.Metrics.SourceAccessResults != 2 ||
		manifest.Metrics.SourceAccessVerified != 1 ||
		manifest.Metrics.SourceAccessDiscoveryOnly != 1 ||
		manifest.Metrics.SourceAccessNetwork != 1 ||
		manifest.Metrics.SourceAccessDynamicPartial != 1 ||
		manifest.Metrics.ContextCompactions != 1 ||
		manifest.Metrics.ReactiveContextCompactions != 1 ||
		manifest.Metrics.ContextCompactionRemoved != 12 ||
		manifest.Metrics.ContextCompactionSummary != 512 ||
		manifest.Metrics.MemoryUpdates != 2 ||
		manifest.Metrics.MemoryUpdateAdd != 1 ||
		manifest.Metrics.MemoryUpdateReplace != 1 ||
		manifest.Metrics.SessionSearchCalls != 1 ||
		manifest.Metrics.SessionSearchResults != 2 ||
		manifest.Metrics.SessionSearchContextHits != 1 ||
		manifest.Metrics.SessionSearchMatchedTerms != 2 ||
		manifest.Metrics.ToolContextTruncated != 2 ||
		manifest.Metrics.ToolContextOmittedBytes != 8192 ||
		manifest.Metrics.ToolFailureByKind["dynamic_shell"] != 1 ||
		manifest.Metrics.ToolRepairCalls != 1 ||
		manifest.Metrics.ToolRepairSucceeded != 1 ||
		manifest.Metrics.ToolRepairNotes != 2 ||
		manifest.Metrics.ToolRepairByKind["alias_rename"] != 1 ||
		manifest.Metrics.InputTokens != 100 ||
		manifest.Metrics.OutputTokens != 20 ||
		manifest.Metrics.TraceEvents != 5 ||
		manifest.Metrics.TraceEventTypes["message.delta"] != 2 ||
		manifest.Metrics.TraceEventTypes["tool.result"] != 1 {
		t.Fatalf("manifest metrics = %+v", manifest.Metrics)
	}
	timeline, err := os.ReadFile(res.TimelinePath)
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	for _, want := range []string{
		"# Affent Eval Timeline",
		"metrics: tools=5 tool_errors=1 repaired=0 canonicalized=0 loop_guard=1 forced_no_tools=0 evidence=1/2_verified,network=1,partial=1,discovery=1 memory_updates=2(add:1,replace:1,remove:0) session_search=calls:1,results:2,context:1,terms:2 tool_context_trunc=2,omitted=8192 compactions=1,reactive=1,removed=12,summary_bytes=512 tokens=100/20",
		"## Runtime Surface",
		"`web_fetch`",
		"trace_deltas: `true`",
		"affentctl_command",
		"--api-key '<redacted>'",
		"## Trace Events",
		"`message.delta`: `2`",
		"## Source Evidence",
		"tool#1 `web_fetch` status=`dynamic_partial` url=`https://taostats.io/subnets/120`",
		"tool#2 `browser_network_read` status=`network` url=`https://taostats.io/api/subnets/120` json_path=`$.price`",
		"tool#3 `browser_navigate` status=`discovery_only` url=`https://search.example/?q=affine`",
		"## Memory Updates",
		"tool#4 action=`replace` location=`memory:markets` call_id=`call-4`",
		"Use direct price labels from dynamic dashboards. -> Use browser_network_read json_path before citing dynamic dashboard metrics.",
		"tool#5 action=`add` location=`memory:research` call_id=`call-5`",
		"Record network evidence gaps explicitly.",
		"## Tool Timeline",
		"failure_kinds: `dynamic_shell`",
		"need browser network evidence",
		"Context Compactions",
		"Final Message",
	} {
		if !strings.Contains(string(timeline), want) {
			t.Fatalf("timeline missing %q:\n%s", want, string(timeline))
		}
	}
}

func TestRedactedCommandArgvHidesAPIKey(t *testing.T) {
	got := redactedCommandArgv("go", []string{
		"run", "./cmd/affentctl", "run",
		"--api-key", "sk-secret",
		"--api-key=sk-other-secret",
		"--prompt", "large prompt body",
		"--prompt=other prompt body",
		"--model", "model-a",
	})
	joined := strings.Join(got, "\x00")
	if strings.Contains(joined, "sk-secret") || strings.Contains(joined, "sk-other-secret") ||
		strings.Contains(joined, "large prompt body") || strings.Contains(joined, "other prompt body") {
		t.Fatalf("command leaked sensitive argv value: %#v", got)
	}
	for _, want := range []string{"go", "--api-key\x00<redacted>", "--api-key=<redacted>", "--prompt\x00<prompt>", "--prompt=<prompt>", "--model\x00model-a"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("redacted command missing %q: %#v", want, got)
		}
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
		RuntimeTools:     " read_file,shell ",
		RuntimeAllTools:  true,
		RuntimeWeb:       true,
		RuntimeBrowser:   true,
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
		"--trace-skip-deltas",
		"--max-turns\x003",
		"--temperature\x000",
		"--top-p\x000.9",
		"--max-tokens\x00512",
		"--seed\x0042",
		"--api-key\x00secret",
		"--eval-mode",
		"--eval-all-tools",
		"--eval-tools\x00read_file,shell",
		"--memory=true",
		"--web=true",
		"--web-search=true",
		"--browser=true",
		"--mcp-config\x00/tmp/eval-mcp.json",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q:\n%q", want, args)
		}
	}
}

func TestBatchRunnerAffentctlRunArgsEvalToolFlagsImplyEvalMode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner BatchRunner
		want   string
	}{
		{
			name:   "runtime tools",
			runner: BatchRunner{RuntimeTools: "read_file"},
			want:   "--eval-tools\x00read_file",
		},
		{
			name:   "all tools",
			runner: BatchRunner{RuntimeAllTools: true},
			want:   "--eval-all-tools",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.runner.affentctlRunArgs("/tmp/ws", "/tmp/ws/trace.jsonl", BatchScenario{Prompt: "debug", MaxTurns: 1})
			joined := strings.Join(args, "\x00")
			if !strings.Contains(joined, "--eval-mode") {
				t.Fatalf("eval tool flags should imply --eval-mode:\n%q", args)
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("args missing %q:\n%q", tc.want, args)
			}
		})
	}
}

func TestBatchRunnerAffentctlRunArgsCanKeepTraceDeltas(t *testing.T) {
	args := (BatchRunner{
		BaseURL:     "https://llm.example/v1",
		Model:       "model-a",
		TraceDeltas: true,
	}).affentctlRunArgs("/tmp/ws", "/tmp/ws/trace.jsonl", BatchScenario{Prompt: "debug stream", MaxTurns: 2})
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "--trace\x00/tmp/ws/trace.jsonl") {
		t.Fatalf("args missing trace path:\n%q", args)
	}
	if strings.Contains(joined, "--trace-skip-deltas") {
		t.Fatalf("TraceDeltas should not pass --trace-skip-deltas:\n%q", args)
	}
}

func TestGoCommandUsableForRepoChecksModuleLoad(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example.test/eval\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "go-good")
	goodScript := "#!/bin/sh\nif [ \"$1\" = list ] && [ \"$2\" = -m ] && [ \"${GOTOOLCHAIN:-}\" = local ]; then exit 0; fi\nexit 1\n"
	if err := os.WriteFile(good, []byte(goodScript), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "go-bad")
	badScript := "#!/bin/sh\necho 'go: go.mod requires go >= 1.24.0 (running go 1.22.12; GOTOOLCHAIN=local)' >&2\nexit 1\n"
	if err := os.WriteFile(bad, []byte(badScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if !goCommandUsableForRepo(good, repoRoot) {
		t.Fatal("expected module-load-capable go command to be usable")
	}
	if goCommandUsableForRepo(bad, repoRoot) {
		t.Fatal("expected stale go command to be rejected")
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
