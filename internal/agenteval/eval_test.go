package agenteval

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

func TestExpectationCapabilityNamesIncludesResearchCheckpoint(t *testing.T) {
	caps := ExpectationCapabilityNames(DebugScenarioExpectations{
		RequiredLoopDecisionMatches: []DebugLoopDecisionRequirement{{
			Kind:     "research_checkpoint",
			Decision: "trigger",
			Trigger:  "external_calibration_requested",
		}},
	})
	if !reflect.DeepEqual(caps, []string{"research_checkpoint"}) {
		t.Fatalf("ExpectationCapabilityNames = %#v, want research checkpoint only", caps)
	}
}

func TestExpectationCapabilityNamesIncludesDelegatedSourceEvidence(t *testing.T) {
	caps := ExpectationCapabilityNames(DebugScenarioExpectations{
		RequiredFocusedTaskSourceCounts: map[string]int{"research": 2},
		RequiredSubagentSourceCounts:    map[string]int{"review": 1},
	})
	want := []string{"delegated_source_evidence", "delegation"}
	if !reflect.DeepEqual(caps, want) {
		t.Fatalf("ExpectationCapabilityNames = %#v, want %#v", caps, want)
	}
}

func TestExpectationCapabilityNamesIncludesSkillInstall(t *testing.T) {
	caps := ExpectationCapabilityNames(DebugScenarioExpectations{
		RequiredToolArgContains: []DebugToolArgContainsRequirement{{
			Tool:      "skill",
			Arg:       "action",
			Substring: "confirm_install",
		}},
	})
	want := []string{"skill", "skill_install"}
	if !reflect.DeepEqual(caps, want) {
		t.Fatalf("ExpectationCapabilityNames = %#v, want %#v", caps, want)
	}
}

func TestDebugSourceExamplesUseFullTraceForQualitySignals(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{Tool: "browser_network_read", Result: `SourceAccess: browser_network_url=https://example.test/api/1; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 10
{}`},
		{Tool: "browser_network_read", Result: `SourceAccess: browser_network_url=https://example.test/api/2; ref=n2; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 10
{}`},
		{Tool: "browser_network_read", Result: `SourceAccess: browser_network_url=https://example.test/api/3; ref=n3; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 10
{}`},
		{Tool: "browser_network_read", Result: `SourceAccess: browser_network_url=https://example.test/api/4; ref=n4; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 10
{}`},
		{Tool: "browser_network_read", Result: `SourceAccess: browser_network_url=https://example.test/api/5; ref=n5; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 10
{}`},
		{Tool: "browser_network_read", Result: `SourceAccess: browser_network_url=https://example.test/api/6; ref=n6; status=200; content_type=application/json; source_method=network_xhr_fetch
BODY_BYTES: 200 (offset 0, showing 80, omitted_after 120, next_offset 80)
{"partial":true}
[... 120 bytes omitted after this chunk; retry with offset=80, a narrower json_path, or max_bytes up to 65536 ...]`},
	}}

	examples := sourceAccessExamplesForDebug(trace)
	if len(examples) != 6 || examples[5].Ref != "n6" || !examples[5].HasMore {
		t.Fatalf("sourceAccessExamplesForDebug = %+v, want all trace source examples including late partial read", examples)
	}
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:  len(examples),
			SourceAccessVerified: len(examples),
			SourceAccessNetwork:  len(examples),
		},
		SourceAccessExamples: examples,
	})
	if !stringSliceContains(brief.Tags, "source_network:partial_read") {
		t.Fatalf("debug brief should see late partial read beyond display cap, tags=%+v", brief.Tags)
	}
}

func TestDebugMemorySearchMissExamplesUseFullTraceForRecallSignals(t *testing.T) {
	anchored := ToolCall{
		Tool:     "memory",
		ExitCode: 0,
		Args: map[string]any{
			"action": "search",
			"target": "memory",
			"query":  "deploy",
		},
		Result: `{"ok":true,"message":"no entries matched. Next: retry with a topic anchor.","target":"memory","results":[],"topics":[{"topic":"deploy","entries":2}]}`,
	}
	trace := Trace{}
	for i := 0; i < 5; i++ {
		trace.Tools = append(trace.Tools, anchored)
	}
	trace.Tools = append(trace.Tools, ToolCall{
		Tool:     "memory",
		CallID:   "late-no-anchor",
		ExitCode: 0,
		Args: map[string]any{
			"action": "search",
			"target": "user",
			"query":  "ssh key preference",
		},
		Result: `{"ok":true,"message":"no entries matched. Next: retry with fewer keywords.","target":"user","results":[]}`,
	})

	examples := memorySearchMissExamplesForDebug(trace)
	if len(examples) != 6 || examples[5].CallID != "late-no-anchor" || examples[5].TopicCount != 0 {
		t.Fatalf("memorySearchMissExamplesForDebug = %+v, want all trace miss examples including late no-anchor miss", examples)
	}
	brief := BuildDebugBrief(BatchResult{
		OK: true,
		ToolStats: ToolRuntimeStats{
			MemorySearchCalls:  len(examples),
			MemorySearchMisses: len(examples),
		},
		MemorySearchMissExamples: examples,
	})
	item := debugBriefItemByKind(brief, "memory_search_miss")
	if item == nil ||
		item.Severity != "warn" ||
		item.Counts["anchor_examples"] != 5 ||
		item.Counts["no_anchor_examples"] != 1 ||
		!stringSliceContains(brief.Tags, "recall:memory_topic_anchors") ||
		!stringSliceContains(brief.Tags, "recall:memory_no_topic_anchors") {
		t.Fatalf("debug brief should see late no-anchor memory miss, item=%+v tags=%+v", item, brief.Tags)
	}
}

func TestDebugRecoveryPriorityTagsIncludesRecallDegradation(t *testing.T) {
	got := debugRecoveryPriorityTags(&DebugBrief{Tags: []string{
		"recall:memory_topic_anchors",
		"recall:memory_no_topic_anchors",
		"recall:weak_matched_terms",
		"recall:weak_context",
		"empty_recall:recent_sessions",
		"source_network:partial_read",
		"outcome:failed",
		"misc:later",
	}})
	want := []string{
		"outcome:failed",
		"source_network:partial_read",
		"empty_recall:recent_sessions",
		"recall:weak_context",
		"recall:weak_matched_terms",
		"recall:memory_no_topic_anchors",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("debugRecoveryPriorityTags = %#v, want %#v", got, want)
	}
}

func TestDebugRecoveryPriorityTagsIncludesVerifierFailures(t *testing.T) {
	got := debugRecoveryPriorityTags(&DebugBrief{Tags: []string{
		"verifier:output_truncated",
		"verifier:abnormal",
		"verifier:failed",
		"source_network:partial_read",
		"outcome:failed",
		"misc:later",
	}})
	want := []string{
		"outcome:failed",
		"verifier:failed",
		"verifier:abnormal",
		"verifier:output_truncated",
		"source_network:partial_read",
		"misc:later",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("debugRecoveryPriorityTags = %#v, want %#v", got, want)
	}
}

func TestDebugRecoveryPriorityTagsIncludesLoopProtocolFixture(t *testing.T) {
	got := debugRecoveryPriorityTags(&DebugBrief{Tags: []string{
		"loop_protocol:fixture",
		"loop_guard:forced_no_tools",
		"outcome:failed",
		"misc:later",
	}})
	want := []string{
		"outcome:failed",
		"loop_protocol:fixture",
		"loop_guard:forced_no_tools",
		"misc:later",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("debugRecoveryPriorityTags = %#v, want %#v", got, want)
	}
}

func TestDebugRecoveryPriorityTagsIncludesResearchCheckpointEvidenceGap(t *testing.T) {
	got := debugRecoveryPriorityTags(&DebugBrief{Tags: []string{
		"research_checkpoint:no_external_evidence",
		"loop_guard:forced_no_tools",
		"outcome:failed",
		"misc:later",
	}})
	want := []string{
		"outcome:failed",
		"research_checkpoint:no_external_evidence",
		"loop_guard:forced_no_tools",
		"misc:later",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("debugRecoveryPriorityTags = %#v, want %#v", got, want)
	}
}

func TestSessionSearchExamplesIncludeRecentNoHitAnchors(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		Tool:     "session_search",
		CallID:   "search-empty",
		ExitCode: 0,
		Result:   `{"query":"missing marker","total":0,"results":[],"message":"no results. Next: retry from anchors.","recent_sessions":[{"session_id":"recent-a","mod_time":"2026-05-27T12:00:00Z","latest_user":"Analyze Alpha Coast recovery","latest_assistant":"final marker HIST-STOCK-44","plan":"plan_status: plan:1/2:active current_step: 2 [in_progress] Recheck Alpha Coast risk","loop":"loop_status: running current_situation: preserve Alpha Coast source evidence","recovery":"turn_end: reason=max_turns; top_failure=loop_guard_no_new_evidence:2"},{"session_id":"recent-b","latest_user":"Other task"}]}`,
	}}}

	examples := trace.SessionSearchExamples(5)
	if len(examples) != 2 {
		t.Fatalf("SessionSearchExamples len = %d, want 2: %+v", len(examples), examples)
	}
	if examples[0].CallID != "search-empty" ||
		examples[0].Query != "missing marker" ||
		examples[0].RecentSessions != 2 ||
		examples[0].RecentSessionID != "recent-a" ||
		examples[0].RecentModTime != "2026-05-27T12:00:00Z" ||
		!strings.Contains(examples[0].RecentUserPreview, "Alpha Coast") ||
		!strings.Contains(examples[0].RecentAssistantPreview, "HIST-STOCK-44") ||
		!strings.Contains(examples[0].RecentPlanPreview, "Recheck Alpha Coast risk") ||
		!strings.Contains(examples[0].RecentLoopPreview, "source evidence") ||
		!strings.Contains(examples[0].RecentRecoveryPreview, "loop_guard_no_new_evidence") ||
		!strings.Contains(examples[0].Message, "retry") {
		t.Fatalf("unexpected recent anchor example: %+v", examples[0])
	}
}

func TestSessionSearchExamplesIncludeRecoveryEventHits(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		Tool:     "session_search",
		CallID:   "search-event",
		ExitCode: 0,
		Result:   `{"query":"loop_guard_no_new_evidence max_turns","total":1,"results":[{"session_id":"stalled-loop","message_idx":3,"role":"event","snippet":"turn_end: reason=max_turns; top_failure=loop_guard_no_new_evidence:2","score":4.4,"matched_terms":["loop","guard","evidence","max","turns"],"mod_time":"2026-05-27T12:00:00Z"}]}`,
	}}}

	examples := trace.SessionSearchExamples(5)
	if len(examples) != 1 {
		t.Fatalf("SessionSearchExamples len = %d, want 1: %+v", len(examples), examples)
	}
	got := examples[0]
	if got.CallID != "search-event" ||
		got.SessionID != "stalled-loop" ||
		got.MessageIdx != 3 ||
		got.Role != "event" ||
		!strings.Contains(got.SnippetPreview, "loop_guard_no_new_evidence") ||
		!reflect.DeepEqual(got.MatchedTerms, []string{"loop", "guard", "evidence", "max", "turns"}) {
		t.Fatalf("unexpected event hit example: %+v", got)
	}
}

func TestMemorySearchMissExamplesIncludeTopicAnchors(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		Tool:     "memory",
		CallID:   "mem-search-empty",
		ExitCode: 0,
		Args: map[string]any{
			"action": "search",
			"target": "memory",
			"query":  "helm deployment",
		},
		Result: `{"ok":true,"message":"no entries matched. Next: retry with fewer/different keywords, search a specific topic from topics, or use action=list for full topic discovery.","target":"memory","topic":"deploy","results":[],"topics":[{"topic":"deploy","entries":2},{"topic":"auth","entries":1}]}`,
	}}}

	examples := trace.MemorySearchMissExamples(5)
	if len(examples) != 1 {
		t.Fatalf("MemorySearchMissExamples len = %d, want 1: %+v", len(examples), examples)
	}
	ex := examples[0]
	if ex.ToolIndex != 1 ||
		ex.CallID != "mem-search-empty" ||
		ex.Target != "memory" ||
		ex.Topic != "deploy" ||
		ex.Query != "helm deployment" ||
		ex.TopicCount != 2 ||
		!reflect.DeepEqual(ex.Topics, []string{"deploy", "auth"}) ||
		!strings.Contains(ex.Message, "no entries matched") {
		t.Fatalf("unexpected memory search miss example: %+v", ex)
	}
}

func TestMemorySearchMissExamplesIncludeNoAnchorMisses(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		Tool:     "memory",
		CallID:   "mem-search-user-empty",
		ExitCode: 0,
		Args: map[string]any{
			"action": "search",
			"target": "user",
			"query":  "ssh key preference",
		},
		Result: `{"ok":true,"message":"no entries matched. Next: retry with fewer/different keywords, search a specific topic from topics, or use action=list for full topic discovery.","target":"user","results":[]}`,
	}}}

	examples := trace.MemorySearchMissExamples(5)
	if len(examples) != 1 {
		t.Fatalf("MemorySearchMissExamples len = %d, want 1: %+v", len(examples), examples)
	}
	ex := examples[0]
	if ex.ToolIndex != 1 ||
		ex.CallID != "mem-search-user-empty" ||
		ex.Target != "user" ||
		ex.Query != "ssh key preference" ||
		ex.TopicCount != 0 ||
		len(ex.Topics) != 0 ||
		!strings.Contains(ex.Message, "no entries matched") {
		t.Fatalf("unexpected no-anchor memory search miss example: %+v", ex)
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

func TestBatchScenarioPromptHelpers(t *testing.T) {
	single := BatchScenario{Prompt: "one"}
	if got := batchScenarioPrompts(single); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("single prompts = %#v", got)
	}
	multi := BatchScenario{Prompt: "legacy", Prompts: []string{"first", "second"}}
	if got := batchScenarioPrompts(multi); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("multi prompts = %#v", got)
	}
	display := batchScenarioPromptDisplay(multi)
	for _, want := range []string{"Turn 1:", "first", "Turn 2:", "second"} {
		if !strings.Contains(display, want) {
			t.Fatalf("prompt display missing %q:\n%s", want, display)
		}
	}
}

func TestBatchRunnerRejectsMultiTurnWithoutSessionID(t *testing.T) {
	res := (BatchRunner{}).Run(context.Background(), BatchScenario{Name: "multi-no-session", Prompts: []string{"first", "second"}})
	if res.OK || len(res.Failures) == 0 || !strings.Contains(res.Failures[0], "requires SessionID") {
		t.Fatalf("multi-turn without session should fail early: %+v", res)
	}
}

func TestLoopProtocolCalibrationExpectationDoesNotRequireActiveFixture(t *testing.T) {
	calibrationOnly := BatchScenario{
		Name:                                    "loop-calibration-only",
		SessionID:                               "loop-calibration-only",
		RequiredLoopProtocolCalibrationRequests: 1,
		RequiredLoopProtocolCalibrations:        1,
	}
	if scenarioRequiresActiveLoopProtocol(calibrationOnly) {
		t.Fatal("calibration-only setup expectations should not require a pre-active LOOP.md fixture")
	}
	withFeed := calibrationOnly
	withFeed.RequiredLoopProtocolFeeds = 1
	if !scenarioRequiresActiveLoopProtocol(withFeed) {
		t.Fatal("loop protocol feed expectations should require an active LOOP.md fixture")
	}
}

func TestBatchRunnerRejectsLoopProtocolExpectationWithoutProtocolFile(t *testing.T) {
	res := (BatchRunner{WorkRoot: t.TempDir()}).Run(context.Background(), BatchScenario{
		Name:                      "loop-missing",
		SessionID:                 "loop-missing",
		RequiredLoopProtocolFeeds: 1,
		Files: map[string]string{
			"README.md": "missing active loop protocol\n",
		},
	})
	if res.OK || len(res.Failures) == 0 {
		t.Fatalf("loop protocol expectation without file should fail early: %+v", res)
	}
	for _, want := range []string{"requires loop protocol feeds", ".affent/loops/loop-missing/LOOP.md", "missing"} {
		if !strings.Contains(res.Failures[0], want) {
			t.Fatalf("failure missing %q: %+v", want, res.Failures)
		}
	}
}

func TestBatchRunnerRejectsLoopProtocolExpectationWithDraftProtocol(t *testing.T) {
	res := (BatchRunner{WorkRoot: t.TempDir()}).Run(context.Background(), BatchScenario{
		Name:                      "loop-draft",
		SessionID:                 "loop-draft",
		RequiredLoopProtocolFeeds: 1,
		Files: map[string]string{
			".affent/loops/loop-draft/LOOP.md": "# Loop Protocol\n\n## 0. Metadata\n\n- status: draft\n",
		},
	})
	if res.OK || len(res.Failures) == 0 {
		t.Fatalf("loop protocol expectation with draft file should fail early: %+v", res)
	}
	for _, want := range []string{"requires loop protocol feeds", ".affent/loops/loop-draft/LOOP.md", `status "draft"`, "want running"} {
		if !strings.Contains(res.Failures[0], want) {
			t.Fatalf("failure missing %q: %+v", want, res.Failures)
		}
	}
}

func TestBatchRunnerRejectsLoopProtocolExpectationWithInactiveState(t *testing.T) {
	res := (BatchRunner{WorkRoot: t.TempDir()}).Run(context.Background(), BatchScenario{
		Name:                      "loop-paused-state",
		SessionID:                 "loop-paused-state",
		RequiredLoopProtocolFeeds: 1,
		Files: map[string]string{
			".affent/loops/loop-paused-state/LOOP.md":    "# Loop Protocol\n\n## 0. Metadata\n\n- status: running\n",
			".affent/loops/loop-paused-state/state.json": `{"status":"paused"}`,
		},
	})
	if res.OK || len(res.Failures) == 0 {
		t.Fatalf("loop protocol expectation with paused state should fail early: %+v", res)
	}
	for _, want := range []string{"requires loop protocol feeds", ".affent/loops/loop-paused-state/LOOP.md", `status "paused"`, "want running"} {
		if !strings.Contains(res.Failures[0], want) {
			t.Fatalf("failure missing %q: %+v", want, res.Failures)
		}
	}
}

func TestBatchRunnerRejectsLoopProtocolExpectationWithInvalidState(t *testing.T) {
	res := (BatchRunner{WorkRoot: t.TempDir()}).Run(context.Background(), BatchScenario{
		Name:                      "loop-invalid-state",
		SessionID:                 "loop-invalid-state",
		RequiredLoopProtocolFeeds: 1,
		Files: map[string]string{
			".affent/loops/loop-invalid-state/LOOP.md":    "# Loop Protocol\n\n## 0. Metadata\n\n- status: running\n",
			".affent/loops/loop-invalid-state/state.json": `{not-json`,
		},
	})
	if res.OK || len(res.Failures) == 0 {
		t.Fatalf("loop protocol expectation with invalid state should fail early: %+v", res)
	}
	for _, want := range []string{"read loop protocol state", ".affent/loops/loop-invalid-state/LOOP.md", "invalid character"} {
		if !strings.Contains(res.Failures[0], want) {
			t.Fatalf("failure missing %q: %+v", want, res.Failures)
		}
	}
}

func TestParseTraceFileReadsToolRequestsAndFinalText(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	body := strings.Join([]string{
		`{"type":"trace.meta","data":{"schema_version":1}}`,
		`{"type":"user.message","data":{"turn_id":"t1","text":"Proceed with the active persisted plan.","display_text":"Run plan step 2","mode":"execute_plan"}}`,
		`{"type":"conversation.repaired","data":{"session_id":"resume","missing_tool_results":1,"failure_kind":"resume_missing_tool_result","next":"do not assume the tool succeeded"}}`,
		`{"type":"runtime.surface","data":{"turn_id":"t1","tool_count":2,"tools":[{"name":"web_fetch","group":"Web"},{"name":"web_search","group":"Web"}],"tool_call_caps":[{"tool":"web_fetch","max":8},{"tool":"web_search","max":4}],"capabilities":{"web_fetch":true,"web_search":true,"session_search":true,"skill":true,"mcp":true},"max_turn_steps":12,"max_tool_calls":7,"tool_result_event_cap_bytes":262144,"tool_result_context_max_bytes":5120,"tool_result_context_budget_bytes":32768,"tool_result_artifact_prefix":".affent/artifacts/tool-results","turn_tool_override":true}}`,
		`{"type":"context.injected","data":{"turn_id":"t1","source":"account_access","title":"Account access context injected","summary":"Account-level environment and SSH access hints were made available for this turn.","preview":"Configured environment variables available to shell commands: GITHUB_TOKEN.","bytes":240,"estimated_tokens":60}}`,
		`{"type":"context.injected","data":{"turn_id":"t1","source":"active_plan","title":"Active plan context injected","summary":"Current step: 2. Execute this step before broadening.","preview":"Current step: 2. Execute this step before broadening. - [ ] verify browser network evidence","bytes":360,"estimated_tokens":90}}`,
		`{"type":"tool.request","data":{"call_id":"c1","tool":"shell","args":{"command":"go test ./..."},"args_truncated":true,"args_bytes":70000,"args_omitted_bytes":512,"args_cap_bytes":65536,"original_tool":"Shell","original_args_summary":"{\"cmd\":\"go test ./...\"}","canonicalized":true,"args_repaired":true,"repair_notes":["renamed tool","renamed field"]}}`,
		`{"type":"tool.result","data":{"call_id":"c1","result_summary":"large market report preview","result":"ok","exit_code":0,"duration_ms":17,"result_truncated":true,"result_bytes":300000,"result_omitted_bytes":4096,"result_cap_bytes":262144,"context_bytes":4096,"context_omitted_bytes":8192,"context_estimated_tokens":1024,"result_artifact_path":".affent/artifacts/tool-results/000001-c1.txt"}}`,
		`{"type":"tool.result","data":{"call_id":"guarded","result":"blocked\nFailure: kind=invalid_args","exit_code":1}}`,
		`{"type":"usage","data":{"input_tokens":11,"output_tokens":7}}`,
		`{"type":"error","data":{"message":"transient stream warning","failure_kind":"llm_timeout"}}`,
		`{"type":"loop.protocol_calibration_request","data":{"loop_id":"longrun","status":"draft","calibration_questions":1,"last_calibration_question_preview":"What should pause this loop?","protocol_path":".affent/loops/longrun/LOOP.md","event_seq":2}}`,
		`{"type":"loop.protocol_calibration","data":{"loop_id":"longrun","status":"draft","calibration_questions":1,"last_calibration_question_preview":"What should pause this loop?","calibration_answers":1,"last_calibration_answer_preview":"Stop if browser network evidence is missing.","protocol_path":".affent/loops/longrun/LOOP.md","event_seq":3}}`,
		`{"type":"loop.protocol_feed","data":{"turn_id":"t1","loop_id":"longrun","status":"running","mode":"digest","feed_number":4,"protocol_feeds":4,"calibration_answers":1,"last_calibration_answer_preview":"Stop if browser network evidence is missing.","protocol_path":".affent/loops/longrun/LOOP.md","current_situation_preview":"current intent: verify browser network evidence; current risk: dashboard metrics are partial until network refs are read","plan_label":"plan:1/3:active","plan_current_step_index":2,"plan_current_step_status":"in_progress","plan_current_step":"verify browser network evidence","last_turn_id":"turn_previous","last_turn_end_reason":"max_turns","last_turn_tool_requests":5,"last_turn_tool_errors":1,"last_turn_forced_no_tools":1,"last_turn_memory_updates":1,"last_turn_memory_search_calls":3,"last_turn_memory_search_misses":2,"last_turn_session_search_calls":1,"last_turn_loop_guards":1,"last_decision_kind":"evidence_quality","last_decision_trigger":"source_access_dynamic_partial","last_decision":"defer","last_decision_confidence":"high","last_decision_reason":"Dynamic widgets had no text values.","last_decision_required_action":"Read browser network responses before citing metrics."}}`,
		`{"type":"loop.decision","data":{"turn_id":"t1","decision_id":"d1","kind":"evidence_quality","trigger":"source_access_dynamic_partial","decision":"defer","confidence":"high","reason":"Dynamic widgets had no text values.","required_action":"Read browser network responses before citing metrics.","visible_in_ui":true}}`,
		`{"type":"loop.turn_checkpoint","data":{"turn_id":"t1","loop_id":"longrun","status":"running","protocol_path":".affent/loops/longrun/LOOP.md","event_seq":7,"turn_checkpoints":1,"end_reason":"max_turns","input_tokens":101,"output_tokens":17,"tool_requests":2,"tool_errors":1,"loop_guards":1,"forced_no_tools":1,"memory_updates":2,"memory_search_calls":2,"memory_search_misses":1,"session_search_calls":1}}`,
		`{"type":"context.compacted","data":{"turn_id":"t1","before_messages":50,"after_messages":18,"removed_messages":32,"reactive":true,"reason":"context_overflow","summary_present":true,"summary_bytes":2048,"summary_preview":"USER_CONTEXT: keep market evidence and exact source URLs","loop_protocol_anchor":"LOOP_PROTOCOL: active path=.affent/loops/longrun/LOOP.md mode=digest feed=4 feeds=4 plan=plan:1/3:active current=2:in_progress"}}`,
		`{"type":"loop.protocol_feed","data":{"turn_id":"t2","loop_id":"longrun","status":"running","mode":"full","feed_number":5,"protocol_feeds":5,"protocol_path":".affent/loops/longrun/LOOP.md","plan_label":"plan:1/3:active","plan_current_step_index":2,"plan_current_step_status":"in_progress","plan_current_step":"verify browser network evidence"}}`,
		`{"type":"message.done","data":{"text":"Conclusion: green","finish_reason":"stop"}}`,
		`{"type":"turn.end","data":{"reason":"completed","tool_stats":{"tool_requests":2,"tool_name_canonicalized":1,"tool_args_repaired":1,"tool_repair_calls":1,"tool_repair_succeeded":1,"tool_repair_failed":0,"tool_repair_notes":2,"tool_repair_by_kind":{"tool_name":1,"alias_rename":1},"tool_failure_by_kind":{"invalid_args":1},"tool_errors":1,"tool_duration_ms":17,"loop_guard_interventions":1,"forced_no_tools":1,"source_access_dynamic_partial":1,"memory_updates":2,"memory_update_add":1,"memory_update_replace":1,"memory_search_calls":2,"memory_search_misses":1,"session_search_calls":1,"session_search_results":2,"session_search_context_hits":1,"session_search_matched_terms":2,"session_search_recent_sessions":3,"tool_context_truncated":2,"tool_context_omitted_bytes":8192}}}`,
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
	if len(trace.UserMessages) != 1 ||
		trace.UserMessages[0].TurnID != "t1" ||
		trace.UserMessages[0].Mode != "execute_plan" ||
		trace.UserMessages[0].DisplayText != "Run plan step 2" {
		t.Fatalf("UserMessages = %+v", trace.UserMessages)
	}
	if len(trace.ConversationRepairs) != 1 ||
		trace.ConversationRepairs[0].SessionID != "resume" ||
		trace.ConversationRepairs[0].MissingToolResults != 1 ||
		trace.ConversationRepairs[0].FailureKind != "resume_missing_tool_result" {
		t.Fatalf("ConversationRepairs = %+v", trace.ConversationRepairs)
	}
	if len(trace.RuntimeSurfaces) != 1 ||
		trace.RuntimeSurfaces[0].ToolCount != 2 ||
		!trace.RuntimeSurfaces[0].Capabilities.WebFetch ||
		!trace.RuntimeSurfaces[0].Capabilities.WebSearch ||
		trace.RuntimeSurfaces[0].Tools[0].Name != "web_fetch" ||
		trace.RuntimeSurfaces[0].MaxTurnSteps != 12 ||
		len(trace.RuntimeSurfaces[0].ToolCallCaps) != 2 ||
		trace.RuntimeSurfaces[0].ToolCallCaps[0].Tool != "web_fetch" ||
		trace.RuntimeSurfaces[0].ToolCallCaps[0].Max != 8 {
		t.Fatalf("RuntimeSurfaces = %+v", trace.RuntimeSurfaces)
	}
	contextInjections := trace.ContextInjectionStats(1)
	if contextInjections.Count != 2 ||
		contextInjections.BySource["account_access"] != 1 ||
		contextInjections.BySource["active_plan"] != 1 ||
		contextInjections.Bytes != 600 ||
		contextInjections.EstimatedTokens != 150 ||
		contextInjections.Latest.Source != "active_plan" {
		t.Fatalf("ContextInjectionStats = %+v", contextInjections)
	}
	if len(contextInjections.Examples) != 1 ||
		contextInjections.Examples[0].Source != "account_access" ||
		!strings.Contains(contextInjections.Examples[0].Preview, "GITHUB_TOKEN") {
		t.Fatalf("ContextInjectionStats examples = %+v", contextInjections.Examples)
	}
	timeline := renderDebugTimeline(BatchResult{BatchScenario: "trace-parse", ContextInjections: contextInjections}, BatchScenario{Prompt: "inspect"}, &trace)
	for _, want := range []string{
		"## Runtime Surface",
		"- max_turn_steps: `12`",
		"- max_tool_calls: `7`",
		"- tool_result_limits: event_cap_bytes=`262144`, context_max_bytes=`5120`, context_budget_bytes=`32768`",
		"- tool_result_artifacts: `.affent/artifacts/tool-results`",
		"- turn_tool_override: `true`",
		"- tool_call_caps: `web_fetch=8`, `web_search=4`",
		"- capabilities: `web_fetch`, `web_search`, `session_search`, `skill`, `mcp`",
		"## Context Injections",
		"- count: `2`",
		"- by_source: `account_access=1`, `active_plan=1`",
		"source=`account_access`",
		"GITHUB_TOKEN",
	} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
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
	if stats := SummarizeToolTruncation(trace); stats.ArgsTruncated != 1 ||
		stats.ArgsOmittedBytes != 512 ||
		stats.ResultsTruncated != 1 ||
		stats.ResultsOmittedBytes != 4096 ||
		stats.ResultArtifacts != 1 ||
		stats.ResultMissingArtifacts != 0 ||
		stats.ContextTruncated != 1 ||
		stats.ContextOmittedBytes != 8192 ||
		stats.ContextArtifacts != 1 ||
		stats.ContextMissingArtifacts != 0 {
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
	if feeds.Count != 2 || feeds.ByMode["digest"] != 1 || feeds.ByMode["full"] != 1 || feeds.Latest.FeedNumber != 5 || feeds.Latest.Mode != "full" || feeds.Latest.ProtocolPath != ".affent/loops/longrun/LOOP.md" || feeds.Latest.PlanLabel != "plan:1/3:active" || feeds.Latest.PlanCurrentStepIndex != 2 {
		t.Fatalf("LoopProtocolFeedStats = %+v", feeds)
	}
	if len(feeds.Examples) != 1 || feeds.Examples[0].LoopID != "longrun" || feeds.Examples[0].Mode != "digest" || feeds.Examples[0].CalibrationAnswers != 1 || feeds.Examples[0].LastCalibrationAnswer != "Stop if browser network evidence is missing." || feeds.Examples[0].PlanCurrentStep != "verify browser network evidence" || feeds.Examples[0].LastTurnID != "turn_previous" || feeds.Examples[0].LastTurnToolErrors != 1 || feeds.Examples[0].LastTurnForcedNoTools != 1 || feeds.Examples[0].LastTurnMemorySearchCalls != 3 || feeds.Examples[0].LastTurnMemorySearchMisses != 2 || feeds.Examples[0].LastTurnSessionSearchCalls != 1 || feeds.Examples[0].LastDecisionKind != "evidence_quality" || !strings.Contains(feeds.Examples[0].LastDecisionReason, "no text values") || !strings.Contains(feeds.Examples[0].LastDecisionAction, "browser network") || !strings.Contains(feeds.Examples[0].CurrentSituation, "dashboard metrics are partial") {
		t.Fatalf("LoopProtocolFeedStats examples = %+v", feeds.Examples)
	}
	calibrations := trace.LoopProtocolCalibrationStats(1)
	if calibrations.Count != 1 || calibrations.Latest.LoopID != "longrun" || calibrations.Latest.CalibrationQuestions != 1 || calibrations.Latest.CalibrationAnswers != 1 || calibrations.Latest.EventSeq != 3 {
		t.Fatalf("LoopProtocolCalibrationStats = %+v", calibrations)
	}
	if len(calibrations.Examples) != 1 ||
		calibrations.Examples[0].Status != "draft" ||
		calibrations.Examples[0].ProtocolPath != ".affent/loops/longrun/LOOP.md" ||
		calibrations.Examples[0].LastCalibrationQuestion != "What should pause this loop?" ||
		calibrations.Examples[0].LastCalibrationAnswer != "Stop if browser network evidence is missing." {
		t.Fatalf("LoopProtocolCalibrationStats examples = %+v", calibrations.Examples)
	}
	calibrationRequests := trace.LoopProtocolCalibrationRequestStats(1)
	if calibrationRequests.Count != 1 || calibrationRequests.Latest.LoopID != "longrun" || calibrationRequests.Latest.CalibrationQuestions != 1 || calibrationRequests.Latest.EventSeq != 2 {
		t.Fatalf("LoopProtocolCalibrationRequestStats = %+v", calibrationRequests)
	}
	if len(calibrationRequests.Examples) != 1 ||
		calibrationRequests.Examples[0].LastCalibrationQuestion != "What should pause this loop?" ||
		calibrationRequests.Examples[0].ProtocolPath != ".affent/loops/longrun/LOOP.md" {
		t.Fatalf("LoopProtocolCalibrationRequestStats examples = %+v", calibrationRequests.Examples)
	}
	if res := LoopProtocolFullFeedAfterCompaction().Eval(trace); !res.Pass {
		t.Fatalf("expected full loop protocol feed after compaction: %+v", res)
	}
	compactions := trace.ContextCompactionStats(1)
	if compactions.Count != 1 || compactions.Reactive != 1 || compactions.Proactive != 0 || compactions.RemovedMessages != 32 || compactions.SummaryBytes != 2048 {
		t.Fatalf("ContextCompactionStats = %+v", compactions)
	}
	if len(compactions.Examples) != 1 ||
		compactions.Examples[0].Reason != "context_overflow" ||
		!compactions.Examples[0].SummaryPresent ||
		!compactions.Examples[0].SummaryPresentKnown ||
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
	if trace.ToolStats.MemorySearchCalls != 2 || trace.ToolStats.MemorySearchMisses != 1 {
		t.Fatalf("memory search ToolStats = %+v", trace.ToolStats)
	}
	if trace.ToolStats.SessionSearchCalls != 1 || trace.ToolStats.SessionSearchResults != 2 || trace.ToolStats.SessionSearchContextHits != 1 || trace.ToolStats.SessionSearchMatchedTerms != 2 || trace.ToolStats.SessionSearchRecent != 3 {
		t.Fatalf("session search ToolStats = %+v", trace.ToolStats)
	}
	if got := trace.RawTypes["trace.meta"]; got != 1 {
		t.Fatalf("RawTypes[trace.meta] = %d", got)
	}
	if got := trace.RawTypes["user.message"]; got != 1 {
		t.Fatalf("RawTypes[user.message] = %d", got)
	}
	if got := trace.RawTypes["conversation.repaired"]; got != 1 {
		t.Fatalf("RawTypes[conversation.repaired] = %d", got)
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
	if got := trace.RawTypes["context.injected"]; got != 2 {
		t.Fatalf("RawTypes[context.injected] = %d", got)
	}
	if got := trace.RawTypes["loop.turn_checkpoint"]; got != 1 {
		t.Fatalf("RawTypes[loop.turn_checkpoint] = %d", got)
	}
}

func TestToolTruncationExamplesPrioritizeMissingArtifacts(t *testing.T) {
	trace := Trace{Tools: []ToolCall{
		{
			CallID:             "args-only",
			Tool:               "read_file",
			ArgsTruncated:      true,
			ArgsBytes:          70000,
			ArgsOmittedBytes:   512,
			ArgsCapBytes:       65536,
			ResultArtifactPath: ".affent/artifacts/tool-results/000001-args-only.txt",
		},
		{
			CallID:              "context-missing",
			Tool:                "web_fetch",
			ResultSummary:       "large dynamic page",
			ContextBytes:        1024,
			ContextOmittedBytes: 4096,
		},
		{
			CallID:             "result-missing",
			Tool:               "shell",
			ResultTruncated:    true,
			ResultBytes:        300000,
			ResultOmittedBytes: 8192,
			ResultCapBytes:     262144,
		},
		{
			CallID:             "artifact-backed",
			Tool:               "browser_snapshot",
			ResultTruncated:    true,
			ResultArtifactPath: ".affent/artifacts/tool-results/000004-backed.txt",
		},
	}}

	one := trace.ToolTruncationExamples(1)
	if len(one) != 1 || one[0].CallID != "context-missing" || one[0].ContextOmittedBytes != 4096 {
		t.Fatalf("ToolTruncationExamples(1) = %+v, want first missing-artifact context truncation", one)
	}
	three := trace.ToolTruncationExamples(3)
	got := []string{three[0].CallID, three[1].CallID, three[2].CallID}
	want := []string{"context-missing", "result-missing", "args-only"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolTruncationExamples priority = %#v, want %#v; examples=%+v", got, want, three)
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
		dynamic[0].ToolIndex != 1 ||
		dynamic[0].CallID != "fetch1" ||
		!strings.Contains(dynamic[0].ArgsSummary, "dashboard.example") ||
		!strings.Contains(dynamic[0].ResultSummary, "dynamic page shell") ||
		!strings.Contains(dynamic[0].ResultSummary, "Next:") ||
		!strings.Contains(dynamic[0].SuggestedNextStep, "text/API/source page") {
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
		`affentctl run failed: exit=1 err=launch chromium: /chrome: error while loading shared libraries: libglib-2.0.so.0: cannot open shared object file
Details: binary=/opt/chrome; missing_shared_library=libglib-2.0.so.0
Failure: kind=browser_launch_failed
Next: install Chromium runtime dependencies for the host image`,
	}}
	mergeRuntimeDiagnosticsFromFailures(&res, 1)
	if res.RuntimeErrorByKind["llm_timeout"] != 1 || res.RuntimeErrorByKind["llm_incomplete_stream"] != 1 || res.RuntimeErrorByKind["browser_launch_failed"] != 1 {
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
	browser := res.RuntimeErrorExamples["browser_launch_failed"]
	if len(browser) != 1 ||
		!strings.Contains(browser[0].Message, "missing_shared_library=libglib-2.0.so.0") ||
		!strings.Contains(browser[0].Message, "install Chromium runtime dependencies") {
		t.Fatalf("browser_launch_failed RuntimeErrorExamples = %#v", browser)
	}
}

func TestRuntimeErrorDiagnosticsFromFailuresAddsActionableLegacyMessages(t *testing.T) {
	failures := []string{
		`affentctl run failed: exit=1 err=context deadline exceeded max-call-timeout/per-call-timeout=4m0s`,
		`affentctl run failed: exit=1 err=stream ended without finish`,
		`affentctl run failed: exit=1 err=launch chromium: executable file not found
Failure: kind=browser_launch_failed`,
	}
	counts, examples := RuntimeErrorDiagnosticsFromFailures(failures, 2)
	if counts["llm_timeout"] != 1 || counts["llm_incomplete_stream"] != 1 || counts["browser_launch_failed"] != 1 {
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
	browser := examples["browser_launch_failed"]
	if len(browser) != 1 ||
		!strings.Contains(browser[0].Message, "Chromium could not start") ||
		!strings.Contains(browser[0].Message, "AFFENT_BROWSER_BINARY") ||
		!strings.Contains(browser[0].Message, "Original error:") {
		t.Fatalf("browser_launch_failed examples = %#v", browser)
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

func TestBatchRunnerRunsSetupBeforeProtectedSnapshot(t *testing.T) {
	runner := BatchRunner{
		RepoRoot: t.TempDir(),
		WorkRoot: t.TempDir(),
		BaseURL:  "",
		APIKey:   "test",
		Model:    "fake",
	}
	res := runner.Run(context.Background(), BatchScenario{
		Name:           "setup-before-protected",
		Files:          map[string]string{"protected.txt": "before\n"},
		SetupCommands:  []string{"printf 'after\\n' > protected.txt"},
		ProtectedFiles: []string{"protected.txt"},
	})
	if res.OK {
		t.Fatal("scenario should fail because affentctl base URL is intentionally empty")
	}
	for _, failure := range res.Failures {
		if strings.Contains(failure, "protected file changed") {
			t.Fatalf("protected snapshot ran before setup command: failures=%v", res.Failures)
		}
	}
	raw, err := os.ReadFile(filepath.Join(res.Workspace, "protected.txt"))
	if err != nil {
		t.Fatalf("read setup output: %v", err)
	}
	if string(raw) != "after\n" {
		t.Fatalf("setup output = %q, want after", string(raw))
	}
}

func TestBatchRunnerPreparesSourceRepoAfterSetup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	workspace := t.TempDir()
	runner := BatchRunner{RepoRoot: t.TempDir()}
	if err := writeScenarioFiles(workspace, map[string]string{
		"seed/go.mod": "module example.com/source\n\ngo 1.22\n",
		"seed/source/source.go": `package source

func Marker() string { return "seed" }
`,
	}); err != nil {
		t.Fatal(err)
	}
	setup := runner.runVerifier(context.Background(), workspace, runner.RepoRoot, "(cd seed && git init && git checkout -b main && git config user.email affent-eval@example.invalid && git config user.name 'Affent Eval' && git add . && git commit -m initial) && git clone --bare seed remote.git && rm -rf seed")
	if setup.Err != nil {
		t.Fatalf("setup source repo: %v\n%s", setup.Err, setup.Output)
	}
	if err := runner.prepareScenarioSourceRepo(context.Background(), workspace, runner.RepoRoot, BatchScenario{
		SourceRepoURL: "remote.git",
		SourceRepoRef: "main",
		SourceRepoDir: "app",
	}); err != nil {
		t.Fatalf("prepare source repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "app", ".git")); err != nil {
		t.Fatalf("cloned app git dir missing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "app", "source", "source.go"))
	if err != nil {
		t.Fatalf("read cloned source: %v", err)
	}
	if !strings.Contains(string(raw), `return "seed"`) {
		t.Fatalf("cloned source = %q", string(raw))
	}
	if _, err := os.Stat(filepath.Join(workspace, "seed")); !os.IsNotExist(err) {
		t.Fatalf("seed checkout should have been removed before source clone, stat err=%v", err)
	}
}

func TestBatchRunnerRejectsUnsafeSourceRepoDir(t *testing.T) {
	for _, dir := range []string{".", "..", "../app", "/tmp/app"} {
		if got, err := cleanScenarioSourceRepoDir(dir); err == nil {
			t.Fatalf("cleanScenarioSourceRepoDir(%q) = %q, want error", dir, got)
		}
	}
	got, err := cleanScenarioSourceRepoDir("")
	if err != nil || got != "repo" {
		t.Fatalf("empty source repo dir = %q err=%v, want repo", got, err)
	}
	got, err = cleanScenarioSourceRepoDir("nested/app")
	if err != nil || got != "nested/app" {
		t.Fatalf("nested source repo dir = %q err=%v, want nested/app", got, err)
	}
}

func TestBatchRunnerReportsSetupCommandFailure(t *testing.T) {
	runner := BatchRunner{
		RepoRoot: t.TempDir(),
		WorkRoot: t.TempDir(),
	}
	res := runner.Run(context.Background(), BatchScenario{
		Name:          "setup-failure",
		SetupCommands: []string{"printf setup-boom; exit 7"},
	})
	if res.OK || len(res.Failures) != 1 {
		t.Fatalf("setup failure result = ok:%v failures:%v", res.OK, res.Failures)
	}
	if !strings.Contains(res.Failures[0], "setup command failed") ||
		!strings.Contains(res.Failures[0], "setup-boom") ||
		!strings.Contains(res.Failures[0], "exit status 7") {
		t.Fatalf("setup failure = %q", res.Failures[0])
	}
}

func TestCheckBatchTraceRequiresCleanTurnEnd(t *testing.T) {
	failures := CheckBatchTrace(Trace{TurnEndReason: "max_turns"}, BatchScenario{})
	if len(failures) != 1 || !strings.Contains(failures[0], "turn ended with reason") {
		t.Fatalf("failures = %+v, want turn-end failure", failures)
	}

	failures = CheckBatchTrace(Trace{TurnEndReason: "max_turns"}, BatchScenario{RequiredTurnEndReason: "max_turns"})
	if len(failures) != 0 {
		t.Fatalf("explicit max_turns turn end should pass: %+v", failures)
	}

	failures = CheckBatchTrace(Trace{TurnEndReason: "completed"}, BatchScenario{RequiredTurnEndReason: "max_turns"})
	if len(failures) != 1 || !strings.Contains(failures[0], "expected max_turns") {
		t.Fatalf("failures = %+v, want explicit turn-end mismatch", failures)
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
		ForbiddenToolArgContains: []ToolArgContainsRequirement{
			{Tool: "memory", Arg: "content", Substring: "commit hash"},
		},
		MaxToolArgContains: []ToolArgContainsRequirement{
			{Tool: "memory", Arg: "content", Substring: "AUTO-MEM-64", Max: 1},
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
		MaxToolFailureKindCounts: map[string]int{
			"loop_guard_call_cap": 0,
		},
		RequiredToolStatsAtLeast: map[string]int{
			"memory_updates": 1,
		},
		RequiredContextInjectionSources: map[string]int{
			"final_evidence_digest": 1,
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
		RequiredLoopProtocolFeeds:               1,
		RequiredLoopProtocolCalibrationRequests: 1,
		RequiredLoopProtocolCalibrations:        1,
		RequiredLoopProtocolFeedModes: map[string]int{
			"digest": 1,
		},
		RequiredLoopProtocolFeedMatches: []LoopProtocolFeedRequirement{
			{Mode: "digest", PlanLabelContains: "market", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "source review", LastTurnEndReason: "completed", MinLastTurnMemorySearchCalls: 1},
		},
		RequireLoopProtocolFullAfterCompact: true,
		RequiredSourceAccess: []SourceAccessRequirement{
			{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch"},
		},
		RequiredSessionSearch: []SessionSearchRequirement{
			{QueryContains: "Alpha Coast", SessionID: "market-alpha", SnippetContains: "HIST-STOCK-44", MatchedTerms: []string{"alpha", "coast"}, ContextIncluded: true},
		},
		RequiredRecentSessionSearch: []RecentSessionSearchRequirement{
			{QueryContains: "missing marker", SessionID: "market-alpha", PlanContains: "source review", LoopContains: "loop.protocol_feed", RecoveryContains: "max_turns"},
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
		RequiredSubagentSourceCounts: map[string]int{
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
		RequiredCommandOrder: []CommandOrderRequirement{
			{Earlier: `git commit`, Later: `git push`},
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
		"tool_arg_lacks:memory:content:commit hash",
		"tool_arg_contains_at_most:memory:content:AUTO-MEM-64:1",
		"tool_result_truncated:shell",
		"tool_result_artifact:shell",
		"tool_called_before:read_file->edit_file",
		"tool_called_at_least:plan:2",
		"tool_failure_kind_at_least:invalid_args:1",
		"tool_failure_kind_at_most:loop_guard_call_cap:0",
		"tool_stats_at_least:memory_updates:1",
		"loop_decision_kind_at_least:evidence_quality:1",
		"loop_decision_result_at_least:defer:1",
		"loop_decision_match_at_least:evidence_quality:defer:source_access_dynamic_partial:1",
		"loop_protocol_feeds_at_least:1",
		"loop_protocol_calibration_requests_at_least:1",
		"loop_protocol_calibrations_at_least:1",
		"loop_protocol_feed_mode_at_least:digest:1",
		"loop_protocol_feed_match_at_least:digest:market:in_progress:source review:completed:turn_mem_search>=1:1",
		"loop_protocol_full_feed_after_compaction",
		"source_access_match_at_least:network:browser_network_read:taostats.io:requested=taostats.io/subnets/120:network_xhr_fetch:*:1",
		"session_search_match_at_least:Alpha Coast:market-alpha:HIST-STOCK-44:alpha,coast:true:0:1",
		"recent_session_search_anchor_at_least:missing marker:market-alpha:*:*:source review:loop.protocol_feed:max_turns:*:1",
		"context_injection_source_at_least:final_evidence_digest:1",
		"context_compactions_at_least:1",
		"reactive_context_compactions_at_least:1",
		"context_compaction_removed_messages_at_least:20",
		"context_compaction_summary_contains:HRO market marker",
		"focused_task_called_at_least:explore:1",
		"subagent_called_at_least:review:1",
		"subagent_source_evidence_at_least:review:1",
		"no_delegation_errors",
		"no_plan_errors",
		"max_successful_tool_calls:read_file:1",
		"shell_command_matching:go test",
		"shell_command_matching:gofmt",
		"shell_command_matching_at_least:go test:2",
		"shell_command_before_tool:go test->edit_file",
		"shell_command_after_tool:go test->edit_file",
		"shell_command_before_command:git commit->git push",
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

func TestBatchScenarioChecks_ForbiddenToolArgContains(t *testing.T) {
	checks := BatchScenarioChecks(BatchScenario{
		ForbiddenToolArgContains: []ToolArgContainsRequirement{
			{Tool: "memory", Arg: "content", Substring: "commit hash"},
		},
	})
	if len(checks) != 2 {
		t.Fatalf("checks count = %d, want turn-end + forbidden arg check: %+v", len(checks), checks)
	}
	if !strings.HasPrefix(checks[1].Name, "tool_arg_lacks:memory:content:commit hash") {
		t.Fatalf("forbidden arg check name = %q", checks[1].Name)
	}
}

func TestBatchScenarioChecks_MaxToolArgContainsDefaultsToOne(t *testing.T) {
	checks := BatchScenarioChecks(BatchScenario{
		MaxToolArgContains: []ToolArgContainsRequirement{
			{Tool: "memory", Arg: "content", Substring: "AUTO-MEM-64"},
		},
	})
	if len(checks) != 2 {
		t.Fatalf("checks count = %d, want turn-end + max arg check: %+v", len(checks), checks)
	}
	if !strings.HasPrefix(checks[1].Name, "tool_arg_contains_at_most:memory:content:AUTO-MEM-64:1") {
		t.Fatalf("max arg check name = %q", checks[1].Name)
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
	foundSkillInstallActivation := false
	foundPlanRepair := false
	foundPlanSkip := false
	foundPlanResume := false
	foundMemoryRecall := false
	foundSessionHistoryRecall := false
	foundMemoryWriteStats := false
	foundMemoryAutonomousWrite := false
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
		if scenario.Name == "skill-reviewed-install-activation" {
			foundSkillInstallActivation = true
			if scenario.SessionID != "skill-reviewed-install" || len(scenario.Prompts) != 3 {
				t.Fatalf("skill-reviewed-install-activation session/prompts = %q/%d", scenario.SessionID, len(scenario.Prompts))
			}
			if scenario.RequiredToolCounts["skill"] != 2 || scenario.MaxParentToolCalls != 2 {
				t.Fatalf("skill-reviewed-install-activation tool counts = required:%#v max_parent:%d", scenario.RequiredToolCounts, scenario.MaxParentToolCalls)
			}
			for _, want := range []ToolArgContainsRequirement{
				{Tool: "skill", Arg: "action", Substring: "propose_install"},
				{Tool: "skill", Arg: "action", Substring: "confirm_install"},
				{Tool: "skill", Arg: "proposal_id", Substring: "1fa99168bf1a0338"},
			} {
				if !toolArgRequirementContains(scenario.RequiredToolArgContains, want) {
					t.Fatalf("skill-reviewed-install-activation RequiredToolArgContains = %#v, want %#v", scenario.RequiredToolArgContains, want)
				}
			}
			caps := ScenarioExpectationCapabilityNames(scenario)
			if !stringSliceContains(caps, "skill_install") || !stringSliceContains(caps, "skill") {
				t.Fatalf("skill-reviewed-install-activation capabilities = %#v, want skill and skill_install", caps)
			}
			if scenario.RequiredContextInjectionSources["skill_provider"] != 1 ||
				scenario.RequiredTraceEventCounts["context.injected"] != 1 {
				t.Fatalf("skill-reviewed-install-activation context requirements = sources:%#v trace:%#v", scenario.RequiredContextInjectionSources, scenario.RequiredTraceEventCounts)
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
			if scenario.RequiredUserMessageModes["execute_plan"] != 1 {
				t.Fatalf("plan-resume-current-step RequiredUserMessageModes = %#v, want execute_plan=1", scenario.RequiredUserMessageModes)
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
		if scenario.Name == "memory-autonomous-durable-rule" {
			foundMemoryAutonomousWrite = true
			if !scenario.EnableMemory || scenario.SessionID != "memory-autonomous-writer" {
				t.Fatalf("memory-autonomous-durable-rule memory/session fields = memory:%v session:%q", scenario.EnableMemory, scenario.SessionID)
			}
			if strings.Contains(scenario.Prompt, "memory tool") || strings.Contains(scenario.Prompt, "action=add") {
				t.Fatalf("memory-autonomous-durable-rule should test autonomous write judgment, prompt=%q", scenario.Prompt)
			}
			for _, want := range []ToolArgContainsRequirement{
				{Tool: "memory", Arg: "action", Substring: "add"},
				{Tool: "memory", Arg: "topic", Substring: "conventions"},
				{Tool: "memory", Arg: "content", Substring: "AUTO-MEM-73"},
				{Tool: "memory", Arg: "content", Substring: "source-led"},
			} {
				if !toolArgRequirementContains(scenario.RequiredToolArgContains, want) {
					t.Fatalf("memory-autonomous-durable-rule RequiredToolArgContains = %#v, want %#v", scenario.RequiredToolArgContains, want)
				}
			}
			for _, want := range []ToolArgContainsRequirement{
				{Tool: "memory", Arg: "content", Substring: "commit hash"},
				{Tool: "memory", Arg: "content", Substring: "push result"},
			} {
				if !toolArgRequirementContains(scenario.ForbiddenToolArgContains, want) {
					t.Fatalf("memory-autonomous-durable-rule ForbiddenToolArgContains = %#v, want %#v", scenario.ForbiddenToolArgContains, want)
				}
			}
			if scenario.RequiredToolStatsAtLeast["memory_updates"] != 1 || scenario.RequiredToolStatsAtLeast["memory_update_add"] != 1 {
				t.Fatalf("memory-autonomous-durable-rule stats = %#v, want memory update/add requirements", scenario.RequiredToolStatsAtLeast)
			}
			if scenario.RequiredToolCounts["memory"] != 1 || scenario.MaxSuccessfulToolCallsByTool["memory"] != 1 {
				t.Fatalf("memory-autonomous-durable-rule tool counts = required:%#v max:%#v", scenario.RequiredToolCounts, scenario.MaxSuccessfulToolCallsByTool)
			}
			for _, want := range []string{"AUTO-MEM-73", "source-led", "commit hash", "push result"} {
				if !strings.Contains(scenario.VerifyCommand, want) {
					t.Fatalf("memory-autonomous-durable-rule VerifyCommand = %q, want %q", scenario.VerifyCommand, want)
				}
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
	if !foundSkillInstallActivation {
		t.Fatalf("small-model-tools suite missing skill-reviewed-install-activation")
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
	if !foundMemoryAutonomousWrite {
		t.Fatalf("small-model-tools suite missing memory-autonomous-durable-rule")
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
	if len(scenarios) != 25 {
		t.Fatalf("long-run suite size = %d, want 25", len(scenarios))
	}
	seen := map[string]BatchScenario{}
	suiteCapabilities := map[string]bool{}
	suiteDomains := map[string]bool{}
	for _, scenario := range scenarios {
		if !scenarioInSuite(scenario, "long-run") {
			t.Fatalf("scenario %s missing long-run suite marker", scenario.Name)
		}
		seen[scenario.Name] = scenario
		for _, cap := range ScenarioExpectationCapabilityNames(scenario) {
			suiteCapabilities[cap] = true
		}
		for _, domain := range ScenarioExpectationDomains(scenario) {
			suiteDomains[domain] = true
		}
	}
	for _, want := range []string{
		"context_compaction",
		"delegation",
		"longrun_recovery",
		"loop_protocol",
		"memory",
		"plan",
		"research_checkpoint",
		"session",
		"session_search",
		"skill",
		"trace",
		"verifier",
		"workspace",
	} {
		if !suiteCapabilities[want] {
			t.Fatalf("long-run suite expectation capabilities missing %q: %#v", want, suiteCapabilities)
		}
	}
	for _, want := range []string{bittensorDomain, codePRDomain, contextCompactionDomain, longRunRecoveryDomain, marketDomain, memoryDomain, sessionRecoveryDomain} {
		if !suiteDomains[want] {
			t.Fatalf("long-run suite domains missing %q: %#v", want, suiteDomains)
		}
	}

	stock, ok := seen["longrun-stock-analysis-synthesis"]
	if !ok {
		t.Fatalf("long-run suite missing stock analysis scenario")
	}
	if !stringSliceContains(stock.RequiredTools, "repo_search") || !stringSliceContains(stock.RequiredTools, "read_file") {
		t.Fatalf("stock scenario RequiredTools = %#v, want repo_search/read_file", stock.RequiredTools)
	}
	if !stringSliceContains(stock.Domains, marketDomain) {
		t.Fatalf("stock scenario Domains = %#v, want market", stock.Domains)
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
	if !stringSliceContains(subnet.Domains, bittensorDomain) {
		t.Fatalf("Bittensor scenario Domains = %#v, want bittensor", subnet.Domains)
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
	if len(pr.SetupCommands) != 1 || !strings.Contains(pr.SetupCommands[0], "git init") {
		t.Fatalf("code PR scenario SetupCommands = %#v, want git repo initialization", pr.SetupCommands)
	}
	if !stringSliceContains(pr.RequiredCommands, `git diff( --)? queue/queue.go`) {
		t.Fatalf("code PR scenario RequiredCommands = %#v, want git diff check", pr.RequiredCommands)
	}
	if !commandToolOrderContains(pr.RequiredCommandAfterTool, CommandToolOrderRequirement{Command: `git diff( --)? queue/queue.go`, Tool: "edit_file"}) {
		t.Fatalf("code PR scenario RequiredCommandAfterTool = %#v, want git diff after edit_file", pr.RequiredCommandAfterTool)
	}
	if !strings.Contains(pr.VerifyCommand, "git diff --name-only") {
		t.Fatalf("code PR scenario VerifyCommand = %q, want git diff verification", pr.VerifyCommand)
	}
	if !stringSliceContains(pr.RequiredFinalText, "PR Summary") || !stringSliceContains(pr.RequiredFinalText, "Tests") || !stringSliceContains(pr.RequiredFinalText, "queue/queue.go") || !stringSliceContains(pr.RequiredFinalText, "diff") {
		t.Fatalf("code PR scenario RequiredFinalText = %#v, want PR Summary, Tests, changed file, and diff", pr.RequiredFinalText)
	}
	if !stringSliceContains(pr.Domains, codePRDomain) {
		t.Fatalf("code PR scenario Domains = %#v, want code_pr", pr.Domains)
	}

	commitPush, ok := seen["longrun-code-commit-push-local-remote"]
	if !ok {
		t.Fatalf("long-run suite missing commit/push scenario")
	}
	if len(commitPush.SetupCommands) != 1 ||
		!strings.Contains(commitPush.SetupCommands[0], "git init") ||
		!strings.Contains(commitPush.SetupCommands[0], "git init --bare ../remote.git") ||
		!strings.Contains(commitPush.SetupCommands[0], "git push -u origin main") {
		t.Fatalf("commit/push SetupCommands = %#v, want local bare remote initialization", commitPush.SetupCommands)
	}
	for _, want := range []string{"git status --porcelain", "git log -1", "git ls-remote --heads origin main"} {
		if !strings.Contains(commitPush.VerifyCommand, want) {
			t.Fatalf("commit/push VerifyCommand = %q, want %q", commitPush.VerifyCommand, want)
		}
	}
	for _, want := range []string{`go test`, `git status`, `git commit`, `git push`} {
		if !stringSliceContains(commitPush.RequiredCommands, want) {
			t.Fatalf("commit/push RequiredCommands = %#v, want %q", commitPush.RequiredCommands, want)
		}
	}
	if commitPush.RequiredCommandCounts[`go test`] != 2 {
		t.Fatalf("commit/push RequiredCommandCounts = %#v, want go test=2", commitPush.RequiredCommandCounts)
	}
	for _, want := range []CommandToolOrderRequirement{
		{Command: `git status`, Tool: "edit_file"},
		{Command: `git commit`, Tool: "edit_file"},
		{Command: `git push`, Tool: "edit_file"},
	} {
		if !commandToolOrderContains(commitPush.RequiredCommandAfterTool, want) {
			t.Fatalf("commit/push RequiredCommandAfterTool = %#v, want %#v", commitPush.RequiredCommandAfterTool, want)
		}
	}
	if !stringSliceContains(commitPush.RequiredFinalText, "status") {
		t.Fatalf("commit/push RequiredFinalText = %#v, want status", commitPush.RequiredFinalText)
	}
	if !stringSliceContains(commitPush.ProtectedFiles, "set/set_test.go") {
		t.Fatalf("commit/push ProtectedFiles = %#v, want test protection", commitPush.ProtectedFiles)
	}
	if !stringSliceContains(commitPush.Domains, codePRDomain) {
		t.Fatalf("commit/push Domains = %#v, want code_pr", commitPush.Domains)
	}

	clonePush, ok := seen["longrun-code-clone-modify-push-local-remote"]
	if !ok {
		t.Fatalf("long-run suite missing clone/modify/push scenario")
	}
	if len(clonePush.SetupCommands) != 1 ||
		!strings.Contains(clonePush.SetupCommands[0], "git clone --bare seed remote.git") ||
		!strings.Contains(clonePush.SetupCommands[0], "rm -rf seed") {
		t.Fatalf("clone/push SetupCommands = %#v, want seeded local bare remote without checkout", clonePush.SetupCommands)
	}
	if _, ok := clonePush.Files["seed/mathutil/clamp.go"]; !ok {
		t.Fatalf("clone/push scenario missing seed clamp implementation")
	}
	for _, want := range []string{"test -d app/.git", "test ! -d seed", "go test ./...", `git diff --name-only HEAD~1..HEAD`, "git ls-remote --heads origin main"} {
		if !strings.Contains(clonePush.VerifyCommand, want) {
			t.Fatalf("clone/push VerifyCommand = %q, want %q", clonePush.VerifyCommand, want)
		}
	}
	for _, want := range []string{`git clone`, `go test`, `git status`, `git commit`, `git push`} {
		if !stringSliceContains(clonePush.RequiredCommands, want) {
			t.Fatalf("clone/push RequiredCommands = %#v, want %q", clonePush.RequiredCommands, want)
		}
	}
	if clonePush.RequiredCommandCounts[`go test`] != 2 {
		t.Fatalf("clone/push RequiredCommandCounts = %#v, want go test=2", clonePush.RequiredCommandCounts)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "read_file", Arg: "path", Substring: "app/mathutil/clamp.go"},
		{Tool: "edit_file", Arg: "path", Substring: "app/mathutil/clamp.go"},
	} {
		if !toolArgRequirementContains(clonePush.RequiredToolArgContains, want) {
			t.Fatalf("clone/push RequiredToolArgContains = %#v, want %#v", clonePush.RequiredToolArgContains, want)
		}
	}
	for _, want := range []CommandToolOrderRequirement{
		{Command: `git clone`, Tool: "read_file"},
		{Command: `go test`, Tool: "edit_file"},
		{Command: `git status`, Tool: "edit_file"},
		{Command: `git commit`, Tool: "edit_file"},
		{Command: `git push`, Tool: "edit_file"},
	} {
		if !commandToolOrderContains(append(clonePush.RequiredCommandBeforeTool, clonePush.RequiredCommandAfterTool...), want) {
			t.Fatalf("clone/push command order requirements = before:%#v after:%#v, want %#v", clonePush.RequiredCommandBeforeTool, clonePush.RequiredCommandAfterTool, want)
		}
	}
	if got := clonePush.RequiredFileSubstrings["app/mathutil/clamp.go"]; !stringSliceContains(got, "return max") {
		t.Fatalf("clone/push RequiredFileSubstrings = %#v, want fixed clamp", clonePush.RequiredFileSubstrings)
	}
	if !stringSliceContains(clonePush.RequiredFinalText, "git clone") || !stringSliceContains(clonePush.RequiredFinalText, "mathutil/clamp.go") || !stringSliceContains(clonePush.RequiredFinalText, "status") {
		t.Fatalf("clone/push RequiredFinalText = %#v, want clone command, changed file, and status", clonePush.RequiredFinalText)
	}
	if strings.Contains(clonePush.Prompt, "请") || !strings.Contains(clonePush.Prompt, "Clone remote.git into app") {
		t.Fatalf("clone/push prompt should be English and clone-specific: %q", clonePush.Prompt)
	}
	if !stringSliceContains(clonePush.Domains, codePRDomain) {
		t.Fatalf("clone/push Domains = %#v, want code_pr", clonePush.Domains)
	}

	sourceRepo, ok := seen["longrun-code-source-repo-modify-push-local-remote"]
	if !ok {
		t.Fatalf("long-run suite missing source repo checkout scenario")
	}
	if sourceRepo.SourceRepoURL != "remote.git" || sourceRepo.SourceRepoRef != "main" || sourceRepo.SourceRepoDir != "app" {
		t.Fatalf("source repo fields = url:%q ref:%q dir:%q", sourceRepo.SourceRepoURL, sourceRepo.SourceRepoRef, sourceRepo.SourceRepoDir)
	}
	if len(sourceRepo.SetupCommands) != 1 ||
		!strings.Contains(sourceRepo.SetupCommands[0], "git clone --bare seed remote.git") ||
		!strings.Contains(sourceRepo.SetupCommands[0], "rm -rf seed") {
		t.Fatalf("source repo SetupCommands = %#v, want seeded local bare remote", sourceRepo.SetupCommands)
	}
	for _, want := range []string{"test -d app/.git", "test ! -d seed", "go test ./...", `git diff --name-only HEAD~1..HEAD`, "git ls-remote --heads origin main"} {
		if !strings.Contains(sourceRepo.VerifyCommand, want) {
			t.Fatalf("source repo VerifyCommand = %q, want %q", sourceRepo.VerifyCommand, want)
		}
	}
	for _, want := range []string{`go test`, `git status`, `git commit`, `git push`} {
		if !stringSliceContains(sourceRepo.RequiredCommands, want) {
			t.Fatalf("source repo RequiredCommands = %#v, want %q", sourceRepo.RequiredCommands, want)
		}
	}
	if stringSliceContains(sourceRepo.RequiredCommands, `git clone`) {
		t.Fatalf("source repo scenario should have runner clone before the agent turn, not require agent git clone: %#v", sourceRepo.RequiredCommands)
	}
	if sourceRepo.RequiredCommandCounts[`go test`] != 2 {
		t.Fatalf("source repo RequiredCommandCounts = %#v, want go test=2", sourceRepo.RequiredCommandCounts)
	}
	if !commandToolOrderContains(sourceRepo.RequiredCommandAfterTool, CommandToolOrderRequirement{Command: `git status`, Tool: "edit_file"}) {
		t.Fatalf("source repo RequiredCommandAfterTool = %#v, want git status after edit_file", sourceRepo.RequiredCommandAfterTool)
	}
	if !commandOrderContains(sourceRepo.RequiredCommandOrder, CommandOrderRequirement{Earlier: `git commit`, Later: `git push`}) {
		t.Fatalf("source repo RequiredCommandOrder = %#v, want git commit before git push", sourceRepo.RequiredCommandOrder)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "read_file", Arg: "path", Substring: "app/greet/greet.go"},
		{Tool: "edit_file", Arg: "path", Substring: "app/greet/greet.go"},
	} {
		if !toolArgRequirementContains(sourceRepo.RequiredToolArgContains, want) {
			t.Fatalf("source repo RequiredToolArgContains = %#v, want %#v", sourceRepo.RequiredToolArgContains, want)
		}
	}
	if got := sourceRepo.RequiredFileSubstrings["app/greet/greet.go"]; !stringSliceContains(got, "hello, guest") {
		t.Fatalf("source repo RequiredFileSubstrings = %#v, want fixed greeting", sourceRepo.RequiredFileSubstrings)
	}
	if !stringSliceContains(sourceRepo.RequiredFinalText, "status") {
		t.Fatalf("source repo RequiredFinalText = %#v, want status", sourceRepo.RequiredFinalText)
	}
	if !stringSliceContains(sourceRepo.ProtectedFiles, "app/greet/greet_test.go") {
		t.Fatalf("source repo ProtectedFiles = %#v, want cloned test protection", sourceRepo.ProtectedFiles)
	}
	sourceRepoCaps := ScenarioExpectationCapabilityNames(sourceRepo)
	for _, want := range []string{"source_repo", "workspace", "verifier", "skill"} {
		if !stringSliceContains(sourceRepoCaps, want) {
			t.Fatalf("source repo expectation capabilities = %#v, want %q", sourceRepoCaps, want)
		}
	}
	if !stringSliceContains(sourceRepo.Domains, codePRDomain) {
		t.Fatalf("source repo Domains = %#v, want code_pr", sourceRepo.Domains)
	}

	scratchProject, ok := seen["longrun-scratch-project-loop-push"]
	if !ok {
		t.Fatalf("long-run suite missing scratch project loop/push scenario")
	}
	if scratchProject.SessionID != "scratch-project-loop" || !scratchProject.EnableLoopProtocol {
		t.Fatalf("scratch project loop fields = session:%q loop:%v", scratchProject.SessionID, scratchProject.EnableLoopProtocol)
	}
	if _, ok := scratchProject.Files[".affent/loops/scratch-project-loop/LOOP.md"]; !ok {
		t.Fatalf("scratch project scenario missing active LOOP.md")
	}
	if !strings.Contains(scratchProject.Prompt, "Build a small Python project") || strings.Contains(scratchProject.Prompt, "请") {
		t.Fatalf("scratch project prompt should be English and task-specific: %q", scratchProject.Prompt)
	}
	for _, want := range []string{"python3 -m unittest discover -s tests", "git status", "git commit", "git push"} {
		if !stringSliceContains(scratchProject.RequiredCommands, want) {
			t.Fatalf("scratch project RequiredCommands = %#v, want %q", scratchProject.RequiredCommands, want)
		}
	}
	if scratchProject.RequiredCommandCounts[`python3 -m unittest`] != 2 {
		t.Fatalf("scratch project RequiredCommandCounts = %#v, want unittest=2", scratchProject.RequiredCommandCounts)
	}
	if !commandToolOrderContains(scratchProject.RequiredCommandAfterTool, CommandToolOrderRequirement{Command: `git status`, Tool: "write_file"}) {
		t.Fatalf("scratch project RequiredCommandAfterTool = %#v, want git status after write_file", scratchProject.RequiredCommandAfterTool)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "write_file", Arg: "path", Substring: "todo_core/store.py"},
		{Tool: "write_file", Arg: "path", Substring: "tests/test_store.py"},
	} {
		if !toolArgRequirementContains(scratchProject.RequiredToolArgContains, want) {
			t.Fatalf("scratch project RequiredToolArgContains = %#v, want %#v", scratchProject.RequiredToolArgContains, want)
		}
	}
	for _, want := range []string{"todo_core/store.py", "tests/test_store.py", "SCRATCH-LOOP-31", "git status --porcelain", "git ls-remote --heads origin main"} {
		if !strings.Contains(scratchProject.VerifyCommand, want) {
			t.Fatalf("scratch project VerifyCommand = %q, want %q", scratchProject.VerifyCommand, want)
		}
	}
	if scratchProject.RequiredLoopProtocolFeeds != 1 ||
		scratchProject.RequiredLoopProtocolFeedModes["full"] != 1 ||
		len(scratchProject.RequiredLoopProtocolFeedMatches) != 1 ||
		!strings.Contains(scratchProject.RequiredLoopProtocolFeedMatches[0].CurrentSituation, "no source package or tests exist yet") ||
		!strings.Contains(scratchProject.RequiredLoopProtocolFeedMatches[0].PlanCurrentStep, "create tests and implementation") {
		t.Fatalf("scratch project loop protocol constraints = feeds:%d modes:%#v matches:%#v", scratchProject.RequiredLoopProtocolFeeds, scratchProject.RequiredLoopProtocolFeedModes, scratchProject.RequiredLoopProtocolFeedMatches)
	}
	if scratchProject.RequiredTraceEventCounts["loop.turn_checkpoint"] != 1 {
		t.Fatalf("scratch project trace event requirements = %#v, want loop.turn_checkpoint=1", scratchProject.RequiredTraceEventCounts)
	}
	if !stringSliceContains(scratchProject.RequiredFinalText, "status") {
		t.Fatalf("scratch project RequiredFinalText = %#v, want status", scratchProject.RequiredFinalText)
	}
	if !stringSliceContains(scratchProject.ProtectedFiles, ".affent/loops/scratch-project-loop/LOOP.md") {
		t.Fatalf("scratch project ProtectedFiles = %#v, want LOOP.md", scratchProject.ProtectedFiles)
	}
	scratchProjectCaps := ScenarioExpectationCapabilityNames(scratchProject)
	for _, want := range []string{"loop_protocol", "skill", "trace", "verifier"} {
		if !stringSliceContains(scratchProjectCaps, want) {
			t.Fatalf("scratch project expectation capabilities = %#v, want %q", scratchProjectCaps, want)
		}
	}
	if !stringSliceContains(scratchProject.Domains, codePRDomain) || !stringSliceContains(scratchProject.Domains, longRunRecoveryDomain) {
		t.Fatalf("scratch project Domains = %#v, want code_pr and longrun_recovery", scratchProject.Domains)
	}

	iterativeProject, ok := seen["longrun-scratch-project-iterative-loop-push"]
	if !ok {
		t.Fatalf("long-run suite missing iterative scratch project loop/push scenario")
	}
	if iterativeProject.SessionID != "scratch-project-iterative-loop" || !iterativeProject.EnableLoopProtocol {
		t.Fatalf("iterative scratch project fields = session:%q loop:%v", iterativeProject.SessionID, iterativeProject.EnableLoopProtocol)
	}
	if len(iterativeProject.Prompts) != 2 || iterativeProject.Prompt != "" {
		t.Fatalf("iterative scratch project prompts = prompt:%q prompts:%d", iterativeProject.Prompt, len(iterativeProject.Prompts))
	}
	for _, prompt := range iterativeProject.Prompts {
		if strings.Contains(prompt, "请") {
			t.Fatalf("iterative scratch project prompt should be English: %q", prompt)
		}
	}
	if _, ok := iterativeProject.Files[".affent/loops/scratch-project-iterative-loop/LOOP.md"]; !ok {
		t.Fatalf("iterative scratch project scenario missing active LOOP.md")
	}
	for _, want := range []string{"python3 -m unittest discover -s tests", "git status", "git commit", "git push"} {
		if !stringSliceContains(iterativeProject.RequiredCommands, want) {
			t.Fatalf("iterative scratch project RequiredCommands = %#v, want %q", iterativeProject.RequiredCommands, want)
		}
	}
	if iterativeProject.RequiredCommandCounts[`python3 -m unittest`] != 4 ||
		iterativeProject.RequiredCommandCounts[`git status`] != 2 ||
		iterativeProject.RequiredCommandCounts[`git commit`] != 2 ||
		iterativeProject.RequiredCommandCounts[`git push`] != 2 {
		t.Fatalf("iterative scratch project RequiredCommandCounts = %#v, want unittest=4 status=2 commit=2 push=2", iterativeProject.RequiredCommandCounts)
	}
	if !commandToolOrderContains(iterativeProject.RequiredCommandAfterTool, CommandToolOrderRequirement{Command: `git status`, Tool: "write_file"}) {
		t.Fatalf("iterative scratch project RequiredCommandAfterTool = %#v, want git status after write_file", iterativeProject.RequiredCommandAfterTool)
	}
	for _, want := range []string{"def save_json", "def load_json", "git rev-list --count HEAD", "git status --porcelain", "git ls-remote --heads origin main"} {
		if !strings.Contains(iterativeProject.VerifyCommand, want) {
			t.Fatalf("iterative scratch project VerifyCommand = %q, want %q", iterativeProject.VerifyCommand, want)
		}
	}
	if iterativeProject.RequiredLoopProtocolFeeds != 2 ||
		iterativeProject.RequiredLoopProtocolFeedModes["full"] != 2 ||
		len(iterativeProject.RequiredLoopProtocolFeedMatches) != 1 ||
		!strings.Contains(iterativeProject.RequiredLoopProtocolFeedMatches[0].CurrentSituation, "no source package or tests exist yet") ||
		!strings.Contains(iterativeProject.RequiredLoopProtocolFeedMatches[0].PlanCurrentStep, "finish iteration 1") {
		t.Fatalf("iterative scratch project loop protocol constraints = feeds:%d modes:%#v matches:%#v", iterativeProject.RequiredLoopProtocolFeeds, iterativeProject.RequiredLoopProtocolFeedModes, iterativeProject.RequiredLoopProtocolFeedMatches)
	}
	if iterativeProject.RequiredTraceEventCounts["loop.turn_checkpoint"] != 2 {
		t.Fatalf("iterative scratch project trace event requirements = %#v, want loop.turn_checkpoint=2", iterativeProject.RequiredTraceEventCounts)
	}
	for _, want := range []string{"ITER-LOOP-57", "iteration 2", "save_json", "load_json", "clean"} {
		if !stringSliceContains(iterativeProject.RequiredFinalText, want) {
			t.Fatalf("iterative scratch project RequiredFinalText = %#v, want %q", iterativeProject.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(iterativeProject.ProtectedFiles, ".affent/loops/scratch-project-iterative-loop/LOOP.md") {
		t.Fatalf("iterative scratch project ProtectedFiles = %#v, want LOOP.md", iterativeProject.ProtectedFiles)
	}
	iterativeProjectCaps := ScenarioExpectationCapabilityNames(iterativeProject)
	for _, want := range []string{"loop_protocol", "session", "skill", "trace", "verifier"} {
		if !stringSliceContains(iterativeProjectCaps, want) {
			t.Fatalf("iterative scratch project expectation capabilities = %#v, want %q", iterativeProjectCaps, want)
		}
	}
	if !stringSliceContains(iterativeProject.Domains, codePRDomain) || !stringSliceContains(iterativeProject.Domains, longRunRecoveryDomain) {
		t.Fatalf("iterative scratch project Domains = %#v, want code_pr and longrun_recovery", iterativeProject.Domains)
	}

	integrated, ok := seen["longrun-integrated-memory-recovery"]
	if !ok {
		t.Fatalf("long-run suite missing integrated memory recovery scenario")
	}
	if integrated.SessionID != "integrated-memory-recovery" || !integrated.EnableMemory || !integrated.EnableLoopProtocol {
		t.Fatalf("integrated memory recovery fields = session:%q memory:%v loop:%v", integrated.SessionID, integrated.EnableMemory, integrated.EnableLoopProtocol)
	}
	if len(integrated.Prompts) != 2 || integrated.Prompt != "" {
		t.Fatalf("integrated memory recovery prompts = prompt:%q prompts:%d", integrated.Prompt, len(integrated.Prompts))
	}
	for _, prompt := range integrated.Prompts {
		if strings.Contains(prompt, "请") {
			t.Fatalf("integrated memory recovery prompt should be English: %q", prompt)
		}
	}
	for _, want := range []string{"plan", "memory", "session_search", "read_file", "edit_file"} {
		if !stringSliceContains(integrated.RequiredTools, want) {
			t.Fatalf("integrated memory recovery RequiredTools = %#v, want %q", integrated.RequiredTools, want)
		}
	}
	for _, want := range []string{"python3 -m unittest discover -s tests", "git status", "git commit", "git push"} {
		if !stringSliceContains(integrated.RequiredCommands, want) {
			t.Fatalf("integrated memory recovery RequiredCommands = %#v, want %q", integrated.RequiredCommands, want)
		}
	}
	if integrated.RequiredCommandCounts[`python3 -m unittest`] != 4 ||
		integrated.RequiredCommandCounts[`git status`] != 2 ||
		integrated.RequiredCommandCounts[`git commit`] != 2 ||
		integrated.RequiredCommandCounts[`git push`] != 2 {
		t.Fatalf("integrated memory recovery RequiredCommandCounts = %#v, want unittest=4 status=2 commit=2 push=2", integrated.RequiredCommandCounts)
	}
	if !commandToolOrderContains(integrated.RequiredCommandAfterTool, CommandToolOrderRequirement{Command: `git status`, Tool: "edit_file"}) {
		t.Fatalf("integrated memory recovery RequiredCommandAfterTool = %#v, want git status after edit_file", integrated.RequiredCommandAfterTool)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "memory", Arg: "action", Substring: "add"},
		{Tool: "memory", Arg: "action", Substring: "search"},
		{Tool: "memory", Arg: "content", Substring: "AUTO-MEM-64"},
		{Tool: "session_search", Arg: "query", Substring: "INTEGRATED-HANDOFF-26"},
		{Tool: "read_file", Arg: "path", Substring: "docs/conventions.md"},
	} {
		if !toolArgRequirementContains(integrated.RequiredToolArgContains, want) {
			t.Fatalf("integrated memory recovery RequiredToolArgContains = %#v, want %#v", integrated.RequiredToolArgContains, want)
		}
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "memory", Arg: "content", Substring: "iteration 1"},
		{Tool: "memory", Arg: "content", Substring: "iteration 2"},
		{Tool: "memory", Arg: "content", Substring: "commit hash"},
		{Tool: "memory", Arg: "content", Substring: "push result"},
	} {
		if !toolArgRequirementContains(integrated.ForbiddenToolArgContains, want) {
			t.Fatalf("integrated memory recovery ForbiddenToolArgContains = %#v, want %#v", integrated.ForbiddenToolArgContains, want)
		}
	}
	if !toolArgRequirementContains(integrated.MaxToolArgContains, ToolArgContainsRequirement{Tool: "memory", Arg: "content", Substring: "AUTO-MEM-64", Max: 1}) {
		t.Fatalf("integrated memory recovery MaxToolArgContains = %#v, want AUTO-MEM-64 max=1", integrated.MaxToolArgContains)
	}
	for _, field := range []string{"memory_updates", "memory_update_add", "memory_search_calls", "session_search_calls", "session_search_results"} {
		if integrated.RequiredToolStatsAtLeast[field] != 1 {
			t.Fatalf("integrated memory recovery RequiredToolStatsAtLeast = %#v, want %s=1", integrated.RequiredToolStatsAtLeast, field)
		}
	}
	for _, kind := range []string{"invalid_args", "loop_guard_call_cap", "loop_guard_no_budget"} {
		if max, ok := integrated.MaxToolFailureKindCounts[kind]; !ok || max != 0 {
			t.Fatalf("integrated memory recovery MaxToolFailureKindCounts = %#v, want %s=0", integrated.MaxToolFailureKindCounts, kind)
		}
	}
	if len(integrated.RequiredSessionSearch) != 1 ||
		integrated.RequiredSessionSearch[0].SessionID != "integrated-prior" ||
		integrated.RequiredSessionSearch[0].QueryContains != "INTEGRATED-HANDOFF-26" ||
		integrated.RequiredSessionSearch[0].SnippetContains != "AUTO-MEM-64" ||
		!integrated.RequiredSessionSearch[0].ContextIncluded {
		t.Fatalf("integrated memory recovery RequiredSessionSearch = %#v", integrated.RequiredSessionSearch)
	}
	for _, want := range []string{"AUTO-MEM-64", "JSON", "--summary", "git rev-list --count HEAD", "git status --porcelain", "git ls-remote --heads origin main"} {
		if !strings.Contains(integrated.VerifyCommand, want) {
			t.Fatalf("integrated memory recovery VerifyCommand = %q, want %q", integrated.VerifyCommand, want)
		}
	}
	if integrated.RequiredLoopProtocolFeeds != 2 ||
		integrated.RequiredLoopProtocolFeedModes["full"] != 2 ||
		len(integrated.RequiredLoopProtocolFeedMatches) != 1 ||
		!strings.Contains(integrated.RequiredLoopProtocolFeedMatches[0].CurrentSituation, "tiny Python CLI with a failing JSON contract test") ||
		!strings.Contains(integrated.RequiredLoopProtocolFeedMatches[0].PlanCurrentStep, "fix JSON mode") {
		t.Fatalf("integrated memory recovery loop protocol constraints = feeds:%d modes:%#v matches:%#v", integrated.RequiredLoopProtocolFeeds, integrated.RequiredLoopProtocolFeedModes, integrated.RequiredLoopProtocolFeedMatches)
	}
	if integrated.RequiredTraceEventCounts["loop.turn_checkpoint"] != 2 {
		t.Fatalf("integrated memory recovery trace event requirements = %#v, want loop.turn_checkpoint=2", integrated.RequiredTraceEventCounts)
	}
	for _, want := range []string{".affent/loops/integrated-memory-recovery/LOOP.md", "docs/conventions.md"} {
		if !stringSliceContains(integrated.ProtectedFiles, want) {
			t.Fatalf("integrated memory recovery ProtectedFiles = %#v, want %q", integrated.ProtectedFiles, want)
		}
	}
	integratedCaps := ScenarioExpectationCapabilityNames(integrated)
	for _, want := range []string{"loop_protocol", "memory", "session_search", "plan", "session", "skill", "trace", "workspace", "verifier"} {
		if !stringSliceContains(integratedCaps, want) {
			t.Fatalf("integrated memory recovery expectation capabilities = %#v, want %q", integratedCaps, want)
		}
	}
	if !stringSliceContains(integrated.Domains, memoryDomain) || !stringSliceContains(integrated.Domains, sessionRecoveryDomain) || !stringSliceContains(integrated.Domains, longRunRecoveryDomain) {
		t.Fatalf("integrated memory recovery Domains = %#v, want memory/session/longrun", integrated.Domains)
	}

	planResume, ok := seen["plan-resume-current-step"]
	if !ok {
		t.Fatalf("long-run suite missing plan resume scenario")
	}
	if !planResume.ExecutePlan || planResume.SessionID != "plan-resume" {
		t.Fatalf("plan resume execution fields = execute_plan:%v session:%q", planResume.ExecutePlan, planResume.SessionID)
	}
	if planResume.RequiredUserMessageModes["execute_plan"] != 1 {
		t.Fatalf("plan resume RequiredUserMessageModes = %#v, want execute_plan=1", planResume.RequiredUserMessageModes)
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
		!strings.Contains(planResume.RequiredLoopProtocolFeedMatches[0].PlanCurrentStep, "read current launch evidence") ||
		!strings.Contains(planResume.RequiredLoopProtocolFeedMatches[0].CurrentSituation, "docs/current-plan.md") {
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

	recentAnchorRecovery, ok := seen["longrun-recent-session-anchor-recovery"]
	if !ok {
		t.Fatalf("long-run suite missing recent session anchor recovery scenario")
	}
	if recentAnchorRecovery.SessionID != "recent-anchor-reader" {
		t.Fatalf("recent anchor recovery fields = session:%q", recentAnchorRecovery.SessionID)
	}
	if recentAnchorRecovery.RequiredToolCounts["session_search"] != 1 || recentAnchorRecovery.MaxSuccessfulToolCallsByTool["session_search"] != 1 {
		t.Fatalf("recent anchor recovery tool constraints = counts:%#v max:%#v", recentAnchorRecovery.RequiredToolCounts, recentAnchorRecovery.MaxSuccessfulToolCallsByTool)
	}
	if recentAnchorRecovery.RequiredToolStatsAtLeast["session_search_recent_sessions"] != 1 {
		t.Fatalf("recent anchor recovery stats = %#v, want recent_sessions", recentAnchorRecovery.RequiredToolStatsAtLeast)
	}
	if len(recentAnchorRecovery.RequiredRecentSessionSearch) != 4 ||
		recentAnchorRecovery.RequiredRecentSessionSearch[0].SessionID != "recent-anchor" ||
		recentAnchorRecovery.RequiredRecentSessionSearch[0].QueryContains != "ORIONABSENT999" ||
		recentAnchorRecovery.RequiredRecentSessionSearch[0].LoopContains != "loop.protocol_feed" ||
		recentAnchorRecovery.RequiredRecentSessionSearch[0].RecoveryContains != "loop_guard_no_new_evidence" {
		t.Fatalf("recent anchor recovery requirement = %#v", recentAnchorRecovery.RequiredRecentSessionSearch)
	}
	for _, want := range []string{"tool_errors=1", "forced_no_tools=1", "browser_network_read ref n7"} {
		found := false
		for _, req := range recentAnchorRecovery.RequiredRecentSessionSearch {
			if req.LoopContains == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("recent anchor recovery RequiredRecentSessionSearch = %#v, want loop_contains %q", recentAnchorRecovery.RequiredRecentSessionSearch, want)
		}
	}
	for _, want := range []string{"RECENT-HANDOFF-42", "loop.protocol_feed", "loop_guard_no_new_evidence", "recent-anchor", "tool_errors=1", "forced_no_tools=1", "browser_network_read ref n7"} {
		if !stringSliceContains(recentAnchorRecovery.RequiredFinalText, want) {
			t.Fatalf("recent anchor recovery RequiredFinalText = %#v, want %q", recentAnchorRecovery.RequiredFinalText, want)
		}
	}

	loopMemoryAnchor, ok := seen["longrun-loop-memory-anchor-recovery"]
	if !ok {
		t.Fatalf("long-run suite missing loop/memory anchor recovery scenario")
	}
	if !loopMemoryAnchor.EnableMemory || loopMemoryAnchor.SessionID != "loop-memory-anchor-reader" {
		t.Fatalf("loop/memory anchor fields = memory:%v session:%q", loopMemoryAnchor.EnableMemory, loopMemoryAnchor.SessionID)
	}
	if loopMemoryAnchor.RequiredToolCounts["session_search"] != 1 ||
		loopMemoryAnchor.RequiredToolCounts["memory"] != 1 ||
		loopMemoryAnchor.MaxSuccessfulToolCallsByTool["session_search"] != 1 ||
		loopMemoryAnchor.MaxSuccessfulToolCallsByTool["memory"] != 1 {
		t.Fatalf("loop/memory anchor tool constraints = counts:%#v max:%#v", loopMemoryAnchor.RequiredToolCounts, loopMemoryAnchor.MaxSuccessfulToolCallsByTool)
	}
	if !toolOrderContains(loopMemoryAnchor.RequiredToolOrder, ToolOrderRequirement{Earlier: "session_search", Later: "memory"}) {
		t.Fatalf("loop/memory anchor RequiredToolOrder = %#v, want session_search before memory", loopMemoryAnchor.RequiredToolOrder)
	}
	if loopMemoryAnchor.RequiredLoopProtocolFeeds != 1 ||
		loopMemoryAnchor.RequiredLoopProtocolFeedModes["full"] != 1 ||
		len(loopMemoryAnchor.RequiredLoopProtocolFeedMatches) != 1 ||
		!strings.Contains(loopMemoryAnchor.RequiredLoopProtocolFeedMatches[0].CurrentSituation, "recent-session anchors then memory") {
		t.Fatalf("loop/memory anchor loop protocol constraints = feeds:%d modes:%#v matches:%#v", loopMemoryAnchor.RequiredLoopProtocolFeeds, loopMemoryAnchor.RequiredLoopProtocolFeedModes, loopMemoryAnchor.RequiredLoopProtocolFeedMatches)
	}
	if loopMemoryAnchor.RequiredToolStatsAtLeast["session_search_recent_sessions"] != 1 ||
		loopMemoryAnchor.RequiredToolStatsAtLeast["memory_search_calls"] != 1 {
		t.Fatalf("loop/memory anchor stats = %#v, want recent sessions and memory search", loopMemoryAnchor.RequiredToolStatsAtLeast)
	}
	if len(loopMemoryAnchor.RequiredRecentSessionSearch) != 3 ||
		loopMemoryAnchor.RequiredRecentSessionSearch[0].SessionID != "loop-anchor-recovery" ||
		loopMemoryAnchor.RequiredRecentSessionSearch[0].QueryContains != "ZETAABSENT404" ||
		loopMemoryAnchor.RequiredRecentSessionSearch[0].LoopContains != "loop.protocol_feed" ||
		loopMemoryAnchor.RequiredRecentSessionSearch[0].RecoveryContains != "loop_guard_no_new_evidence" {
		t.Fatalf("loop/memory anchor recent-session requirements = %#v", loopMemoryAnchor.RequiredRecentSessionSearch)
	}
	for _, want := range []string{"LOOP-ANCHOR-61", "MEM-LOOP-61", "evidence-before-synthesis", "api-price-mismatch", "API price ref hx9", "loop.protocol_feed", "loop_guard_no_new_evidence", "loop-anchor-recovery"} {
		if !stringSliceContains(loopMemoryAnchor.RequiredFinalText, want) {
			t.Fatalf("loop/memory anchor RequiredFinalText = %#v, want %q", loopMemoryAnchor.RequiredFinalText, want)
		}
	}
	if _, ok := loopMemoryAnchor.Files[".affent/loops/loop-memory-anchor-reader/LOOP.md"]; !ok {
		t.Fatalf("loop/memory anchor scenario missing active LOOP.md")
	}
	if !stringSliceContains(loopMemoryAnchor.ProtectedFiles, ".affent/loops/loop-memory-anchor-reader/LOOP.md") {
		t.Fatalf("loop/memory anchor ProtectedFiles = %#v, want active LOOP.md", loopMemoryAnchor.ProtectedFiles)
	}
	loopMemoryAnchorCaps := ExpectationCapabilityNames(debugScenarioExpectations(loopMemoryAnchor))
	if !stringSliceContains(loopMemoryAnchorCaps, "longrun_recovery") ||
		!stringSliceContains(loopMemoryAnchorCaps, "loop_protocol") ||
		!stringSliceContains(loopMemoryAnchorCaps, "memory") ||
		!stringSliceContains(loopMemoryAnchorCaps, "session_search") {
		t.Fatalf("loop/memory anchor expectation capabilities = %#v, want longrun recovery stack", loopMemoryAnchorCaps)
	}

	crashResume, ok := seen["longrun-crash-missing-tool-result-resume"]
	if !ok {
		t.Fatalf("long-run suite missing crash missing-tool-result resume scenario")
	}
	if crashResume.SessionID != "resume-missing-tool-result" {
		t.Fatalf("crash resume SessionID = %q, want resume-missing-tool-result", crashResume.SessionID)
	}
	if _, ok := crashResume.Files[".affentctl/resume-missing-tool-result.jsonl"]; !ok {
		t.Fatalf("crash resume scenario missing seeded broken conversation")
	}
	for _, want := range []string{"RECOVER-TOOL-19", "current/recovery.md", "do not assume the tool succeeded", "safe to repeat"} {
		if !stringSliceContains(crashResume.RequiredFinalText, want) {
			t.Fatalf("crash resume RequiredFinalText = %#v, want %q", crashResume.RequiredFinalText, want)
		}
	}
	for _, forbidden := range []string{"read_file", "web_fetch", "browser_network_read", "session_search", "memory", "plan"} {
		if !stringSliceContains(crashResume.ForbiddenTools, forbidden) {
			t.Fatalf("crash resume ForbiddenTools = %#v, want %q", crashResume.ForbiddenTools, forbidden)
		}
	}
	requiredConversation := crashResume.RequiredFileSubstrings[".affentctl/resume-missing-tool-result.jsonl"]
	for _, want := range []string{"Failure: kind=resume_missing_tool_result", "Next: do not assume the tool succeeded", "safe to repeat", "call-web-crashed"} {
		if !stringSliceContains(requiredConversation, want) {
			t.Fatalf("crash resume RequiredFileSubstrings = %#v, want %q", crashResume.RequiredFileSubstrings, want)
		}
	}
	if crashResume.RequiredTraceEventCounts["conversation.repaired"] != 1 {
		t.Fatalf("crash resume RequiredTraceEventCounts = %#v, want conversation.repaired=1", crashResume.RequiredTraceEventCounts)
	}
	if crashResume.RequiredConversationRepairStatsAtLeast["events"] != 1 ||
		crashResume.RequiredConversationRepairStatsAtLeast["missing_tool_results"] != 1 ||
		crashResume.RequiredConversationRepairKinds["resume_missing_tool_result"] != 1 {
		t.Fatalf("crash resume conversation repair requirements = stats:%#v kinds:%#v", crashResume.RequiredConversationRepairStatsAtLeast, crashResume.RequiredConversationRepairKinds)
	}
	if stringSliceContains(crashResume.ProtectedFiles, ".affentctl/resume-missing-tool-result.jsonl") {
		t.Fatalf("crash resume conversation must not be protected because repair rewrites it: %#v", crashResume.ProtectedFiles)
	}

	duplicateResume, ok := seen["longrun-crash-duplicate-tool-result-resume"]
	if !ok {
		t.Fatalf("long-run suite missing crash duplicate-tool-result resume scenario")
	}
	if duplicateResume.SessionID != "resume-duplicate-tool-result" {
		t.Fatalf("duplicate resume SessionID = %q, want resume-duplicate-tool-result", duplicateResume.SessionID)
	}
	if _, ok := duplicateResume.Files[".affentctl/resume-duplicate-tool-result.jsonl"]; !ok {
		t.Fatalf("duplicate resume scenario missing seeded broken conversation")
	}
	for _, want := range []string{"RECOVER-DUP-23", "current/duplicate.md", "resume_duplicate_tool_result", "resume_unexpected_tool_result"} {
		if !stringSliceContains(duplicateResume.RequiredFinalText, want) {
			t.Fatalf("duplicate resume RequiredFinalText = %#v, want %q", duplicateResume.RequiredFinalText, want)
		}
	}
	for _, forbidden := range []string{"read_file", "web_fetch", "browser_network_read", "session_search", "memory", "plan"} {
		if !stringSliceContains(duplicateResume.ForbiddenTools, forbidden) {
			t.Fatalf("duplicate resume ForbiddenTools = %#v, want %q", duplicateResume.ForbiddenTools, forbidden)
		}
	}
	requiredDuplicateConversation := duplicateResume.RequiredFileSubstrings[".affentctl/resume-duplicate-tool-result.jsonl"]
	for _, want := range []string{"Failure: kind=resume_duplicate_tool_result", "Failure: kind=resume_unexpected_tool_result", "duplicate stale retry", "unexpected orphan web result", "call-orphan-web"} {
		if !stringSliceContains(requiredDuplicateConversation, want) {
			t.Fatalf("duplicate resume RequiredFileSubstrings = %#v, want %q", duplicateResume.RequiredFileSubstrings, want)
		}
	}
	if duplicateResume.RequiredTraceEventCounts["conversation.repaired"] != 1 {
		t.Fatalf("duplicate resume RequiredTraceEventCounts = %#v, want conversation.repaired=1", duplicateResume.RequiredTraceEventCounts)
	}
	if duplicateResume.RequiredConversationRepairStatsAtLeast["events"] != 1 ||
		duplicateResume.RequiredConversationRepairStatsAtLeast["duplicate_tool_results"] != 2 ||
		duplicateResume.RequiredConversationRepairStatsAtLeast["unexpected_tool_results"] != 1 ||
		duplicateResume.RequiredConversationRepairKinds["resume_duplicate_tool_result"] != 1 {
		t.Fatalf("duplicate resume conversation repair requirements = stats:%#v kinds:%#v", duplicateResume.RequiredConversationRepairStatsAtLeast, duplicateResume.RequiredConversationRepairKinds)
	}
	if stringSliceContains(duplicateResume.ProtectedFiles, ".affentctl/resume-duplicate-tool-result.jsonl") {
		t.Fatalf("duplicate resume conversation must not be protected because repair rewrites it: %#v", duplicateResume.ProtectedFiles)
	}

	compactionRetention, ok := seen["longrun-context-compaction-retention"]
	if !ok {
		t.Fatalf("long-run suite missing context compaction retention scenario")
	}
	if compactionRetention.CompactTrigger != 6 || compactionRetention.CompactKeepLast != 3 {
		t.Fatalf("compaction retention settings = trigger:%d keep_last:%d, want 6/3", compactionRetention.CompactTrigger, compactionRetention.CompactKeepLast)
	}
	if compactionRetention.SessionID != "longrun-compaction-retention" {
		t.Fatalf("compaction retention SessionID = %q, want longrun-compaction-retention", compactionRetention.SessionID)
	}
	if len(compactionRetention.Prompts) != 2 || !strings.Contains(compactionRetention.Prompts[1], "不要调用任何工具") {
		t.Fatalf("compaction retention Prompts = %#v, want two-turn recovery prompt", compactionRetention.Prompts)
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
	for _, want := range []string{".affent/loops/longrun-compaction-retention/LOOP.md", "loop_id=longrun-compaction-retention"} {
		if !stringSliceContains(compactionRetention.RequiredContextLoopProtocolAnchorText, want) {
			t.Fatalf("compaction retention RequiredContextLoopProtocolAnchorText = %#v, want %q", compactionRetention.RequiredContextLoopProtocolAnchorText, want)
		}
	}
	if compactionRetention.RequiredLoopProtocolFeeds != 2 ||
		compactionRetention.RequiredLoopProtocolFeedModes["full"] != 2 ||
		!compactionRetention.RequireLoopProtocolFullAfterCompact ||
		len(compactionRetention.RequiredLoopProtocolFeedMatches) != 1 ||
		!strings.Contains(compactionRetention.RequiredLoopProtocolFeedMatches[0].CurrentSituation, "current/*.md handoff files") ||
		compactionRetention.RequiredLoopProtocolFeedMatches[0].LastTurnEndReason != "completed" ||
		compactionRetention.RequiredLoopProtocolFeedMatches[0].MinLastTurnToolRequests != 5 {
		t.Fatalf("compaction retention loop protocol constraints = feeds:%d modes:%#v", compactionRetention.RequiredLoopProtocolFeeds, compactionRetention.RequiredLoopProtocolFeedModes)
	}
	if _, ok := compactionRetention.Files[".affent/loops/longrun-compaction-retention/LOOP.md"]; !ok {
		t.Fatalf("compaction retention missing seeded LOOP.md")
	}
	if !stringSliceContains(compactionRetention.ProtectedFiles, ".affent/loops/longrun-compaction-retention/LOOP.md") {
		t.Fatalf("compaction retention ProtectedFiles = %#v, want LOOP.md", compactionRetention.ProtectedFiles)
	}
	if !stringSliceContains(compactionRetention.ForbiddenTools, "shell") {
		t.Fatalf("compaction retention ForbiddenTools = %#v, want shell", compactionRetention.ForbiddenTools)
	}

	loopCalibration, ok := seen["longrun-loop-activation-calibration"]
	if !ok {
		t.Fatalf("long-run suite missing loop activation calibration scenario")
	}
	if !loopCalibration.EnableLoopProtocol || loopCalibration.SessionID != "loop-activation-calibration" {
		t.Fatalf("loop calibration fields = enable:%v session:%q", loopCalibration.EnableLoopProtocol, loopCalibration.SessionID)
	}
	if len(loopCalibration.Prompts) != 2 ||
		loopCalibration.RequiredLoopProtocolCalibrationRequests != 1 ||
		loopCalibration.RequiredLoopProtocolCalibrations != 1 ||
		loopCalibration.RequiredLoopProtocolCalibrationRequestStatuses["draft"] != 1 ||
		loopCalibration.RequiredLoopProtocolCalibrationStatuses["draft"] != 1 ||
		loopCalibration.RequiredTraceEventCounts["loop.protocol_calibration_request"] != 1 ||
		loopCalibration.RequiredTraceEventCounts["loop.protocol_calibration"] != 1 {
		t.Fatalf("loop calibration expectations = prompts:%d requests:%d answers:%d request_statuses:%#v answer_statuses:%#v trace:%#v", len(loopCalibration.Prompts), loopCalibration.RequiredLoopProtocolCalibrationRequests, loopCalibration.RequiredLoopProtocolCalibrations, loopCalibration.RequiredLoopProtocolCalibrationRequestStatuses, loopCalibration.RequiredLoopProtocolCalibrationStatuses, loopCalibration.RequiredTraceEventCounts)
	}
	for _, want := range []string{"LOOP-CALIBRATION-Q17", "LOOP-CALIBRATION-A17", "Pause if source evidence is unavailable", "repeated tool failures", "objective changed"} {
		if !stringSliceContains(loopCalibration.RequiredFinalText, want) {
			t.Fatalf("loop calibration RequiredFinalText = %#v, want %q", loopCalibration.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(loopCalibration.ForbiddenTools, "loop_protocol") || loopCalibration.MaxParentToolCalls != 0 {
		t.Fatalf("loop calibration tool constraints = forbidden:%#v max_parent:%d", loopCalibration.ForbiddenTools, loopCalibration.MaxParentToolCalls)
	}
	loopCalibrationCaps := ExpectationCapabilityNames(debugScenarioExpectations(loopCalibration))
	if !stringSliceContains(loopCalibrationCaps, "loop_protocol") ||
		!stringSliceContains(loopCalibrationCaps, "trace") {
		t.Fatalf("loop calibration expectation capabilities = %#v, want loop protocol and trace", loopCalibrationCaps)
	}
	if scenarioRequiresActiveLoopProtocol(loopCalibration) {
		t.Fatal("loop calibration setup scenario must not require a pre-active LOOP.md fixture")
	}

	researchCheckpoint, ok := seen["longrun-research-checkpoint"]
	if !ok {
		t.Fatalf("long-run suite missing research checkpoint scenario")
	}
	if researchCheckpoint.SessionID != "longrun-research-checkpoint" {
		t.Fatalf("research checkpoint SessionID = %q, want longrun-research-checkpoint", researchCheckpoint.SessionID)
	}
	if researchCheckpoint.RequiredLoopDecisionKinds["research_checkpoint"] != 1 ||
		researchCheckpoint.RequiredLoopDecisionResults["trigger"] != 1 ||
		len(researchCheckpoint.RequiredLoopDecisionMatches) != 1 ||
		researchCheckpoint.RequiredLoopDecisionMatches[0] != (LoopDecisionRequirement{Kind: "research_checkpoint", Decision: "trigger", Trigger: "external_calibration_requested"}) {
		t.Fatalf("research checkpoint loop decision constraints = kinds:%#v results:%#v matches:%#v", researchCheckpoint.RequiredLoopDecisionKinds, researchCheckpoint.RequiredLoopDecisionResults, researchCheckpoint.RequiredLoopDecisionMatches)
	}
	if researchCheckpoint.RequiredLoopProtocolFeeds != 1 || researchCheckpoint.RequiredLoopProtocolFeedModes["full"] != 1 {
		t.Fatalf("research checkpoint loop protocol constraints = feeds:%d modes:%#v", researchCheckpoint.RequiredLoopProtocolFeeds, researchCheckpoint.RequiredLoopProtocolFeedModes)
	}
	if researchCheckpoint.RequiredTraceEventCounts["loop.turn_checkpoint"] != 1 {
		t.Fatalf("research checkpoint trace event constraints = %#v, want loop.turn_checkpoint", researchCheckpoint.RequiredTraceEventCounts)
	}
	if _, ok := researchCheckpoint.Files[".affent/loops/longrun-research-checkpoint/LOOP.md"]; !ok {
		t.Fatalf("research checkpoint missing seeded LOOP.md")
	}
	if !stringSliceContains(researchCheckpoint.RequiredFinalText, "RESEARCH-CHECKPOINT-37") {
		t.Fatalf("research checkpoint RequiredFinalText = %#v, want marker", researchCheckpoint.RequiredFinalText)
	}
	researchCheckpointCaps := ExpectationCapabilityNames(debugScenarioExpectations(researchCheckpoint))
	if !stringSliceContains(researchCheckpointCaps, "research_checkpoint") ||
		!stringSliceContains(researchCheckpointCaps, "loop_protocol") {
		t.Fatalf("research checkpoint expectation capabilities = %#v, want research checkpoint and loop protocol", researchCheckpointCaps)
	}
	if !stringSliceContains(researchCheckpoint.ForbiddenTools, "run_task") || !stringSliceContains(researchCheckpoint.ForbiddenTools, "web_fetch") {
		t.Fatalf("research checkpoint ForbiddenTools = %#v, want no research tools in smoke scenario", researchCheckpoint.ForbiddenTools)
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

	memoryAutonomousWrite, ok := seen["memory-autonomous-durable-rule"]
	if !ok {
		t.Fatalf("long-run suite missing autonomous memory write scenario")
	}
	if !memoryAutonomousWrite.EnableMemory || memoryAutonomousWrite.SessionID != "memory-autonomous-writer" {
		t.Fatalf("autonomous memory write fields = memory:%v session:%q", memoryAutonomousWrite.EnableMemory, memoryAutonomousWrite.SessionID)
	}
	if strings.Contains(memoryAutonomousWrite.Prompt, "memory tool") || strings.Contains(memoryAutonomousWrite.Prompt, "action=add") {
		t.Fatalf("autonomous memory write should not directly command the tool: %q", memoryAutonomousWrite.Prompt)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "memory", Arg: "content", Substring: "AUTO-MEM-73"},
		{Tool: "memory", Arg: "content", Substring: "source-led"},
	} {
		if !toolArgRequirementContains(memoryAutonomousWrite.RequiredToolArgContains, want) {
			t.Fatalf("autonomous memory write RequiredToolArgContains = %#v, want %#v", memoryAutonomousWrite.RequiredToolArgContains, want)
		}
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "memory", Arg: "content", Substring: "commit hash"},
		{Tool: "memory", Arg: "content", Substring: "push result"},
	} {
		if !toolArgRequirementContains(memoryAutonomousWrite.ForbiddenToolArgContains, want) {
			t.Fatalf("autonomous memory write ForbiddenToolArgContains = %#v, want %#v", memoryAutonomousWrite.ForbiddenToolArgContains, want)
		}
	}
	if !stringSliceContains(memoryAutonomousWrite.Domains, memoryDomain) || !stringSliceContains(memoryAutonomousWrite.Domains, longRunRecoveryDomain) {
		t.Fatalf("autonomous memory write Domains = %#v, want memory and longrun_recovery", memoryAutonomousWrite.Domains)
	}

	skillInstall, ok := seen["skill-reviewed-install-activation"]
	if !ok {
		t.Fatalf("long-run suite missing reviewed skill install activation scenario")
	}
	if skillInstall.SessionID != "skill-reviewed-install" || len(skillInstall.Prompts) != 3 {
		t.Fatalf("reviewed skill install session/prompts = %q/%d", skillInstall.SessionID, len(skillInstall.Prompts))
	}
	skillInstallCaps := ScenarioExpectationCapabilityNames(skillInstall)
	if !stringSliceContains(skillInstallCaps, "skill_install") || !stringSliceContains(skillInstallCaps, "skill") {
		t.Fatalf("reviewed skill install capabilities = %#v, want skill and skill_install", skillInstallCaps)
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
	if len(scenarios) != 7 {
		t.Fatalf("live-web suite size = %d, want 7", len(scenarios))
	}
	seen := map[string]BatchScenario{}
	for _, scenario := range scenarios {
		seen[scenario.Name] = scenario
	}
	skillURL, ok := seen["live-web-skill-url-install-activation"]
	if !ok {
		t.Fatalf("live-web suite missing skill URL install activation scenario")
	}
	if skillURL.SessionID != "skill-url-install-activation" || len(skillURL.Prompts) != 3 {
		t.Fatalf("skill URL scenario session/prompts = %q/%d", skillURL.SessionID, len(skillURL.Prompts))
	}
	if skillURL.RequiredToolCounts["skill"] != 2 {
		t.Fatalf("skill URL RequiredToolCounts = %#v, want skill=2", skillURL.RequiredToolCounts)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "skill", Arg: "action", Substring: "propose_url"},
		{Tool: "skill", Arg: "source", Substring: "https://raw.githubusercontent.com/openai/skills/b0401f07213a66414d84a65cb50c1d226f99485a/skills/.curated/playwright/SKILL.md"},
		{Tool: "skill", Arg: "triggers", Substring: "playwright_eval"},
		{Tool: "skill", Arg: "action", Substring: "confirm_install"},
		{Tool: "skill", Arg: "proposal_id", Substring: "54e64fbbf4bfaf9f"},
	} {
		if !toolArgRequirementContains(skillURL.RequiredToolArgContains, want) {
			t.Fatalf("skill URL RequiredToolArgContains = %#v, want %#v", skillURL.RequiredToolArgContains, want)
		}
	}
	for _, want := range []string{"prepared skill install proposal_id=54e64fbbf4bfaf9f", "installed skill \"playwright\"", "active_now=true"} {
		if !stringSliceContains(skillURL.RequiredToolResultText["skill"], want) {
			t.Fatalf("skill URL tool result requirements = %#v, want %q", skillURL.RequiredToolResultText["skill"], want)
		}
	}
	if skillURL.RequiredContextInjectionSources["skill_provider"] != 1 ||
		skillURL.RequiredTraceEventCounts["context.injected"] != 1 {
		t.Fatalf("skill URL context requirements = sources:%#v trace:%#v", skillURL.RequiredContextInjectionSources, skillURL.RequiredTraceEventCounts)
	}
	for _, want := range []string{"Playwright CLI Skill", "command -v npx"} {
		if !stringSliceContains(skillURL.RequiredFinalText, want) {
			t.Fatalf("skill URL RequiredFinalText = %#v, want %q", skillURL.RequiredFinalText, want)
		}
	}
	if !stringSliceContains(skillURL.ForbiddenTools, "shell") || !stringSliceContains(skillURL.ForbiddenTools, "web_fetch") {
		t.Fatalf("skill URL ForbiddenTools = %#v, want no shell/web_fetch", skillURL.ForbiddenTools)
	}
	researchEvidence, ok := seen["live-web-research-checkpoint-evidence"]
	if !ok {
		t.Fatalf("live-web suite missing research checkpoint evidence scenario")
	}
	if researchEvidence.SessionID != "live-web-research-checkpoint-evidence" {
		t.Fatalf("research evidence SessionID = %q", researchEvidence.SessionID)
	}
	if !stringSliceContains(researchEvidence.RequiredTools, "web_fetch") ||
		researchEvidence.RequiredToolCounts["web_fetch"] != 1 {
		t.Fatalf("research evidence web_fetch requirements = tools:%#v counts:%#v", researchEvidence.RequiredTools, researchEvidence.RequiredToolCounts)
	}
	if !toolArgRequirementContains(researchEvidence.RequiredToolArgContains, ToolArgContainsRequirement{Tool: "web_fetch", Arg: "url", Substring: "code.claude.com/docs/en/overview"}) {
		t.Fatalf("research evidence RequiredToolArgContains = %#v", researchEvidence.RequiredToolArgContains)
	}
	if researchEvidence.RequiredLoopDecisionKinds["research_checkpoint"] != 1 ||
		researchEvidence.RequiredLoopDecisionResults["trigger"] != 1 ||
		len(researchEvidence.RequiredLoopDecisionMatches) != 1 ||
		researchEvidence.RequiredLoopDecisionMatches[0] != (LoopDecisionRequirement{Kind: "research_checkpoint", Decision: "trigger", Trigger: "external_calibration_requested"}) {
		t.Fatalf("research evidence loop decision constraints = kinds:%#v results:%#v matches:%#v", researchEvidence.RequiredLoopDecisionKinds, researchEvidence.RequiredLoopDecisionResults, researchEvidence.RequiredLoopDecisionMatches)
	}
	if researchEvidence.RequiredLoopProtocolFeeds != 1 || researchEvidence.RequiredLoopProtocolFeedModes["full"] != 1 {
		t.Fatalf("research evidence loop protocol constraints = feeds:%d modes:%#v", researchEvidence.RequiredLoopProtocolFeeds, researchEvidence.RequiredLoopProtocolFeedModes)
	}
	for _, field := range []string{"source_access_results", "source_access_verified"} {
		if researchEvidence.RequiredToolStatsAtLeast[field] != 1 {
			t.Fatalf("research evidence source access requirements = %#v, want %s=1", researchEvidence.RequiredToolStatsAtLeast, field)
		}
	}
	if len(researchEvidence.RequiredSourceAccess) != 1 ||
		researchEvidence.RequiredSourceAccess[0] != (SourceAccessRequirement{Status: "verified", Tool: "web_fetch", URLContains: "code.claude.com/docs/en/overview"}) {
		t.Fatalf("research evidence RequiredSourceAccess = %#v", researchEvidence.RequiredSourceAccess)
	}
	for _, want := range []string{"SourceAccess:", "fetched_url=", "requested_url="} {
		if !stringSliceContains(researchEvidence.RequiredToolResultText["web_fetch"], want) {
			t.Fatalf("research evidence web_fetch result requirements = %#v, want %q", researchEvidence.RequiredToolResultText["web_fetch"], want)
		}
	}
	for _, want := range []string{"RESEARCH-EVIDENCE-42", "Claude Code", "code.claude.com", "external calibration", "fetched_url", "requested_url"} {
		if !stringSliceContains(researchEvidence.RequiredFinalText, want) {
			t.Fatalf("research evidence RequiredFinalText = %#v, want %q", researchEvidence.RequiredFinalText, want)
		}
	}
	researchEvidenceCaps := ExpectationCapabilityNames(debugScenarioExpectations(researchEvidence))
	for _, want := range []string{"loop_protocol", "research_checkpoint", "source_access", "web"} {
		if !stringSliceContains(researchEvidenceCaps, want) {
			t.Fatalf("research evidence expectation capabilities = %#v, want %q", researchEvidenceCaps, want)
		}
	}
	if !stringSliceContains(researchEvidence.ForbiddenTools, "browser_navigate") ||
		!stringSliceContains(researchEvidence.ForbiddenTools, "run_task") {
		t.Fatalf("research evidence ForbiddenTools = %#v, want browser and delegation forbidden", researchEvidence.ForbiddenTools)
	}

	delegatedResearch, ok := seen["live-web-research-checkpoint-delegated-evidence"]
	if !ok {
		t.Fatalf("live-web suite missing delegated research checkpoint evidence scenario")
	}
	if delegatedResearch.SessionID != "live-web-research-checkpoint-delegated-evidence" {
		t.Fatalf("delegated research SessionID = %q", delegatedResearch.SessionID)
	}
	if !stringSliceContains(delegatedResearch.RequiredTools, "run_task") ||
		delegatedResearch.RequiredToolCounts["run_task"] != 1 ||
		delegatedResearch.RequiredFocusedTaskCounts["research"] != 1 ||
		delegatedResearch.RequiredFocusedTaskSourceCounts["research"] != 2 ||
		!delegatedResearch.RequireNoDelegationErrors {
		t.Fatalf("delegated research requirements = tools:%#v counts:%#v focused:%#v sources:%#v no_errors:%v", delegatedResearch.RequiredTools, delegatedResearch.RequiredToolCounts, delegatedResearch.RequiredFocusedTaskCounts, delegatedResearch.RequiredFocusedTaskSourceCounts, delegatedResearch.RequireNoDelegationErrors)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "run_task", Arg: "task_type", Substring: "research"},
		{Tool: "run_task", Arg: "objective", Substring: "Claude Code"},
		{Tool: "run_task", Arg: "objective", Substring: "Hermes"},
	} {
		if !toolArgRequirementContains(delegatedResearch.RequiredToolArgContains, want) {
			t.Fatalf("delegated research RequiredToolArgContains = %#v, want %#v", delegatedResearch.RequiredToolArgContains, want)
		}
	}
	for _, want := range []string{`"task_type":"research"`, `"ok":true`, `"findings"`, `"source"`} {
		if !stringSliceContains(delegatedResearch.RequiredToolResultText["run_task"], want) {
			t.Fatalf("delegated research run_task result requirements = %#v, want %q", delegatedResearch.RequiredToolResultText["run_task"], want)
		}
	}
	if delegatedResearch.RequiredLoopDecisionKinds["research_checkpoint"] != 1 ||
		delegatedResearch.RequiredLoopDecisionResults["trigger"] != 1 ||
		len(delegatedResearch.RequiredLoopDecisionMatches) != 1 ||
		delegatedResearch.RequiredLoopDecisionMatches[0] != (LoopDecisionRequirement{Kind: "research_checkpoint", Decision: "trigger", Trigger: "external_calibration_requested"}) {
		t.Fatalf("delegated research loop decision constraints = kinds:%#v results:%#v matches:%#v", delegatedResearch.RequiredLoopDecisionKinds, delegatedResearch.RequiredLoopDecisionResults, delegatedResearch.RequiredLoopDecisionMatches)
	}
	if delegatedResearch.RequiredLoopProtocolFeeds != 1 || delegatedResearch.RequiredLoopProtocolFeedModes["full"] != 1 {
		t.Fatalf("delegated research loop protocol constraints = feeds:%d modes:%#v", delegatedResearch.RequiredLoopProtocolFeeds, delegatedResearch.RequiredLoopProtocolFeedModes)
	}
	for _, want := range []string{"RESEARCH-DELEGATED-58", "research", "run_task", "Claude Code", "Hermes", "external calibration"} {
		if !stringSliceContains(delegatedResearch.RequiredFinalText, want) {
			t.Fatalf("delegated research RequiredFinalText = %#v, want %q", delegatedResearch.RequiredFinalText, want)
		}
	}
	delegatedResearchCaps := ExpectationCapabilityNames(debugScenarioExpectations(delegatedResearch))
	for _, want := range []string{"delegated_source_evidence", "delegation", "loop_protocol", "research_checkpoint"} {
		if !stringSliceContains(delegatedResearchCaps, want) {
			t.Fatalf("delegated research expectation capabilities = %#v, want %q", delegatedResearchCaps, want)
		}
	}
	for _, forbidden := range []string{"web_fetch", "web_search", "browser_navigate", "subagent_run"} {
		if !stringSliceContains(delegatedResearch.ForbiddenTools, forbidden) {
			t.Fatalf("delegated research ForbiddenTools = %#v, want %q", delegatedResearch.ForbiddenTools, forbidden)
		}
	}
	if delegatedResearch.MaxParentToolCalls != 1 {
		t.Fatalf("delegated research MaxParentToolCalls = %d, want 1", delegatedResearch.MaxParentToolCalls)
	}
	for _, want := range []string{webEvidenceDomain, longRunRecoveryDomain} {
		if !stringSliceContains(delegatedResearch.Domains, want) {
			t.Fatalf("delegated research Domains = %#v, want %q", delegatedResearch.Domains, want)
		}
	}

	scenario, ok := seen["live-web-taostats-sn120-dynamic-evidence"]
	if !ok {
		t.Fatalf("live-web suite missing dynamic evidence scenario")
	}
	for _, want := range []string{"browser_navigate", "browser_network_read"} {
		if !stringSliceContains(scenario.RequiredTools, want) {
			t.Fatalf("live-web RequiredTools = %#v, want %q", scenario.RequiredTools, want)
		}
	}
	if stringSliceContains(scenario.RequiredTools, "browser_network") {
		t.Fatalf("live-web RequiredTools = %#v, should allow direct browser_network_read from snapshot refs", scenario.RequiredTools)
	}
	for _, want := range []string{bittensorDomain, webEvidenceDomain} {
		if !stringSliceContains(scenario.Domains, want) {
			t.Fatalf("live-web Domains = %#v, want %q", scenario.Domains, want)
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
	for _, want := range []string{"web_fetch", "browser_navigate", "browser_network_read"} {
		if !stringSliceContains(recovery.RequiredTools, want) {
			t.Fatalf("live-web recovery RequiredTools = %#v, want %q", recovery.RequiredTools, want)
		}
	}
	if stringSliceContains(recovery.RequiredTools, "browser_network") {
		t.Fatalf("live-web recovery RequiredTools = %#v, should allow direct browser_network_read from snapshot refs", recovery.RequiredTools)
	}
	if recovery.RequiredToolCounts["web_fetch"] != 1 || recovery.RequiredToolCounts["browser_network_read"] != 1 {
		t.Fatalf("live-web recovery tool counts = %#v, want web_fetch/browser_network_read once", recovery.RequiredToolCounts)
	}
	if len(recovery.RequiredToolOrder) != 2 ||
		recovery.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "web_fetch", Later: "browser_navigate"}) ||
		recovery.RequiredToolOrder[1] != (ToolOrderRequirement{Earlier: "browser_navigate", Later: "browser_network_read"}) {
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

	scrollRecovery, ok := seen["live-web-taostats-scroll-network-recovery"]
	if !ok {
		t.Fatalf("live-web suite missing scroll network recovery scenario")
	}
	for _, want := range []string{"browser_navigate", "browser_scroll", "browser_network_read"} {
		if !stringSliceContains(scrollRecovery.RequiredTools, want) {
			t.Fatalf("live-web scroll recovery RequiredTools = %#v, want %q", scrollRecovery.RequiredTools, want)
		}
	}
	if scrollRecovery.RequiredToolCounts["browser_scroll"] != 1 || scrollRecovery.RequiredToolCounts["browser_network_read"] != 1 {
		t.Fatalf("live-web scroll recovery tool counts = %#v, want browser_scroll/browser_network_read once", scrollRecovery.RequiredToolCounts)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "browser_navigate", Arg: "url", Substring: "taostats.io/subnets/120"},
		{Tool: "browser_scroll", Arg: "direction", Substring: "down"},
	} {
		if !toolArgRequirementContains(scrollRecovery.RequiredToolArgContains, want) {
			t.Fatalf("live-web scroll recovery RequiredToolArgContains = %#v, want %#v", scrollRecovery.RequiredToolArgContains, want)
		}
	}
	if len(scrollRecovery.RequiredToolOrder) != 2 ||
		scrollRecovery.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "browser_navigate", Later: "browser_scroll"}) ||
		scrollRecovery.RequiredToolOrder[1] != (ToolOrderRequirement{Earlier: "browser_scroll", Later: "browser_network_read"}) {
		t.Fatalf("live-web scroll recovery tool order = %#v", scrollRecovery.RequiredToolOrder)
	}
	if len(scrollRecovery.RequiredSourceAccess) != 1 ||
		scrollRecovery.RequiredSourceAccess[0] != (SourceAccessRequirement{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch"}) {
		t.Fatalf("live-web scroll recovery RequiredSourceAccess = %#v", scrollRecovery.RequiredSourceAccess)
	}
	for _, want := range []string{"SourceAccess:", "SCROLL:"} {
		if !stringSliceContains(scrollRecovery.RequiredToolResultText["browser_scroll"], want) {
			t.Fatalf("live-web scroll recovery browser_scroll result requirements = %#v, want %q", scrollRecovery.RequiredToolResultText["browser_scroll"], want)
		}
	}
	for _, want := range []string{"SourceAccess:", "browser_network_url=", "requested_url=", "ref=", "status=", "content_type=", "source_method=network_xhr_fetch"} {
		if !stringSliceContains(scrollRecovery.RequiredToolResultText["browser_network_read"], want) {
			t.Fatalf("live-web scroll recovery browser_network_read result requirements = %#v, want %q", scrollRecovery.RequiredToolResultText["browser_network_read"], want)
		}
	}
	for _, want := range []string{"browser_scroll", "browser_network_url", "requested_url", "ref=", "status=", "content_type=", "source_method", "未验证"} {
		if !stringSliceContains(scrollRecovery.RequiredFinalText, want) {
			t.Fatalf("live-web scroll recovery RequiredFinalText = %#v, want %q", scrollRecovery.RequiredFinalText, want)
		}
	}

	networkSearch, ok := seen["live-web-taostats-network-search-read"]
	if !ok {
		t.Fatalf("live-web suite missing network search/read scenario")
	}
	for _, want := range []string{"browser_navigate", "browser_network", "browser_network_read"} {
		if !stringSliceContains(networkSearch.RequiredTools, want) {
			t.Fatalf("live-web network search RequiredTools = %#v, want %q", networkSearch.RequiredTools, want)
		}
	}
	if networkSearch.RequiredToolCounts["browser_network"] != 1 || networkSearch.RequiredToolCounts["browser_network_read"] != 1 {
		t.Fatalf("live-web network search tool counts = %#v, want browser_network/browser_network_read once", networkSearch.RequiredToolCounts)
	}
	for _, want := range []ToolArgContainsRequirement{
		{Tool: "browser_navigate", Arg: "url", Substring: "taostats.io/subnets/120"},
		{Tool: "browser_network", Arg: "query", Substring: "market cap"},
	} {
		if !toolArgRequirementContains(networkSearch.RequiredToolArgContains, want) {
			t.Fatalf("live-web network search RequiredToolArgContains = %#v, want %#v", networkSearch.RequiredToolArgContains, want)
		}
	}
	if len(networkSearch.RequiredToolOrder) != 2 ||
		networkSearch.RequiredToolOrder[0] != (ToolOrderRequirement{Earlier: "browser_navigate", Later: "browser_network"}) ||
		networkSearch.RequiredToolOrder[1] != (ToolOrderRequirement{Earlier: "browser_network", Later: "browser_network_read"}) {
		t.Fatalf("live-web network search tool order = %#v", networkSearch.RequiredToolOrder)
	}
	for _, field := range []string{"source_access_results", "source_access_verified", "source_access_network"} {
		if networkSearch.RequiredToolStatsAtLeast[field] != 1 {
			t.Fatalf("live-web network search source access requirements = %#v, want %s=1", networkSearch.RequiredToolStatsAtLeast, field)
		}
	}
	if len(networkSearch.RequiredSourceAccess) != 1 ||
		networkSearch.RequiredSourceAccess[0] != (SourceAccessRequirement{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch"}) {
		t.Fatalf("live-web network search RequiredSourceAccess = %#v", networkSearch.RequiredSourceAccess)
	}
	for _, want := range []string{"BROWSER NETWORK EVIDENCE", "EVIDENCE_STATUS: refs_only_not_citable", "read_required=true", "query:", "Next:", "browser_network_read"} {
		if !stringSliceContains(networkSearch.RequiredToolResultText["browser_network"], want) {
			t.Fatalf("live-web network search browser_network result requirements = %#v, want %q", networkSearch.RequiredToolResultText["browser_network"], want)
		}
	}
	for _, want := range []string{"SourceAccess:", "browser_network_url=", "requested_url=", "ref=", "status=", "content_type=", "source_method=network_xhr_fetch"} {
		if !stringSliceContains(networkSearch.RequiredToolResultText["browser_network_read"], want) {
			t.Fatalf("live-web network search browser_network_read result requirements = %#v, want %q", networkSearch.RequiredToolResultText["browser_network_read"], want)
		}
	}
	for _, want := range []string{"browser_network", "market cap", "browser_network_url", "requested_url", "ref=", "status=", "content_type=", "source_method", "未验证"} {
		if !stringSliceContains(networkSearch.RequiredFinalText, want) {
			t.Fatalf("live-web network search RequiredFinalText = %#v, want %q", networkSearch.RequiredFinalText, want)
		}
	}
}

func TestBuiltinGitCommitPushScenariosRequireCommandOrder(t *testing.T) {
	for _, scenario := range BuiltinBatchScenarios() {
		if !scenarioRequiresGitCommitAndPush(scenario) {
			continue
		}
		if !commandOrderContains(scenario.RequiredCommandOrder, CommandOrderRequirement{Earlier: `git commit`, Later: `git push`}) {
			t.Fatalf("%s requires git commit and git push but lacks RequiredCommandOrder git commit -> git push; order=%#v", scenario.Name, scenario.RequiredCommandOrder)
		}
	}
}

func TestBuiltinCleanGitStatusScenariosRequireStatusEvidence(t *testing.T) {
	for _, scenario := range BuiltinBatchScenarios() {
		if !scenarioRequiresGitCommitAndPush(scenario) || !scenarioRequiresCleanGitStatus(scenario) {
			continue
		}
		if !scenarioHasCommandRequirement(scenario, `git status`) {
			t.Fatalf("%s requires a clean git status but lacks a git status command requirement; commands=%#v counts=%#v", scenario.Name, scenario.RequiredCommands, scenario.RequiredCommandCounts)
		}
		if !scenarioHasGitStatusAfterMutation(scenario) {
			t.Fatalf("%s requires a clean git status but lacks git status after mutation; after=%#v", scenario.Name, scenario.RequiredCommandAfterTool)
		}
		if !stringSliceContains(scenario.RequiredFinalText, "status") && !stringSliceContains(scenario.RequiredFinalText, "clean") {
			t.Fatalf("%s requires a clean git status but final text does not require status/clean evidence: %#v", scenario.Name, scenario.RequiredFinalText)
		}
	}
}

func scenarioRequiresGitCommitAndPush(scenario BatchScenario) bool {
	return scenarioHasCommandRequirement(scenario, `git commit`) && scenarioHasCommandRequirement(scenario, `git push`)
}

func scenarioHasCommandRequirement(scenario BatchScenario, command string) bool {
	if stringSliceContains(scenario.RequiredCommands, command) {
		return true
	}
	_, ok := scenario.RequiredCommandCounts[command]
	return ok
}

func scenarioRequiresCleanGitStatus(scenario BatchScenario) bool {
	text := strings.ToLower(scenario.Prompt + "\n" + strings.Join(scenario.Prompts, "\n") + "\n" + scenario.VerifyCommand)
	return strings.Contains(text, "git status clean") ||
		strings.Contains(text, "clean git status") ||
		strings.Contains(text, "git status --porcelain")
}

func scenarioHasGitStatusAfterMutation(scenario BatchScenario) bool {
	for _, tool := range []string{"edit_file", "write_file"} {
		if commandToolOrderContains(scenario.RequiredCommandAfterTool, CommandToolOrderRequirement{Command: `git status`, Tool: tool}) {
			return true
		}
	}
	return false
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
		if scenario.RequiredSubagentSourceCounts["explore"] != 2 {
			t.Fatalf("subagent-project-facts RequiredSubagentSourceCounts = %#v, want explore=2", scenario.RequiredSubagentSourceCounts)
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

func commandOrderContains(values []CommandOrderRequirement, want CommandOrderRequirement) bool {
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

func TestRequiredFileSubstrings(t *testing.T) {
	dir := t.TempDir()
	if err := writeScenarioFiles(dir, map[string]string{"trace.jsonl": "alpha\nFailure: kind=resume_missing_tool_result\n"}); err != nil {
		t.Fatal(err)
	}
	if err := verifyRequiredFileSubstrings(dir, map[string][]string{"trace.jsonl": {"alpha", "resume_missing_tool_result"}}); err != nil {
		t.Fatalf("required substrings should pass: %v", err)
	}
	if err := verifyRequiredFileSubstrings(dir, map[string][]string{"trace.jsonl": {"missing marker"}}); err == nil || !strings.Contains(err.Error(), "required content") {
		t.Fatalf("expected missing required content error, got %v", err)
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
		Verifier: VerifierResult{
			Command:            "go test ./... && git diff --name-only -- queue/queue.go",
			Ran:                true,
			OK:                 false,
			ExitCode:           1,
			Duration:           1500 * time.Millisecond,
			OutputBytes:        2048,
			OutputTruncated:    true,
			OutputOmittedBytes: 512,
			OutputCapBytes:     1536,
		},
		ToolCalls: 8,
		Repair:    ToolRepairStats{Calls: 1, SucceededCalls: 1, Notes: 2, ByKind: map[string]int{"tool_name": 1, "alias_rename": 1}},
		Delegation: DelegationStats{
			FocusedTaskCalls:                1,
			FocusedTaskByType:               map[string]int{"research": 1},
			FocusedTaskSourceFindingsByType: map[string]int{"research": 2},
			SubagentCalls:                   1,
			SubagentByMode:                  map[string]int{"review": 1},
			SubagentSourceEvidenceByMode:    map[string]int{"review": 1},
			SubagentErrors:                  1,
			SubagentIncomplete:              1,
		},
		Plan: PlanStats{
			Calls:    2,
			ByAction: map[string]int{"set": 1, "update": 1},
			Errors:   1,
		},
		ToolFailureExamples: map[string][]ToolFailureExample{
			"dynamic_shell": {{
				Kind:              "dynamic_shell",
				Tool:              "web_fetch",
				ArgsSummary:       `url="https://example.test/report"`,
				ResultSummary:     "dynamic dashboard exposed empty metric widgets; next use browser_network_read",
				SuggestedNextStep: "switch to browser_network_read before citing dynamic dashboard metrics",
				ExitCode:          1,
			}},
		},
		RuntimeErrorByKind: map[string]int{"llm_timeout": 1},
		RuntimeErrorExamples: map[string][]RuntimeErrorExample{
			"llm_timeout": {{
				Kind:    "llm_timeout",
				Message: "llm stream timed out after first token",
			}},
		},
		ConversationRepairs: []sse.ConversationRepairedPayload{{
			SessionID:          "debug-session",
			MissingToolResults: 1,
			FailureKind:        "resume_missing_tool_result",
			Next:               "do not assume the tool succeeded",
		}},
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
			MemorySearchCalls:          2,
			MemorySearchMisses:         1,
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
		LoopProtocolCalibrationRequests: LoopProtocolCalibrationStats{
			Count: 1,
			Latest: LoopProtocolCalibration{
				LoopID:                  "debug",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				ProtocolPath:            ".affent/loops/debug/LOOP.md",
				EventSeq:                2,
			},
			Examples: []LoopProtocolCalibration{{
				LoopID:                  "debug",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				ProtocolPath:            ".affent/loops/debug/LOOP.md",
				EventSeq:                2,
			}},
		},
		LoopProtocolCalibrations: LoopProtocolCalibrationStats{
			Count: 1,
			Latest: LoopProtocolCalibration{
				LoopID:                  "debug",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				CalibrationAnswers:      1,
				LastCalibrationAnswer:   "Stop when browser evidence is weak.",
				ProtocolPath:            ".affent/loops/debug/LOOP.md",
				EventSeq:                3,
			},
			Examples: []LoopProtocolCalibration{{
				LoopID:                  "debug",
				Status:                  "draft",
				CalibrationQuestions:    1,
				LastCalibrationQuestion: "What should pause this loop?",
				CalibrationAnswers:      1,
				LastCalibrationAnswer:   "Stop when browser evidence is weak.",
				ProtocolPath:            ".affent/loops/debug/LOOP.md",
				EventSeq:                3,
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
			"conversation.repaired": 1,
			"message.delta":         2,
			"runtime.surface":       1,
			"tool.request":          1,
			"tool.result":           1,
		},
		ConversationRepairs: append([]sse.ConversationRepairedPayload(nil), res.ConversationRepairs...),
		RuntimeSurfaces:     []sse.RuntimeSurfacePayload{*res.RuntimeSurface},
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
			Result:   `{"query":"Alpha Coast","total":2,"results":[{"session_id":"market-alpha","turn_idx":4,"message_idx":8,"role":"assistant","snippet":"history marker ALPHA-COAST risk label elevated","score":2.5,"matched_terms":["alpha","coast"],"context_included":true,"mod_time":"2026-05-27T12:00:00Z"},{"session_id":"market-beta","turn_idx":2,"message_idx":3,"role":"user","snippet":"older Alpha note without the current risk label","score":1,"matched_terms":["alpha"],"context_included":false,"mod_time":"2026-05-25T12:00:00Z"}]}`,
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
				"EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n" +
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
		Prompt:        "research with evidence",
		Suites:        []string{longRunSuite, liveWebSuite},
		Domains:       []string{"market", "web_evidence"},
		MaxTurns:      12,
		SetupCommands: []string{"git init"},
		SourceRepoURL: "remote.git",
		SourceRepoRef: "main",
		SourceRepoDir: "app",
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
		RequiredTraceEventCounts: map[string]int{
			"conversation.repaired": 1,
		},
		RequiredContextInjectionSources: map[string]int{
			"final_evidence_digest": 1,
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
		RequiredLoopProtocolFeeds:               1,
		RequiredLoopProtocolCalibrationRequests: 1,
		RequiredLoopProtocolCalibrations:        1,
		RequiredLoopProtocolFeedModes: map[string]int{
			"digest": 1,
		},
		RequiredLoopProtocolFeedMatches: []LoopProtocolFeedRequirement{
			{Mode: "digest", PlanLabelContains: "debug", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "browser network evidence", CurrentSituation: "dynamic source recovery", LastTurnEndReason: "completed", MinLastTurnMemorySearchCalls: 1},
		},
		RequireLoopProtocolFullAfterCompact: true,
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
		RequiredCommandOrder: []CommandOrderRequirement{
			{Earlier: "git commit", Later: "git push"},
		},
		RequiredFocusedTaskCounts: map[string]int{
			"research": 1,
		},
		RequiredFocusedTaskSourceCounts: map[string]int{
			"research": 2,
		},
		RequiredSubagentModeCounts: map[string]int{
			"review": 1,
		},
		RequiredSubagentSourceCounts: map[string]int{
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
		RequiredRecentSessionSearch: []RecentSessionSearchRequirement{
			{QueryContains: "missing marker", SessionID: "market-alpha", PlanContains: "browser network evidence", LoopContains: "loop.protocol_feed", RecoveryContains: "max_turns"},
		},
		RequiredFinalText:             []string{"0.06342 T"},
		ForbiddenFinalText:            []string{"subnet price $277.32"},
		RequiredTruncatedResults:      []string{"web_fetch"},
		RequiredResultArtifacts:       []string{"web_fetch"},
		RequiredContextCompactions:    1,
		RequiredCompactionRemovedMsgs: 12,
		RequiredContextSummaryText:    []string{"browser network evidence"},
		RequiredContextLoopProtocolAnchorText: []string{
			"path=.affent/loops/debug/LOOP.md",
		},
		ProtectedFiles: []string{"README.md"},
		RequiredFileSubstrings: map[string][]string{
			"trace.jsonl": {"resume_missing_tool_result"},
		},
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
	if manifest.Verifier == nil ||
		manifest.Verifier.Command != "go test ./... && git diff --name-only -- queue/queue.go" ||
		!manifest.Verifier.Ran ||
		manifest.Verifier.OK ||
		manifest.Verifier.ExitCode != 1 ||
		manifest.Verifier.DurationMS != 1500 ||
		manifest.Verifier.OutputBytes != 2048 ||
		!manifest.Verifier.OutputTruncated ||
		manifest.Verifier.OutputOmittedBytes != 512 ||
		manifest.Verifier.OutputCapBytes != 1536 {
		t.Fatalf("manifest verifier = %+v", manifest.Verifier)
	}
	wantCapabilities := []string{"browser", "context_compaction", "context_injection", "delegated_source_evidence", "delegation", "loop_protocol", "memory", "plan", "session_search", "source_access", "source_repo", "trace", "web", "workspace"}
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
		!stringSliceContains(manifest.Expectations.CheckNames, "context_compaction_loop_protocol_anchor_contains:path=.affent/loops/debug/LOOP.md") ||
		!reflect.DeepEqual(manifest.Expectations.Suites, []string{longRunSuite, liveWebSuite}) ||
		!reflect.DeepEqual(manifest.Expectations.SetupCommands, []string{"git init"}) ||
		manifest.Expectations.SourceRepoURL != "remote.git" ||
		manifest.Expectations.SourceRepoRef != "main" ||
		manifest.Expectations.SourceRepoDir != "app" ||
		!reflect.DeepEqual(manifest.Expectations.RequiredTools, []string{"web_fetch", "browser_network_read"}) ||
		!reflect.DeepEqual(manifest.Expectations.ForbiddenTools, []string{"shell"}) ||
		manifest.Expectations.RequiredToolCounts["browser_network_read"] != 1 ||
		manifest.Expectations.RequiredToolFailureKindCounts["dynamic_shell"] != 1 ||
		manifest.Expectations.RequiredToolStatsAtLeast["memory_updates"] != 2 ||
		manifest.Expectations.RequiredToolStatsAtLeast["source_access_dynamic_partial"] != 1 ||
		manifest.Expectations.RequiredToolStatsAtLeast["source_access_network"] != 1 ||
		manifest.Expectations.RequiredTraceEventCounts["conversation.repaired"] != 1 ||
		manifest.Expectations.RequiredContextInjectionSources["final_evidence_digest"] != 1 ||
		manifest.Expectations.RequiredLoopDecisionKinds["evidence_quality"] != 1 ||
		manifest.Expectations.RequiredLoopDecisionResults["defer"] != 1 ||
		len(manifest.Expectations.RequiredLoopDecisionMatches) != 1 ||
		manifest.Expectations.RequiredLoopDecisionMatches[0] != (DebugLoopDecisionRequirement{Kind: "evidence_quality", Decision: "defer", Trigger: "source_access_dynamic_partial"}) ||
		manifest.Expectations.RequiredLoopProtocolFeeds != 1 ||
		manifest.Expectations.RequiredLoopProtocolCalibrationRequests != 1 ||
		manifest.Expectations.RequiredLoopProtocolCalibrations != 1 ||
		manifest.Expectations.RequiredLoopProtocolFeedModes["digest"] != 1 ||
		len(manifest.Expectations.RequiredLoopProtocolFeedMatches) != 1 ||
		manifest.Expectations.RequiredLoopProtocolFeedMatches[0] != (DebugLoopProtocolFeedRequirement{Mode: "digest", PlanLabelContains: "debug", PlanCurrentStepStatus: "in_progress", PlanCurrentStep: "browser network evidence", CurrentSituation: "dynamic source recovery", LastTurnEndReason: "completed", MinLastTurnMemorySearchCalls: 1}) ||
		!manifest.Expectations.RequireLoopProtocolFullAfterCompact ||
		!reflect.DeepEqual(manifest.Expectations.Domains, []string{"market", "web_evidence"}) ||
		!reflect.DeepEqual(manifest.Expectations.RequiredToolResultText["browser_network_read"], []string{"SourceAccess:", "requested_url=", "source_method=network_xhr_fetch"}) ||
		len(manifest.Expectations.RequiredToolOrder) != 1 ||
		manifest.Expectations.RequiredToolOrder[0] != (DebugToolOrderRequirement{Earlier: "web_fetch", Later: "browser_network_read"}) ||
		len(manifest.Expectations.RequiredCommandBeforeTool) != 1 ||
		manifest.Expectations.RequiredCommandBeforeTool[0] != (DebugCommandToolOrderRequirement{Command: "go test", Tool: "memory"}) ||
		len(manifest.Expectations.RequiredCommandAfterTool) != 1 ||
		manifest.Expectations.RequiredCommandAfterTool[0] != (DebugCommandToolOrderRequirement{Command: "go test", Tool: "edit_file"}) ||
		len(manifest.Expectations.RequiredCommandOrder) != 1 ||
		manifest.Expectations.RequiredCommandOrder[0] != (DebugCommandOrderRequirement{Earlier: "git commit", Later: "git push"}) ||
		manifest.Expectations.RequiredFocusedTaskCounts["research"] != 1 ||
		manifest.Expectations.RequiredFocusedTaskSourceCounts["research"] != 2 ||
		manifest.Expectations.RequiredSubagentModeCounts["review"] != 1 ||
		manifest.Expectations.RequiredSubagentSourceCounts["review"] != 1 ||
		!manifest.Expectations.RequireNoDelegationErrors ||
		!manifest.Expectations.RequireNoPlanErrors ||
		len(manifest.Expectations.RequiredToolArgContains) != 1 ||
		manifest.Expectations.RequiredToolArgContains[0] != (DebugToolArgContainsRequirement{Tool: "browser_network_read", Arg: "json_path", Substring: "$.price"}) ||
		len(manifest.Expectations.RequiredSourceAccess) != 1 ||
		manifest.Expectations.RequiredSourceAccess[0] != (DebugSourceAccessRequirement{Status: "network", Tool: "browser_network_read", URLContains: "taostats.io/api", RequestedURLContains: "taostats.io/subnets/120", SourceMethod: "network_xhr_fetch", JSONPath: "$.price"}) ||
		len(manifest.Expectations.RequiredSessionSearch) != 1 ||
		!reflect.DeepEqual(manifest.Expectations.RequiredSessionSearch[0], DebugSessionSearchRequirement{QueryContains: "Alpha Coast", SessionID: "market-alpha", SnippetContains: "history marker", MatchedTerms: []string{"alpha", "coast"}, ContextIncluded: true, TurnIdx: 4}) ||
		len(manifest.Expectations.RequiredRecentSessionSearch) != 1 ||
		!reflect.DeepEqual(manifest.Expectations.RequiredRecentSessionSearch[0], DebugRecentSessionSearchRequirement{QueryContains: "missing marker", SessionID: "market-alpha", PlanContains: "browser network evidence", LoopContains: "loop.protocol_feed", RecoveryContains: "max_turns"}) ||
		!stringSliceContains(manifest.Expectations.RequiredFinalText, "0.06342 T") ||
		!stringSliceContains(manifest.Expectations.ForbiddenFinalText, "subnet price $277.32") ||
		!reflect.DeepEqual(manifest.Expectations.RequiredTruncatedResults, []string{"web_fetch"}) ||
		!reflect.DeepEqual(manifest.Expectations.RequiredResultArtifacts, []string{"web_fetch"}) ||
		manifest.Expectations.RequiredContextCompactions != 1 ||
		manifest.Expectations.RequiredCompactionRemovedMsgs != 12 ||
		!stringSliceContains(manifest.Expectations.RequiredContextSummaryText, "browser network evidence") ||
		!stringSliceContains(manifest.Expectations.RequiredContextLoopProtocolAnchorText, "path=.affent/loops/debug/LOOP.md") ||
		!reflect.DeepEqual(manifest.Expectations.ProtectedFiles, []string{"README.md"}) ||
		!reflect.DeepEqual(manifest.Expectations.RequiredFileSubstrings["trace.jsonl"], []string{"resume_missing_tool_result"}) ||
		!reflect.DeepEqual(manifest.Expectations.ForbiddenFileSubstrings["notes.md"], []string{"uncited taostats metric"}) {
		t.Fatalf("manifest expectations = %+v", manifest.Expectations)
	}
	timelineRaw, err := os.ReadFile(res.TimelinePath)
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	if !strings.Contains(string(timelineRaw), "- source_repo: `url=remote.git ref=main dir=app`") {
		t.Fatalf("timeline missing source repo expectation:\n%s", string(timelineRaw))
	}
	if manifest.DebugBrief == nil || len(manifest.DebugBrief.Tags) == 0 {
		t.Fatalf("manifest debug brief missing: %+v", manifest.DebugBrief)
	}
	if len(manifest.ConversationRepairExamples) != 1 ||
		manifest.ConversationRepairExamples[0].SessionID != "debug-session" ||
		manifest.ConversationRepairExamples[0].MissingToolResults != 1 ||
		manifest.ConversationRepairExamples[0].FailureKind != "resume_missing_tool_result" {
		t.Fatalf("manifest conversation repairs = %+v", manifest.ConversationRepairExamples)
	}
	if manifest.RecoveryGuide == nil ||
		!strings.Contains(manifest.RecoveryGuide.Summary, "scenario failed") ||
		!reflect.DeepEqual(manifest.RecoveryGuide.ExactRerunCommand, res.AffentctlCommand) ||
		!stringSliceContains(manifest.RecoveryGuide.Inspect, res.TimelinePath) ||
		!stringSliceContains(manifest.RecoveryGuide.Inspect, res.DebugManifestPath) ||
		!stringSliceContains(manifest.RecoveryGuide.Inspect, tracePath) ||
		!stringSliceContains(manifest.RecoveryGuide.Inspect, filepath.Join(workspace, ".affent", "artifacts")) ||
		!stringSliceContains(manifest.RecoveryGuide.Inspect, filepath.Join(workspace, ".affentctl")) ||
		!strings.Contains(manifest.RecoveryGuide.ContinuePrompt, "structured failures") {
		t.Fatalf("manifest recovery guide = %+v", manifest.RecoveryGuide)
	}
	if !stringSliceContains(manifest.DebugBrief.Tags, "tool_failure:dynamic_shell") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "runtime_error:llm_timeout") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "conversation_repair:resume_missing_tool_result") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "source_dynamic_partial") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "recall:context") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "memory_update:replace") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "context_compaction:reactive") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "browser_network:refs") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "verifier:failed") ||
		!stringSliceContains(manifest.DebugBrief.Tags, "verifier:output_truncated") ||
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
	if manifest.Metrics.LoopProtocolCalibrationRequests != 1 ||
		len(manifest.LoopProtocolCalibrationRequestExamples) != 1 ||
		manifest.LoopProtocolCalibrationRequestExamples[0].LoopID != "debug" ||
		manifest.LoopProtocolCalibrationRequestExamples[0].EventSeq != 2 ||
		!strings.Contains(manifest.LoopProtocolCalibrationRequestExamples[0].LastCalibrationQuestion, "pause this loop") ||
		manifest.Metrics.LoopProtocolCalibrations != 1 ||
		len(manifest.LoopProtocolCalibrationExamples) != 1 ||
		manifest.LoopProtocolCalibrationExamples[0].LoopID != "debug" ||
		manifest.LoopProtocolCalibrationExamples[0].EventSeq != 3 ||
		!strings.Contains(manifest.LoopProtocolCalibrationExamples[0].LastCalibrationAnswer, "browser evidence") {
		t.Fatalf("manifest loop protocol calibration = metrics:%+v request_examples:%+v answer_examples:%+v", manifest.Metrics, manifest.LoopProtocolCalibrationRequestExamples, manifest.LoopProtocolCalibrationExamples)
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
		manifest.BrowserNetworkExamples[0].EvidenceStatus != "refs_only_not_citable; read_required=true" ||
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
		manifest.SessionSearchExamples[0].ModTime != "2026-05-27T12:00:00Z" ||
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
		manifest.Metrics.MemorySearchCalls != 2 ||
		manifest.Metrics.MemorySearchMisses != 1 ||
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
		manifest.Metrics.FocusedTaskCalls != 1 ||
		manifest.Metrics.FocusedTaskByType["research"] != 1 ||
		manifest.Metrics.FocusedTaskSources["research"] != 2 ||
		manifest.Metrics.SubagentCalls != 1 ||
		manifest.Metrics.SubagentByMode["review"] != 1 ||
		manifest.Metrics.SubagentSources["review"] != 1 ||
		manifest.Metrics.SubagentErrors != 1 ||
		manifest.Metrics.SubagentIncomplete != 1 ||
		manifest.Metrics.PlanCalls != 2 ||
		manifest.Metrics.PlanByAction["set"] != 1 ||
		manifest.Metrics.PlanByAction["update"] != 1 ||
		manifest.Metrics.PlanErrors != 1 ||
		manifest.Metrics.InputTokens != 100 ||
		manifest.Metrics.OutputTokens != 20 ||
		manifest.Metrics.TraceEvents != 6 ||
		manifest.Metrics.TraceEventTypes["conversation.repaired"] != 1 ||
		manifest.Metrics.TraceEventTypes["message.delta"] != 2 ||
		manifest.Metrics.TraceEventTypes["tool.result"] != 1 ||
		manifest.Metrics.LoopProtocolCalibrationRequests != 1 ||
		manifest.Metrics.LoopProtocolCalibrations != 1 {
		t.Fatalf("manifest metrics = %+v", manifest.Metrics)
	}
	timeline, err := os.ReadFile(res.TimelinePath)
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	for _, want := range []string{
		"# Affent Eval Timeline",
		"metrics: tools=8 tool_errors=1 repaired=0 canonicalized=0 loop_guard=1 forced_no_tools=0 evidence=1/2_verified,network=1,partial=1,discovery=1 memory_updates=2(add:1,replace:1,remove:0) memory_search=calls:2,misses:1 session_search=calls:1,results:2,context:1,terms:2,terms_per_call:2.00 tool_context_trunc=2,omitted=8192 compactions=1,reactive=1,removed=12,summary_bytes=512,summary_missing=0,summary_empty=0 loop_calibrations=1 loop_calibration_requests=1 tokens=100/20",
		"## Runtime Surface",
		"`web_fetch`",
		"## Tool Repair",
		"tool#1 `web_fetch` original=`webFetch` call_id=`call-1` canonicalized=`true` args_repaired=`true` exit=`1` kinds=`tool_name,alias_rename`",
		"note: renamed field URL to url",
		"trace_deltas: `true`",
		"affentctl_command",
		"--api-key '<redacted>'",
		"## Debug Brief",
		"## Recovery Guide",
		"## Verifier",
		"status: `failed`",
		"command: `go test ./... && git diff --name-only -- queue/queue.go`",
		"exit_code: `1`",
		"duration_ms: `1500`",
		"output_bytes: `2048`",
		"output_truncated: `true`",
		"output_omitted_bytes: `512`",
		"output_cap_bytes: `1536`",
		"summary: scenario failed; inspect the ordered artifacts below before trusting final text or rerunning",
		"inspect_order:",
		"affenteval-debug.json",
		"exact_rerun_command:",
		"continue_prompt: Investigate this Affent eval failure using the retained debug artifacts before changing code.",
		"outcome: `failed`; inspect the failure list",
		"tool_failure_by_kind: `dynamic_shell=1`",
		"tool_failure_example[dynamic_shell]: tool=`web_fetch` exit=`1` args=url=\"https://example.test/report\"",
		"next=switch to browser_network_read before citing dynamic dashboard metrics",
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
		"expectation_capabilities: `browser`, `context_compaction`, `context_injection`, `delegated_source_evidence`, `delegation`, `loop_protocol`, `memory`, `plan`, `session_search`, `source_access`, `source_repo`, `trace`, `web`, `workspace` outcome=`failed`",
		"suites: `long-run`, `live-web`",
		"domains: `market`, `web_evidence`",
		"runtime: `max_turns=12 compact_trigger=6 compact_keep_last=3`",
		"source_repo: `url=remote.git ref=main dir=app`",
		"checks: `turn_ended_cleanly`",
		"required_tools: `web_fetch`, `browser_network_read`",
		"forbidden_tools: `shell`",
		"required_tool_counts: `browser_network_read=1`",
		"required_tool_order: `web_fetch -> browser_network_read`",
		"required_command_order: `git commit -> git push`",
		"required_command_before_tool: `go test -> memory`",
		"required_command_after_tool: `go test -> edit_file`",
		"required_tool_failure_kind_counts: `dynamic_shell=1`",
		"required_tool_stats_at_least: `memory_updates=2,source_access_dynamic_partial=1,source_access_network=1`",
		"required_trace_event_counts: `conversation.repaired=1`",
		"required_context_injection_sources: `final_evidence_digest=1`",
		"required_loop_decision_kinds: `evidence_quality=1`",
		"required_loop_decision_results: `defer=1`",
		"required_loop_protocol_feeds: `1`",
		"required_loop_protocol_calibration_requests: `1`",
		"required_loop_protocol_calibrations: `1`",
		"required_loop_protocol_feed_modes: `digest=1`",
		"required_loop_protocol_full_after_compaction: `true`",
		"required_focused_task_counts: `research=1`",
		"required_subagent_mode_counts: `review=1`",
		"required_no_errors: `delegation plan`",
		"required_loop_decision: `kind=evidence_quality decision=defer trigger=source_access_dynamic_partial min=1`",
		"required_loop_protocol_feed: `mode=digest plan_label_contains=debug plan_current_step_status=in_progress plan_current_step=browser network evidence current_situation=dynamic source recovery last_turn_end_reason=completed last_turn_memory_search_calls>=1 min=1`",
		"required_tool_result_text[browser_network_read]: `SourceAccess:`, `requested_url=`, `source_method=network_xhr_fetch`",
		"required_source_access: `status=network tool=browser_network_read url_contains=taostats.io/api requested_url_contains=taostats.io/subnets/120 source_method=network_xhr_fetch json_path=$.price min=1`",
		"required_session_search: `query_contains=Alpha Coast session=market-alpha snippet_contains=history marker terms=alpha,coast context=true turn=4 min=1`",
		"required_recent_session_search: `query_contains=missing marker recent_session=market-alpha plan_contains=browser network evidence loop_contains=loop.protocol_feed recovery_contains=max_turns min=1`",
		"required_final_text: `0.06342 T`",
		"forbidden_final_text: `subnet price $277.32`",
		"required_truncated_results: `web_fetch`",
		"required_result_artifacts: `web_fetch`",
		"required_tool_arg: `browser_network_read.json_path` contains `$.price` min=`1`",
		"context_requirements: `compactions>=1 removed_messages>=12`",
		"context_summary_contains: `browser network evidence`",
		"context_loop_protocol_anchor_contains: `path=.affent/loops/debug/LOOP.md`",
		"protected_files: `README.md`",
		"required_file_substrings[trace.jsonl]: `resume_missing_tool_result`",
		"forbidden_file_substrings[notes.md]: `uncited taostats metric`",
		"evidence: `1/2` verified, network=`1`, partial=`1`, discovery=`1`",
		"recall_weak_context: calls=`1`, results=`2`, context=`1`, terms=`2`; only some hits included adjacent context or persisted task-state anchors; inspect Session Search examples for incomplete recovery.",
		"context: compactions=`1`, reactive=`1`, removed_messages=`12`, summary_bytes=`512`",
		"truncation: tool_context=2 omitted_context=8192 args=1 args_omitted=128 results=1 results_omitted=4096 artifacts=1 context_artifacts=0 missing_artifacts=0",
		"## Trace Events",
		"`conversation.repaired`: `1`",
		"`message.delta`: `2`",
		"## Conversation Repairs",
		"repair#1 session=`debug-session` missing_tool_results=`1` failure_kind=`resume_missing_tool_result` next=do not assume the tool succeeded",
		"## Source Evidence",
		"tool#1 `web_fetch` status=`dynamic_partial` url=`https://taostats.io/subnets/120`",
		"preview: PAGE DIAGNOSTICS: - empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value PAGE TEXT: Affine SN120",
		"tool#2 `browser_network_read` status=`network` url=`https://taostats.io/api/subnets/120` requested=`https://taostats.io/subnets/120` ref=`n1` source_method=`network_xhr_fetch` http_status=`200` content_type=`application/json` json_path=`$.price`",
		"preview: JSON_PATH: $.price \"0.06342 T\"",
		"tool#3 `browser_navigate` status=`discovery_only` url=`https://search.example/?q=affine`",
		"## Browser Network Searches",
		"tool#8 status=`matches` query=`market_cap` page=`https://taostats.io/subnets/120` call_id=`call-8` evidence_status=`refs_only_not_citable; read_required=true` requires_read=`true` citable=`false`",
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
		"tool#6 query=`Alpha Coast` total=`2` session=`market-alpha` turn=`4` message=`8` role=`assistant` mod_time=`2026-05-27T12:00:00Z` terms=`alpha,coast` context=`true` call_id=`call-6`",
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

func TestBuildDebugRecoveryGuideAddsFullTraceRerunCommand(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/debug",
		TimelinePath:      "/tmp/affent-eval/debug/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/debug/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/debug/trace.jsonl",
		Failures:          []string{"missing browser network evidence"},
		AffentctlCommand:  []string{"go", "run", "./cmd/affentctl", "run", "--trace-skip-deltas", "--prompt", "<prompt>"},
		TraceDeltas:       false,
		ToolStats: ToolRuntimeStats{
			SourceAccessResults:        2,
			SourceAccessDynamicPartial: 1,
		},
		SourceAccessExamples: []SourceAccessExample{{
			Tool:         "browser_network_read",
			Status:       "network",
			URLField:     "browser_network_url",
			SourceMethod: "network_xhr_fetch",
			Ref:          "n1",
			HTTPStatus:   "200",
			ContentType:  "application/json",
			BodyBytes:    120000,
			ShowingBytes: 65536,
			OmittedAfter: 54464,
			NextOffset:   65536,
			HasMore:      true,
		}},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	if strings.Join(guide.FullTraceRerunCommand, "\x00") != "go\x00run\x00./cmd/affentctl\x00run\x00--prompt\x00<prompt>" {
		t.Fatalf("full trace rerun command = %#v", guide.FullTraceRerunCommand)
	}
	for _, want := range []string{
		"First failure: missing browser network evidence.",
		"explicit expectation failed",
		"Priority debug tags:",
		"outcome:failed",
		"source_dynamic_without_network",
		"source_network:partial_read",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
}

func TestBuildDebugRecoveryGuideIncludesContextArtifactDir(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/context-artifact",
		TimelinePath:      "/tmp/affent-eval/context-artifact/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/context-artifact/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/context-artifact/trace.jsonl",
		Failures:          []string{"context-truncated tool result needs artifact inspection"},
		ToolTruncation: ToolTruncationStats{
			ContextTruncated: 1,
			ContextArtifacts: 1,
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	artifactDir := filepath.Join(res.Workspace, ".affent", "artifacts")
	if !stringSliceContains(guide.Inspect, artifactDir) {
		t.Fatalf("recovery guide inspect = %#v, want context artifact dir %q", guide.Inspect, artifactDir)
	}
}

func TestBuildDebugRecoveryGuideAddsFailedToolRepairAction(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/tool-repair",
		TimelinePath:      "/tmp/affent-eval/tool-repair/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/tool-repair/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/tool-repair/trace.jsonl",
		Failures:          []string{"tool call could not be repaired"},
		Repair: ToolRepairStats{
			Calls:          2,
			SucceededCalls: 1,
			FailedCalls:    1,
			Notes:          1,
			ByKind:         map[string]int{"malformed_json": 1},
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"tool_repair:failed",
		"inspect tool_repair_examples",
		"tool aliasing, argument repair, or model guidance",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	if !stringSliceContains(guide.Inspect, "tool_repair_examples") ||
		!stringSliceContains(guide.Inspect, "tool_timeline") {
		t.Fatalf("recovery guide inspect = %#v, want repair examples and timeline", guide.Inspect)
	}
}

func TestBuildDebugRecoveryGuideAddsUnreadBrowserNetworkAction(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/browser-network",
		TimelinePath:      "/tmp/affent-eval/browser-network/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/browser-network/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/browser-network/trace.jsonl",
		Failures:          []string{"browser network refs were discovered but not read"},
		BrowserNetworkExamples: []BrowserNetworkSearchExample{{
			ToolIndex:      4,
			CallID:         "call-4",
			CurrentPageURL: "https://taostats.io/subnets/120",
			Query:          "market cap",
			Status:         "matches",
			Refs:           []string{"n7"},
			RequiresRead:   true,
			NotCitable:     true,
		}},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"browser_network:unread_refs",
		"inspect browser_network_examples and source_evidence",
		"call browser_network_read on the listed ref",
		"browser_network refs/checks are not citable SourceAccess evidence",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	if !stringSliceContains(guide.Inspect, "browser_network_examples") ||
		!stringSliceContains(guide.Inspect, "source_evidence") ||
		!stringSliceContains(guide.Inspect, "tool_timeline") {
		t.Fatalf("recovery guide inspect = %#v, want browser network, source evidence, and timeline", guide.Inspect)
	}
}

func TestBuildDebugRecoveryGuideAddsVerifierRecoveryActions(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/code-pr",
		TimelinePath:      "/tmp/affent-eval/code-pr/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/code-pr/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/code-pr/trace.jsonl",
		Failures:          []string{"verify command failed: go test ./..."},
		Verifier: VerifierResult{
			Command:            "go test ./...",
			Ran:                true,
			OK:                 false,
			ExitCode:           -1,
			OutputTruncated:    true,
			OutputOmittedBytes: 8192,
			OutputCapBytes:     2048,
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"verifier:failed",
		"verifier:abnormal",
		"verifier:output_truncated",
		"inspect the Verifier section, failures, and retained workspace diff",
		"rerun the exact verifier command in the workspace",
		"inspect verifier timeout/cancel symptoms",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	for _, want := range []string{"verifier", "failures", "timeline"} {
		if !stringSliceContains(guide.Inspect, want) {
			t.Fatalf("recovery guide inspect = %#v, want %q", guide.Inspect, want)
		}
	}
}

func TestBuildDebugRecoveryGuideAddsLoopProtocolFixtureAction(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/loop-draft",
		TimelinePath:      "/tmp/affent-eval/loop-draft/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/loop-draft/affenteval-debug.json",
		Failures: []string{
			`scenario "loop-draft" requires loop protocol feeds but active protocol file .affent/loops/loop-draft/LOOP.md has status "draft", want running`,
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"loop_protocol:fixture",
		"fix the per-session .affent/loops/<session_id>/LOOP.md fixture",
		"state.json lifecycle status",
		"scenario setup, not model behavior",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	for _, want := range []string{"failures", "expectations", "debug_manifest"} {
		if !stringSliceContains(guide.Inspect, want) {
			t.Fatalf("recovery guide inspect = %#v, want %q", guide.Inspect, want)
		}
	}
}

func TestBuildDebugRecoveryGuideAddsResearchCheckpointEvidenceGapAction(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/research-checkpoint",
		TimelinePath:      "/tmp/affent-eval/research-checkpoint/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/research-checkpoint/affenteval-debug.json",
		LoopDecisionStats: LoopDecisionStats{
			ByKind: map[string]int{"research_checkpoint": 1},
			Examples: []LoopDecision{{
				Kind:           "research_checkpoint",
				Decision:       "trigger",
				Trigger:        "external_calibration_requested",
				RequiredAction: "Use a narrow web/browser pass before changing durable direction.",
			}},
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"research_checkpoint:no_external_evidence",
		"inspect loop_decision_examples",
		"source_evidence or child_transcripts",
		"internal review rather than externally calibrated route changes",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	for _, want := range []string{"loop_decision_examples", "source_evidence", "child_transcripts"} {
		if !stringSliceContains(guide.Inspect, want) {
			t.Fatalf("recovery guide inspect = %#v, want %q", guide.Inspect, want)
		}
	}
}

func TestBuildDebugRecoveryGuideCleanPassWithoutBrief(t *testing.T) {
	guide := BuildDebugRecoveryGuide(BatchResult{
		OK:          true,
		Workspace:   "/tmp/affent-eval/pass",
		RunExitCode: 0,
	})
	if guide != nil {
		t.Fatalf("clean pass should not emit recovery guide: %+v", guide)
	}
}

func TestBuildDebugRecoveryGuidePrioritizesManualLongRunToolFailures(t *testing.T) {
	res := BatchResult{
		OK:                true,
		Workspace:         "/tmp/affent-eval/manual-longrun",
		TimelinePath:      "/tmp/affent-eval/manual-longrun/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/manual-longrun/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/manual-longrun/events.jsonl",
		ToolStats: ToolRuntimeStats{
			ToolFailureByKind: map[string]int{
				"blocked":              1,
				"invalid_args":         1,
				"loop_guard_call_cap":  1,
				"loop_guard_no_budget": 3,
			},
			LoopGuardInterventions: 1,
		},
		Plan: PlanStats{
			Calls:  14,
			Errors: 5,
			ByAction: map[string]int{
				"set":     4,
				"update":  5,
				"unknown": 3,
			},
		},
		Delegation: DelegationStats{
			SubagentCalls:  1,
			SubagentErrors: 1,
			SubagentByMode: map[string]int{"research": 1},
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"tool_failure:loop_guard_call_cap",
		"tool_failure:loop_guard_no_budget",
		"tool_failure:invalid_args",
		"tool_failure:blocked",
		"plan/tool-call-cap failures",
		"inspect tool_failure_examples and the exact tool schema",
		"requested-but-unrun tools",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	for _, want := range []string{
		"tool_failure_examples",
		"loop_guard_examples",
		"plan_calls",
		"child_transcripts",
	} {
		if !stringSliceContains(guide.Inspect, want) {
			t.Fatalf("recovery guide inspect = %#v, want %q", guide.Inspect, want)
		}
	}
}

func TestBuildDebugRecoveryGuideAddsLongRunRecallRecoveryActions(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/longrun-recall",
		TimelinePath:      "/tmp/affent-eval/longrun-recall/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/longrun-recall/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/longrun-recall/trace.jsonl",
		Failures:          []string{"long-run recovery lost durable context"},
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:       1,
			SessionSearchResults:     1,
			SessionSearchContextHits: 0,
			MemorySearchCalls:        1,
			MemorySearchMisses:       1,
		},
		ContextCompactions: ContextCompactionStats{
			Count:          1,
			SummaryMissing: 1,
		},
		MemorySearchMissExamples: []MemorySearchMissExample{{
			ToolIndex: 3,
			CallID:    "mem-miss",
			Target:    "memory",
			Query:     "Northstar recovery marker",
		}},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"context_compaction:summary_missing",
		"recall:no_context",
		"recall:memory_no_topic_anchors",
		"recover from persisted LOOP.md, plan state, session_search, memory, or authoritative files",
		"rerun with narrower identifiers, adjacent context, plan anchors, or loop anchors",
		"retry with target/topic discovery or confirm the memory bucket is empty",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
		}
	}
	for _, want := range []string{
		"context_compaction_examples",
		"context_compactions",
		"session_search_examples",
		"session_search_results",
		"memory_search_miss_examples",
		"tool_timeline",
	} {
		if !stringSliceContains(guide.Inspect, want) {
			t.Fatalf("recovery guide inspect = %#v, want %q", guide.Inspect, want)
		}
	}
}

func TestBuildDebugRecoveryGuideAddsRecentSessionRecoveryAction(t *testing.T) {
	res := BatchResult{
		Workspace:         "/tmp/affent-eval/recent-session",
		TimelinePath:      "/tmp/affent-eval/recent-session/affenteval-timeline.md",
		DebugManifestPath: "/tmp/affent-eval/recent-session/affenteval-debug.json",
		TracePath:         "/tmp/affent-eval/recent-session/trace.jsonl",
		Failures:          []string{"direct recall missed but recent sessions were available"},
		ToolStats: ToolRuntimeStats{
			SessionSearchCalls:  1,
			SessionSearchRecent: 1,
		},
	}
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		t.Fatal("recovery guide missing")
	}
	for _, want := range []string{
		"empty_recall:recent_sessions",
		"inspect session_search_examples",
		"retry from recent_sessions plan, loop, or recovery anchors",
	} {
		if !strings.Contains(guide.ContinuePrompt, want) {
			t.Fatalf("continue prompt missing %q:\n%s", want, guide.ContinuePrompt)
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
		Prompt:             "fix it",
		SessionID:          "planned",
		ExecutePlan:        true,
		EnableMemory:       true,
		EnableLoopProtocol: true,
		MaxTurns:           3,
		CompactTrigger:     6,
		CompactKeepLast:    3,
	}, "fix it")
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
		"--loop-protocol",
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
			args := tc.runner.affentctlRunArgs("/tmp/ws", "/tmp/ws/trace.jsonl", BatchScenario{Prompt: "debug", MaxTurns: 1}, "debug")
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
	}).affentctlRunArgs("/tmp/ws", "/tmp/ws/trace.jsonl", BatchScenario{Prompt: "debug stream", MaxTurns: 2}, "debug stream")
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "--trace\x00/tmp/ws/trace.jsonl") {
		t.Fatalf("args missing trace path:\n%q", args)
	}
	if strings.Contains(joined, "--trace-skip-deltas") {
		t.Fatalf("TraceDeltas should not pass --trace-skip-deltas:\n%q", args)
	}
}

func TestEvalPathPrefersEvalToolchainsBeforeAmbientPath(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	ambient := filepath.Join(t.TempDir(), "ambient-bin")
	t.Setenv("HOME", home)
	t.Setenv("PATH", ambient)

	parts := strings.Split(evalPath(repoRoot), string(os.PathListSeparator))
	wantPrefix := []string{
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin"),
		filepath.Join(home, ".local", "go-toolchain", "go", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "go", "bin"),
	}
	if len(parts) < len(wantPrefix)+1 {
		t.Fatalf("evalPath parts = %#v", parts)
	}
	for i, want := range wantPrefix {
		if parts[i] != want {
			t.Fatalf("evalPath[%d] = %q, want %q in %#v", i, parts[i], want, parts)
		}
	}
	if parts[len(parts)-1] != ambient {
		t.Fatalf("ambient PATH should come last, got %#v", parts)
	}
}

func TestFindGoPrefersUserLocalToolchainOutsidePath(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example.test/eval\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-path"))
	goBin := filepath.Join(home, ".local", "go-toolchain", "go", "bin", "go")
	if err := os.MkdirAll(filepath.Dir(goBin), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = list ] && [ \"$2\" = -m ] && [ \"${GOTOOLCHAIN:-}\" = local ]; then exit 0; fi\nexit 1\n"
	if err := os.WriteFile(goBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findGo(repoRoot); got != goBin {
		t.Fatalf("findGo() = %q, want %q", got, goBin)
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
