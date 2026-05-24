package agenteval

import (
	"encoding/json"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestApplyTraceEventFiltersToolResultsByTurn(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}

	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolRequest, json.RawMessage(`{"turn_id":"turn-1","call_id":"c1","tool":"shell","args":{}}`), "turn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolResult, json.RawMessage(`{"turn_id":"turn-2","call_id":"orphan","result":"wrong turn","exit_code":0}`), "turn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolResult, json.RawMessage(`{"turn_id":"turn-1","call_id":"c1","result":"ok","exit_code":0}`), "turn-1"); err != nil {
		t.Fatal(err)
	}

	if len(trace.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(trace.Tools))
	}
	if trace.Tools[0].TurnID != "turn-1" || trace.Tools[0].Result != "ok" {
		t.Fatalf("tool result not stitched to matching turn: %+v", trace.Tools[0])
	}
}

func TestApplyTraceEventKeepsLegacyToolResultsWithoutTurnID(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}

	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolResult, json.RawMessage(`{"call_id":"legacy","result":"ok","exit_code":0}`), "turn-1"); err != nil {
		t.Fatal(err)
	}

	if len(trace.Tools) != 1 {
		t.Fatalf("legacy tool result should remain compatible, tools = %d", len(trace.Tools))
	}
	if trace.Tools[0].CallID != "legacy" || trace.Tools[0].TurnID != "" {
		t.Fatalf("legacy tool result parsed incorrectly: %+v", trace.Tools[0])
	}
}
