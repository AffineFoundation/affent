package agent

import (
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestToolFailureKind(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "Error\nFailure: kind=blocked\nNext: use another source", want: "blocked"},
		{name: "with status", in: "Failure: kind=server_error, status=502", want: "server_error"},
		{name: "later line", in: "first\nFailure: status=403, kind=blocked", want: "blocked"},
		{name: "invalid", in: "Failure: kind=blocked; rm -rf", want: ""},
		{name: "missing", in: "Next: retry", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolFailureKind(c.in); got != c.want {
				t.Fatalf("toolFailureKind() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRecordToolFailureKind(t *testing.T) {
	var stats sse.ToolRuntimeStats
	recordToolFailureKind(&stats, "read_file", "Failure: kind=invalid_args", false)
	if len(stats.ToolFailureByKind) != 0 {
		t.Fatalf("successful outcome should not record failure kind: %+v", stats.ToolFailureByKind)
	}

	recordToolFailureKind(&stats, "read_file", "Failure: kind=invalid_args", true)
	recordToolFailureKind(&stats, "read_file", "Failure: kind=invalid_args", true)
	recordToolFailureKind(&stats, "read_file", "Failure: kind=timeout", true)
	if stats.ToolFailureByKind["invalid_args"] != 2 || stats.ToolFailureByKind["timeout"] != 1 {
		t.Fatalf("ToolFailureByKind = %+v", stats.ToolFailureByKind)
	}

	recordToolFailureKind(&stats, "web_fetch", "Failure: kind=blocked, status=403\nFailure: kind=loop_guard_repeated_failures", true)
	if stats.ToolFailureByKind["blocked"] != 1 || stats.ToolFailureByKind["loop_guard_repeated_failures"] != 1 {
		t.Fatalf("combined ToolFailureByKind = %+v", stats.ToolFailureByKind)
	}

	recordToolFailureKind(&stats, "web_fetch", "[dynamic page shell: URL=https://example]\nFailure: kind=dynamic_shell", false)
	if stats.ToolFailureByKind["dynamic_shell"] != 1 {
		t.Fatalf("no-evidence web_fetch should count failure kind: %+v", stats.ToolFailureByKind)
	}

	recordToolFailureKind(&stats, "browser_network", "BROWSER NETWORK EVIDENCE\nMATCHES: none\nFailure: kind=no_matches\nNext: wait once.", false)
	if stats.ToolFailureByKind["no_matches"] != 1 {
		t.Fatalf("no-evidence browser_network should count failure kind: %+v", stats.ToolFailureByKind)
	}
}

func TestRecordSourceAccessStats(t *testing.T) {
	var stats sse.ToolRuntimeStats
	recordSourceAccessStats(&stats, "SourceAccess: browser_rendered_url=https://example.com/page; page_text_below=verified_page_evidence\nPAGE TEXT:\nok")
	recordSourceAccessStats(&stats, "SourceAccess: browser_rendered_url=https://example.com/dynamic; page_text_below=verified_page_evidence\nPAGE DIAGNOSTICS:\n- empty_dynamic_metric_widgets: 2 visible custom metric widget(s) exposed no text value")
	recordSourceAccessStats(&stats, "SourceAccess: browser_rendered_url=https://example.com/search; page_text_below=search_results_discovery_only\nPAGE TEXT:\nresult")
	recordSourceAccessStats(&stats, "SourceAccess: browser_network_url=https://example.com/api; source_method=network_xhr_fetch\n{\"ok\":true}")
	recordSourceAccessStats(&stats, "plain tool output")

	if stats.SourceAccessResults != 4 {
		t.Fatalf("SourceAccessResults = %d, want 4", stats.SourceAccessResults)
	}
	if stats.SourceAccessVerified != 2 {
		t.Fatalf("SourceAccessVerified = %d, want 2", stats.SourceAccessVerified)
	}
	if stats.SourceAccessDiscoveryOnly != 1 {
		t.Fatalf("SourceAccessDiscoveryOnly = %d, want 1", stats.SourceAccessDiscoveryOnly)
	}
	if stats.SourceAccessNetwork != 1 {
		t.Fatalf("SourceAccessNetwork = %d, want 1", stats.SourceAccessNetwork)
	}
	if stats.SourceAccessDynamicPartial != 1 {
		t.Fatalf("SourceAccessDynamicPartial = %d, want 1", stats.SourceAccessDynamicPartial)
	}
}

func TestRecordMemoryUpdateStats(t *testing.T) {
	var stats sse.ToolRuntimeStats
	recordMemoryUpdateStats(&stats, "memory", []byte(`{"action":"add","target":"memory","topic":"markets"}`), `{"ok":true,"target":"memory","topic":"markets","message":"added"}`, false)
	recordMemoryUpdateStats(&stats, "memory", []byte(`{"action":"replace","target":"user"}`), `{"ok":true,"target":"user","message":"replaced"}`, false)
	recordMemoryUpdateStats(&stats, "memory", []byte(`{"action":"remove","target":"memory","topic":"old"}`), `{"ok":true,"target":"memory","topic":"old","message":"removed"}`, false)

	recordMemoryUpdateStats(&stats, "memory", []byte(`{"action":"search","query":"markets"}`), `{"ok":true}`, false)
	recordMemoryUpdateStats(&stats, "memory", []byte(`{"action":"add","content":"blocked"}`), `{"ok":false,"message":"blocked"}`, false)
	recordMemoryUpdateStats(&stats, "memory", []byte(`{"action":"add","content":"failed"}`), `{"ok":true}`, true)
	recordMemoryUpdateStats(&stats, "read_file", []byte(`{"action":"add"}`), `{"ok":true}`, false)

	if stats.MemoryUpdates != 3 || stats.MemoryUpdateAdd != 1 || stats.MemoryUpdateReplace != 1 || stats.MemoryUpdateRemove != 1 {
		t.Fatalf("memory update stats = %+v", stats)
	}
}

func TestRecordMemorySearchStatsCountsSearchCallsAndNoHitSearches(t *testing.T) {
	var stats sse.ToolRuntimeStats
	recordMemorySearchStats(&stats, "memory", []byte(`{"action":"search","query":"helm"}`), `{"ok":true,"target":"memory","results":[],"topics":[{"topic":"deploy","entries":1}]}`, false)

	recordMemorySearchStats(&stats, "memory", []byte(`{"action":"search","query":"helm"}`), `{"ok":true,"target":"memory","results":[{"topic":"deploy","snippet":"helm chart","score":1}]}`, false)
	recordMemorySearchStats(&stats, "memory", []byte(`{"action":"list"}`), `{"ok":true,"topics":[{"topic":"deploy","entries":1}]}`, false)
	recordMemorySearchStats(&stats, "memory", []byte(`{"action":"search"}`), `{"ok":false,"message":"blocked"}`, false)
	recordMemorySearchStats(&stats, "memory", []byte(`{"action":"search"}`), `{"ok":true,"results":[]}`, true)
	recordMemorySearchStats(&stats, "read_file", []byte(`{"action":"search"}`), `{"ok":true,"results":[]}`, false)

	if stats.MemorySearchCalls != 4 {
		t.Fatalf("MemorySearchCalls = %d, want 4: %+v", stats.MemorySearchCalls, stats)
	}
	if stats.MemorySearchMisses != 1 {
		t.Fatalf("MemorySearchMisses = %d, want 1: %+v", stats.MemorySearchMisses, stats)
	}
}

func TestMemoryUpdateMetaForResult(t *testing.T) {
	add := memoryUpdateMetaForResult("memory",
		[]byte(`{"action":"add","target":"memory","topic":"markets","content":"Alpha Coast reports use marker MEM-STOCK-73 for source-led confidence."}`),
		`{"ok":true,"target":"memory","topic":"markets","message":"added"}`,
		false,
	)
	if add == nil {
		t.Fatal("add memory update meta missing")
	}
	if add.Action != "add" || add.Target != "memory" || add.Topic != "markets" || add.Location != "memory:markets" ||
		add.Preview != "Alpha Coast reports use marker MEM-STOCK-73 for source-led confidence." ||
		add.NextPreview != add.Preview {
		t.Fatalf("add memory update meta = %+v", add)
	}

	replace := memoryUpdateMetaForResult("memory",
		[]byte(`{"action":"replace","target":"user","old_text":"prefers terse answers","content":"prefers concise answers with test evidence"}`),
		`{"ok":true,"target":"user","message":"replaced"}`,
		false,
	)
	if replace == nil || replace.Topic != "user" || replace.Location != "user:user" ||
		replace.PreviousPreview != "prefers terse answers" ||
		replace.NextPreview != "prefers concise answers with test evidence" ||
		replace.Preview != "prefers terse answers -> prefers concise answers with test evidence" {
		t.Fatalf("replace memory update meta = %+v", replace)
	}

	if got := memoryUpdateMetaForResult("memory", []byte(`{"action":"search","query":"markets"}`), `{"ok":true}`, false); got != nil {
		t.Fatalf("search should not produce memory update meta: %+v", got)
	}
	if got := memoryUpdateMetaForResult("memory", []byte(`{"action":"add","content":"blocked"}`), `{"ok":false}`, false); got != nil {
		t.Fatalf("failed memory response should not produce memory update meta: %+v", got)
	}
	if got := memoryUpdateMetaForResult("memory", []byte(`{"action":"add","content":"errored"}`), `{"ok":true}`, true); got != nil {
		t.Fatalf("errored memory tool should not produce memory update meta: %+v", got)
	}
}

func TestRecordSessionSearchStats(t *testing.T) {
	var stats sse.ToolRuntimeStats
	recordSessionSearchStats(&stats, "session_search", `{"query":"Alpha Coast","total":4,"results":[{"session_id":"market-alpha","matched_terms":["alpha","coast"],"context_included":true},{"session_id":"market-beta","matched_terms":["alpha"],"context_included":false},{"session_id":"market-plan","role":"plan","matched_terms":["plan"],"context_included":false},{"session_id":"market-loop","role":"loop","matched_terms":["loop"],"context_included":false}]}`, false)
	recordSessionSearchStats(&stats, "session_search", `{"query":"empty","total":0,"results":[],"recent_sessions":[{"session_id":"recent-a"},{"session_id":"recent-b"}]}`, false)
	recordSessionSearchStats(&stats, "session_search", `not json`, false)
	recordSessionSearchStats(&stats, "session_search", `{"total":1,"results":[{"matched_terms":["ignored"],"context_included":true}]}`, true)
	recordSessionSearchStats(&stats, "memory", `{"total":1}`, false)

	if stats.SessionSearchCalls != 4 {
		t.Fatalf("SessionSearchCalls = %d, want 4", stats.SessionSearchCalls)
	}
	if stats.SessionSearchResults != 4 {
		t.Fatalf("SessionSearchResults = %d, want 4", stats.SessionSearchResults)
	}
	if stats.SessionSearchContextHits != 3 {
		t.Fatalf("SessionSearchContextHits = %d, want 3", stats.SessionSearchContextHits)
	}
	if stats.SessionSearchMatchedTerms != 4 {
		t.Fatalf("SessionSearchMatchedTerms = %d, want 4", stats.SessionSearchMatchedTerms)
	}
	if stats.SessionSearchRecent != 2 {
		t.Fatalf("SessionSearchRecent = %d, want 2", stats.SessionSearchRecent)
	}
}

func TestToolFailureKindForOutcome(t *testing.T) {
	if got := toolFailureKindForOutcome("web_fetch", "fetch failed\nFailure: kind=blocked, status=403\nNext: use another source", true); got != "blocked" {
		t.Fatalf("hard failure kind = %q, want blocked", got)
	}
	if got := toolFailureKindForOutcome("web_fetch", "[empty response: URL=https://example]\nFailure: kind=empty_response", false); got != "empty_response" {
		t.Fatalf("no-evidence failure kind = %q, want empty_response", got)
	}
	if got := toolFailureKindForOutcome("web_search", "(no results)\nFailure: kind=no_results", false); got != "no_results" {
		t.Fatalf("no-results failure kind = %q, want no_results", got)
	}
	if got := toolFailureKindForOutcome("browser_network", "BROWSER NETWORK EVIDENCE\nMATCHES: none\nFailure: kind=no_matches", false); got != "no_matches" {
		t.Fatalf("browser-network no-match failure kind = %q, want no_matches", got)
	}
	if got := toolFailureKindForOutcome("read_file", "Failure: kind=blocked", false); got != "" {
		t.Fatalf("successful read_file content should not set FailureKind, got %q", got)
	}
}

func TestToolFailureKindsForOutcome(t *testing.T) {
	got := toolFailureKindsForOutcome("web_fetch", "fetch failed\nFailure: kind=blocked\n\nloop_guard\nFailure: kind=loop_guard_repeated_failed_input", true)
	want := []string{"blocked", "loop_guard_repeated_failed_input"}
	if len(got) != len(want) {
		t.Fatalf("toolFailureKindsForOutcome() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("toolFailureKindsForOutcome() = %#v, want %#v", got, want)
		}
	}
}

func TestTruncateToolResultForContextIncludesArtifactHint(t *testing.T) {
	got := truncateToolResultForContext("shell", strings.Repeat("x", 128), 16, ".affent/artifacts/tool-results/000001-c1.txt")
	if !strings.Contains(got, ".affent/artifacts/tool-results/000001-c1.txt") {
		t.Fatalf("expected artifact path hint in truncated result, got %q", got)
	}
	if !strings.Contains(got, "read_file") {
		t.Fatalf("expected read_file hint in truncated result, got %q", got)
	}
}
