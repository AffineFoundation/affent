package taskstate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

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
			TurnID:                    "t1",
			MaxTurnSteps:              12,
			MaxTurnInputTokens:        300000,
			ModelContextWindowTokens:  100000,
			ReservedOutputTokens:      30000,
			CompactTriggerInputTokens: 70000,
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
		state.VerificationState != "last_shell_passed" {
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
	if !taskStateEvidenceContains(state.Evidence, "runtime_surface", "reserved_output_tokens=30000") ||
		!taskStateEvidenceContains(state.Evidence, "runtime_surface", "compact_trigger_input_tokens=70000") {
		t.Fatalf("evidence = %+v, want runtime surface limits", state.Evidence)
	}

	text := SearchText(state.Snapshot)
	for _, want := range []string{"task_state:", "objective: Fix clamp behavior", "failed_action:", "test_failed", "next=inspect clamp bounds", "evidence: source=git_push", "reserved_output_tokens=30000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SearchText missing %q:\n%s", want, text)
		}
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
	if state.Status != "completed" || state.VerificationState != "failed" {
		t.Fatalf("status/verification = %q/%q, want completed/failed", state.Status, state.VerificationState)
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
			TurnID:             "t1",
			BeforeMessages:     42,
			AfterMessages:      10,
			RemovedMessages:    32,
			BeforeBytes:        10000,
			AfterBytes:         3000,
			ReducedBytes:       7000,
			Reactive:           true,
			Reason:             "context_overflow",
			SummaryPresent:     true,
			SummaryBytes:       900,
			LoopProtocolAnchor: "LOOP_PROTOCOL: id=demo status=running step=verify",
		}) +
		taskStateEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndMaxTurns,
		})

	state, err := ScanEvents(strings.NewReader(input), EventScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		t.Fatal("ScanEvents returned nil")
	}
	if !taskStateEvidenceContains(state.Evidence, "context_compaction", "context_overflow") ||
		!taskStateEvidenceContains(state.Evidence, "context_compaction", "LOOP_PROTOCOL: id=demo") {
		t.Fatalf("context compaction evidence = %+v", state.Evidence)
	}
	if !stringSliceContains(state.Sources, "context_compaction") {
		t.Fatalf("sources = %+v, want context_compaction", state.Sources)
	}
	text := SearchText(state.Snapshot)
	for _, want := range []string{"evidence: source=context_compaction", "removed_messages=32", "reactive=true", "LOOP_PROTOCOL: id=demo"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SearchText missing %q:\n%s", want, text)
		}
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

func stringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
