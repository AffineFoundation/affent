package agenteval

import (
	"reflect"
	"testing"
)

func TestTrace_PlanStats_Aggregation(t *testing.T) {
	tr := Trace{
		Tools: []ToolCall{
			{Tool: "plan", Args: map[string]any{"action": "set"}},
			{Tool: "plan", Args: map[string]any{"action": " update "}},
			{Tool: "plan", Args: map[string]any{"action": "UPDATE"}, ExitCode: 1, IsErr: true},
			{Tool: "plan", Args: map[string]any{"action": 3}},
			{Tool: "read_file", Args: map[string]any{"action": "view"}},
		},
	}

	got := tr.PlanStats()
	if got.Calls != 4 {
		t.Fatalf("Calls = %d, want 4", got.Calls)
	}
	if got.Errors != 1 {
		t.Fatalf("Errors = %d, want 1", got.Errors)
	}
	wantActions := map[string]int{"set": 1, "update": 2, "unknown": 1}
	if !reflect.DeepEqual(got.ByAction, wantActions) {
		t.Fatalf("ByAction = %#v, want %#v", got.ByAction, wantActions)
	}
	if !got.HasAny() {
		t.Fatal("HasAny should report true when plan calls were observed")
	}
}

func TestTrace_PlanStats_EmptyTraceProducesZeroValue(t *testing.T) {
	got := Trace{}.PlanStats()
	if got.Calls != 0 || got.Errors != 0 || got.ByAction != nil {
		t.Fatalf("PlanStats on empty trace = %+v, want zero value", got)
	}
	if got.HasAny() {
		t.Fatal("HasAny should be false when no plan calls were observed")
	}
}

func TestTrace_PlanExamples(t *testing.T) {
	tr := Trace{Tools: []ToolCall{{
		CallID: "plan-1",
		Tool:   "plan",
		Args: map[string]any{
			"action":   "update",
			"index":    float64(2),
			"status":   "completed",
			"evidence": []any{"go test ./internal/agenteval"},
			"note":     "verified resume step",
		},
		Result: `{"version":1,"message":"updated step 2","steps":[{"text":"inspect trace","status":"completed"},{"text":"verify resume behavior","status":"completed","evidence":["go test ./internal/agenteval"],"note":"verified resume step"},{"text":"ship docs","status":"pending"}]}`,
	}, {
		CallID:   "plan-2",
		Tool:     "plan",
		Args:     map[string]any{"action": "set"},
		Result:   "unused field(s) for action=set: index",
		ExitCode: 1,
		IsErr:    true,
	}}}

	got := tr.PlanExamples(4)
	if len(got) != 2 {
		t.Fatalf("PlanExamples len = %d, want 2: %+v", len(got), got)
	}
	if got[0].CallID != "plan-1" ||
		got[0].Action != "update" ||
		got[0].Index != 2 ||
		got[0].Status != "completed" ||
		got[0].StepText != "verify resume behavior" ||
		got[0].TotalSteps != 3 ||
		got[0].CompletedSteps != 2 ||
		got[0].CurrentStepIndex != 3 ||
		got[0].CurrentStepStatus != "pending" ||
		got[0].CurrentStep != "ship docs" ||
		!reflect.DeepEqual(got[0].Evidence, []string{"go test ./internal/agenteval"}) ||
		got[0].NotePreview != "verified resume step" ||
		got[0].ResultMessage != "updated step 2" {
		t.Fatalf("PlanExamples[0] = %+v", got[0])
	}
	if !got[1].Error || got[1].ResultSummary != "unused field(s) for action=set: index" {
		t.Fatalf("PlanExamples[1] = %+v", got[1])
	}
}
