package agenteval

import (
	"encoding/json"
	"strings"
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

func TestApplyTraceEventKeepsMemoryUpdateMetadata(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}

	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolRequest, json.RawMessage(`{"turn_id":"turn-1","call_id":"mem1","tool":"memory","args":{"action":"replace","target":"memory","topic":"markets"},"args_truncated":true}`), "turn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTraceEvent(&trace, pending, sse.TypeToolResult, json.RawMessage(`{"turn_id":"turn-1","call_id":"mem1","result":"{\"ok\":true}","exit_code":0,"memory_update":{"action":"replace","target":"memory","topic":"markets","location":"memory:markets","preview":"old fact -> new fact","previous_preview":"old fact","next_preview":"new fact"}}`), "turn-1"); err != nil {
		t.Fatal(err)
	}

	if len(trace.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(trace.Tools))
	}
	update := trace.Tools[0].MemoryUpdate
	if update == nil || update.Action != "replace" || update.Location != "memory:markets" || update.Preview != "old fact -> new fact" {
		t.Fatalf("MemoryUpdate = %+v", update)
	}
	examples := trace.MemoryUpdateExamples(2)
	if len(examples) != 1 || examples[0].CallID != "mem1" || examples[0].PreviousPreview != "old fact" || examples[0].NextPreview != "new fact" {
		t.Fatalf("MemoryUpdateExamples = %+v", examples)
	}

	timeline := renderDebugTimeline(BatchResult{BatchScenario: "memory-meta"}, BatchScenario{Prompt: "remember this"}, &trace)
	for _, want := range []string{
		"## Memory Updates",
		"tool#1 action=`replace` location=`memory:markets` call_id=`mem1`",
		"old fact -> new fact",
	} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
	}
}
