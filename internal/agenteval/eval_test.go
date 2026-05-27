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
		`{"type":"tool.result","data":{"call_id":"c1","result_summary":"large market report preview","result":"ok","exit_code":0,"duration_ms":17,"result_truncated":true,"result_bytes":300000,"result_omitted_bytes":4096,"result_cap_bytes":262144,"context_bytes":4096,"context_omitted_bytes":8192,"context_estimated_tokens":1024,"result_artifact_path":".affent/artifacts/tool-results/000001-c1.txt"}}`,
		`{"type":"tool.result","data":{"call_id":"guarded","result":"blocked\nFailure: kind=invalid_args","exit_code":1}}`,
		`{"type":"usage","data":{"input_tokens":11,"output_tokens":7}}`,
		`{"type":"error","data":{"message":"transient stream warning","failure_kind":"llm_timeout"}}`,
		`{"type":"loop.protocol_feed","data":{"turn_id":"t1","loop_id":"longrun","status":"running","mode":"digest","feed_number":4,"protocol_feeds":4,"protocol_path":".affent/loops/longrun/LOOP.md","plan_label":"plan:1/3:active","plan_current_step_index":2,"plan_current_step_status":"in_progress","plan_current_step":"verify browser network evidence"}}`,
		`{"type":"loop.decision","data":{"turn_id":"t1","decision_id":"d1","kind":"evidence_quality","trigger":"source_access_dynamic_partial","decision":"defer","confidence":"high","reason":"Dynamic widgets had no text values.","required_action":"Read browser network responses before citing metrics.","visible_in_ui":true}}`,
		`{"type":"context.compacted","data":{"turn_id":"t1","before_messages":50,"after_messages":18,"removed_messages":32,"reactive":true,"reason":"context_overflow","summary_present":true,"summary_bytes":2048,"summary_preview":"USER_CONTEXT: keep market evidence and exact source URLs","loop_protocol_anchor":"LOOP_PROTOCOL: active path=.affent/loops/longrun/LOOP.md mode=digest feed=4 feeds=4 plan=plan:1/3:active current=2:in_progress"}}`,
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
	if tc.ResultSummary != "large market report preview" {
		t.Fatalf("ResultSummary = %q", tc.ResultSummary)
	}
	if !tc.ResultTruncated || tc.ResultBytes != 300000 || tc.ResultOmittedBytes != 4096 || tc.ResultCapBytes != 262144 {
		t.Fatalf("tool result truncation metadata not parsed: %+v", tc)
	}
	if tc.ResultArtifactPath != ".affent/artifacts/tool-results/000001-c1.txt" {
		t.Fatalf("ResultArtifactPath = %q", tc.ResultArtifactPath)
	}
	if tc.ContextBytes != 4096 || tc.ContextOmittedBytes != 8192 || tc.ContextEstimatedTokens != 1024 {
		t.Fatalf("tool result context truncation metadata not parsed: %+v", tc)
	}
	examples := trace.ToolTruncationExamples(1)
	if len(examples) != 1 ||
		examples[0].CallID != "c1" ||
		!examples[0].ArgsTruncated ||
		!examples[0].ResultTruncated ||
		examples[0].ResultSummary != "large market report preview" ||
		examples[0].ContextOmittedBytes != 8192 ||
		examples[0].ResultArtifactPath != ".affent/artifacts/tool-results/000001-c1.txt" {
		t.Fatalf("ToolTruncationExamples = %+v", examples)
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
	if loopDecisions.Count != 1 ||
		loopDecisions.ByKind["evidence_quality"] != 1 ||
		loopDecisions.ByDecision["defer"] != 1 ||
		loopDecisions.ByMatch[loopDecisionMatchKey("evidence_quality", "defer", "source_access_dynamic_partial")] != 1 {
		t.Fatalf("LoopDecisionStats = %+v", loopDecisions)
	}
	if len(loopDecisions.Examples) != 1 ||
		loopDecisions.Examples[0].Trigger != "source_access_dynamic_partial" ||
		!strings.Contains(loopDecisions.Examples[0].RequiredAction, "browser network") {
		t.Fatalf("LoopDecisionStats examples = %+v", loopDecisions.Examples)
	}
	feeds := trace.LoopProtocolFeedStats(1)
	if feeds.Count != 1 || feeds.ByMode["digest"] != 1 || feeds.Latest.FeedNumber != 4 || feeds.Latest.ProtocolPath != ".affent/loops/longrun/LOOP.md" || feeds.Latest.PlanLabel != "plan:1/3:active" || feeds.Latest.PlanCurrentStepIndex != 2 {
		t.Fatalf("LoopProtocolFeedStats = %+v", feeds)
	}
	if len(feeds.Examples) != 1 || feeds.Examples[0].LoopID != "longrun" || feeds.Examples[0].Mode != "digest" || feeds.Examples[0].PlanCurrentStep != "verify browser network evidence" {
		t.Fatalf("LoopProtocolFeedStats examples = %+v", feeds.Examples)
	}
	compactions := trace.ContextCompactionStats(1)
	if compactions.Count != 1 || compactions.Reactive != 1 || compactions.Proactive != 0 || compactions.RemovedMessages != 32 || compactions.SummaryBytes != 2048 {
		t.Fatalf("ContextCompactionStats = %+v", compactions)
	}
	if len(compactions.Examples) != 1 ||
		compactions.Examples[0].Reason != "context_overflow" ||
		!compactions.Examples[0].SummaryPresent ||
		!strings.Contains(compactions.Examples[0].SummaryPreview, "market evidence") ||
		!strings.Contains(compactions.Examples[0].LoopProtocolAnchor, "path=.affent/loops/longrun/LOOP.md") {
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
		RequiredLoopProtocolFeeds: 1,
		RequiredLoopProtocolFeedModes: map[string]int{
			"digest": 1,
		},
		RequiredLoopProtocolFeedMatches: []LoopProtocolFeedRequirement{
			{Mode: "digest", PlanLabelContains: "market", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "source review"},
		},
		RequiredSourceAccess: []SourceAccessRequirement{
			{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch"},
		},
		RequiredSessionSearch: []SessionSearchRequirement{
			{QueryContains: "Alpha Coast", SessionID: "market-alpha", SnippetContains: "HIST-STOCK-44", MatchedTerms: []string{"alpha", "coast"}, ContextIncluded: true},
		},
		RequiredContextCompactions:    1,
		RequiredReactiveCompactions:   1,
		RequiredCompactionRemovedMsgs: 20,
		RequiredContextSummaryText:    []string{"HRO market marker"},
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
		"loop_protocol_feeds_at_least:1",
		"loop_protocol_feed_mode_at_least:digest:1",
		"loop_protocol_feed_match_at_least:digest:market:in_progress:source review:1",
		"source_access_match_at_least:network:browser_network_read:taostats.io:requested=taostats.io/subnets/120:network_xhr_fetch:*:1",
		"session_search_match_at_least:Alpha Coast:market-alpha:HIST-STOCK-44:alpha,coast:true:0:1",
		"context_compactions_at_least:1",
		"reactive_context_compactions_at_least:1",
		"context_compaction_removed_messages_at_least:20",
		"context_compaction_summary_contains:HRO market marker",
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

func TestBatchScenarioChecks_SourceAccessRequirementDefaultsToOne(t *testing.T) {
	checks := BatchScenarioChecks(BatchScenario{
		RequiredSourceAccess: []SourceAccessRequirement{
			{Status: "network", URLContains: "taostats.io"},
		},
	})
	if len(checks) != 2 {
		t.Fatalf("checks count = %d, want turn-end + source access match: %+v", len(checks), checks)
	}
	if !strings.HasPrefix(checks[1].Name, "source_access_match_at_least:network:*:taostats.io:*:*:1") {
		t.Fatalf("default source access check name = %q", checks[1].Name)
	}
}

func TestBatchScenarioChecks_SourceAccessRequirementCanMatchRequestedURL(t *testing.T) {
	checks := BatchScenarioChecks(BatchScenario{
		RequiredSourceAccess: []SourceAccessRequirement{
			{Status: "network", URLContains: "api.taostats.io", RequestedURLContains: "app.taostats.io/subnets/120"},
		},
	})
	if len(checks) != 2 {
		t.Fatalf("checks count = %d, want turn-end + source access match: %+v", len(checks), checks)
	}
	if !strings.HasPrefix(checks[1].Name, "source_access_match_at_least:network:*:api.taostats.io:requested=app.taostats.io/subnets") {
		t.Fatalf("requested source access check name = %q", checks[1].Name)
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
	if len(scenarios) != 11 {
		t.Fatalf("long-run suite size = %d, want 11", len(scenarios))
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
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "read_file", Arg: "path", Substring: "data/prices.csv"},
		{Tool: "read_file", Arg: "path", Substring: "data/analyst-estimates.md"},
		{Tool: "read_file", Arg: "path", Substring: "filings/2026-q1.md"},
	} {
		if !toolArgRequirementContains(stock.RequiredToolArgContains, want) {
			t.Fatalf("stock scenario RequiredToolArgContains = %#v, want %#v", stock.RequiredToolArgContains, want)
		}
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
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "read_file", Arg: "path", Substring: "official/affine-sn120.md"},
		{Tool: "read_file", Arg: "path", Substring: "metrics/tao-app-snapshot.txt"},
		{Tool: "read_file", Arg: "path", Substring: "network/validators.md"},
		{Tool: "read_file", Arg: "path", Substring: "sentiment/community-notes.md"},
	} {
		if !toolArgRequirementContains(subnet.RequiredToolArgContains, want) {
			t.Fatalf("Bittensor scenario RequiredToolArgContains = %#v, want %#v", subnet.RequiredToolArgContains, want)
		}
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
	if !stringSliceContains(pr.RequiredTools, "read_file") {
		t.Fatalf("code PR scenario RequiredTools = %#v, want read_file", pr.RequiredTools)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "read_file", Arg: "path", Substring: "queue/queue.go"},
		{Tool: "edit_file", Arg: "path", Substring: "queue/queue.go"},
	} {
		if !toolArgRequirementContains(pr.RequiredToolArgContains, want) {
			t.Fatalf("code PR scenario RequiredToolArgContains = %#v, want %#v", pr.RequiredToolArgContains, want)
		}
	}
	if !toolOrderContains(pr.RequiredToolOrder, ToolOrderRequirement{Earlier: "read_file", Later: "edit_file"}) {
		t.Fatalf("code PR scenario RequiredToolOrder = %#v, want read_file before edit_file", pr.RequiredToolOrder)
	}
	if pr.RequiredCommandCounts[`go test`] != 2 {
		t.Fatalf("code PR scenario RequiredCommandCounts = %#v, want go test=2", pr.RequiredCommandCounts)
	}
	if !stringSliceContains(pr.RequiredFinalText, "PR Summary") || !stringSliceContains(pr.RequiredFinalText, "Tests") || !stringSliceContains(pr.RequiredFinalText, "queue/queue.go") {
		t.Fatalf("code PR scenario RequiredFinalText = %#v, want PR Summary, Tests, and changed file", pr.RequiredFinalText)
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
	if planResume.RequiredLoopProtocolFeeds != 1 ||
		planResume.RequiredLoopProtocolFeedModes["full"] != 1 ||
		len(planResume.RequiredLoopProtocolFeedMatches) != 1 ||
		planResume.RequiredLoopProtocolFeedMatches[0].PlanCurrentStepStatus != "in_progress" ||
		!strings.Contains(planResume.RequiredLoopProtocolFeedMatches[0].PlanCurrentStep, "read current launch evidence") {
		t.Fatalf("plan resume loop protocol constraints = feeds:%d modes:%#v matches:%#v", planResume.RequiredLoopProtocolFeeds, planResume.RequiredLoopProtocolFeedModes, planResume.RequiredLoopProtocolFeedMatches)
	}
	if !stringSliceContains(planResume.ProtectedFiles, ".affent/loops/plan-resume/LOOP.md") {
		t.Fatalf("plan resume ProtectedFiles = %#v, want loop protocol", planResume.ProtectedFiles)
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

	memorySessionJoin, ok := seen["longrun-memory-session-join"]
	if !ok {
		t.Fatalf("long-run suite missing memory/session join scenario")
	}
	if !memorySessionJoin.EnableMemory || memorySessionJoin.SessionID != "memory-session-join-reader" {
		t.Fatalf("memory/session join fields = memory:%v session:%q", memorySessionJoin.EnableMemory, memorySessionJoin.SessionID)
	}
	if memorySessionJoin.RequiredToolCounts["memory"] != 1 ||
		memorySessionJoin.RequiredToolCounts["session_search"] != 1 ||
		memorySessionJoin.MaxSuccessfulToolCallsByTool["memory"] != 1 ||
		memorySessionJoin.MaxSuccessfulToolCallsByTool["session_search"] != 1 {
		t.Fatalf("memory/session join tool constraints = counts:%#v max:%#v", memorySessionJoin.RequiredToolCounts, memorySessionJoin.MaxSuccessfulToolCallsByTool)
	}
	for _, want := range []string{"MEM-JOIN-22", "HIST-JOIN-88", "backlog-slippage", "source-led", "alpha-current"} {
		if !stringSliceContains(memorySessionJoin.RequiredFinalText, want) {
			t.Fatalf("memory/session join RequiredFinalText = %#v, want %q", memorySessionJoin.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(memorySessionJoin.ForbiddenFinalText, "HIST-JOIN-OLD") {
		t.Fatalf("memory/session join ForbiddenFinalText = %#v, want stale history marker", memorySessionJoin.ForbiddenFinalText)
	}
	assertSessionSearchDiagnosticsRequired(t, memorySessionJoin)

	multiTaskRecovery, ok := seen["longrun-multitask-session-recovery"]
	if !ok {
		t.Fatalf("long-run suite missing multi-task session recovery scenario")
	}
	if multiTaskRecovery.SessionID != "longrun-recovery-reader" {
		t.Fatalf("multi-task recovery fields = session:%q", multiTaskRecovery.SessionID)
	}
	if multiTaskRecovery.RequiredToolCounts["session_search"] != 1 || multiTaskRecovery.MaxSuccessfulToolCallsByTool["session_search"] != 1 {
		t.Fatalf("multi-task recovery tool constraints = counts:%#v max:%#v", multiTaskRecovery.RequiredToolCounts, multiTaskRecovery.MaxSuccessfulToolCallsByTool)
	}
	for _, want := range []string{"RECOVER-NSTAR-58", "trial-delay", "verify FDA calendar", "northstar-q3-current"} {
		if !stringSliceContains(multiTaskRecovery.RequiredFinalText, want) {
			t.Fatalf("multi-task recovery RequiredFinalText = %#v, want %q", multiTaskRecovery.RequiredFinalText, want)
		}
	}
	for _, forbidden := range []string{"RECOVER-OLD-12", "RECOVER-SN120-77", "HIST-STOCK-44"} {
		if !stringSliceContains(multiTaskRecovery.ForbiddenFinalText, forbidden) {
			t.Fatalf("multi-task recovery ForbiddenFinalText = %#v, want %q", multiTaskRecovery.ForbiddenFinalText, forbidden)
		}
	}
	assertSessionSearchDiagnosticsRequiredForTerms(t, multiTaskRecovery, []string{`"northstar"`, `"biotech"`})

	compactionRetention, ok := seen["longrun-context-compaction-retention"]
	if !ok {
		t.Fatalf("long-run suite missing context compaction retention scenario")
	}
	if compactionRetention.CompactTrigger != 6 || compactionRetention.CompactKeepLast != 3 {
		t.Fatalf("compaction retention settings = trigger:%d keep_last:%d, want 6/3", compactionRetention.CompactTrigger, compactionRetention.CompactKeepLast)
	}
	if compactionRetention.RequiredContextCompactions != 1 || compactionRetention.RequiredCompactionRemovedMsgs != 1 {
		t.Fatalf("compaction retention requirements = compactions:%d removed:%d, want 1/1", compactionRetention.RequiredContextCompactions, compactionRetention.RequiredCompactionRemovedMsgs)
	}
	if compactionRetention.RequiredToolCounts["read_file"] != 5 || compactionRetention.MaxSuccessfulToolCallsByTool["read_file"] != 5 {
		t.Fatalf("compaction retention read constraints = counts:%#v max:%#v", compactionRetention.RequiredToolCounts, compactionRetention.MaxSuccessfulToolCallsByTool)
	}
	for _, want := range []string{"COMPRESS-HRO-31", "COMPRESS-SN120-42", "COMPRESS-PR-77"} {
		if !stringSliceContains(compactionRetention.RequiredContextSummaryText, want) {
			t.Fatalf("compaction retention RequiredContextSummaryText = %#v, want %q", compactionRetention.RequiredContextSummaryText, want)
		}
	}
	if !stringSliceContains(compactionRetention.ForbiddenTools, "shell") {
		t.Fatalf("compaction retention ForbiddenTools = %#v, want shell", compactionRetention.ForbiddenTools)
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

	focusedRecovery, ok := seen["longrun-focused-task-recovery-synthesis"]
	if !ok {
		t.Fatalf("long-run suite missing focused-task recovery scenario")
	}
	if focusedRecovery.RequiredFocusedTaskCounts["explore"] != 1 || !focusedRecovery.RequireNoDelegationErrors {
		t.Fatalf("focused recovery delegation constraints = counts:%#v no_errors:%v", focusedRecovery.RequiredFocusedTaskCounts, focusedRecovery.RequireNoDelegationErrors)
	}
	if focusedRecovery.MaxParentToolCalls != 1 {
		t.Fatalf("focused recovery MaxParentToolCalls = %d, want 1", focusedRecovery.MaxParentToolCalls)
	}
	for _, forbidden := range []string{"read_file", "repo_search", "subagent_run"} {
		if !stringSliceContains(focusedRecovery.ForbiddenTools, forbidden) {
			t.Fatalf("focused recovery ForbiddenTools = %#v, want %q", focusedRecovery.ForbiddenTools, forbidden)
		}
	}
	for _, want := range []string{"LOOP-FOCUS-64", "verify inventory trend", "validator concentration", "current/loop-state.md"} {
		if !stringSliceContains(focusedRecovery.RequiredFinalText, want) {
			t.Fatalf("focused recovery RequiredFinalText = %#v, want %q", focusedRecovery.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(focusedRecovery.ForbiddenFinalText, "LOOP-OLD-00") {
		t.Fatalf("focused recovery ForbiddenFinalText = %#v, want stale marker guard", focusedRecovery.ForbiddenFinalText)
	}
}

func TestSelectLiveWebSuite(t *testing.T) {
	scenarios, err := SelectBatchScenariosForSuite("live-web", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("live-web suite size = %d, want 2", len(scenarios))
	}
	seen := map[string]BatchScenario{}
	for _, scenario := range scenarios {
		seen[scenario.Name] = scenario
	}
	scenario, ok := seen["live-web-taostats-sn120-dynamic-evidence"]
	if !ok {
		t.Fatalf("live-web suite missing dynamic evidence scenario")
	}
	for _, want := range []string{"browser_navigate", "browser_network", "browser_network_read"} {
		if !stringSliceContains(scenario.RequiredTools, want) {
			t.Fatalf("live-web RequiredTools = %#v, want %q", scenario.RequiredTools, want)
		}
	}
	for _, field := range []string{"source_access_results", "source_access_verified", "source_access_network"} {
		if scenario.RequiredToolStatsAtLeast[field] != 1 {
			t.Fatalf("live-web source access requirements = %#v, want %s=1", scenario.RequiredToolStatsAtLeast, field)
		}
	}
	if len(scenario.RequiredSourceAccess) != 1 ||
		scenario.RequiredSourceAccess[0] != (SourceAccessRequirement{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch"}) {
		t.Fatalf("live-web RequiredSourceAccess = %#v", scenario.RequiredSourceAccess)
	}
	for _, want := range []string{"SourceAccess:", "browser_network_url=", "requested_url=", "ref=", "status=", "content_type=", "source_method=network_xhr_fetch"} {
		if !stringSliceContains(scenario.RequiredToolResultText["browser_network_read"], want) {
			t.Fatalf("live-web browser_network_read result requirements = %#v, want %q", scenario.RequiredToolResultText["browser_network_read"], want)
		}
	}
	for _, want := range []string{"browser_network_url", "requested_url", "ref=", "status=", "content_type=", "source_method"} {
		if !stringSliceContains(scenario.RequiredFinalText, want) {
			t.Fatalf("live-web RequiredFinalText = %#v, want %q", scenario.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(scenario.ForbiddenTools, "shell") {
		t.Fatalf("live-web ForbiddenTools = %#v, want shell", scenario.ForbiddenTools)
	}

	recovery, ok := seen["live-web-taostats-web-fetch-recovery"]
	if !ok {
		t.Fatalf("live-web suite missing web_fetch recovery scenario")
	}
	for _, want := range []string{"web_fetch", "browser_navigate", "browser_network", "browser_network_read"} {
		if !stringSliceContains(recovery.RequiredTools, want) {
			t.Fatalf("live-web recovery RequiredTools = %#v, want %q", recovery.RequiredTools, want)
		}
	}
	if recovery.RequiredToolCounts["web_fetch"] != 1 || recovery.RequiredToolCounts["browser_network_read"] != 1 {
		t.Fatalf("live-web recovery tool counts = %#v, want web_fetch/browser_network_read once", recovery.RequiredToolCounts)
	}
	if len(recovery.RequiredToolOrder) != 3 ||
		recovery.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "web_fetch", Later: "browser_navigate"}) ||
		recovery.RequiredToolOrder[1] != (ToolOrderRequirement{Earlier: "browser_navigate", Later: "browser_network"}) ||
		recovery.RequiredToolOrder[2] != (ToolOrderRequirement{Earlier: "browser_network", Later: "browser_network_read"}) {
		t.Fatalf("live-web recovery tool order = %#v", recovery.RequiredToolOrder)
	}
	if len(recovery.RequiredSourceAccess) != 1 ||
		recovery.RequiredSourceAccess[0] != (SourceAccessRequirement{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch"}) {
		t.Fatalf("live-web recovery RequiredSourceAccess = %#v", recovery.RequiredSourceAccess)
	}
	for _, want := range []string{"SourceAccess:", "browser_network_url=", "requested_url=", "ref=", "status=", "content_type=", "source_method=network_xhr_fetch"} {
		if !stringSliceContains(recovery.RequiredToolResultText["browser_network_read"], want) {
			t.Fatalf("live-web recovery browser_network_read result requirements = %#v, want %q", recovery.RequiredToolResultText["browser_network_read"], want)
		}
	}
	for _, want := range []string{"web_fetch", "browser_network_url", "requested_url", "ref=", "status=", "content_type=", "source_method"} {
		if !stringSliceContains(recovery.RequiredFinalText, want) {
			t.Fatalf("live-web recovery RequiredFinalText = %#v, want %q", recovery.RequiredFinalText, want)
		}
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

func toolArgRequirementContains(values []ToolArgContainsRequirement, want ToolArgContainsRequirement) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertSessionSearchDiagnosticsRequired(t *testing.T, scenario BatchScenario) {
	t.Helper()
	assertSessionSearchDiagnosticsRequiredForTerms(t, scenario, []string{`"alpha"`, `"coast"`})
}

func assertSessionSearchDiagnosticsRequiredForTerms(t *testing.T, scenario BatchScenario, terms []string) {
	t.Helper()
	required := strings.Join(scenario.RequiredToolResultText["session_search"], "\n")
	wants := append([]string{`"context_included":true`, `"matched_terms"`}, terms...)
	for _, want := range wants {
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
	if len(scenario.RequiredSessionSearch) == 0 {
		t.Fatalf("%s RequiredSessionSearch missing", scenario.Name)
	}
	req := scenario.RequiredSessionSearch[0]
	if !req.ContextIncluded {
		t.Fatalf("%s RequiredSessionSearch should require context: %+v", scenario.Name, req)
	}
	for _, want := range terms {
		term := strings.Trim(want, `"`)
		if !stringSliceContains(req.MatchedTerms, term) {
			t.Fatalf("%s RequiredSessionSearch matched terms = %#v, want %q", scenario.Name, req.MatchedTerms, term)
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

func toolOrderContains(values []ToolOrderRequirement, want ToolOrderRequirement) bool {
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
	focusedTranscript := filepath.Join(workspace, ".affentctl", "focused-tasks", "debug-session", "focused_alpha.jsonl")
	subagentTranscript := filepath.Join(workspace, ".affentctl", "subagents", "debug-session", "subagent_beta.jsonl")
	for path, body := range map[string]string{
		focusedTranscript:  `{"role":"system","content":"focused child"}` + "\n",
		subagentTranscript: `{"role":"system","content":"subagent child"}` + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
		ToolCalls:        8,
		Repair:           ToolRepairStats{Calls: 1, SucceededCalls: 1, Notes: 2, ByKind: map[string]int{"tool_name": 1, "alias_rename": 1}},
		ToolFailureExamples: map[string][]ToolFailureExample{
			"dynamic_shell": {{
				Kind:          "dynamic_shell",
				Tool:          "web_fetch",
				ArgsSummary:   `url="https://example.test/report"`,
				ResultSummary: "dynamic dashboard exposed empty metric widgets; next use browser_network_read",
				ExitCode:      1,
			}},
		},
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
		RuntimeErrorExamples: map[string][]RuntimeErrorExample{
			"llm_timeout": {{
				Kind:    "llm_timeout",
				Message: "llm stream timed out after first token",
			}},
		},
		ToolTruncation: ToolTruncationStats{ArgsTruncated: 1, ArgsOmittedBytes: 128, ResultsTruncated: 1, ResultsOmittedBytes: 4096, ResultArtifacts: 1},
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
		ContextCompactions: ContextCompactionStats{
			Count:           1,
			Reactive:        1,
			RemovedMessages: 12,
			SummaryBytes:    512,
			Examples: []ContextCompaction{{
				TurnID:          "turn-debug",
				BeforeMessages:  30,
				AfterMessages:   12,
				RemovedMessages: 18,
				Reactive:        true,
				Reason:          "context_overflow",
				SummaryPresent:  true,
				SummaryBytes:    512,
				SummaryPreview:  "USER_CONTEXT: debug run must preserve browser network evidence.",
			}},
		},
		Usage: Usage{InputTokens: 100, OutputTokens: 20},
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
			TurnID:                 "turn-debug",
			CallID:                 "call-1",
			Tool:                   "web_fetch",
			OriginalTool:           "webFetch",
			Canonicalized:          true,
			ArgsRepaired:           true,
			OriginalArgsSummary:    `{"URL":"https://example.test/report"}`,
			RepairNotes:            []string{"canonicalized tool webFetch to web_fetch", "renamed field URL to url"},
			Args:                   map[string]any{"url": "https://example.test/report"},
			ArgsTruncated:          true,
			ArgsBytes:              70000,
			ArgsOmittedBytes:       128,
			ArgsCapBytes:           65536,
			Result:                 "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence\nPAGE DIAGNOSTICS:\n- empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value\nPAGE TEXT:\nAffine SN120\nloop_guard: blocked repeated failed call to \"web_fetch\" with the same effective URL after previous Failure kind=dynamic_shell.\nNext: switch to browser_network_read before citing dynamic dashboard metrics.\nFailure: kind=loop_guard_repeated_failed_input",
			ResultSummary:          "Rendered page partial dynamic evidence: empty metric widgets",
			ResultTruncated:        true,
			ResultBytes:            300000,
			ResultOmittedBytes:     4096,
			ResultCapBytes:         262144,
			ResultArtifactPath:     ".affent/artifacts/tool-results/000001-call-1.txt",
			ContextBytes:           4096,
			ContextOmittedBytes:    8192,
			ContextEstimatedTokens: 1024,
			FailureKinds:           []string{"dynamic_shell", "loop_guard_repeated_failed_input"},
			ExitCode:               1,
			DurationMS:             42,
		}, {
			TurnID:     "turn-debug",
			CallID:     "call-2",
			Tool:       "browser_network_read",
			Args:       map[string]any{"ref": "n1", "json_path": "$.price"},
			Result:     "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\nJSON_PATH: $.price\n\"0.06342 T\"",
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
		}, {
			TurnID:   "turn-debug",
			CallID:   "call-6",
			Tool:     "session_search",
			Args:     map[string]any{"query": "Alpha Coast", "top_k": 3},
			Result:   `{"query":"Alpha Coast","total":2,"results":[{"session_id":"market-alpha","turn_idx":4,"message_idx":8,"role":"assistant","snippet":"history marker ALPHA-COAST risk label elevated","score":2.5,"matched_terms":["alpha","coast"],"context_included":true},{"session_id":"market-beta","turn_idx":2,"message_idx":3,"role":"user","snippet":"older Alpha note without the current risk label","score":1,"matched_terms":["alpha"],"context_included":false}]}`,
			ExitCode: 0,
		}, {
			TurnID: "turn-debug",
			CallID: "call-7",
			Tool:   "plan",
			Args: map[string]any{
				"action":   "update",
				"index":    float64(2),
				"status":   "completed",
				"evidence": []any{"go test ./internal/agenteval"},
				"note":     "verified browser evidence step",
			},
			Result:   `{"version":1,"message":"updated step 2","steps":[{"text":"inspect dynamic dashboard","status":"completed"},{"text":"verify browser network evidence","status":"completed","evidence":["go test ./internal/agenteval"],"note":"verified browser evidence step"},{"text":"summarize findings","status":"pending"}]}`,
			ExitCode: 0,
		}, {
			TurnID: "turn-debug",
			CallID: "call-8",
			Tool:   "browser_network",
			Args:   map[string]any{"query": "market_cap", "max_results": float64(5)},
			Result: "BROWSER NETWORK EVIDENCE\n" +
				"CURRENT_PAGE: https://taostats.io/subnets/120\n" +
				"query: \"market_cap\"\n" +
				"MATCHES:\n" +
				"- n1 status=200 resource=fetch content_type=application/json url=https://taostats.io/api/subnets/120\n" +
				"  preview: {\"price\":\"0.06342 T\"}\n" +
				"Next: call browser_network_read with the most relevant ref and json_path before citing values.\n",
			ExitCode: 0,
		}},
		LoopDecisions: []LoopDecision{{
			Kind:     "evidence_quality",
			Decision: "defer",
			Trigger:  "source_access_dynamic_partial",
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
			SummaryPreview:  "USER_CONTEXT: debug run must preserve browser network evidence.",
		}},
		FinalText:    "partial answer",
		FinishReason: "stop",
	}
	scenario := BatchScenario{
		Prompt:   "research with evidence",
		Suites:   []string{longRunSuite, liveWebSuite},
		MaxTurns: 12,
		RequiredTools: []string{
			"web_fetch",
			"browser_network_read",
		},
		ForbiddenTools: []string{"shell"},
		RequiredToolCounts: map[string]int{
			"browser_network_read": 1,
		},
		RequiredToolFailureKindCounts: map[string]int{
			"dynamic_shell": 1,
		},
		RequiredToolStatsAtLeast: map[string]int{
			"memory_updates":                2,
			"source_access_dynamic_partial": 1,
			"source_access_network":         1,
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
		RequiredLoopProtocolFeeds: 1,
		RequiredLoopProtocolFeedModes: map[string]int{
			"digest": 1,
		},
		RequiredLoopProtocolFeedMatches: []LoopProtocolFeedRequirement{
			{Mode: "digest", PlanLabelContains: "debug", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "browser network evidence"},
		},
		RequiredToolResultText: map[string][]string{
			"browser_network_read": {"SourceAccess:", "requested_url=", "source_method=network_xhr_fetch"},
		},
		RequiredToolOrder: []ToolOrderRequirement{
			{Earlier: "web_fetch", Later: "browser_network_read"},
		},
		RequiredToolArgContains: []ToolArgContainsRequirement{
			{Tool: "browser_network_read", Arg: "json_path", Substring: "$.price"},
		},
		RequiredCommandBeforeTool: []CommandToolOrderRequirement{
			{Command: "go test", Tool: "memory"},
		},
		RequiredCommandAfterTool: []CommandToolOrderRequirement{
			{Command: "go test", Tool: "edit_file"},
		},
		RequiredFocusedTaskCounts: map[string]int{
			"research": 1,
		},
		RequiredSubagentModeCounts: map[string]int{
			"review": 1,
		},
		RequireNoDelegationErrors: true,
		RequireNoPlanErrors:       true,
		RequiredSourceAccess: []SourceAccessRequirement{
			{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io/api", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch", JSONPath: "$.price"},
		},
		RequiredSessionSearch: []SessionSearchRequirement{
			{QueryContains: "Alpha Coast", SessionID: "market-alpha", SnippetContains: "history marker", MatchedTerms: []string{"alpha", "coast"}, ContextIncluded: true, TurnIdx: 4},
		},
		RequiredFinalText:             []string{"0.06342 T"},
		ForbiddenFinalText:            []string{"subnet price $277.32"},
		RequiredTruncatedResults:      []string{"web_fetch"},
		RequiredResultArtifacts:       []string{"web_fetch"},
		RequiredContextCompactions:    1,
		RequiredCompactionRemovedMsgs: 12,
		RequiredContextSummaryText:    []string{"browser network evidence"},
		ProtectedFiles:                []string{"README.md"},
		ForbiddenFileSubstrings: map[string][]string{
			"notes.md": {"uncited taostats metric"},
		},
		CompactTrigger:  6,
		CompactKeepLast: 3,
	}
	err := writeScenarioDebugArtifacts(&res, scenario, "partial answer\n", "runtime log\n", &trace)
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
	wantCapabilities := []string{"browser", "context_compaction", "delegation", "loop_protocol", "memory", "plan", "session_search", "source_access", "web", "workspace"}
	if !reflect.DeepEqual(manifest.ExpectationCapabilityNames, wantCapabilities) ||
		manifest.ExpectationCapabilityOutcome != "failed" ||
		len(manifest.ExpectationCapabilityPassedNames) != 0 ||
		!reflect.DeepEqual(manifest.ExpectationCapabilityFailedNames, wantCapabilities) {
		t.Fatalf("manifest expectation capabilities = names:%#v outcome:%q passed:%#v failed:%#v",
			manifest.ExpectationCapabilityNames,
			manifest.ExpectationCapabilityOutcome,
			manifest.ExpectationCapabilityPassedNames,
			manifest.ExpectationCapabilityFailedNames,
		)
	}
	if manifest.Expectations.MaxTurns != 12 ||
		manifest.Expectations.CompactTrigger != 6 ||
		manifest.Expectations.CompactKeepLast != 3 ||
		!stringSliceContains(manifest.Expectations.CheckNames, "turn_ended_cleanly") ||
		!stringSliceContains(manifest.Expectations.CheckNames, "tool_called:web_fetch") ||
		!stringSliceContains(manifest.Expectations.CheckNames, "context_compaction_summary_contains:browser network evidence") ||
		!reflect.DeepEqual(manifest.Expectations.Suites, []string{longRunSuite, liveWebSuite}) ||
		!reflect.DeepEqual(manifest.Expectations.RequiredTools, []string{"web_fetch", "browser_network_read"}) ||
		!reflect.DeepEqual(manifest.Expectations.ForbiddenTools, []string{"shell"}) ||
		manifest.Expectations.RequiredToolCounts["browser_network_read"] != 1 ||
		manifest.Expectations.RequiredToolFailureKindCounts["dynamic_shell"] != 1 ||
		manifest.Expectations.RequiredToolStatsAtLeast["memory_updates"] != 2 ||
		manifest.Expectations.RequiredToolStatsAtLeast["source_access_dynamic_partial"] != 1 ||
		manifest.Expectations.RequiredToolStatsAtLeast["source_access_network"] != 1 ||
		manifest.Expectations.RequiredLoopDecisionKinds["evidence_quality"] != 1 ||
		manifest.Expectations.RequiredLoopDecisionResults["defer"] != 1 ||
		len(manifest.Expectations.RequiredLoopDecisionMatches) != 1 ||
		manifest.Expectations.RequiredLoopDecisionMatches[0] != (DebugLoopDecisionRequirement{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial"}) ||
		manifest.Expectations.RequiredLoopProtocolFeeds != 1 ||
		manifest.Expectations.RequiredLoopProtocolFeedModes["digest"] != 1 ||
		len(manifest.Expectations.RequiredLoopProtocolFeedMatches) != 1 ||
		manifest.Expectations.RequiredLoopProtocolFeedMatches[0] != (DebugLoopProtocolFeedRequirement{Mode: "digest", PlanLabelContains: "debug", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "browser network evidence"}) ||
		!reflect.DeepEqual(manifest.Expectations.RequiredToolResultText["browser_network_read"], []string{"SourceAccess:", "requested_url=", "source_method=network_xhr_fetch"}) ||
		len(manifest.Expectations.RequiredToolOrder) != 1 ||
		manifest.Expectations.RequiredToolOrder[0] != (DebugToolOrderRequirement{Earlier: "web_fetch", Later: "browser_network_read"}) ||
		len(manifest.Expectations.RequiredCommandBeforeTool) != 1 ||
		manifest.Expectations.RequiredCommandBeforeTool[0] != (DebugCommandToolOrderRequirement{Command: "go test", Tool: "memory"}) ||
		len(manifest.Expectations.RequiredCommandAfterTool) != 1 ||
		manifest.Expectations.RequiredCommandAfterTool[0] != (DebugCommandToolOrderRequirement{Command: "go test", Tool: "edit_file"}) ||
		manifest.Expectations.RequiredFocusedTaskCounts["research"] != 1 ||
		manifest.Expectations.RequiredSubagentModeCounts["review"] != 1 ||
		!manifest.Expectations.RequireNoDelegationErrors ||
		!manifest.Expectations.RequireNoPlanErrors ||
		len(manifest.Expectations.RequiredToolArgContains) != 1 ||
		manifest.Expectations.RequiredToolArgContains[0] != (DebugToolArgContainsRequirement{Tool: "browser_network_read", Arg: "json_path", Substring: "$.price"}) ||
		len(manifest.Expectations.RequiredSourceAccess) != 1 ||
		manifest.Expectations.RequiredSourceAccess[0] != (DebugSourceAccessRequirement{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io/api", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch", JSONPath: "$.price"}) ||
		len(manifest.Expectations.RequiredSessionSearch) != 1 ||
		!reflect.DeepEqual(manifest.Expectations.RequiredSessionSearch[0], DebugSessionSearchRequirement{QueryContains: "Alpha Coast", SessionID: "market-alpha", SnippetContains: "history marker", MatchedTerms: []string{"alpha", "coast"}, ContextIncluded: true, TurnIdx: 4}) ||
		!stringSliceContains(manifest.Expectations.RequiredFinalText, "0.06342 T") ||
		!stringSliceContains(manifest.Expectations.ForbiddenFinalText, "subnet price $277.32") ||
		!reflect.DeepEqual(manifest.Expectations.RequiredTruncatedResults, []string{"web_fetch"}) ||
		!reflect.DeepEqual(manifest.Expectations.RequiredResultArtifacts, []string{"web_fetch"}) ||
		manifest.Expectations.RequiredContextCompactions != 1 ||
		manifest.Expectations.RequiredCompactionRemovedMsgs != 12 ||
		!stringSliceContains(manifest.Expectations.RequiredContextSummaryText, "browser network evidence") ||
		!reflect.DeepEqual(manifest.Expectations.ProtectedFiles, []string{"README.md"}) ||
		!reflect.DeepEqual(manifest.Expectations.ForbiddenFileSubstrings["notes.md"], []string{"uncited taostats metric"}) {
		t.Fatalf("manifest expectations = %+v", manifest.Expectations)
	}
	if manifest.DebugBrief == nil || len(manifest.DebugBrief.Tags) == 0 {
		t.Fatalf("manifest debug brief missing: %+v", manifest.DebugBrief)
	}
	if !stringSliceContains(manifest.DebugBrief.Tags, "tool_failure:dynamic_shell") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "runtime_error:llm_timeout") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "source_dynamic_partial") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "recall:context") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "memory_update:replace") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "context_compaction:reactive") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "browser_network:refs") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "truncation") {
		t.Fatalf("manifest debug brief tags = %+v", manifest.DebugBrief.Tags)
	}
	if manifest.RuntimeSurface == nil ||
		manifest.RuntimeSurface.ToolCount != 2 ||
		!manifest.RuntimeSurface.Capabilities.WebFetch ||
		!manifest.RuntimeSurface.Capabilities.WebSearch ||
		manifest.RuntimeSurface.Tools[0].Name != "web_fetch" {
		t.Fatalf("manifest runtime surface = %+v", manifest.RuntimeSurface)
	}
	if len(manifest.ToolRepairExamples) != 1 ||
		manifest.ToolRepairExamples[0].ToolIndex != 1 ||
		manifest.ToolRepairExamples[0].Tool != "web_fetch" ||
		manifest.ToolRepairExamples[0].OriginalTool != "webFetch" ||
		!manifest.ToolRepairExamples[0].Canonicalized ||
		!manifest.ToolRepairExamples[0].ArgsRepaired ||
		!reflect.DeepEqual(manifest.ToolRepairExamples[0].RepairKinds, []string{"tool_name", "alias_rename"}) {
		t.Fatalf("manifest tool repair examples = %+v", manifest.ToolRepairExamples)
	}
	if len(manifest.SourceAccessExamples) != 3 ||
		manifest.SourceAccessExamples[0].Tool != "web_fetch" ||
		manifest.SourceAccessExamples[0].Status != "dynamic_partial" ||
		manifest.SourceAccessExamples[1].Status != "network" ||
		manifest.SourceAccessExamples[1].RequestedURL != "https://taostats.io/subnets/120" ||
		manifest.SourceAccessExamples[1].JSONPath != "$.price" ||
		manifest.SourceAccessExamples[1].Ref != "n1" ||
		manifest.SourceAccessExamples[1].HTTPStatus != "200" ||
		manifest.SourceAccessExamples[1].ContentType != "application/json" ||
		!strings.Contains(manifest.SourceAccessExamples[1].ResultPreview, `"0.06342 T"`) ||
		manifest.SourceAccessExamples[2].Status != "discovery_only" {
		t.Fatalf("manifest source access examples = %+v", manifest.SourceAccessExamples)
	}
	if len(manifest.BrowserNetworkExamples) != 1 ||
		manifest.BrowserNetworkExamples[0].ToolIndex != 8 ||
		manifest.BrowserNetworkExamples[0].CallID != "call-8" ||
		manifest.BrowserNetworkExamples[0].Status != "matches" ||
		manifest.BrowserNetworkExamples[0].Query != "market_cap" ||
		!manifest.BrowserNetworkExamples[0].RequiresRead ||
		!manifest.BrowserNetworkExamples[0].NotCitable ||
		!reflect.DeepEqual(manifest.BrowserNetworkExamples[0].Refs, []string{"n1"}) ||
		!reflect.DeepEqual(manifest.BrowserNetworkExamples[0].Previews, []string{`{"price":"0.06342 T"}`}) {
		t.Fatalf("manifest browser network examples = %+v", manifest.BrowserNetworkExamples)
	}
	if len(manifest.LoopGuardExamples) != 1 ||
		manifest.LoopGuardExamples[0].ToolIndex != 1 ||
		manifest.LoopGuardExamples[0].CallID != "call-1" ||
		manifest.LoopGuardExamples[0].Tool != "web_fetch" ||
		manifest.LoopGuardExamples[0].Kind != "loop_guard_repeated_failed_input" ||
		manifest.LoopGuardExamples[0].Category != "loop_guard" ||
		!strings.Contains(manifest.LoopGuardExamples[0].ArgsSummary, "https://example.test/report") ||
		!strings.Contains(manifest.LoopGuardExamples[0].GuardSummary, "blocked repeated failed call") ||
		!strings.Contains(manifest.LoopGuardExamples[0].SuggestedNextStep, "browser_network_read") {
		t.Fatalf("manifest loop guard examples = %+v", manifest.LoopGuardExamples)
	}
	if len(manifest.MemoryUpdateExamples) != 2 ||
		manifest.MemoryUpdateExamples[0].ToolIndex != 4 ||
		manifest.MemoryUpdateExamples[0].Action != "replace" ||
		manifest.MemoryUpdateExamples[0].Location != "memory:markets" ||
		!strings.Contains(manifest.MemoryUpdateExamples[0].Preview, "browser_network_read") ||
		manifest.MemoryUpdateExamples[1].Action != "add" ||
		manifest.MemoryUpdateExamples[1].Location != "memory:research" {
		t.Fatalf("manifest memory update examples = %+v", manifest.MemoryUpdateExamples)
	}
	if len(manifest.SessionSearchExamples) != 2 ||
		manifest.SessionSearchExamples[0].ToolIndex != 6 ||
		manifest.SessionSearchExamples[0].CallID != "call-6" ||
		manifest.SessionSearchExamples[0].Query != "Alpha Coast" ||
		manifest.SessionSearchExamples[0].SessionID != "market-alpha" ||
		manifest.SessionSearchExamples[0].TurnIdx != 4 ||
		manifest.SessionSearchExamples[0].MessageIdx != 8 ||
		!manifest.SessionSearchExamples[0].ContextIncluded ||
		!reflect.DeepEqual(manifest.SessionSearchExamples[0].MatchedTerms, []string{"alpha", "coast"}) ||
		!strings.Contains(manifest.SessionSearchExamples[0].SnippetPreview, "history marker") {
		t.Fatalf("manifest session search examples = %+v", manifest.SessionSearchExamples)
	}
	if len(manifest.PlanExamples) != 1 ||
		manifest.PlanExamples[0].ToolIndex != 7 ||
		manifest.PlanExamples[0].CallID != "call-7" ||
		manifest.PlanExamples[0].Action != "update" ||
		manifest.PlanExamples[0].Index != 2 ||
		manifest.PlanExamples[0].Status != "completed" ||
		manifest.PlanExamples[0].StepText != "verify browser network evidence" ||
		manifest.PlanExamples[0].CurrentStep != "summarize findings" ||
		!reflect.DeepEqual(manifest.PlanExamples[0].Evidence, []string{"go test ./internal/agenteval"}) {
		t.Fatalf("manifest plan examples = %+v", manifest.PlanExamples)
	}
	if len(manifest.ToolTruncationExamples) != 1 ||
		manifest.ToolTruncationExamples[0].ToolIndex != 1 ||
		manifest.ToolTruncationExamples[0].CallID != "call-1" ||
		!manifest.ToolTruncationExamples[0].ArgsTruncated ||
		!manifest.ToolTruncationExamples[0].ResultTruncated ||
		manifest.ToolTruncationExamples[0].ResultSummary != "Rendered page partial dynamic evidence: empty metric widgets" ||
		manifest.ToolTruncationExamples[0].ContextOmittedBytes != 8192 ||
		manifest.ToolTruncationExamples[0].ResultArtifactPath != ".affent/artifacts/tool-results/000001-call-1.txt" {
		t.Fatalf("manifest tool truncation examples = %+v", manifest.ToolTruncationExamples)
	}
	if len(manifest.ContextCompactionExamples) != 1 ||
		manifest.ContextCompactionExamples[0].TurnID != "turn-debug" ||
		!manifest.ContextCompactionExamples[0].Reactive ||
		manifest.ContextCompactionExamples[0].RemovedMessages != 18 ||
		manifest.ContextCompactionExamples[0].Reason != "context_overflow" ||
		!strings.Contains(manifest.ContextCompactionExamples[0].SummaryPreview, "browser network evidence") {
		t.Fatalf("manifest context compaction examples = %+v", manifest.ContextCompactionExamples)
	}
	if len(manifest.ChildTranscripts) != 2 ||
		manifest.ChildTranscripts[0].Kind != "focused_task" ||
		manifest.ChildTranscripts[0].Path != ".affentctl/focused-tasks/debug-session/focused_alpha.jsonl" ||
		manifest.ChildTranscripts[1].Kind != "subagent" ||
		manifest.ChildTranscripts[1].Path != ".affentctl/subagents/debug-session/subagent_beta.jsonl" {
		t.Fatalf("manifest child transcript refs = %+v", manifest.ChildTranscripts)
	}
	if manifest.Metrics.ToolCalls != 8 ||
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
		"metrics: tools=8 tool_errors=1 repaired=0 canonicalized=0 loop_guard=1 forced_no_tools=0 evidence=1/2_verified,network=1,partial=1,discovery=1 memory_updates=2(add:1,replace:1,remove:0) session_search=calls:1,results:2,context:1,terms:2,terms_per_call:2.00 tool_context_trunc=2,omitted=8192 compactions=1,reactive=1,removed=12,summary_bytes=512,summary_missing=0,summary_empty=0 tokens=100/20",
		"## Runtime Surface",
		"`web_fetch`",
		"## Tool Repair",
		"tool#1 `web_fetch` original=`webFetch` call_id=`call-1` canonicalized=`true` args_repaired=`true` exit=`1` kinds=`tool_name,alias_rename`",
		"note: renamed field URL to url",
		"trace_deltas: `true`",
		"affentctl_command",
		"--api-key '<redacted>'",
		"## Debug Brief",
		"outcome: `failed`; inspect the failure list",
		"tool_failure_by_kind: `dynamic_shell=1`",
		"tool_failure_example[dynamic_shell]: tool=`web_fetch` exit=`1` args=url=\"https://example.test/report\"",
		"runtime_error_by_kind: `llm_timeout=1`",
		"runtime_error_example[llm_timeout]: llm stream timed out after first token",
		"loop_guard: `1` intervention(s), `0` forced no-tools",
		"## Loop Guard",
		"tool#1 `web_fetch` kind=`loop_guard_repeated_failed_input` category=`loop_guard` exit=`1` call_id=`call-1`",
		"args: url=\"https://example.test/report\"",
		"guard: blocked repeated failed call to \"web_fetch\" with the same effective URL after previous Failure kind=dynamic_shell.",
		"next: switch to browser_network_read before citing dynamic dashboard metrics.",
		"child_transcripts: `2` indexed",
		"## Child Transcripts",
		"kind=`focused_task` path=`.affentctl/focused-tasks/debug-session/focused_alpha.jsonl`",
		"kind=`subagent` path=`.affentctl/subagents/debug-session/subagent_beta.jsonl`",
		"## Scenario Expectations",
		"expectation_capabilities: `browser`, `context_compaction`, `delegation`, `loop_protocol`, `memory`, `plan`, `session_search`, `source_access`, `web`, `workspace` outcome=`failed`",
		"suites: `long-run`, `live-web`",
		"runtime: `max_turns=12 compact_trigger=6 compact_keep_last=3`",
		"checks: `turn_ended_cleanly`",
		"required_tools: `web_fetch`, `browser_network_read`",
		"forbidden_tools: `shell`",
		"required_tool_counts: `browser_network_read=1`",
		"required_tool_order: `web_fetch -> browser_network_read`",
		"required_command_before_tool: `go test -> memory`",
		"required_command_after_tool: `go test -> edit_file`",
		"required_tool_failure_kind_counts: `dynamic_shell=1`",
		"required_tool_stats_at_least: `memory_updates=2,source_access_dynamic_partial=1,source_access_network=1`",
		"required_loop_decision_kinds: `evidence_quality=1`",
		"required_loop_decision_results: `defer=1`",
		"required_loop_protocol_feeds: `1`",
		"required_loop_protocol_feed_modes: `digest=1`",
		"required_focused_task_counts: `research=1`",
		"required_subagent_mode_counts: `review=1`",
		"required_no_errors: `delegation plan`",
		"required_loop_decision: `kind=evidence_quality decision=defer trigger=source_access_dynamic_partial min=1`",
		"required_loop_protocol_feed: `mode=digest plan_label_contains=debug plan_current_step_status=in_progress plan_current_step=browser network evidence min=1`",
		"required_tool_result_text[browser_network_read]: `SourceAccess:`, `requested_url=`, `source_method=network_xhr_fetch`",
		"required_source_access: `status=network tool=browser_network_read url_contains=taostats.io/api requested_url_contains=taostats.io/subnets/120 source_method=network_xhr_fetch json_path=$.price min=1`",
		"required_session_search: `query_contains=Alpha Coast session=market-alpha snippet_contains=history marker terms=alpha,coast context=true turn=4 min=1`",
		"required_final_text: `0.06342 T`",
		"forbidden_final_text: `subnet price $277.32`",
		"required_truncated_results: `web_fetch`",
		"required_result_artifacts: `web_fetch`",
		"required_tool_arg: `browser_network_read.json_path` contains `$.price` min=`1`",
		"context_requirements: `compactions>=1 removed_messages>=12`",
		"context_summary_contains: `browser network evidence`",
		"protected_files: `README.md`",
		"forbidden_file_substrings[notes.md]: `uncited taostats metric`",
		"evidence: `1/2` verified, network=`1`, partial=`1`, discovery=`1`",
		"recall_weak_context: calls=`1`, results=`2`, context=`1`, terms=`2`; only some hits included adjacent context; inspect Session Search examples for incomplete recovery.",
		"context: compactions=`1`, reactive=`1`, removed_messages=`12`, summary_bytes=`512`",
		"truncation: tool_context=2 omitted_context=8192 args=1 args_omitted=128 results=1 results_omitted=4096 artifacts=1",
		"## Trace Events",
		"`message.delta`: `2`",
		"## Source Evidence",
		"tool#1 `web_fetch` status=`dynamic_partial` url=`https://taostats.io/subnets/120`",
		"preview: PAGE DIAGNOSTICS: - empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value PAGE TEXT: Affine SN120",
		"tool#2 `browser_network_read` status=`network` url=`https://taostats.io/api/subnets/120` requested=`https://taostats.io/subnets/120` ref=`n1` source_method=`network_xhr_fetch` http_status=`200` content_type=`application/json` json_path=`$.price`",
		"preview: JSON_PATH: $.price \"0.06342 T\"",
		"tool#3 `browser_navigate` status=`discovery_only` url=`https://search.example/?q=affine`",
		"## Browser Network Searches",
		"tool#8 status=`matches` query=`market_cap` page=`https://taostats.io/subnets/120` call_id=`call-8` requires_read=`true` citable=`false`",
		"refs: `n1`",
		"## Plan Updates",
		"tool#7 action=`update` index=`2` status=`completed` progress=`2/3` current=`3:pending` call_id=`call-7`",
		"step: verify browser network evidence",
		"current_step: summarize findings",
		"evidence: `go test ./internal/agenteval`",
		"## Memory Updates",
		"tool#4 action=`replace` location=`memory:markets` call_id=`call-4`",
		"Use direct price labels from dynamic dashboards. -> Use browser_network_read json_path before citing dynamic dashboard metrics.",
		"tool#5 action=`add` location=`memory:research` call_id=`call-5`",
		"Record network evidence gaps explicitly.",
		"## Session Search",
		"tool#6 query=`Alpha Coast` total=`2` session=`market-alpha` turn=`4` message=`8` role=`assistant` terms=`alpha,coast` context=`true` call_id=`call-6`",
		"snippet: history marker ALPHA-COAST risk label elevated",
		"## Tool Truncation",
		"tool#1 `web_fetch` call_id=`call-1`",
		"args: truncated=`true` bytes=`70000` omitted=`128` cap=`65536`",
		"result: truncated=`true` bytes=`300000` omitted=`4096` cap=`262144`",
		"summary: Rendered page partial dynamic evidence: empty metric widgets",
		"context: bytes=`4096` omitted=`8192` estimated_tokens=`1024`",
		"artifact: `.affent/artifacts/tool-results/000001-call-1.txt`",
		"## Tool Timeline",
		"failure_kinds: `dynamic_shell`",
		"need browser network evidence",
		"Context Compactions",
		"summary_preview:",
		"USER_CONTEXT: debug run must preserve browser network evidence.",
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
		Prompt:          "fix it",
		SessionID:       "planned",
		ExecutePlan:     true,
		EnableMemory:    true,
		MaxTurns:        3,
		CompactTrigger:  6,
		CompactKeepLast: 3,
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
		"--compact-trigger\x006",
		"--compact-keep-last\x003",
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
