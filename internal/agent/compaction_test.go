package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
)

// TestFormatEvent_Roles pins the per-role rendering the summarizer
// sees. Each <EVENT> block in the summarizer prompt comes from
// formatEvent; if the format silently changes (e.g. tool_calls
// dropped from assistant messages) the summary quality degrades
// without anything obvious going red.
func TestFormatEvent_Roles(t *testing.T) {
	cases := []struct {
		name string
		msg  ChatMessage
		want []string // substrings that must appear
	}{
		{
			name: "plain user",
			msg:  ChatMessage{Role: "user", Content: "what's the weather"},
			want: []string{"USER:", "what's the weather"},
		},
		{
			name: "assistant content + tool_calls",
			msg: ChatMessage{
				Role:    "assistant",
				Content: "let me look that up",
				ToolCalls: []ToolCall{{Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "weather", Arguments: `{"city":"sf"}`}}},
			},
			want: []string{"ASSISTANT", "let me look that up", "tool weather", `{"city":"sf"}`},
		},
		{
			name: "assistant reasoning surfaces as [thinking: ...]",
			msg: ChatMessage{
				Role:             "assistant",
				ReasoningContent: "I should call the weather API",
				Content:          "checking",
			},
			want: []string{"ASSISTANT", "[thinking:", "weather API", "checking"},
		},
		{
			name: "tool result names its tool",
			msg:  ChatMessage{Role: "tool", Name: "weather", Content: "59F"},
			want: []string{"TOOL_RESULT[weather]", "59F"},
		},
		{
			name: "unknown role falls through",
			msg:  ChatMessage{Role: "system", Content: "be helpful"},
			want: []string{"system:", "be helpful"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatEvent(c.msg)
			for _, s := range c.want {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in %q", s, got)
				}
			}
		})
	}
}

func TestFormatEvent_CompactsDelegationToolResults(t *testing.T) {
	t.Run("subagent result keeps report and metadata without raw JSON", func(t *testing.T) {
		raw := `{"report":"Conclusion:\nAffine SN120 evidence found.\nEvidence:\n- metrics/tao-app-snapshot.txt shows Price 0.06342 T.","ok":true,"turn_end_reason":"completed","mode":"research","child_session_id":"subagent_123","depth":1,"max_depth":2,"usage":{"input_tokens":100,"output_tokens":25},"tool_calls":[{"tool":"repo_search","args":{"query":"SN120"}},{"tool":"read_file","args":{"path":"metrics/tao-app-snapshot.txt"}}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: SubagentToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[subagent_run]",
			"ok=true mode=research",
			"child_session_id=subagent_123",
			"report:",
			"Affine SN120 evidence found",
			"tool_calls:",
			"repo_search query=SN120",
			"read_file path=metrics/tao-app-snapshot.txt",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact subagent result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"tool_calls"`) || strings.Contains(got, `"report"`) {
			t.Fatalf("compact subagent result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("focused task result keeps summary findings and warnings", func(t *testing.T) {
		raw := `{"task_type":"web_extract","ok":true,"summary":"extracted subnet snapshot","findings":[{"claim":"Affine is Bittensor SN120","evidence":"page body labels Affine as subnet 120","source":"https://www.tao.app/subnets/120","confidence":"high"},{"claim":"Subnet body price is 0.06342 T","evidence":"body row Price 0.06342 T","source":"https://www.tao.app/subnets/120"}],"warnings":["TAO top-bar price is not the subnet price"],"suggested_next":["verify validator count"],"child_session_id":"focused_123","turn_end_reason":"completed","depth":1,"usage":{"input_tokens":80,"output_tokens":20},"tool_calls":[{"tool":"web_fetch","args":{"url":"https://www.tao.app/subnets/120"}}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: FocusedTaskToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[run_task]",
			"ok=true task_type=web_extract",
			"summary: extracted subnet snapshot",
			"Affine is Bittensor SN120 source=https://www.tao.app/subnets/120",
			"TAO top-bar price is not the subnet price",
			"web_fetch url=https://www.tao.app/subnets/120",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact focused-task result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"findings"`) || strings.Contains(got, `"summary"`) {
			t.Fatalf("compact focused-task result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("memory result keeps durable update state without raw JSON", func(t *testing.T) {
		raw := `{"ok":true,"message":"entry added","target":"memory","topic":"markets","entries":["Alpha Coast reports use marker MEM-STOCK-73.","Use source-led confidence for market notes."],"usage":{"percent":12,"chars_used":98,"chars_limit":800,"entry_count":2}}`
		got := formatEvent(ChatMessage{Role: "tool", Name: MemoryToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[memory]",
			"ok=true target=memory topic=markets usage=12%,98/800 chars,2 entries",
			"message: entry added",
			"Alpha Coast reports use marker MEM-STOCK-73.",
			"Use source-led confidence for market notes.",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact memory result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"entries"`) || strings.Contains(got, `"usage"`) {
			t.Fatalf("compact memory result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("memory search result keeps snippets and freshness", func(t *testing.T) {
		raw := `{"ok":true,"message":"1 result(s)","target":"memory","results":[{"topic":"ops","snippet":"deploys use fly.io remote builders","score":2.5,"created_at":"2026-05-20T10:00:00Z"}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: MemoryToolName, Content: raw})
		for _, want := range []string{
			"message: 1 result(s)",
			"topic=ops created_at=2026-05-20T10:00:00Z score=2.500 deploys use fly.io remote builders",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact memory search result missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("plan result keeps step state without raw JSON", func(t *testing.T) {
		raw := `{"version":1,"updated_at":"2026-05-26T10:00:00Z","message":"updated step 2","steps":[{"text":"Inspect long-run trace pressure","status":"completed","evidence":["cmd/affenteval/main.go","go test ./cmd/affenteval"],"note":"gate added"},{"text":"Patch plan compaction summary","status":"in_progress","evidence":["internal/agent/compaction.go"]},{"text":"Run focused tests","status":"pending"}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: PlanToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[plan]",
			"message: updated step 2",
			"updated_at: 2026-05-26T10:00:00Z",
			"1. [completed] Inspect long-run trace pressure evidence=cmd/affenteval/main.go; go test ./cmd/affenteval note=gate added",
			"2. [in_progress] Patch plan compaction summary evidence=internal/agent/compaction.go",
			"3. [pending] Run focused tests",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact plan result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"steps"`) || strings.Contains(got, `"status"`) {
			t.Fatalf("compact plan result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("session search result keeps provenance without raw JSON", func(t *testing.T) {
		raw := `{"query":"Northstar Biotech Q3","total":1,"results":[{"session_id":"northstar-q3-recovery","turn_idx":2,"message_idx":5,"role":"assistant","snippet":"decision: recovery marker REC-Q3-19, risk label trial-delay, next action verify the FDA calendar.","score":5.5,"matched_terms":["northstar","biotech","q3"],"context_included":true,"mod_time":"2026-05-25T09:00:00Z"}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: SessionSearchToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[session_search]",
			"query: Northstar Biotech Q3",
			"total: 1 result(s)",
			"session=northstar-q3-recovery turn=2 message=5 role=assistant context=true mod_time=2026-05-25T09:00:00Z score=5.500 terms=northstar; biotech; q3 decision: recovery marker REC-Q3-19",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact session_search result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"results"`) || strings.Contains(got, `"session_id"`) {
			t.Fatalf("compact session_search result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("session search no-hit result keeps recent plan and loop anchors", func(t *testing.T) {
		raw := `{"query":"missing Alpha Coast marker","total":0,"results":[],"message":"no results. Next: retry from anchors.","recent_sessions":[{"session_id":"alpha-loop","mod_time":"2026-05-27T12:00:00Z","latest_user":"Analyze Alpha Coast recovery","latest_assistant":"final marker HIST-STOCK-44","plan":"plan_status: plan:1/2:active current_step: 2 [in_progress] Recheck Alpha Coast risk","loop":"loop_status: running current_situation: preserve Alpha Coast source evidence before citing values"}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: SessionSearchToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[session_search]",
			"query: missing Alpha Coast marker",
			"message: no results. Next: retry from anchors.",
			"recent_sessions:",
			"session=alpha-loop mod_time=2026-05-27T12:00:00Z user=Analyze Alpha Coast recovery assistant=final marker HIST-STOCK-44",
			"plan=plan_status: plan:1/2:active current_step: 2 [in_progress] Recheck Alpha Coast risk",
			"loop=loop_status: running current_situation: preserve Alpha Coast source evidence",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact session_search no-hit result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"recent_sessions"`) || strings.Contains(got, `"session_id"`) {
			t.Fatalf("compact session_search no-hit result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("session search no-hit result keeps checkpoint recovery anchors", func(t *testing.T) {
		raw := `{"query":"missing checkpoint recovery","total":0,"results":[],"message":"no results. Next: retry from anchors.","recent_sessions":[{"session_id":"checkpoint-recovery","mod_time":"2026-05-28T08:00:00Z","latest_user":"Run long loop until recovery","recovery":"loop_turn_checkpoint; end=max_turns; loop=longrun; tool_errors=2; loop_guards=1; forced_no_tools=1; memory_misses=1; session_search=1"}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: SessionSearchToolName, Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[session_search]",
			"query: missing checkpoint recovery",
			"recent_sessions:",
			"session=checkpoint-recovery mod_time=2026-05-28T08:00:00Z user=Run long loop until recovery",
			"recovery=loop_turn_checkpoint; end=max_turns; loop=longrun; tool_errors=2; loop_guards=1; forced_no_tools=1; memory_misses=1; session_search=1",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact session_search checkpoint recovery result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"recent_sessions"`) || strings.Contains(got, `"session_id"`) {
			t.Fatalf("compact session_search checkpoint recovery result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("web source result keeps source access and bounded evidence", func(t *testing.T) {
		raw := "SourceAccess: fetched_url=https://metrics.example/affine; requested_url=https://dashboard.example/affine; linked_urls_in_content=discovered_unverified_until_fetched\n" +
			"Affine SN120 metrics as of 2026-05-24T12:00:00Z: price $0.0632, market cap $195094, 24h volume $5001.\n" +
			strings.Repeat("extra market table row ", 240)
		got := formatEvent(ChatMessage{Role: "tool", Name: "web_fetch", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[web_fetch]",
			"source_access: fetched_url=https://metrics.example/affine requested_url=https://dashboard.example/affine",
			"body_preview:",
			"market cap $195094",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact web_fetch result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, strings.Repeat("extra market table row ", 120)) {
			t.Fatalf("compact web_fetch result should bound long page text:\n%s", got)
		}
	})

	t.Run("browser network read keeps json path source metadata", func(t *testing.T) {
		raw := "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n3; status=200; content_type=application/json; source_method=network_xhr_fetch\n" +
			"JSON_PATH: $.data.market_cap\n" +
			"BODY_BYTES: 8\n" +
			"\"195094\""
		got := formatEvent(ChatMessage{Role: "tool", Name: "browser_network_read", Content: raw})
		for _, want := range []string{
			"source_access: browser_network_url=https://taostats.io/api/subnets/120 requested_url=https://taostats.io/subnets/120 source_method=network_xhr_fetch ref=n3 http_status=200 content_type=application/json json_path=$.data.market_cap",
			"body_preview:\n\"195094\"",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact browser_network_read result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "BODY_BYTES") || strings.Contains(got, "JSON_PATH:") {
			t.Fatalf("compact browser_network_read result should fold transport headers into metadata:\n%s", got)
		}
	})

	t.Run("truncated source result keeps artifact recovery path", func(t *testing.T) {
		artifactPath := ".affent/artifacts/tool-results/000123-browser-network-read.txt"
		raw := "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n3; status=200; content_type=application/json; source_method=network_xhr_fetch\n" +
			"BODY_BYTES: 32000\n" +
			strings.Repeat("large metric row\n", 400) +
			"\n\n[... 4096 more bytes truncated from browser_network_read before model context.]\n" +
			"Use the saved artifact if you need the complete output: " + artifactPath + " (use read_file when this path is inside the workspace; in affentserve/WebUI, open the session artifact from the artifacts panel or /v1/sessions/{id}/artifacts)."
		got := formatEvent(ChatMessage{Role: "tool", Name: "browser_network_read", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[browser_network_read]",
			"source_access: browser_network_url=https://taostats.io/api/subnets/120",
			"artifact: " + artifactPath,
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact truncated source result missing %q:\n%s", want, got)
			}
		}
		if strings.Count(got, artifactPath) != 1 {
			t.Fatalf("compact truncated source result should include artifact path once:\n%s", got)
		}
		if strings.Contains(got, strings.Repeat("large metric row\n", 160)) {
			t.Fatalf("compact truncated source result should bound body preview:\n%s", got)
		}
	})

	t.Run("browser scroll keeps source access and boundary telemetry compact", func(t *testing.T) {
		raw := strings.Join([]string{
			"SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence; snapshot_id=8",
			"URL: https://taostats.io/subnets/120",
			"TITLE: SN120 Affine",
			"PAGE TEXT:",
			"Market Cap",
			"SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
			"Next: scrolling did not move the page; use browser_network/browser_network_read for hidden XHR/fetch data.",
		}, "\n")
		got := formatEvent(ChatMessage{Role: "tool", Name: "browser_scroll", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[browser_scroll]",
			"source_access: browser_rendered_url=https://taostats.io/subnets/120",
			"page_text_below=partial_dynamic_page_evidence",
			"rendered_status=partial_dynamic_page_evidence",
			"body_preview:",
			"SCROLL: direction=down",
			"movement=none",
			"browser_network/browser_network_read",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact browser_scroll result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "PAGE TEXT:") || strings.Contains(got, "SourceAccess:") {
			t.Fatalf("compact browser_scroll result should not expose raw scaffolding:\n%s", got)
		}
	})

	t.Run("browser network search keeps no-match recovery state", func(t *testing.T) {
		raw := "BROWSER NETWORK EVIDENCE\n" +
			"CURRENT_PAGE: https://taostats.io/subnets/120/statistics\n" +
			"query: \"market_cap\"\n" +
			"EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n" +
			"MATCHES: none\n" +
			"Failure: kind=no_matches\n" +
			"Next: wait for the page to load dynamic data, try a shorter label/entity/API-path query, interact with the relevant tab, or mark hidden fields unverified.\n"
		got := formatEvent(ChatMessage{Role: "tool", Name: "browser_network", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[browser_network]",
			"browser_network: current_page=https://taostats.io/subnets/120/statistics query=\"market_cap\" evidence_status=refs_only_not_citable; read_required=true match_status=none failure_kind=no_matches",
			"Next: wait for the page to load dynamic data",
			"mark hidden fields unverified",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact browser_network no-match result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "BROWSER NETWORK EVIDENCE") || strings.Contains(got, "CURRENT_PAGE:") {
			t.Fatalf("compact browser_network result should not expose raw scaffolding:\n%s", got)
		}
	})

	t.Run("browser network search keeps refs paths and bounded previews", func(t *testing.T) {
		raw := "BROWSER NETWORK EVIDENCE\n" +
			"CURRENT_PAGE: https://taostats.io/subnets/120/statistics\n" +
			"query: \"validator market cap\"\n" +
			"EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n" +
			"MATCHES:\n" +
			"- n7 status=200 resource=fetch content_type=application/json url=https://api.taostats.io/subnet/120/metrics\n" +
			"  preview: {\"subnet\":\"120\",\"market_cap\":\"195094\",\"validators\":42," + strings.Repeat("\"noise\":\"row\",", 120) + "}\n" +
			"  json_paths: $.data.market_cap=195094; $.data.validators=42\n" +
			"Next: call browser_network_read with the most relevant ref and json_path before citing values.\n"
		got := formatEvent(ChatMessage{Role: "tool", Name: "browser_network", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[browser_network]",
			"browser_network: current_page=https://taostats.io/subnets/120/statistics query=\"validator market cap\" evidence_status=refs_only_not_citable; read_required=true matches=1",
			"n7 status=200 resource=fetch content_type=application/json url=https://api.taostats.io/subnet/120/metrics",
			"preview: {\"subnet\":\"120\",\"market_cap\":\"195094\",\"validators\":42",
			"json_paths: $.data.market_cap=195094; $.data.validators=42",
			"Next: call browser_network_read",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact browser_network match result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, strings.Repeat("\"noise\":\"row\",", 80)) {
			t.Fatalf("compact browser_network result should bound long previews:\n%s", got)
		}
	})

	t.Run("file context result keeps file structure without raw JSON", func(t *testing.T) {
		raw := `{"path":"internal/agent/compaction.go","bytes":12000,"truncated":true,"lines":320,"query":"compact","head":[{"line":1,"text":"package agent"}],"matches":[{"start_line":347,"end_line":352,"hit_line":349,"text":"func compactToolResultForSummary(toolName, content string) string {"}],"tail":[{"line":320,"text":"}"}],"symbols":[{"name":"compactToolResultForSummary","kind":"func","line":347,"signature":"func compactToolResultForSummary(toolName, content string) string"}]}`
		got := formatEvent(ChatMessage{Role: "tool", Name: "file_context", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[file_context]",
			"file_context: path=internal/agent/compaction.go bytes=12000 lines=320 truncated=true query=compact",
			"symbols:",
			"line=347 kind=func name=compactToolResultForSummary signature=func compactToolResultForSummary(toolName, content string) string",
			"matches:",
			"lines=347-352 hit=349 func compactToolResultForSummary",
			"head:",
			"L1: package agent",
			"tail:",
			"L320: }",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact file_context result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, `"matches"`) || strings.Contains(got, `"head"`) {
			t.Fatalf("compact file_context result should not expose raw JSON scaffolding:\n%s", got)
		}
	})

	t.Run("read file result bounds long file bodies", func(t *testing.T) {
		raw := "package agent\n" + strings.Repeat("func noisy() {}\n", 300)
		got := formatEvent(ChatMessage{Role: "tool", Name: "read_file", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[read_file]",
			"file_body_preview:",
			"package agent",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact read_file result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, strings.Repeat("func noisy() {}\n", 180)) {
			t.Fatalf("compact read_file result should bound long file text:\n%s", got)
		}
	})

	t.Run("shell result preserves exit status when output is long", func(t *testing.T) {
		raw := "STDOUT:\n" + strings.Repeat("build log line\n", 240) + "\nSTDERR:\nfailed assertion\n[exit 1]"
		got := formatEvent(ChatMessage{Role: "tool", Name: "shell", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[shell]",
			"exit: [exit 1]",
			"output_preview:",
			"build log line",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact shell result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, strings.Repeat("build log line\n", 180)) {
			t.Fatalf("compact shell result should bound long output:\n%s", got)
		}
	})

	t.Run("repo search result is bounded text preview", func(t *testing.T) {
		raw := "found 240 hit(s) in 12 file(s)\n" + strings.Repeat("internal/agent/compaction.go:347: compact helper hit\n", 200)
		got := formatEvent(ChatMessage{Role: "tool", Name: "repo_search", Content: raw})
		for _, want := range []string{
			"TOOL_RESULT[repo_search]",
			"text_preview:",
			"found 240 hit(s) in 12 file(s)",
			"internal/agent/compaction.go:347",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("compact repo_search result missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, strings.Repeat("internal/agent/compaction.go:347: compact helper hit\n", 120)) {
			t.Fatalf("compact repo_search result should bound long search output:\n%s", got)
		}
	})
}

// TestTruncateChars pins the byte-cap + UTF-8-safe truncation +
// "...(truncated)" marker. Called both from formatEvent and from
// the summarizer prompt body — a regression would silently truncate
// mid-rune and leak invalid UTF-8 into the summarizer LLM call.
func TestTruncateChars(t *testing.T) {
	t.Run("under limit unchanged", func(t *testing.T) {
		if got := truncateChars("hello", 100); got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
	})
	t.Run("over limit gets marker", func(t *testing.T) {
		got := truncateChars(strings.Repeat("a", 100), 30)
		if !strings.HasSuffix(got, "...(truncated)") {
			t.Errorf("missing truncation marker: %q", got)
		}
	})
	t.Run("multibyte boundary doesn't split rune", func(t *testing.T) {
		// "你" is 3 bytes. Cap at 2 should align back to 0.
		got := truncateChars("你好", 2)
		if !strings.HasSuffix(got, "...(truncated)") {
			t.Errorf("missing marker: %q", got)
		}
		head := strings.TrimSuffix(got, "...(truncated)")
		for _, r := range head {
			if r == 0xFFFD {
				t.Errorf("produced invalid UTF-8: %q", got)
			}
		}
	})
}

// TestLLMSummaryCompactor_Compact_Real drives the REAL Compact()
// method (not the stub at the top of this file) through a fake LLM.
// The stub-based tests cover the slicing logic; this one pins that
// the live method actually invokes the LLM and threads its response
// into the synthetic summary message at the right slot.
func TestLLMSummaryCompactor_Compact_Real(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		lines := []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"FAKE SUMMARY"},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	llm := NewLLMClient(srv.URL, "", "fake")

	mk := func(role, content string) ChatMessage {
		return ChatMessage{Role: role, Content: content}
	}

	t.Run("nil LLM rejected", func(t *testing.T) {
		c := &LLMSummaryCompactor{TriggerMsgs: 0}
		_, err := c.Compact(context.Background(), []ChatMessage{mk("user", "x")})
		if err == nil {
			t.Error("nil LLM must error")
		}
	})

	t.Run("below trigger is no-op", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 100, KeepFirst: 2, KeepLast: 5}
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "q"),
			mk("assistant", "a"),
		}
		got, _ := c.Compact(context.Background(), msgs)
		if len(got) != len(msgs) {
			t.Errorf("below-trigger compact must not change len; got %d want %d", len(got), len(msgs))
		}
	})

	t.Run("byte trigger compacts large tool arguments below message trigger", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 100, TriggerBytes: 512, KeepFirst: 1, KeepLast: 2}
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "create a file"),
			{
				Role:    "assistant",
				Content: "writing file",
				ToolCalls: []ToolCall{{
					ID:   "call_large",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "write_file", Arguments: `{"path":"big.txt","content":"` + strings.Repeat("x", 2048) + `"}`},
				}},
			},
			{Role: "tool", Name: "write_file", ToolCallID: "call_large", Content: "ok"},
			mk("assistant", "done"),
			mk("user", "continue"),
		}
		got, err := c.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) >= len(msgs) {
			t.Fatalf("byte-triggered compaction did not shorten messages: got %d want < %d", len(got), len(msgs))
		}
		foundSummary := false
		for _, msg := range got {
			if msg.Role == "user" && strings.HasPrefix(msg.Content, summaryPrefix) {
				foundSummary = true
				break
			}
		}
		if !foundSummary {
			t.Fatalf("byte-triggered compaction missing summary message: %+v", got)
		}
	})

	t.Run("byte pressure reduces tail retention on short huge logs", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 100, TriggerBytes: 512, KeepFirst: 1, KeepLast: 10}
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "continue the coding task"),
			mk("assistant", strings.Repeat("large historical tool result ", 256)),
			mk("user", "next step"),
			mk("assistant", "short progress"),
			mk("user", "tail1"),
			mk("assistant", "tail2"),
			mk("user", "tail3"),
		}
		got, err := c.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) >= len(msgs) {
			t.Fatalf("byte-pressure compaction did not shorten short log: got %d want < %d", len(got), len(msgs))
		}
		if ApproximateConversationBytes(got) >= ApproximateConversationBytes(msgs) {
			t.Fatalf("byte-pressure compaction did not reduce bytes: before=%d after=%d", ApproximateConversationBytes(msgs), ApproximateConversationBytes(got))
		}
		if got[2].Role != "user" || !strings.HasPrefix(got[2].Content, summaryPrefix) {
			t.Fatalf("byte-pressure compaction missing summary at adjusted boundary: %+v", got)
		}
	})

	t.Run("above trigger folds middle into one summary msg", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 0, KeepFirst: 2, KeepLast: 3}
		// 1 system + 2 head + 6 middle + 3 tail = 12.
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "head1"),
			mk("assistant", "head2"),
			mk("user", "mid1"),
			mk("assistant", "mid2"),
			mk("user", "mid3"),
			mk("assistant", "mid4"),
			mk("user", "mid5"),
			mk("assistant", "mid6"),
			mk("user", "tail1"),
			mk("assistant", "tail2"),
			mk("user", "tail3"),
		}
		got, err := c.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		// Expect 1 system + 2 head + 1 summary + 3 tail = 7.
		if len(got) != 7 {
			t.Fatalf("expected 7 msgs after compaction, got %d: %+v", len(got), got)
		}
		if got[3].Role != "user" || !strings.HasPrefix(got[3].Content, summaryPrefix) {
			t.Errorf("position 3 must be synthetic summary; got role=%q content=%q",
				got[3].Role, got[3].Content)
		}
		if !strings.Contains(got[3].Content, "FAKE SUMMARY") {
			t.Errorf("LLM response must be embedded in summary content; got %q", got[3].Content)
		}
		if got[1].Content != "head1" || got[6].Content != "tail3" {
			t.Errorf("head/tail bookends wrong: head=%q tail=%q", got[1].Content, got[6].Content)
		}
	})

	t.Run("preserves loop protocol anchor when LLM summary omits it", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 0, KeepFirst: 1, KeepLast: 1}
		loopBlock := strings.Join([]string{
			"AFFENT LOOP PROTOCOL:",
			"feed_mode=digest feed_number=4 protocol_path=.affent/loops/longrun/LOOP.md",
			"loop_id=longrun status=running protocol_feeds=4 last_feed=digest",
			"plan_label=plan:1/3:active plan_step_index=2 plan_step_status=in_progress",
			"plan_current_step: verify browser evidence",
			"",
			"# Loop",
			"Keep long-run evidence anchored.",
		}, "\n")
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "head"),
			mk("system", loopBlock),
			mk("assistant", "middle"),
			mk("user", "tail"),
		}
		got, err := c.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 || got[2].Role != "user" || !strings.HasPrefix(got[2].Content, summaryPrefix) {
			t.Fatalf("expected synthetic summary at index 2, got %+v", got)
		}
		summary := got[2].Content
		for _, want := range []string{
			"FAKE SUMMARY",
			"LOOP_PROTOCOL: active",
			"path=.affent/loops/longrun/LOOP.md",
			"mode=digest",
			"feed=4",
			"plan=plan:1/3:active",
			"current=2:in_progress",
			`step="verify browser evidence"`,
			"reload LOOP.md",
		} {
			if !strings.Contains(summary, want) {
				t.Fatalf("summary missing %q:\n%s", want, summary)
			}
		}
	})
}

func TestRenderSummaryPromptCapsMiddleEventsByPromptBudget(t *testing.T) {
	events := []ChatMessage{
		{Role: "assistant", Content: "OLD_MARKER " + strings.Repeat("old ", 200)},
		{Role: "assistant", Content: "MID_MARKER " + strings.Repeat("mid ", 200)},
		{Role: "assistant", Content: "NEW_MARKER " + strings.Repeat("new ", 200)},
	}

	got := renderSummaryPrompt("Track concise state.", "", events, 2048, 700)
	if strings.Contains(got, "OLD_MARKER") || strings.Contains(got, "MID_MARKER") {
		t.Fatalf("summary prompt should drop older middle events under budget:\n%s", got)
	}
	if !strings.Contains(got, "NEW_MARKER") {
		t.Fatalf("summary prompt should retain newest middle event:\n%s", got)
	}
	if len(got) > 900 {
		t.Fatalf("summary prompt len = %d, want bounded near budget", len(got))
	}
}

func TestLLMSummaryCompactorRetriesWithSmallerInputAfterContextOverflow(t *testing.T) {
	var requestBodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("messages = %d, want 1", len(req.Messages))
		}
		requestBodies = append(requestBodies, req.Messages[0].Content)
		if len(requestBodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"prompt is too long"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"RETRY SUMMARY"},"finish_reason":"stop"}]}` + "\n\n"))
		fl.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	events := make([]ChatMessage, 0, 8)
	for i := 0; i < 8; i++ {
		events = append(events, ChatMessage{Role: "assistant", Content: fmt.Sprintf("event-%d %s", i, strings.Repeat("payload ", 200))})
	}
	c := &LLMSummaryCompactor{
		LLM:            NewLLMClient(srv.URL, "", "fake"),
		MaxPromptBytes: -1,
	}
	got, err := c.summarize(context.Background(), "", events)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if got != "RETRY SUMMARY" {
		t.Fatalf("summary = %q", got)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("requests = %d, want one retry after context overflow", len(requestBodies))
	}
	if len(requestBodies[1]) >= len(requestBodies[0]) {
		t.Fatalf("retry prompt did not shrink: first=%d second=%d", len(requestBodies[0]), len(requestBodies[1]))
	}
	if strings.Contains(requestBodies[1], "event-0") || !strings.Contains(requestBodies[1], "event-7") {
		t.Fatalf("retry prompt should drop oldest events and keep recent state:\n%s", requestBodies[1])
	}
}

func TestBackUpToSafeBoundary(t *testing.T) {
	// Sequence: assistant(tool_calls) → tool → tool → assistant → user
	msgs := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolCallID: "a", Content: "ra"},
		{Role: "tool", ToolCallID: "b", Content: "rb"},
		{Role: "assistant", Content: "thinking aloud"},
		{Role: "user", Content: "next"},
	}
	for in, want := range map[int]int{
		0: 0, // already at start
		2: 0, // mid tool reply chain — back up past tools then past owner
		3: 3, // assistant (no tool_calls) is a safe boundary, no back-up
		4: 4, // user is safe
	} {
		got := backUpToSafeBoundary(msgs, in)
		if got != want {
			t.Errorf("backUpToSafeBoundary(_, %d) = %d, want %d", in, got, want)
		}
	}
}

// stubCompactor replicates the rolling Compact() flow but stubs out the
// LLM call. Lets us exercise the head/middle/tail split + previous
// summary detection deterministically.
type stubCompactor struct {
	*LLMSummaryCompactor
	summary string
}

func (s *stubCompactor) Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	keepFirst := s.KeepFirst
	if keepFirst <= 0 {
		keepFirst = 2
	}
	keepLast := s.KeepLast
	if keepLast <= 0 {
		keepLast = 10
	}
	if s.TriggerMsgs > 0 && len(msgs) <= s.TriggerMsgs {
		return msgs, nil
	}

	sysHead := 0
	for sysHead < len(msgs) && msgs[sysHead].Role == "system" {
		sysHead++
	}
	if len(msgs)-sysHead <= keepFirst+keepLast+1 {
		return msgs, nil
	}

	headEnd := forwardToSafeBoundary(msgs, sysHead+keepFirst)
	summaryEnd := headEnd
	if headEnd < len(msgs) {
		if m := msgs[headEnd]; m.Role == "user" && strings.HasPrefix(m.Content, summaryPrefix) {
			summaryEnd = headEnd + 1
		}
	}
	tailStart := backUpToSafeBoundary(msgs, len(msgs)-keepLast)
	if tailStart <= summaryEnd {
		return msgs, nil
	}
	middle := msgs[summaryEnd:tailStart]
	if len(middle) == 0 {
		return msgs, nil
	}

	out := make([]ChatMessage, 0, headEnd+1+(len(msgs)-tailStart))
	out = append(out, msgs[:headEnd]...)
	out = append(out, ChatMessage{Role: "user", Content: summaryPrefix + s.summary})
	out = append(out, msgs[tailStart:]...)
	return out, nil
}

func TestCompact_BelowThreshold_NoOp(t *testing.T) {
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 10, TriggerMsgs: 100},
		summary:             "S",
	}
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(msgs) {
		t.Errorf("below threshold: expected no change, got len=%d", len(out))
	}
}

func TestCompact_PreservesHeadAndTail(t *testing.T) {
	// system + 20 alternating user/assistant. KeepFirst=1, KeepLast=4 →
	// system + 1 head + 1 summary + 4 tail = 7.
	msgs := []ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "msg" + string(rune('a'+i))})
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 4},
		summary:             "earlier work",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(out) != 1+1+1+4 {
		t.Fatalf("unexpected len: got %d, want 7", len(out))
	}
	if out[0].Role != "system" {
		t.Errorf("head[0] should be system; got %q", out[0].Role)
	}
	if out[1].Content != "msga" {
		t.Errorf("first non-system message should be preserved verbatim; got %q", out[1].Content)
	}
	if !strings.Contains(out[2].Content, summaryPrefix) {
		t.Errorf("expected synthetic summary user message at index 2; got %+v", out[2])
	}
	last := msgs[len(msgs)-4:]
	for i, m := range last {
		if out[3+i].Content != m.Content {
			t.Errorf("tail[%d]: got %q, want %q", i, out[3+i].Content, m.Content)
		}
	}
}

func TestLoopMaybeCompactPublishesContextCompacted(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	msgs := []ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "msg" + string(rune('a'+i))})
	}
	if err := conv.Replace(msgs); err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 8)
	loop := &Loop{
		Conv:   conv,
		Events: events,
		Compactor: &stubCompactor{
			LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 4},
			summary:             "earlier work",
		},
	}

	if !loop.maybeCompact(context.Background(), "turn-1", false) {
		t.Fatal("maybeCompact should report a successful compaction")
	}

	var payload sse.ContextCompactPayload
	select {
	case ev := <-events:
		if ev.Type != sse.TypeContextCompact {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextCompact)
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode context.compacted: %v", err)
		}
	default:
		t.Fatal("expected context.compacted event")
	}
	if payload.TurnID != "turn-1" || payload.BeforeMessages != len(msgs) || payload.AfterMessages >= payload.BeforeMessages || payload.RemovedMessages != payload.BeforeMessages-payload.AfterMessages {
		t.Fatalf("payload = %+v, want before/after/removal metadata", payload)
	}
	if payload.BeforeBytes <= payload.AfterBytes || payload.ReducedBytes != payload.BeforeBytes-payload.AfterBytes {
		t.Fatalf("payload = %+v, want byte reduction metadata", payload)
	}
	if payload.Reactive || payload.Reason != "threshold" || !payload.SummaryPresent || payload.SummaryBytes != len("earlier work") || payload.SummaryPreview != "earlier work" {
		t.Fatalf("payload = %+v, want proactive threshold summary metadata", payload)
	}
	if payload.LoopProtocolAnchor != "" {
		t.Fatalf("payload should not expose loop anchor when summary has none: %+v", payload)
	}
}

func TestLoopMaybeCompactPublishesLoopProtocolAnchor(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	loopDir := t.TempDir()
	protocolPath := filepath.Join(loopDir, "LOOP.md")
	if err := os.WriteFile(protocolPath, []byte("# Loop\n\n## North Star\n\nRecover after compaction."), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "msg" + string(rune('a'+i))})
	}
	if err := conv.Replace(msgs); err != nil {
		t.Fatal(err)
	}
	anchor := `LOOP_PROTOCOL: active path=.affent/loops/longrun/LOOP.md mode=digest feed=4 feeds=4 loop_id=longrun status=running plan=plan:1/3:active current=2:in_progress step="verify browser evidence"; reload LOOP.md when needed.`
	events := make(chan sse.Event, 8)
	loop := &Loop{
		Conv:             conv,
		Events:           events,
		LoopProtocolPath: protocolPath,
		Compactor: &stubCompactor{
			LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 4},
			summary:             "earlier work\n" + anchor,
		},
	}

	if !loop.maybeCompact(context.Background(), "turn-loop", true) {
		t.Fatal("maybeCompact should report a successful compaction")
	}

	var payload sse.ContextCompactPayload
	select {
	case ev := <-events:
		if ev.Type != sse.TypeContextCompact {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextCompact)
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode context.compacted: %v", err)
		}
	default:
		t.Fatal("expected context.compacted event")
	}
	if !payload.Reactive || payload.Reason != "context_overflow" {
		t.Fatalf("payload = %+v, want reactive context_overflow", payload)
	}
	if payload.LoopProtocolAnchor != anchor {
		t.Fatalf("LoopProtocolAnchor = %q, want %q", payload.LoopProtocolAnchor, anchor)
	}
	state, found, err := loopstate.ReadState(filepath.Join(loopDir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if !state.NeedsFullProtocolFeed || state.ContextCompactions != 1 || state.LastCompactionReason != "context_overflow" || !state.LastCompactionReactive {
		t.Fatalf("loop protocol state after compaction = %+v", state)
	}
}

type sameCountByteCompactor struct{}

func (sameCountByteCompactor) Compact(context.Context, []ChatMessage) ([]ChatMessage, error) {
	return []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: summaryPrefix + "short summary"},
		{Role: "assistant", Content: "tail"},
	}, nil
}

func TestLoopMaybeCompactAcceptsByteReductionWithoutMessageReduction(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("large prior context ", 512)},
		{Role: "assistant", Content: "tail"},
	}
	if err := conv.Replace(msgs); err != nil {
		t.Fatal(err)
	}
	events := make(chan sse.Event, 8)
	loop := &Loop{
		Conv:      conv,
		Events:    events,
		Compactor: sameCountByteCompactor{},
	}

	if !loop.maybeCompact(context.Background(), "turn-bytes", false) {
		t.Fatal("maybeCompact should accept byte reduction even when message count is unchanged")
	}

	var payload sse.ContextCompactPayload
	select {
	case ev := <-events:
		if ev.Type != sse.TypeContextCompact {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextCompact)
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode context.compacted: %v", err)
		}
	default:
		t.Fatal("expected context.compacted event")
	}
	if payload.BeforeMessages != payload.AfterMessages || payload.RemovedMessages != 0 {
		t.Fatalf("payload = %+v, want same message count with no removed messages", payload)
	}
	if payload.BeforeBytes <= payload.AfterBytes || payload.ReducedBytes <= 0 {
		t.Fatalf("payload = %+v, want positive byte reduction", payload)
	}
	if got := conv.Snapshot(); len(got) != 3 || strings.Contains(got[1].Content, "large prior context") {
		t.Fatalf("conversation was not replaced with compacted byte-smaller state: %+v", got)
	}
}

func TestLoopMaybeCompactForRequestPressureIncludesToolSchemas(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "initial task"},
		{Role: "assistant", Content: "historical progress"},
		{Role: "user", Content: "continue"},
	}
	if err := conv.Replace(msgs); err != nil {
		t.Fatal(err)
	}
	var toolDef ToolDef
	toolDef.Type = "function"
	toolDef.Function.Name = "large_schema_tool"
	toolDef.Function.Description = strings.Repeat("schema pressure ", 80)
	toolDef.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	events := make(chan sse.Event, 8)
	loop := &Loop{
		Conv:                      conv,
		Events:                    events,
		CompactTriggerInputTokens: 32,
		Compactor:                 sameCountByteCompactor{},
	}

	if !loop.maybeCompactForRequestPressure(context.Background(), "turn-pressure", []ToolDef{toolDef}) {
		t.Fatal("request-pressure compaction should run when tool schemas push estimated input tokens over the trigger")
	}

	var payload sse.ContextCompactPayload
	select {
	case ev := <-events:
		if ev.Type != sse.TypeContextCompact {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextCompact)
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode context.compacted: %v", err)
		}
	default:
		t.Fatal("expected context.compacted event")
	}
	if payload.Reactive || payload.Reason != "estimated_context_pressure" {
		t.Fatalf("payload = %+v, want proactive estimated_context_pressure", payload)
	}
	if payload.EstimatedInputTokens < payload.TriggerInputTokens || payload.TriggerInputTokens != 32 {
		t.Fatalf("payload = %+v, want request-pressure token policy metadata", payload)
	}
	if payload.AfterEstimatedInputTokens != EstimateRequestInputTokens(conv.Snapshot(), []ToolDef{toolDef}) ||
		payload.AfterEstimatedInputTokens <= 0 {
		t.Fatalf("payload = %+v, want post-compaction request pressure estimate", payload)
	}
	if got := conv.Snapshot(); len(got) != 3 || !strings.Contains(got[1].Content, "short summary") {
		t.Fatalf("conversation was not compacted through request pressure: %+v", got)
	}
}

type stagedPreRequestCompactor struct {
	calls int32
}

func (c *stagedPreRequestCompactor) Compact(context.Context, []ChatMessage) ([]ChatMessage, error) {
	call := atomic.AddInt32(&c.calls, 1)
	switch call {
	case 1:
		return []ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: summaryPrefix + "threshold summary " + strings.Repeat("STILL_PRESSURE ", 80)},
			{Role: "assistant", Content: "tail"},
		}, nil
	case 2:
		return []ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: summaryPrefix + "FINAL_SMALL_SUMMARY"},
			{Role: "assistant", Content: "tail"},
		}, nil
	default:
		return nil, fmt.Errorf("unexpected compaction call %d", call)
	}
}

type worseningRequestPressureCompactor struct {
	calls int32
}

func (c *worseningRequestPressureCompactor) Compact(context.Context, []ChatMessage) ([]ChatMessage, error) {
	call := atomic.AddInt32(&c.calls, 1)
	if call > 1 {
		return nil, fmt.Errorf("unexpected compaction call %d", call)
	}
	return []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: summaryPrefix + strings.Repeat("LARGER_SUMMARY ", 700)},
		{Role: "assistant", Content: "tail"},
	}, nil
}

func TestLoopCompactBeforeRequestRejectsNonImprovingPressureCompaction(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	original := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("OLD_CONTEXT ", 256)},
		{Role: "assistant", Content: "historical progress"},
		{Role: "user", Content: "continue"},
	}
	if err := conv.Replace(original); err != nil {
		t.Fatal(err)
	}
	var toolDef ToolDef
	toolDef.Type = "function"
	toolDef.Function.Name = "pressure_tool"
	toolDef.Function.Description = strings.Repeat("schema pressure ", 80)
	toolDef.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	events := make(chan sse.Event, 8)
	compactor := &worseningRequestPressureCompactor{}
	loop := &Loop{
		Conv:                      conv,
		Events:                    events,
		CompactTriggerInputTokens: 500,
		Compactor:                 compactor,
	}

	loop.compactBeforeRequest(context.Background(), "turn-diminishing", []ToolDef{toolDef})

	if got := atomic.LoadInt32(&compactor.calls); got != 1 {
		t.Fatalf("compaction calls = %d, want one call after diminishing return", got)
	}
	if got := conv.Snapshot(); !reflect.DeepEqual(got, original) {
		t.Fatalf("conversation changed after non-improving compaction: %+v", got)
	}
	select {
	case ev := <-events:
		if ev.Type != sse.TypeContextCompactSkipped {
			t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeContextCompactSkipped)
		}
		var payload sse.ContextCompactSkippedPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("decode context.compaction_skipped: %v", err)
		}
		if payload.Cause != "request_pressure_not_reduced" ||
			payload.Reason != "estimated_context_pressure" ||
			payload.AfterEstimatedInputTokens < payload.EstimatedInputTokens ||
			payload.TriggerInputTokens != 500 {
			t.Fatalf("context.compaction_skipped payload = %+v", payload)
		}
	default:
		t.Fatal("missing context.compaction_skipped event")
	}
}

func TestLoopCompactBeforeRequestPrioritizesRequestPressure(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("OLD_CONTEXT ", 512)},
		{Role: "assistant", Content: "historical progress"},
		{Role: "user", Content: "continue"},
	}
	if err := conv.Replace(msgs); err != nil {
		t.Fatal(err)
	}
	var toolDef ToolDef
	toolDef.Type = "function"
	toolDef.Function.Name = "pressure_tool"
	toolDef.Function.Description = strings.Repeat("schema pressure ", 80)
	toolDef.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	events := make(chan sse.Event, 8)
	compactor := &stagedPreRequestCompactor{}
	loop := &Loop{
		Conv:                      conv,
		Events:                    events,
		CompactTriggerInputTokens: 500,
		Compactor:                 compactor,
	}

	loop.compactBeforeRequest(context.Background(), "turn-preflight", []ToolDef{toolDef})

	if got := atomic.LoadInt32(&compactor.calls); got != 2 {
		t.Fatalf("compaction calls = %d, want bounded request-pressure passes", got)
	}
	got := conv.Snapshot()
	if len(got) != 3 || !strings.Contains(got[1].Content, "FINAL_SMALL_SUMMARY") || strings.Contains(got[1].Content, "STILL_PRESSURE") {
		t.Fatalf("conversation after pre-request compaction = %+v, want final compact summary", got)
	}
	wantReasons := []string{"estimated_context_pressure", "estimated_context_pressure"}
	for i, want := range wantReasons {
		select {
		case ev := <-events:
			if ev.Type != sse.TypeContextCompact {
				t.Fatalf("event[%d] type = %q, want %q", i, ev.Type, sse.TypeContextCompact)
			}
			var payload sse.ContextCompactPayload
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("decode context.compacted[%d]: %v", i, err)
			}
			if payload.Reason != want {
				t.Fatalf("event[%d] reason = %q, want %q", i, payload.Reason, want)
			}
		default:
			t.Fatalf("missing context.compacted event %d", i)
		}
	}
}

func TestCompactTriggerInputTokensForPolicy(t *testing.T) {
	if got := CompactTriggerInputTokensForPolicy(4096, 100_000, 80, DefaultSummaryTriggerInputTokens); got != 4096 {
		t.Fatalf("explicit trigger = %d, want 4096", got)
	}
	if got := CompactTriggerInputTokensForPolicy(-1, 100_000, 80, DefaultSummaryTriggerInputTokens); got != 0 {
		t.Fatalf("disabled trigger = %d, want 0", got)
	}
	if got := CompactTriggerInputTokensForPolicy(0, 100_000, 80, DefaultSummaryTriggerInputTokens); got != 80_000 {
		t.Fatalf("window-derived trigger = %d, want 80000", got)
	}
	if got := CompactTriggerInputTokensForPolicy(0, 100_000, 0, DefaultSummaryTriggerInputTokens); got != 80_000 {
		t.Fatalf("default percent trigger = %d, want 80000", got)
	}
	if got := CompactTriggerInputTokensForPolicy(0, 0, 80, DefaultSummaryTriggerInputTokens); got != DefaultSummaryTriggerInputTokens {
		t.Fatalf("fallback trigger = %d, want %d", got, DefaultSummaryTriggerInputTokens)
	}
	if got := CompactTriggerInputTokensForModelPolicy(0, 100_000, 80, 30_000, DefaultSummaryTriggerInputTokens); got != 70_000 {
		t.Fatalf("output-reserved trigger = %d, want 70000", got)
	}
	if got := CompactTriggerInputTokensForModelPolicy(0, 100_000, 80, 4_000, DefaultSummaryTriggerInputTokens); got != 80_000 {
		t.Fatalf("small output reserve should not lower 80%% trigger; got %d, want 80000", got)
	}
	if got := CompactTriggerInputTokensForModelPolicy(4096, 100_000, 80, 30_000, DefaultSummaryTriggerInputTokens); got != 4096 {
		t.Fatalf("explicit output-reserved trigger = %d, want 4096", got)
	}
	if got := ClampAutoCompactTokenLimit(120_000, 131_072, 0, 0); got != 104_857 {
		t.Fatalf("clamped auto compact limit = %d, want default 80%% policy 104857", got)
	}
	if got := ClampAutoCompactTokenLimit(90_000, 131_072, 0, 0); got != 90_000 {
		t.Fatalf("provider lower auto compact limit = %d, want 90000", got)
	}
	if got := ClampAutoCompactTokenLimit(90_000, 100_000, 80, 30_000); got != 70_000 {
		t.Fatalf("output-reserved auto compact limit = %d, want 70000", got)
	}
	if got := CompactTriggerBytesForModelPolicy(0, 100_000, 80, 30_000, DefaultSummaryTriggerBytes); got != 280_000 {
		t.Fatalf("output-reserved byte trigger = %d, want 280000", got)
	}
	if got := CompactTriggerBytesForModelPolicy(4096, 100_000, 80, 30_000, DefaultSummaryTriggerBytes); got != DefaultSummaryTriggerBytes {
		t.Fatalf("explicit request-input threshold should leave byte trigger at fallback; got %d want %d", got, DefaultSummaryTriggerBytes)
	}
	if got := SummaryPromptMaxBytesForModelPolicy(100_000, 80, 30_000, DefaultSummaryPromptMaxBytes); got != DefaultSummaryPromptMaxBytes {
		t.Fatalf("large-window summary prompt cap = %d, want conservative default %d", got, DefaultSummaryPromptMaxBytes)
	}
	if got := SummaryPromptMaxBytesForModelPolicy(200, 80, 30, DefaultSummaryPromptMaxBytes); got != 640 {
		t.Fatalf("small-window summary prompt cap = %d, want 640", got)
	}
	if got := SummaryPromptMaxBytesForModelPolicy(0, 80, 30, DefaultSummaryPromptMaxBytes); got != DefaultSummaryPromptMaxBytes {
		t.Fatalf("unknown-window summary prompt cap = %d, want default %d", got, DefaultSummaryPromptMaxBytes)
	}
}

// Rolling: a second compaction pass should detect the existing summary
// (left by the first pass), not start over from msg #1.
func TestCompact_RollingDoesNotMultiplySummary(t *testing.T) {
	// Conversation already in post-compaction shape, then has more events
	// appended that need re-compaction.
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "initial task"},
		{Role: "user", Content: summaryPrefix + "old summary"},
	}
	for i := 0; i < 30; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "ev" + string(rune('a'+i))})
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 4},
		summary:             "rolled-up summary",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Expect: system + initial task + ONE summary (the new one replaces
	// the old) + 4 tail events. Crucially not two summaries stacked.
	summaryCount := 0
	for _, m := range out {
		if m.Role == "user" && strings.HasPrefix(m.Content, summaryPrefix) {
			summaryCount++
		}
	}
	if summaryCount != 1 {
		t.Fatalf("expected exactly one rolling summary, got %d. out=%+v", summaryCount, out)
	}
	if len(out) != 1+1+1+4 {
		t.Fatalf("unexpected len: got %d, want 7", len(out))
	}
}

func TestCompact_DoesNotSeverToolCallPair(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1"}, {ID: "t2"}}},
		{Role: "tool", ToolCallID: "t1", Content: "r1"},
		{Role: "tool", ToolCallID: "t2", Content: "r2"},
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 2},
		summary:             "S",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	for i := 0; i < len(out); i++ {
		if len(out[i].ToolCalls) > 0 {
			needed := len(out[i].ToolCalls)
			for j := i + 1; j < i+1+needed; j++ {
				if j >= len(out) || out[j].Role != "tool" {
					t.Fatalf("tool_calls at %d (%d calls) not followed by %d role=tool messages; got %+v",
						i, needed, needed, out[j:])
				}
			}
			i += needed
		}
	}
}

// TestCompact_DoesNotSeverToolCallPair_AtHeadBoundary pins the symmetric
// boundary check at the HEAD side. The tail side already had
// backUpToSafeBoundary protecting it, but if KeepFirst landed the head
// right after an assistant.tool_calls, its tool replies got swept into
// the middle and summarized — leaving the resulting head with an
// assistant.tool_calls and NO matching tool replies. Strict
// OpenAI-compat upstreams reject that pairing on the next request,
// turning long sessions into reason=error the moment compaction fires.
func TestCompact_DoesNotSeverToolCallPair_AtHeadBoundary(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1"}, {ID: "t2"}}},
		{Role: "tool", ToolCallID: "t1", Content: "r1"},
		{Role: "tool", ToolCallID: "t2", Content: "r2"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", Content: "a4"},
	}
	// KeepFirst=2 means head=[system, user, assistant(tc)] without the
	// safety fix — the tool replies go into the middle.
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 2, KeepLast: 2},
		summary:             "S",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	for i := 0; i < len(out); i++ {
		if len(out[i].ToolCalls) > 0 {
			needed := len(out[i].ToolCalls)
			for j := i + 1; j < i+1+needed; j++ {
				if j >= len(out) || out[j].Role != "tool" {
					t.Fatalf("assistant.tool_calls at %d (%d calls) not followed by %d role=tool messages; got conv:\n%+v",
						i, needed, needed, out)
				}
			}
			i += needed
		}
	}
}

func TestCompactPlanResultForSummaryIncludesPlanStatusLabel(t *testing.T) {
	got, ok := compactPlanResultForSummary(`{"message":"updated step 2","steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"in_progress","evidence":["cmd/affentctl/cmd_run.go"]}]}`)
	if !ok {
		t.Fatal("compactPlanResultForSummary ok=false")
	}
	for _, want := range []string{
		"plan_status: plan:1/2:active",
		"message: updated step 2",
		"steps:",
		"2. [in_progress] ship",
		"evidence=cmd/affentctl/cmd_run.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plan summary missing %q:\n%s", want, got)
		}
	}
}

func TestIsContextOverflow(t *testing.T) {
	cases := map[string]bool{
		// Already covered before this iteration:
		`chat http 400: input length (12345 tokens) exceeds the maximum allowed length`: true,
		`chat http 400: This model's maximum context length is 8192 tokens`:             true,
		`ContextWindowExceededError: ...`:                                               true,
		// Real provider phrasings broadened in iter 16:
		`chat http 400: prompt is too long: 100000 tokens > 80000 maximum`:   true, // Anthropic via proxy
		`chat http 400: input is too long`:                                   true, // Anthropic
		`chat http 413: Request too large`:                                   true, // Groq
		`chat http 400: error code "request_too_large"`:                      true, // Groq enum
		`chat http 400: too many tokens in messages`:                         true, // some Together/vLLM builds
		`chat http 400: context_length_exceeded`:                             true, // vLLM error code
		`chat http 400: ... is greater than the maximum allowed token count`: true, // Fireworks/Together
		// "Range of input length should be [1, N]" — another flavor matched
		// via the existing "input length" keyword. Pinned so a future cleanup
		// of the keyword list can't silently break reactive compaction.
		`chat http 400: InvalidParameter: Range of input length should be [1, 229376]`: true,
		// Non-overflow errors must still be classified as false:
		`chat http 429: rate limit exceeded`: false,
		`chat http 500: internal error`:      false,
		``:                                   false,
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := IsContextOverflow(err); got != want {
			t.Errorf("IsContextOverflow(%q) = %v, want %v", msg, got, want)
		}
	}

	structured := &LLMHTTPError{
		Status:  400,
		Body:    `{"error":{"message":"provider returned a bounded structured error","type":"invalid_request_error","code":"context_length_exceeded"}}`,
		Code:    "context_length_exceeded",
		Type:    "invalid_request_error",
		Message: "provider returned a bounded structured error",
	}
	if !IsContextOverflow(structured) {
		t.Fatalf("structured context_length_exceeded error should trigger compaction: %+v", structured)
	}
	wrapped := fmt.Errorf("chat call failed: %w", structured)
	if !IsContextOverflow(wrapped) {
		t.Fatalf("wrapped structured context_length_exceeded error should trigger compaction: %v", wrapped)
	}
	nonOverflow := &LLMHTTPError{Status: 400, Body: `{"error":{"code":"invalid_api_key"}}`, Code: "invalid_api_key"}
	if IsContextOverflow(nonOverflow) {
		t.Fatalf("structured non-overflow error should not trigger compaction: %+v", nonOverflow)
	}
}

// Defensive: prompt body keeps the OpenHands V1 base fields plus
// Affent recovery fields for long-running sessions.
func TestDefaultSummaryPrompt_StandardFields(t *testing.T) {
	required := []string{
		"USER_CONTEXT", "TASK_TRACKING", "COMPLETED", "PENDING",
		"CURRENT_STATE", "SOURCE_EVIDENCE", "RECOVERY_STATE",
		"MEMORY_AND_RECALL", "ARTIFACT_TRACE", "NEXT_ACTION",
		"CODE_STATE", "TESTS", "CHANGES", "DEPS",
		"VERSION_CONTROL_STATUS", "PRIORITIZE", "SKIP", "Example formats",
	}
	for _, kw := range required {
		if !strings.Contains(defaultSummaryPrompt, kw) {
			t.Errorf("default prompt missing standard field %q", kw)
		}
	}
	// PRESERVE TASK IDs is the V1-specific instruction; guard against
	// accidental drop.
	if !strings.Contains(defaultSummaryPrompt, "PRESERVE TASK IDs") {
		t.Error("default prompt missing 'PRESERVE TASK IDs' V1 instruction")
	}
	for _, phrase := range []string{
		"do not upgrade discovery-only or partial dynamic evidence into verified facts",
		"latest actionable Next guidance",
		"memory/session recall markers",
		"verification commands",
		"AFFENT REJECTED FINAL DRAFT",
	} {
		if !strings.Contains(defaultSummaryPrompt, phrase) {
			t.Fatalf("default prompt missing Affent recovery phrase %q", phrase)
		}
	}
}
