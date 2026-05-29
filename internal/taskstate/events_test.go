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
		taskStateEventLine(t, sse.TypeContextInjected, sse.ContextInjectedPayload{
			TurnID:  "t1",
			Source:  "runtime_workspace",
			Summary: "Workspace tools resolve relative paths from the session workspace root.",
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

	text := SearchText(state.Snapshot)
	for _, want := range []string{"task_state:", "objective: Fix clamp behavior", "failed_action:", "test_failed", "next=inspect clamp bounds", "evidence: source=git_push"} {
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
