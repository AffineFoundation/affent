package taskstate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

const defaultSummaryPromptMaxBytesForTaskStateTest = 196608

func TestScanEventsDerivesAuditableTaskState(t *testing.T) {
	input := taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID:       "t1",
		Text:         "Fix clamp behavior and push the result.",
		Mode:         "execute_plan",
		Source:       "schedule",
		ScheduleID:   "sched_clamp",
		ScheduleKind: "checkin",
	}) +
		taskStateEventLine(t, sse.TypeRuntimeSurface, sse.RuntimeSurfacePayload{
			TurnID:                             "t1",
			MaxTurnSteps:                       12,
			MaxTurnInputTokens:                 300000,
			ModelContextWindowTokens:           100000,
			ModelContextWindowAuto:             true,
			ModelContextWindowEffectivePercent: 95,
			ReservedOutputTokens:               30000,
			CompactTriggerInputTokens:          70000,
			CompactScopeActive:                 true,
			CompactWindowOrdinal:               2,
			CompactWindowPrefillInputTokens:    45000,
			CompactScopedInputTokens:           12000,
			CompactHardInputLimitTokens:        70000,
			CompactSummaryPromptMaxBytes:       defaultSummaryPromptMaxBytesForTaskStateTest,
			ToolSchemaBudgetTokens:             3000,
			EstimatedToolSchemaTokens:          2000,
			EstimatedRequestInputTokens:        5000,
		}) +
		taskStateEventLine(t, sse.TypeContextInjected, sse.ContextInjectedPayload{
			TurnID:  "t1",
			Source:  "runtime_workspace",
			Summary: "Workspace tools resolve relative paths from the active workspace root.",
		}) +
		taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "edit-1",
			Tool:   "edit_file",
			Args:   map[string]any{"path": "app/mathutil/clamp.go"},
		}) +
		taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "edit-1",
			ExitCode:      0,
			ResultSummary: "replaced 1 occurrence",
		}) +
		taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "test-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "go test ./..."},
		}) +
		taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "test-1",
			ExitCode:      1,
			FailureKind:   "test_failed",
			ResultSummary: "FAIL ./...",
			Result:        "FAIL ./...\nNext: inspect clamp bounds then rerun go test\nFailure: kind=test_failed",
		}) +
		taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "push-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "git push origin main"},
		}) +
		taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "push-1",
			ExitCode:      0,
			ResultSummary: "pushed",
			Result:        "pushed",
		}) +
		taskStateEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("ScanEvents returned nil")
	}
	if state.Objective != "Fix clamp behavior and push the result." ||
		state.RequestMode != "execute_plan" ||
		state.RequestSource != "schedule" ||
		state.ScheduleID != "sched_clamp" ||
		state.ScheduleKind != "checkin" ||
		state.Status != "completed" ||
		state.VerificationState != "failed" {
		t.Fatalf("unexpected task state: %+v", state.Snapshot)
	}
	if len(state.ChangedFiles) != 1 || state.ChangedFiles[0].Path != "app/mathutil/clamp.go" {
		t.Fatalf("changed files = %+v", state.ChangedFiles)
	}
	if len(state.FailedActions) != 1 || state.FailedActions[0].Next != "inspect clamp bounds then rerun go test" {
		t.Fatalf("failed actions = %+v", state.FailedActions)
	}
	if !taskStateEvidenceContains(state.Evidence, GitPushEvidenceSource, "git push origin main") {
		t.Fatalf("evidence = %+v, want git push handoff", state.Evidence)
	}
	if !taskStateEvidenceContains(state.Evidence, "runtime_surface", "model_context_window_auto=true") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "model_context_window_effective_percent=95") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "reserved_output_tokens=30000") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "compact_trigger_input_tokens=70000") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "compact_scope_active=true") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "compact_window_prefill_input_tokens=45000") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "compact_scoped_input_tokens=12000") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "compact_summary_prompt_max_bytes=196608") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "tool_schema_budget_tokens=3000") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "estimated_tool_schema_tokens=2000") {
		t.Fatalf("evidence = %+v, want runtime surface limits", state.Evidence)
	}

	text := SearchText(state.Snapshot)
	for _, want := range []string{"task_state:", "objective: Fix clamp behavior", "failed_action:", "test_failed", "next=inspect clamp bounds", "evidence: source=git_push", "model_context_window_effective_percent=95", "reserved_output_tokens=30000", "compact_scope_active=true", "compact_scoped_input_tokens=12000", "compact_summary_prompt_max_bytes=196608", "tool_schema_budget_tokens=3000", "estimated_tool_schema_tokens=2000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SearchText missing %q:\n%s", want, text)
		}
	}
}

func TestScanEventsVerificationStateIgnoresNonVerificationFailuresAfterPassingTests(t *testing.T) {
	input := taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Run tests, then inspect status.",
	}) +
		taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "test-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "python3 -m unittest discover -s tests"},
		}) +
		taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:   "t1",
			CallID:   "test-1",
			ExitCode: 0,
			Result:   "OK",
		}) +
		taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "status-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "git status --short"},
		}) +
		taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:      "t1",
			CallID:      "status-1",
			ExitCode:    1,
			FailureKind: "turn_input_budget_exhausted",
			Result:      "Failure: kind=turn_input_budget_exhausted",
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state.VerificationState != "last_shell_passed" {
		t.Fatalf("verification state = %q, want last_shell_passed despite non-verification failure", state.VerificationState)
	}
	if len(state.FailedActions) != 1 || state.FailedActions[0].Tool != "shell" {
		t.Fatalf("failed actions = %+v, want status failure retained", state.FailedActions)
	}
}

func TestScanEventsPreservesDistinctEvidenceSourcesAtLimit(t *testing.T) {
	var input strings.Builder
	input.WriteString(taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Fix and push.",
	}))
	for i, command := range []string{
		"go test ./...",
		"go test ./...",
		"go test ./...",
		"go test ./...",
		"go test ./...",
		"go test ./...",
		`git commit -m "fix"`,
		"git push origin main",
	} {
		callID := "shell-" + string(rune('a'+i))
		input.WriteString(taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: callID,
			Tool:   "shell",
			Args:   map[string]any{"command": command},
		}))
		input.WriteString(taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:   "t1",
			CallID:   callID,
			ExitCode: 0,
			Result:   "ok",
		}))
	}

	state, err := ScanEvents(strings.NewReader(input.String()), EventScanOptions{MaxItems: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !taskStateEvidenceContains(state.Evidence, GitCommitEvidenceSource, `git commit -m "fix"`) ||
		!taskStateEvidenceContains(state.Evidence, GitPushEvidenceSource, "git push origin main") {
		t.Fatalf("evidence = %+v, want commit and push sources preserved", state.Evidence)
	}
}

func TestScanEventsPreservesHandoffEvidenceUnderRuntimePressure(t *testing.T) {
	var input strings.Builder
	input.WriteString(taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Fix, commit, and push.",
	}))
	for i, turnID := range []string{"rt-a", "rt-b", "rt-c"} {
		input.WriteString(taskStateEventLine(t, sse.TypeRuntimeSurface, sse.RuntimeSurfacePayload{
			TurnID:                      turnID,
			ModelContextWindowTokens:    100000,
			ReservedOutputTokens:        30000,
			CompactTriggerInputTokens:   70000,
			ToolSchemaBudgetTokens:      3000 + i,
			EstimatedToolSchemaTokens:   2000 + i,
			EstimatedRequestInputTokens: 5000 + i,
		}))
	}
	for i, command := range []string{
		`git commit -m "fix"`,
		"git push origin main",
	} {
		callID := "shell-" + string(rune('a'+i))
		input.WriteString(taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: callID,
			Tool:   "shell",
			Args:   map[string]any{"command": command},
		}))
		input.WriteString(taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:   "t1",
			CallID:   callID,
			ExitCode: 0,
			Result:   "ok",
		}))
	}

	state, err := ScanEvents(strings.NewReader(input.String()), EventScanOptions{MaxItems: 6})
	if err != nil {
		t.Fatal(err)
	}
	if !taskStateEvidenceContains(state.Evidence, GitCommitEvidenceSource, `git commit -m "fix"`) ||
		!taskStateEvidenceContains(state.Evidence, GitPushEvidenceSource, "git push origin main") {
		t.Fatalf("evidence = %+v, want commit and push sources preserved under runtime pressure", state.Evidence)
	}
	if !taskStateEvidenceContains(state.Evidence, "runtime_surface", "tool_schema_budget_tokens=3002") {
		t.Fatalf("evidence = %+v, want latest runtime pressure evidence preserved", state.Evidence)
	}
}

func TestScanEventsPreservesDistinctAttemptedActionToolsAtLimit(t *testing.T) {
	var input strings.Builder
	input.WriteString(taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Switch workspace and fix.",
	}))
	for i, req := range []ToolRequest{
		{Tool: "session_workspace", Args: map[string]any{"action": "set", "path": "app"}},
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		{Tool: "list_files", Args: map[string]any{"path": "."}},
		{Tool: "read_file", Args: map[string]any{"path": "a_test.go"}},
		{Tool: "read_file", Args: map[string]any{"path": "a.go"}},
		{Tool: "edit_file", Args: map[string]any{"path": "a.go"}},
		{Tool: "shell", Args: map[string]any{"command": "go test ./..."}},
		{Tool: "shell", Args: map[string]any{"command": `git commit -m "fix"`}},
		{Tool: "shell", Args: map[string]any{"command": "git push origin main"}},
		{Tool: "shell", Args: map[string]any{"command": "git status --short"}},
	} {
		callID := "tool-" + string(rune('a'+i))
		input.WriteString(taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: callID,
			Tool:   req.Tool,
			Args:   req.Args,
		}))
		input.WriteString(taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:   "t1",
			CallID:   callID,
			ExitCode: 0,
		}))
	}

	state, err := ScanEvents(strings.NewReader(input.String()), EventScanOptions{MaxItems: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !taskStateActionContains(state.AttemptedActions, "session_workspace", "app") {
		t.Fatalf("attempted actions = %+v, want session_workspace app preserved", state.AttemptedActions)
	}
}

func TestToolSemanticsAreSharedTaskStateInputs(t *testing.T) {
	req := ToolRequest{
		Tool:   "web_fetch",
		TurnID: "turn-1",
		CallID: "fetch-1",
		Args:   map[string]any{"url": "https://example.test/report"},
	}
	if got := ToolActionSummary(req); got != "url: https://example.test/report" {
		t.Fatalf("ToolActionSummary = %q", got)
	}
	if file := ToolChangedFile(ToolRequest{Tool: "edit_file", Args: map[string]any{"path": "app/main.go"}}); file.Path != "app/main.go" || file.Action != "edit" {
		t.Fatalf("ToolChangedFile = %+v", file)
	}
	kinds := ToolFailureKinds(ToolResult{
		Tool:         "web_fetch",
		FailureKind:  "empty_response",
		FailureKinds: []string{"empty_response", "blocked"},
		Result:       "[empty response: URL=https://example.test/report]\nFailure: kind=no_results",
	}, DefaultMaxItems)
	if strings.Join(kinds, ",") != "empty_response,blocked,no_results" {
		t.Fatalf("ToolFailureKinds = %+v", kinds)
	}
	if !ToolFailed(ToolResult{Tool: "shell", ExitCode: 1}, DefaultMaxItems) {
		t.Fatal("ToolFailed returned false for non-zero exit")
	}
}

func TestAppendActionRetentionKeepsHandoffOverRecentLowValueAction(t *testing.T) {
	actions := []Action{
		{Tool: "session_workspace", Summary: "app"},
		{Tool: "shell", Summary: "go test ./..."},
		{Tool: "read_file", Summary: "greet/greet_test.go"},
		{Tool: "read_file", Summary: "greet/greet.go"},
		{Tool: "edit_file", Summary: "greet/greet.go"},
		{Tool: "shell", Summary: "git commit -m fix"},
		{Tool: "shell", Summary: "git push origin main"},
		{Tool: "shell", Summary: "git status --short"},
	}
	got := AppendAction(actions, Action{Tool: "list_files", Summary: "docs"}, DefaultMaxItems)
	if len(got) != DefaultMaxItems {
		t.Fatalf("actions len = %d, want %d: %+v", len(got), DefaultMaxItems, got)
	}
	for _, want := range []string{"git commit", "git push"} {
		if !taskStateActionContains(got, "shell", want) {
			t.Fatalf("actions = %+v, want shell action containing %q", got, want)
		}
	}
	if taskStateActionContains(got, "list_files", "docs") {
		t.Fatalf("actions = %+v, low-value overflow action should not displace stronger anchors", got)
	}
}

func TestScanEventsKeepsDurableObjectiveAcrossScheduledTurns(t *testing.T) {
	input := taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Build a small release notes generator and keep iterating until tests pass.",
	}) +
		taskStateEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
		}) +
		taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
			TurnID:       "t2",
			Text:         "Scheduled loop tick for release notes generator",
			DisplayText:  "Loop tick: continue release notes generator",
			Source:       "schedule",
			ScheduleID:   "sched_release_notes",
			ScheduleKind: "loop_tick",
		}) +
		taskStateEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t2",
			Reason: sse.TurnEndCompleted,
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("ScanEvents returned nil")
	}
	if state.Objective != "Build a small release notes generator and keep iterating until tests pass." {
		t.Fatalf("objective = %q, want first durable task request", state.Objective)
	}
	if state.LatestRequestText != "Scheduled loop tick for release notes generator" {
		t.Fatalf("latest request = %q, want latest scheduled prompt", state.LatestRequestText)
	}
	if state.RequestSource != "schedule" || state.ScheduleKind != "loop_tick" || state.ScheduleID != "sched_release_notes" {
		t.Fatalf("request provenance = source:%q kind:%q id:%q, want latest scheduled tick", state.RequestSource, state.ScheduleKind, state.ScheduleID)
	}
}

func TestScanEventsKeepsRecoveryNextStepAfterCompletedFailedTurn(t *testing.T) {
	input := taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Append the latest BTC price.",
	}) +
		taskStateEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "read-1",
			Tool:   "read_file",
			Args:   map[string]any{"path": "btc_price_tracker.md"},
		}) +
		taskStateEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "read-1",
			ExitCode:      1,
			FailureKind:   "not_found",
			ResultSummary: "Error: btc_price_tracker.md not found",
			Result:        "Error: btc_price_tracker.md not found\nFailure: kind=not_found\nNext: call list_files on . before retrying read_file",
		}) +
		taskStateEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("ScanEvents returned nil")
	}
	if state.Status != "completed" || state.VerificationState != "unknown" {
		t.Fatalf("status/verification = %q/%q, want completed/unknown", state.Status, state.VerificationState)
	}
	if state.NextStep != "call list_files on . before retrying read_file" {
		t.Fatalf("next_step = %q, want failed tool recovery hint", state.NextStep)
	}
}

func TestScanEventsRecordsContextCompactionEvidence(t *testing.T) {
	input := taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Continue the long-running project.",
	}) +
		taskStateEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{
			TurnID:                             "t1",
			BeforeMessages:                     42,
			AfterMessages:                      10,
			RemovedMessages:                    32,
			BeforeBytes:                        10000,
			AfterBytes:                         3000,
			ReducedBytes:                       7000,
			EstimatedInputTokens:               120000,
			TriggerInputTokens:                 70000,
			ModelContextWindowTokens:           100000,
			ModelContextWindowEffectivePercent: 95,
			ReservedOutputTokens:               30000,
			CompactTriggerInputPercent:         80,
			CompactScopeActive:                 true,
			CompactWindowOrdinal:               3,
			CompactWindowPrefillInputTokens:    68000,
			CompactScopedInputTokens:           50000,
			CompactHardInputLimitTokens:        70000,
			Reactive:                           true,
			Reason:                             "context_overflow",
			SummaryPresent:                     true,
			SummaryBytes:                       900,
			LoopProtocolAnchor:                 "LOOP_PROTOCOL: id=demo status=running step=verify",
		}) +
		taskStateEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndMaxTurns,
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{SummaryMaxChar: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("ScanEvents returned nil")
	}
	if !taskStateEvidenceContains(state.Evidence, "context_compaction", "context_overflow") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "LOOP_PROTOCOL: id=demo") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "estimated_input_tokens=120000") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "trigger_input_tokens=70000") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "model_context_window_effective_percent=95") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "compact_window_prefill_input_tokens=68000") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "compact_scoped_input_tokens=50000") {
		t.Fatalf("context compaction evidence = %+v", state.Evidence)
	}
	if !stringSliceContains(state.Sources, "context_compaction") {
		t.Fatalf("sources = %+v, want context_compaction", state.Sources)
	}
	text := SearchText(state.Snapshot)
	for _, want := range []string{"evidence: source=context_compaction", "removed_messages=32", "reactive=true", "estimated_input_tokens=120000", "trigger_input_tokens=70000", "model_context_window_effective_percent=95", "compact_scoped_input_tokens=50000", "LOOP_PROTOCOL: id=demo"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SearchText missing %q:\n%s", want, text)
		}
	}
}

func TestScanEventsRecordsContextCompactionSkippedEvidence(t *testing.T) {
	input := taskStateEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Continue under context pressure.",
	}) +
		taskStateEventLine(t, sse.TypeContextCompactSkipped, sse.ContextCompactSkippedPayload{
			TurnID:                    "t1",
			Cause:                     "request_pressure_not_reduced",
			Reason:                    "estimated_context_pressure",
			BeforeMessages:            6,
			CandidateMessages:         5,
			BeforeBytes:               25396,
			CandidateBytes:            25535,
			EstimatedInputTokens:      6732,
			AfterEstimatedInputTokens: 6766,
			TriggerInputTokens:        1,
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("ScanEvents returned nil")
	}
	if !taskStateEvidenceContains(state.Evidence, "context_compaction_skipped", "request_pressure_not_reduced") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction_skipped", "estimated_context_pressure") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction_skipped", "after_estimated_input_tokens=6766") {
		t.Fatalf("evidence = %+v, want compaction skipped policy evidence", state.Evidence)
	}
	if !stringSliceContains(state.Sources, "context_compaction_skipped") {
		t.Fatalf("sources = %+v, want context_compaction_skipped", state.Sources)
	}
}

func taskStateEventLine(t *testing.T, eventType string, payload any) string {
	t.Helper()
	ev, err := sse.NewEvent(eventType, payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

func taskStateEvidenceContains(evidence []Evidence, source, summaryPart string) bool {
	for _, item := range evidence {
		if item.Source == source && strings.Contains(item.Summary, summaryPart) {
			return true
		}
	}
	return false
}

func taskStateActionContains(actions []Action, tool, summaryPart string) bool {
	for _, item := range actions {
		if item.Tool == tool && strings.Contains(item.Summary, summaryPart) {
			return true
		}
	}
	return false
}

func stringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
