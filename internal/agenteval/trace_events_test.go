package agenteval

import (
	"encoding/json"
	"reflect"
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

func TestTraceBrowserNetworkSearchExamplesAreRefsNotSources(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		CallID: "net1",
		Tool:   "browser_network",
		Args:   map[string]any{"query": "market_cap"},
		Result: "BROWSER NETWORK EVIDENCE\n" +
			"CURRENT_PAGE: https://taostats.io/subnets/120\n" +
			"query: \"market_cap\"\n" +
			"MATCHES:\n" +
			"- n7 status=200 resource=fetch content_type=application/json url=https://api.taostats.io/subnet/120/metrics\n" +
			"  preview: {\"market_cap\":\"195094\"}\n" +
			"Next: call browser_network_read with the most relevant ref and json_path before citing values.\n",
		ExitCode: 0,
	}, {
		CallID: "net2",
		Tool:   "browser_network",
		Args:   map[string]any{"query": "validators"},
		Result: "BROWSER NETWORK EVIDENCE\n" +
			"CURRENT_PAGE: https://taostats.io/subnets/120\n" +
			"query: \"validators\"\n" +
			"MATCHES: none\n" +
			"Next: wait once, then mark hidden fields unverified.\n",
		ExitCode: 0,
	}}}

	examples := trace.BrowserNetworkSearchExamples(5)
	if len(examples) != 2 {
		t.Fatalf("BrowserNetworkSearchExamples = %+v", examples)
	}
	if examples[0].ToolIndex != 1 ||
		examples[0].CallID != "net1" ||
		examples[0].CurrentPageURL != "https://taostats.io/subnets/120" ||
		examples[0].Query != "market_cap" ||
		examples[0].Status != "matches" ||
		!examples[0].RequiresRead ||
		!examples[0].NotCitable ||
		!reflect.DeepEqual(examples[0].Refs, []string{"n7"}) ||
		!strings.Contains(examples[0].SuggestedNextStep, "browser_network_read") {
		t.Fatalf("first browser network example = %+v", examples[0])
	}
	if examples[1].Status != "no_matches" || !examples[1].NotCitable || examples[1].RequiresRead {
		t.Fatalf("no-match browser network example = %+v", examples[1])
	}
	if len(trace.SourceAccessExamples(5)) != 0 {
		t.Fatalf("browser_network refs must not be SourceAccess examples: %+v", trace.SourceAccessExamples(5))
	}

	timeline := renderDebugTimeline(BatchResult{BatchScenario: "network-refs"}, BatchScenario{Prompt: "inspect dashboard"}, &trace)
	for _, want := range []string{
		"## Browser Network Searches",
		"not citable sources",
		"tool#1 status=`matches` query=`market_cap` page=`https://taostats.io/subnets/120` call_id=`net1` requires_read=`true` citable=`false`",
		"refs: `n7`",
		"next: call browser_network_read",
		"tool#2 status=`no_matches` query=`validators`",
	} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
	}
}
