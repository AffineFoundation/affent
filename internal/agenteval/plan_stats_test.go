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
