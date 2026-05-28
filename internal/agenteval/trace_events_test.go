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

func TestApplyTraceEventAggregatesDiskParsedTurnStats(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}

	if done, err := applyTraceEvent(&trace, pending, sse.TypeTurnEnd, json.RawMessage(`{"turn_id":"turn-1","reason":"completed","tool_stats":{"tool_requests":2,"tool_errors":1,"tool_failure_by_kind":{"invalid_args":1},"memory_updates":1}}`), ""); err != nil || !done {
		t.Fatalf("first turn.end done=%t err=%v", done, err)
	}
	if done, err := applyTraceEvent(&trace, pending, sse.TypeTurnEnd, json.RawMessage(`{"turn_id":"turn-2","reason":"completed","tool_stats":{"tool_requests":3,"tool_errors":2,"tool_failure_by_kind":{"loop_guard_no_budget":2},"session_search_calls":1}}`), ""); err != nil || !done {
		t.Fatalf("second turn.end done=%t err=%v", done, err)
	}

	if trace.TurnEndReason != "completed" {
		t.Fatalf("TurnEndReason = %q, want completed", trace.TurnEndReason)
	}
	if trace.ToolStats.ToolRequests != 5 ||
		trace.ToolStats.ToolErrors != 3 ||
		trace.ToolStats.MemoryUpdates != 1 ||
		trace.ToolStats.SessionSearchCalls != 1 {
		t.Fatalf("ToolStats did not aggregate multi-turn values: %+v", trace.ToolStats)
	}
	if trace.ToolStats.ToolFailureByKind["invalid_args"] != 1 ||
		trace.ToolStats.ToolFailureByKind["loop_guard_no_budget"] != 2 {
		t.Fatalf("ToolFailureByKind did not aggregate: %+v", trace.ToolStats.ToolFailureByKind)
	}
}

func TestApplyTraceEventCapturesContextInjectionMetadata(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}

	if _, err := applyTraceEvent(&trace, pending, sse.TypeContextInjected, json.RawMessage(`{"turn_id":"turn-2","source":"active_plan","title":"wrong turn","bytes":10,"estimated_tokens":3}`), "turn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTraceEvent(&trace, pending, sse.TypeContextInjected, json.RawMessage(`{"turn_id":"turn-1","source":"account_access","title":"Account access context injected","summary":"Account hints were made available.","preview":"GITHUB_TOKEN","bytes":120,"estimated_tokens":30}`), "turn-1"); err != nil {
		t.Fatal(err)
	}

	if len(trace.ContextInjections) != 1 {
		t.Fatalf("ContextInjections = %+v", trace.ContextInjections)
	}
	injection := trace.ContextInjections[0]
	if injection.TurnID != "turn-1" ||
		injection.Source != "account_access" ||
		injection.Title != "Account access context injected" ||
		injection.Bytes != 120 ||
		injection.EstimatedTokens != 30 ||
		injection.Preview != "GITHUB_TOKEN" {
		t.Fatalf("ContextInjection = %+v", injection)
	}
}

func TestApplyTraceEventTracksContextCompactionSummaryPresenceKnown(t *testing.T) {
	trace := Trace{}
	pending := map[string]int{}

	if _, err := applyTraceEvent(&trace, pending, sse.TypeContextCompact, json.RawMessage(`{"turn_id":"turn-1","before_messages":40,"after_messages":12,"removed_messages":28,"reactive":true,"reason":"legacy_trace"}`), "turn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := applyTraceEvent(&trace, pending, sse.TypeContextCompact, json.RawMessage(`{"turn_id":"turn-1","before_messages":42,"after_messages":10,"removed_messages":32,"reactive":true,"reason":"context_overflow","summary_present":false}`), "turn-1"); err != nil {
		t.Fatal(err)
	}

	stats := trace.ContextCompactionStats(2)
	if stats.Count != 2 || stats.SummaryMissing != 1 || stats.SummaryEmpty != 0 {
		t.Fatalf("ContextCompactionStats = %+v", stats)
	}
	if len(stats.Examples) != 2 ||
		stats.Examples[0].SummaryPresentKnown ||
		!stats.Examples[1].SummaryPresentKnown {
		t.Fatalf("ContextCompaction examples should preserve summary_present known state: %+v", stats.Examples)
	}
	timeline := renderDebugTimeline(BatchResult{BatchScenario: "compaction-known"}, BatchScenario{Prompt: "continue"}, &trace)
	for _, want := range []string{"summary_state=unknown", "summary_state=missing"} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
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
			"EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n" +
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
			"EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n" +
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
		examples[0].EvidenceStatus != "refs_only_not_citable; read_required=true" ||
		!examples[0].RequiresRead ||
		!examples[0].NotCitable ||
		!reflect.DeepEqual(examples[0].Refs, []string{"n7"}) ||
		!reflect.DeepEqual(examples[0].Previews, []string{`{"market_cap":"195094"}`}) ||
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
		"tool#1 status=`matches` query=`market_cap` page=`https://taostats.io/subnets/120` call_id=`net1` evidence_status=`refs_only_not_citable; read_required=true` requires_read=`true` citable=`false`",
		"refs: `n7`",
		`preview: {"market_cap":"195094"}`,
		"next: call browser_network_read",
		"tool#2 status=`no_matches` query=`validators`",
	} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
	}
}

func TestTraceSourceAccessExamplesCaptureNetworkReadContinuation(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		CallID: "read1",
		Tool:   "browser_network_read",
		Result: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\n" +
			"BODY_BYTES: 70 (offset 14, showing 12, omitted_before 14, omitted_after 44, next_offset 26)\n" +
			`3456789","ma` + "\n" +
			"[... 44 bytes omitted after this chunk; retry with offset=26, a narrower json_path, or max_bytes up to 65536 ...]\n",
		ExitCode: 0,
	}}}

	examples := trace.SourceAccessExamples(5)
	if len(examples) != 1 {
		t.Fatalf("SourceAccessExamples = %+v", examples)
	}
	ex := examples[0]
	if ex.BodyBytes != 70 ||
		ex.BodyOffset != 14 ||
		ex.ShowingBytes != 12 ||
		ex.OmittedBefore != 14 ||
		ex.OmittedAfter != 44 ||
		ex.NextOffset != 26 ||
		!ex.HasMore {
		t.Fatalf("network read continuation fields = %+v", ex)
	}

	timeline := renderDebugTimeline(BatchResult{BatchScenario: "network-read"}, BatchScenario{Prompt: "inspect dashboard"}, &trace)
	for _, want := range []string{
		"body_bytes=`70`",
		"body_offset=`14`",
		"showing=`12`",
		"next_offset=`26`",
	} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
	}
}

func TestTraceBrowserScrollExamplesCaptureBoundaryTelemetry(t *testing.T) {
	trace := Trace{Tools: []ToolCall{{
		CallID: "scroll1",
		Tool:   "browser_scroll",
		Result: strings.Join([]string{
			"SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence; snapshot_id=8",
			"PAGE TEXT:",
			"Market Cap",
			"SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
			"Next: scrolling did not move the page; use browser_network/browser_network_read for hidden XHR/fetch data.",
		}, "\n"),
		ExitCode: 0,
	}}}

	examples := trace.BrowserScrollExamples(5)
	if len(examples) != 1 {
		t.Fatalf("BrowserScrollExamples = %+v", examples)
	}
	ex := examples[0]
	if ex.ToolIndex != 1 ||
		ex.CallID != "scroll1" ||
		ex.URL != "https://taostats.io/subnets/120" ||
		ex.Direction != "down" ||
		ex.BeforeY != "1200" ||
		ex.AfterY != "1200" ||
		ex.MaxY != "1200" ||
		ex.Movement != "none" ||
		ex.Boundary != "bottom" ||
		ex.Status != "boundary" ||
		!strings.Contains(ex.SuggestedNextStep, "browser_network_read") ||
		!strings.Contains(ex.ResultPreview, "Market Cap") {
		t.Fatalf("browser scroll example = %+v", ex)
	}

	timeline := renderDebugTimeline(BatchResult{BatchScenario: "scroll"}, BatchScenario{Prompt: "scroll"}, &trace)
	for _, want := range []string{
		"## Browser Scrolls",
		"status=`boundary`",
		"movement=`none`",
		"boundary=`bottom`",
		"y=`1200->1200/1200`",
		"next: scrolling did not move",
	} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline missing %q:\n%s", want, timeline)
		}
	}
}
