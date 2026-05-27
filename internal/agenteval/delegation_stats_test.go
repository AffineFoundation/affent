package agenteval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

// TestTrace_DelegationStats_Aggregation pins the per-kind aggregation
// the eval JSONL summary depends on.
func TestTrace_DelegationStats_Aggregation(t *testing.T) {
	tr := Trace{
		Tools: []ToolCall{
			{Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "recall"}},
			{Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "recall"}},
			{Tool: "run_task", Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"}, ExitCode: 1, IsErr: true},
			{Tool: "run_task", Result: `{"task_type":"explore","ok":false,"warnings":["no_valid_evidence_backed_findings"]}`, Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"}},
			{Tool: "run_task", Result: `{"task_type":"verify","ok":false,"summary":"claim falsified"}`, Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "verify"}},
			{Tool: "subagent_run", Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "test"}},
			{Tool: "subagent_run", Result: `{"ok":false,"report":"partial"}`, Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "research"}},
			{Tool: "read_file"}, // no delegation: must be ignored
		},
	}
	got := tr.DelegationStats()

	if got.FocusedTaskCalls != 5 {
		t.Errorf("FocusedTaskCalls = %d, want 5", got.FocusedTaskCalls)
	}
	if got.FocusedTaskErrors != 2 {
		t.Errorf("FocusedTaskErrors = %d, want 2 (exit failure plus non-verify ok=false)", got.FocusedTaskErrors)
	}
	if got.FocusedTaskIncomplete != 1 {
		t.Errorf("FocusedTaskIncomplete = %d, want 1 (non-verify ok=false)", got.FocusedTaskIncomplete)
	}
	if !reflect.DeepEqual(got.FocusedTaskByType, map[string]int{"recall": 2, "explore": 2, "verify": 1}) {
		t.Errorf("FocusedTaskByType = %+v", got.FocusedTaskByType)
	}
	if got.SubagentCalls != 2 {
		t.Errorf("SubagentCalls = %d, want 2", got.SubagentCalls)
	}
	if got.SubagentErrors != 1 {
		t.Errorf("SubagentErrors = %d, want 1 (ok=false partial child report)", got.SubagentErrors)
	}
	if got.SubagentIncomplete != 1 {
		t.Errorf("SubagentIncomplete = %d, want 1 (ok=false partial child report)", got.SubagentIncomplete)
	}
	if !reflect.DeepEqual(got.SubagentByMode, map[string]int{"test": 1, "research": 1}) {
		t.Errorf("SubagentByMode = %+v", got.SubagentByMode)
	}
	if !got.HasAny() {
		t.Error("HasAny() should report true when there are delegation calls")
	}
}

func TestTrace_DelegationStats_EmptyTraceProducesZeroValueAndHasAnyFalse(t *testing.T) {
	got := Trace{}.DelegationStats()
	if got.FocusedTaskCalls != 0 || got.SubagentCalls != 0 {
		t.Fatalf("expected zero counts on empty trace, got %+v", got)
	}
	if got.HasAny() {
		t.Error("HasAny() must be false when no delegation calls observed")
	}
	if got.FocusedTaskByType != nil || got.SubagentByMode != nil {
		t.Error("sub-maps must stay nil when no delegation calls observed (keeps JSONL clean)")
	}
}

// TestParseTraceFile_RecoversDelegationEndToEnd is the wire-format
// contract pin for runtime eventlog JSONL replay.
func TestParseTraceFile_RecoversDelegationEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	// Real wire format: one {"type":"...","data":{...payload...}}
	// JSON event per line, matching internal/eventlog.Recorder.
	events := []struct {
		eventType string
		payload   any
	}{
		{sse.TypeTraceMeta, sse.TraceMetaPayload{SchemaVersion: sse.TraceSchemaVersion}},
		{sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID:     "turn-1",
			CallID:     "c1",
			Tool:       "run_task",
			Args:       map[string]any{"task_type": "recall", "objective": "find prefs"},
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "recall"},
		}},
		{sse.TypeToolResult, sse.ToolResultPayload{
			CallID:     "c1",
			ExitCode:   0,
			Result:     `{"task_type":"recall","ok":true,"summary":"found 1"}`,
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "recall"},
		}},
		{sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID:     "turn-1",
			CallID:     "c2",
			Tool:       "run_task",
			Args:       map[string]any{"task_type": "explore"},
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"},
		}},
		{sse.TypeToolResult, sse.ToolResultPayload{
			CallID:     "c2",
			ExitCode:   0,
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "explore"},
		}},
		{sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID:     "turn-1",
			CallID:     "c3",
			Tool:       "subagent_run",
			Args:       map[string]any{"mode": "review"},
			Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"},
		}},
		{sse.TypeToolResult, sse.ToolResultPayload{
			CallID:     "c3",
			ExitCode:   1, // simulate a child-side failure
			Delegation: &sse.DelegationMeta{Kind: "subagent", Mode: "review"},
		}},
		{sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: "turn-1", Reason: sse.TurnEndCompleted}},
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false) // mirror eventlog.Recorder
	for _, e := range events {
		payloadBytes, err := json.Marshal(e.payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.Encode(struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}{Type: e.eventType, Data: payloadBytes}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	trace, err := ParseTraceFile(path)
	if err != nil {
		t.Fatalf("ParseTraceFile: %v", err)
	}
	if len(trace.Tools) != 3 {
		t.Fatalf("expected 3 tool calls, got %d: %+v", len(trace.Tools), trace.Tools)
	}
	for i, c := range trace.Tools {
		if c.Delegation == nil {
			t.Errorf("tool[%d] (%s) lost its Delegation through trace round-trip", i, c.Tool)
		}
	}
	if trace.Tools[0].Delegation.TaskType != "recall" {
		t.Errorf("tool[0].Delegation.TaskType = %q", trace.Tools[0].Delegation.TaskType)
	}
	if trace.Tools[2].Delegation.Mode != "review" {
		t.Errorf("tool[2].Delegation.Mode = %q", trace.Tools[2].Delegation.Mode)
	}

	// Aggregation must reflect the disk-replayed Trace exactly.
	got := trace.DelegationStats()
	if got.FocusedTaskCalls != 2 {
		t.Errorf("FocusedTaskCalls after replay = %d, want 2", got.FocusedTaskCalls)
	}
	if got.SubagentCalls != 1 {
		t.Errorf("SubagentCalls after replay = %d, want 1", got.SubagentCalls)
	}
	if got.SubagentErrors != 1 {
		t.Errorf("SubagentErrors after replay = %d, want 1 (the ExitCode=1 review call)", got.SubagentErrors)
	}
	if !reflect.DeepEqual(got.FocusedTaskByType, map[string]int{"recall": 1, "explore": 1}) {
		t.Errorf("FocusedTaskByType after replay = %+v", got.FocusedTaskByType)
	}
}

// TestApplyTraceEvent_PropagatesDelegationFromRequestAndResult ensures
// the trace reader preserves delegation from request and result events.
func TestApplyTraceEvent_PropagatesDelegationFromRequestAndResult(t *testing.T) {
	t.Run("request carries delegation", func(t *testing.T) {
		tr := &Trace{}
		pending := map[string]int{}
		reqPayload := sse.ToolRequestPayload{
			TurnID:     "turn-1",
			CallID:     "c1",
			Tool:       "run_task",
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "verify"},
		}
		data, _ := json.Marshal(reqPayload)
		if _, err := applyTraceEvent(tr, pending, sse.TypeToolRequest, data, "turn-1"); err != nil {
			t.Fatal(err)
		}
		if len(tr.Tools) != 1 || tr.Tools[0].Delegation == nil {
			t.Fatalf("delegation not propagated to ToolCall: %+v", tr.Tools)
		}
		if tr.Tools[0].Delegation.TaskType != "verify" {
			t.Errorf("TaskType = %q, want verify", tr.Tools[0].Delegation.TaskType)
		}
	})

	t.Run("result fills in when request lacked delegation", func(t *testing.T) {
		// Simulates a replay where the on-disk request event predates
		// the field (or a producer omitted it for whatever reason).
		// The result mirror should fill the gap, not silently drop the
		// classification.
		tr := &Trace{}
		pending := map[string]int{}
		reqPayload := sse.ToolRequestPayload{
			TurnID: "turn-1",
			CallID: "c1",
			Tool:   "run_task",
			// no Delegation
		}
		reqData, _ := json.Marshal(reqPayload)
		if _, err := applyTraceEvent(tr, pending, sse.TypeToolRequest, reqData, "turn-1"); err != nil {
			t.Fatal(err)
		}
		resPayload := sse.ToolResultPayload{
			CallID:     "c1",
			ExitCode:   0,
			Delegation: &sse.DelegationMeta{Kind: "focused_task", TaskType: "review"},
		}
		resData, _ := json.Marshal(resPayload)
		if _, err := applyTraceEvent(tr, pending, sse.TypeToolResult, resData, "turn-1"); err != nil {
			t.Fatal(err)
		}
		if tr.Tools[0].Delegation == nil {
			t.Fatal("result delegation should backfill missing request delegation")
		}
		if tr.Tools[0].Delegation.TaskType != "review" {
			t.Errorf("TaskType = %q, want review", tr.Tools[0].Delegation.TaskType)
		}
	})
}
