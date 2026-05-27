package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestToolLoopGuard_BlocksExactRepeatedCalls(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"path":"a.txt"}`)
	if got := g.recordAttempt("read_file", args); got != "" {
		t.Fatalf("first attempt blocked: %s", got)
	}
	if got := g.recordAttempt("read_file", args); got != "" {
		t.Fatalf("second attempt blocked: %s", got)
	}
	got := g.recordAttempt("read_file", args)
	if !strings.Contains(got, "blocked repeated call") {
		t.Fatalf("third attempt should be blocked, got %q", got)
	}
	if !strings.Contains(got, "Next:") || !strings.Contains(got, "change the arguments") || !strings.Contains(got, "Failure: kind=loop_guard_repeated_call") {
		t.Fatalf("repeat guard should include corrective Next step, got %q", got)
	}
	if got := g.recordAttempt("read_file", json.RawMessage(`{"path":"b.txt"}`)); got != "" {
		t.Fatalf("different args should pass, got %q", got)
	}
}

func TestToolLoopGuard_NormalizesFileToolPathVariants(t *testing.T) {
	g := newToolLoopGuard()
	for i, args := range []json.RawMessage{
		json.RawMessage(`{"path":"docs/readme.md"}`),
		json.RawMessage(`{"path":"./docs//readme.md"}`),
		json.RawMessage(`{"path":" docs/./readme.md "}`),
	} {
		got := g.recordAttempt("read_file", args)
		if i < 2 && got != "" {
			t.Fatalf("attempt %d should pass, got %q", i+1, got)
		}
		if i == 2 && !strings.Contains(got, "blocked repeated call") {
			t.Fatalf("third normalized path variant should be blocked, got %q", got)
		}
	}
}

func TestToolLoopGuard_KeepsMeaningfulFileToolArgsDistinct(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"path":"docs/readme.md","max_bytes":128}`)
	second := json.RawMessage(`{"path":"./docs/readme.md","max_bytes":256}`)
	if got := g.recordAttempt("read_file", first); got != "" {
		t.Fatalf("first attempt blocked: %q", got)
	}
	if got := g.recordAttempt("read_file", first); got != "" {
		t.Fatalf("second same-cap attempt blocked too early: %q", got)
	}
	if got := g.recordAttempt("read_file", second); got != "" {
		t.Fatalf("changed max_bytes should stay distinct, got %q", got)
	}
}

func TestToolLoopGuard_DoesNotNormalizeShellCommandPaths(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"path":"docs/readme.md"}`)
	second := json.RawMessage(`{"path":"./docs//readme.md"}`)
	third := json.RawMessage(`{"path":" docs/./readme.md "}`)
	_ = g.recordAttempt("shell", first)
	_ = g.recordAttempt("shell", second)
	if got := g.recordAttempt("shell", third); got != "" {
		t.Fatalf("non-file tools should not normalize path-like fields, got %q", got)
	}
}

func TestToolLoopGuard_TracksConsecutiveFailures(t *testing.T) {
	g := newToolLoopGuard()
	for i := 1; i < toolFailureWarnThreshold; i++ {
		if got := g.recordOutcome("shell", false); got != "" {
			t.Fatalf("failure %d should not warn yet: %q", i, got)
		}
	}
	if got := g.recordOutcome("shell", false); !strings.Contains(got, "failed 3 consecutive times") {
		t.Fatalf("expected warning, got %q", got)
	} else if !strings.Contains(got, "Next:") || !strings.Contains(got, "verify prerequisites") || !strings.Contains(got, "Failure: kind=loop_guard_repeated_failures") {
		t.Fatalf("failure warning should include corrective Next step, got %q", got)
	}
	if got := g.recordOutcome("shell", true); got != "" {
		t.Fatalf("success should reset failures, got %q", got)
	}
	for i := 1; i < toolFailureHaltThreshold; i++ {
		_ = g.recordOutcome("shell", false)
	}
	if got := g.recordOutcome("shell", false); !strings.Contains(got, "failed 8 consecutive times") {
		t.Fatalf("expected halt message, got %q", got)
	} else if !strings.Contains(got, "Next:") || !strings.Contains(got, "different tool") || !strings.Contains(got, "Failure: kind=loop_guard_halted_tool") {
		t.Fatalf("halt message should include corrective Next step, got %q", got)
	}
	if got := g.recordAttempt("shell", json.RawMessage(`{}`)); !strings.Contains(got, "already failed 8 consecutive times") {
		t.Fatalf("halted tool should be blocked, got %q", got)
	} else if !strings.Contains(got, "Next:") || !strings.Contains(got, "evidence already gathered") || !strings.Contains(got, "Failure: kind=loop_guard_halted_tool") {
		t.Fatalf("halted-tool block should include corrective Next step, got %q", got)
	}
}

func TestToolLoopGuard_WebFetchFailsFast(t *testing.T) {
	g := newToolLoopGuard()
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("first web_fetch failure should not warn yet: %q", got)
	}
	got := g.recordOutcome("web_fetch", false)
	if !strings.Contains(got, "failed 2 consecutive times") {
		t.Fatalf("second web_fetch failure should warn early, got %q", got)
	}
	for _, want := range []string{"Failure kind", "Next:", "stop opening search results one by one", "Failure: kind=loop_guard_repeated_failures"} {
		if !strings.Contains(got, want) {
			t.Fatalf("web_fetch warning missing %q: %q", want, got)
		}
	}
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("third web_fetch failure should wait for halt threshold, got %q", got)
	}
	for i := 4; i < webFetchFailureHaltThreshold; i++ {
		if got := g.recordOutcome("web_fetch", false); got != "" {
			t.Fatalf("web_fetch failure %d should wait for halt threshold, got %q", i, got)
		}
	}
	got = g.recordOutcome("web_fetch", false)
	if !strings.Contains(got, "failed 8 consecutive times") {
		t.Fatalf("eighth web_fetch failure should halt, got %q", got)
	}
	got = g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://example.com/other"}`))
	if !strings.Contains(got, "already failed 8 consecutive times") {
		t.Fatalf("halted web_fetch should block subsequent attempts with web threshold, got %q", got)
	}
}

func TestToolLoopGuard_WebFetchSuccessResetsFailureCount(t *testing.T) {
	g := newToolLoopGuard()
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("first web_fetch failure should not warn yet: %q", got)
	}
	if got := g.recordOutcome("web_fetch", true); got != "" {
		t.Fatalf("web_fetch success should reset silently, got %q", got)
	}
	if got := g.recordOutcome("web_fetch", false); got != "" {
		t.Fatalf("post-success web_fetch failure should start a fresh count, got %q", got)
	}
}

func TestToolLoopGuard_BrowserInteractionFailsFast(t *testing.T) {
	g := newToolLoopGuard()
	if got := g.recordOutcome("browser_click", false); got != "" {
		t.Fatalf("first browser_click failure should not warn yet: %q", got)
	}
	got := g.recordOutcome("browser_click", false)
	for _, want := range []string{
		"browser interaction tool",
		"failed 2 consecutive times",
		"Dynamic pages",
		"browser_find/browser_snapshot",
		"canonical URL",
		"marked gap",
		"Failure: kind=loop_guard_repeated_failures",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("browser interaction warning missing %q: %q", want, got)
		}
	}
	if got := g.recordOutcome("browser_click", false); got != "" {
		t.Fatalf("third browser_click failure should wait for halt threshold, got %q", got)
	}
	if got := g.recordOutcome("browser_click", false); got != "" {
		t.Fatalf("fourth browser_click failure should wait for halt threshold, got %q", got)
	}
	got = g.recordOutcome("browser_click", false)
	if !strings.Contains(got, "failed 5 consecutive times") || !strings.Contains(got, "Failure: kind=loop_guard_halted_tool") {
		t.Fatalf("fifth browser_click failure should halt, got %q", got)
	}
	got = g.recordAttempt("browser_click", json.RawMessage(`{"ref":99}`))
	if !strings.Contains(got, "already failed 5 consecutive times") {
		t.Fatalf("halted browser_click should block subsequent attempts with browser threshold, got %q", got)
	}
}

func TestToolLoopGuard_BrowserInteractionSuccessResetsFailureCount(t *testing.T) {
	g := newToolLoopGuard()
	if got := g.recordOutcome("browser_scroll", false); got != "" {
		t.Fatalf("first browser_scroll failure should not warn yet: %q", got)
	}
	if got := g.recordOutcome("browser_scroll", true); got != "" {
		t.Fatalf("browser_scroll success should reset silently, got %q", got)
	}
	if got := g.recordOutcome("browser_scroll", false); got != "" {
		t.Fatalf("post-success browser_scroll failure should start a fresh count, got %q", got)
	}
}

func TestToolLoopGuard_BrowserScrollNoMovement(t *testing.T) {
	g := newToolLoopGuard()
	result := strings.Join([]string{
		"SourceAccess: browser_rendered_url=https://example.com/dashboard; page_text_below=partial_dynamic_page_evidence",
		"URL: https://example.com/dashboard",
		"SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
	}, "\n")
	if guard, ok := g.recordToolResult("browser_scroll", json.RawMessage(`{"direction":"down"}`), result, false); guard != "" || !ok {
		t.Fatalf("first no-movement scroll should be recorded without immediate guard; guard=%q ok=%v", guard, ok)
	}
	guard, ok := g.recordToolResult("browser_scroll", json.RawMessage(`{"direction":"down"}`), result, false)
	if ok {
		t.Fatal("second no-movement scroll should count as no new evidence")
	}
	for _, want := range []string{
		"browser_scroll produced no page movement",
		"https://example.com/dashboard",
		"scrolling down",
		"browser_network/browser_network_read",
		"mark the field unavailable",
		"Failure: kind=loop_guard_no_new_evidence",
	} {
		if !strings.Contains(guard, want) {
			t.Fatalf("scroll no-movement guard missing %q:\n%s", want, guard)
		}
	}
}

func TestToolLoopGuard_BrowserScrollMovementResetsNoMovement(t *testing.T) {
	g := newToolLoopGuard()
	noMove := strings.Join([]string{
		"SourceAccess: browser_rendered_url=https://example.com/dashboard; page_text_below=verified_page_evidence",
		"SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
	}, "\n")
	moved := strings.Join([]string{
		"SourceAccess: browser_rendered_url=https://example.com/dashboard; page_text_below=verified_page_evidence",
		"SCROLL: direction=down before_y=0 after_y=600 max_y=1200 movement=moved",
	}, "\n")
	if guard, ok := g.recordToolResult("browser_scroll", json.RawMessage(`{"direction":"down"}`), noMove, false); guard != "" || !ok {
		t.Fatalf("first no-movement scroll should not guard: guard=%q ok=%v", guard, ok)
	}
	if guard, ok := g.recordToolResult("browser_scroll", json.RawMessage(`{"direction":"down"}`), moved, false); guard != "" || !ok {
		t.Fatalf("moving scroll should reset no-movement state: guard=%q ok=%v", guard, ok)
	}
	if guard, ok := g.recordToolResult("browser_scroll", json.RawMessage(`{"direction":"down"}`), noMove, false); guard != "" || !ok {
		t.Fatalf("post-movement no-movement scroll should start fresh: guard=%q ok=%v", guard, ok)
	}
}

func TestToolLoopGuard_BlocksRepeatedFailedWebFetchURL(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"url":"https://blocked.example/metrics"}`)
	if got := g.recordAttempt("web_fetch", args); got != "" {
		t.Fatalf("first fetch attempt should pass guard: %q", got)
	}
	guard, ok := g.recordToolResult("web_fetch", args, "http 403\nFailure: kind=blocked, status=403\nNext: use another source", true)
	if guard != "" || ok {
		t.Fatalf("first blocked fetch should record failure without immediate guard message; guard=%q ok=%v", guard, ok)
	}
	got := g.recordAttempt("web_fetch", args)
	for _, want := range []string{"blocked repeated failed call", "web_fetch", "same effective URL", "kind=blocked", "do not retry the same failing URL", "Failure: kind=loop_guard_repeated_failed_input"} {
		if !strings.Contains(got, want) {
			t.Fatalf("repeated blocked fetch guard missing %q: %q", want, got)
		}
	}
	if got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://metrics.example/fallback"}`)); got != "" {
		t.Fatalf("different fetch URL should remain available after one blocked URL: %q", got)
	}
}

func TestToolLoopGuard_AllowsOneTransientWebFetchRetry(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"url":"https://slow.example/metrics"}`)
	if got := g.recordAttempt("web_fetch", args); got != "" {
		t.Fatalf("first fetch attempt should pass guard: %q", got)
	}
	if guard, ok := g.recordToolResult("web_fetch", args, "deadline exceeded\nFailure: kind=timeout\nNext: retry once with the same canonical URL", true); guard != "" || ok {
		t.Fatalf("first timeout should record failure without guard; guard=%q ok=%v", guard, ok)
	}
	if got := g.recordAttempt("web_fetch", args); got != "" {
		t.Fatalf("one transient retry should be allowed, got %q", got)
	}
	if guard, ok := g.recordToolResult("web_fetch", args, "deadline exceeded again\nFailure: kind=timeout\nNext: switch source", true); guard == "" || ok {
		t.Fatalf("second consecutive web_fetch failure should still trigger tool-level warning; guard=%q ok=%v", guard, ok)
	}
	got := g.recordAttempt("web_fetch", args)
	for _, want := range []string{"blocked repeated failed call", "kind=timeout", "same effective URL", "Failure: kind=loop_guard_repeated_failed_input"} {
		if !strings.Contains(got, want) {
			t.Fatalf("third timeout attempt guard missing %q: %q", want, got)
		}
	}
}

func TestToolLoopGuard_BlocksRepeatedFailedWebFetchHost(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"url":"https://www.blocked.example/example/status/1"}`)
	second := json.RawMessage(`{"url":"https://blocked.example/example/status/2"}`)
	third := json.RawMessage(`{"url":"https://blocked.example/example/status/3"}`)
	if got := g.recordAttempt("web_fetch", first); got != "" {
		t.Fatalf("first host fetch should pass guard: %q", got)
	}
	if guard, ok := g.recordToolResult("web_fetch", first, "http 403\nFailure: kind=blocked, status=403\nNext: use another source", true); guard != "" || ok {
		t.Fatalf("first blocked host fetch should only record failure; guard=%q ok=%v", guard, ok)
	}
	if got := g.recordAttempt("web_fetch", second); got != "" {
		t.Fatalf("second distinct URL on same host should pass once: %q", got)
	}
	if _, ok := g.recordToolResult("web_fetch", second, "http 403\nFailure: kind=blocked, status=403\nNext: use another source", true); ok {
		t.Fatal("second blocked host fetch should count as failure")
	}
	got := g.recordAttempt("web_fetch", third)
	for _, want := range []string{"blocked web_fetch to host", "blocked.example", "previous URL failures", "Failure kind=blocked", "stop trying more URLs from this host", "blocked/unverified", "Failure: kind=loop_guard_repeated_failed_input"} {
		if !strings.Contains(got, want) {
			t.Fatalf("host failure guard missing %q: %q", want, got)
		}
	}
	if got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://taostats.io/subnets/120"}`)); got != "" {
		t.Fatalf("different host should remain available: %q", got)
	}
}

func TestToolLoopGuard_BlocksKnownDirectFetchTrapHostAfterOneFailure(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"url":"https://www.x.com/example/status/1"}`)
	second := json.RawMessage(`{"url":"https://x.com/example/status/2"}`)
	if got := g.recordAttempt("web_fetch", first); got != "" {
		t.Fatalf("first host fetch should pass guard: %q", got)
	}
	if guard, ok := g.recordToolResult("web_fetch", first, "[blocked response: URL=https://www.x.com/example/status/1, Content-Type=\"\", Reason=\"site usually blocks direct HTTP readers\"]\nFailure: kind=blocked\nNext: use snippets", false); guard != "" || ok {
		t.Fatalf("first blocked no-evidence result should record failure; guard=%q ok=%v", guard, ok)
	}
	got := g.recordAttempt("web_fetch", second)
	for _, want := range []string{"blocked web_fetch to host", "x.com", "previous URL failures", "Failure kind=blocked", "stop trying more URLs from this host"} {
		if !strings.Contains(got, want) {
			t.Fatalf("known trap host guard missing %q: %q", want, got)
		}
	}
}

func TestToolLoopGuard_DoesNotHostBlockPageScopedFetchFailures(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < 3; i++ {
		args := json.RawMessage(`{"url":"https://docs.example/missing-` + fmt.Sprintf("%d", i) + `"}`)
		if got := g.recordAttempt("web_fetch", args); got != "" {
			t.Fatalf("not_found on same host should not trigger host guard at attempt %d: %q", i+1, got)
		}
		g.recordToolResult("web_fetch", args, "http 404\nFailure: kind=not_found, status=404\nNext: use discovery", true)
	}
}

func TestToolLoopGuard_BlocksRepeatedDynamicShellHostPages(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"url":"https://metrics.example/app/subnet/21"}`)
	second := json.RawMessage(`{"url":"https://metrics.example/subnets/21"}`)
	third := json.RawMessage(`{"url":"https://www.metrics.example/validators"}`)
	api := json.RawMessage(`{"url":"https://metrics.example/api/subnets/21.json"}`)
	for _, args := range []json.RawMessage{first, second} {
		if got := g.recordAttempt("web_fetch", args); got != "" {
			t.Fatalf("dynamic shell fetch should pass before threshold: %q", got)
		}
		if _, ok := g.recordToolResult("web_fetch", args, "[dynamic page shell: URL=https://metrics.example]\nFailure: kind=dynamic_shell\nNext: use a canonical API/text/source page", false); ok {
			t.Fatal("dynamic shell result should count as no-evidence failure")
		}
	}
	got := g.recordAttempt("web_fetch", third)
	for _, want := range []string{"blocked web_fetch to host", "metrics.example", "Failure kind=dynamic_shell", "switch to a canonical/API/text source"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dynamic host guard missing %q: %q", want, got)
		}
	}
	if got := g.recordAttempt("web_fetch", api); got != "" {
		t.Fatalf("API/text fallback on dynamic host should remain fetchable: %q", got)
	}
}

func TestToolLoopGuard_FetchHostSuccessClearsHostFailure(t *testing.T) {
	g := newToolLoopGuard()
	first := json.RawMessage(`{"url":"https://blocked.example/a"}`)
	second := json.RawMessage(`{"url":"https://blocked.example/b"}`)
	third := json.RawMessage(`{"url":"https://blocked.example/c"}`)
	if got := g.recordAttempt("web_fetch", first); got != "" {
		t.Fatalf("first host fetch should pass: %q", got)
	}
	g.recordToolResult("web_fetch", first, "http 403\nFailure: kind=blocked, status=403\nNext: use another source", true)
	if got := g.recordAttempt("web_fetch", second); got != "" {
		t.Fatalf("second host fetch should pass: %q", got)
	}
	g.recordToolResult("web_fetch", second, "readable evidence", false)
	if got := g.recordAttempt("web_fetch", third); got != "" {
		t.Fatalf("success on host should clear previous host failure: %q", got)
	}
}

func TestToolLoopGuard_BlocksRepeatedFailedWebSearchQuery(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"query":"Vega recent trend market metrics sentiment","num_results":5}`)
	if got := g.recordAttempt("web_search", args); got != "" {
		t.Fatalf("first search attempt should pass guard: %q", got)
	}
	guard, ok := g.recordToolResult("web_search", args, "(no results)\nFailure: kind=no_results\nNext: use distinctive entities", false)
	if guard != "" || ok {
		t.Fatalf("first no-results search should record failure without immediate guard; guard=%q ok=%v", guard, ok)
	}
	got := g.recordAttempt("web_search", args)
	for _, want := range []string{"blocked repeated failed call", "web_search", "same effective query", "kind=no_results", "distinctive entities", "Failure: kind=loop_guard_repeated_failed_input"} {
		if !strings.Contains(got, want) {
			t.Fatalf("repeated no-results search guard missing %q: %q", want, got)
		}
	}
	if got := g.recordAttempt("web_search", json.RawMessage(`{"query":"Vega subnet 88 official domain metrics","num_results":5}`)); got != "" {
		t.Fatalf("refined search query should remain available: %q", got)
	}
}

func TestToolLoopGuard_BlocksWebFetchMarkedBySearchWarning(t *testing.T) {
	g := newToolLoopGuard()
	searchArgs := json.RawMessage(`{"query":"Nimbus recent trend market metrics sentiment","num_results":5}`)
	searchResult := `1. Nimbus official docs
   https://official.example/nimbus/about
   Primary docs.

2. Recent social discussion
   https://x.com/example/status/123
   Community reaction.
   Direct-reader warning: do not use direct page fetch on this URL.

3. Live dashboard
   https://metrics.example/app/nimbus
   Client-rendered market dashboard.
   Direct-reader caution: result appears to be a dynamic or JavaScript-rendered page.

Next: choose the 1-3 most authoritative/current result URLs.`
	if guard, ok := g.recordToolResult("web_search", searchArgs, searchResult, false); guard != "" || !ok {
		t.Fatalf("successful search result should record warnings without failing; guard=%q ok=%v", guard, ok)
	}
	if got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://official.example/nimbus/about"}`)); got != "" {
		t.Fatalf("ordinary search result URL should remain fetchable: %q", got)
	}
	got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://www.x.com/example/status/123#ignored"}`))
	for _, want := range []string{"Direct-reader warning", "web_search marked that URL", "weak discovery/sentiment evidence", "canonical API/text/source URL", "Failure: kind=loop_guard_direct_reader_warning"} {
		if !strings.Contains(got, want) {
			t.Fatalf("direct-reader warning guard missing %q: %q", want, got)
		}
	}
	for _, forbidden := range []string{"browser", "rendering"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("direct-reader warning guard should not mention unavailable %q capability: %q", forbidden, got)
		}
	}
	if got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://metrics.example/app/nimbus"}`)); got != "" {
		t.Fatalf("caution-only URL should not be hard-blocked: %q", got)
	}
}

func TestToolLoopGuard_SearchWarningIgnoresURLsInsideSnippets(t *testing.T) {
	g := newToolLoopGuard()
	searchArgs := json.RawMessage(`{"query":"Nimbus recent trend","num_results":5}`)
	searchResult := `1. Recent social discussion
   https://x.com/example/status/123
   Snippet mentions a mirror at https://mirror.example/nimbus but the result URL above is the warned URL.
   Direct-reader warning: do not use direct page fetch on this URL.

Next: use snippets only as weak sentiment evidence.`
	if guard, ok := g.recordToolResult("web_search", searchArgs, searchResult, false); guard != "" || !ok {
		t.Fatalf("successful search result should record warnings without failing; guard=%q ok=%v", guard, ok)
	}
	got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://x.com/example/status/123"}`))
	if !strings.Contains(got, "loop_guard_direct_reader_warning") {
		t.Fatalf("warned result URL should be blocked, got %q", got)
	}
	if got := g.recordAttempt("web_fetch", json.RawMessage(`{"url":"https://mirror.example/nimbus"}`)); got != "" {
		t.Fatalf("snippet-only URL should not inherit the warning, got %q", got)
	}
}

func TestCanonicalWebURLNormalizesHostAndPreservesPort(t *testing.T) {
	got := canonicalWebURL(`"https://www.Example.com:8443/path?q=1#frag"`)
	if got != "https://example.com:8443/path?q=1" {
		t.Fatalf("canonicalWebURL() = %q", got)
	}
}

func TestToolOutcomeCountsNoEvidenceWebFetchAsFailure(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		result string
		isErr  bool
		want   bool
	}{
		{name: "web fetch text", tool: "web_fetch", result: "readable page text", want: true},
		{name: "web fetch empty", tool: "web_fetch", result: "[empty response: URL=https://example]", want: false},
		{name: "web fetch blocked challenge", tool: "web_fetch", result: "[blocked response: URL=https://example]\nFailure: kind=blocked", want: false},
		{name: "web fetch dynamic shell", tool: "web_fetch", result: "[dynamic page shell: URL=https://example]\nFailure: kind=dynamic_shell", want: false},
		{name: "web fetch dynamic shell embedded evidence", tool: "web_fetch", result: "[dynamic page shell: URL=https://example]\nEmbedded data preview (page source evidence; verify relevance before using):\n- {\"id\":120,\"name\":\"Affine\"}", want: true},
		{name: "web fetch non text", tool: "web_fetch", result: "  [non-text response: URL=https://example]  ", want: false},
		{name: "web fetch hard error", tool: "web_fetch", result: "Error: http 403", isErr: true, want: false},
		{name: "web search no results", tool: "web_search", result: "(no results)\nFailure: kind=no_results", want: false},
		{name: "web search hits", tool: "web_search", result: "1. Result\n   https://example.com\n   snippet", want: true},
		{name: "other tool literal text", tool: "shell", result: "[empty response: not a web_fetch marker]", want: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolOutcomeCountsAsSuccess(c.tool, c.result, c.isErr); got != c.want {
				t.Fatalf("toolOutcomeCountsAsSuccess() = %t, want %t", got, c.want)
			}
		})
	}
}

func TestToolLoopGuard_WebFetchNoEvidenceResultsFailFast(t *testing.T) {
	g := newToolLoopGuard()
	ok := toolOutcomeCountsAsSuccess("web_fetch", "[empty response: URL=https://example/a]", false)
	if got := g.recordOutcome("web_fetch", ok); got != "" {
		t.Fatalf("first no-evidence web_fetch result should not warn yet: %q", got)
	}
	ok = toolOutcomeCountsAsSuccess("web_fetch", "[non-text response: URL=https://example/b]", false)
	got := g.recordOutcome("web_fetch", ok)
	if !strings.Contains(got, "failed 2 consecutive times") {
		t.Fatalf("second no-evidence web_fetch result should warn, got %q", got)
	}
}

func TestToolLoopGuard_WebSearchNoEvidenceUsesSearchGuidance(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < toolFailureWarnThreshold-1; i++ {
		ok := toolOutcomeCountsAsSuccess("web_search", "(no results)\nFailure: kind=no_results", false)
		if got := g.recordOutcome("web_search", ok); got != "" {
			t.Fatalf("web_search no-results failure %d should not warn yet: %q", i+1, got)
		}
	}
	ok := toolOutcomeCountsAsSuccess("web_search", "(no usable results: search provider returned no URLs)\nFailure: kind=no_results", false)
	got := g.recordOutcome("web_search", ok)
	for _, want := range []string{"web_search", "failed 3 consecutive times", "Failure kind", "distinctive entities", "known source URLs", "Failure: kind=loop_guard_repeated_failures"} {
		if !strings.Contains(got, want) {
			t.Fatalf("web_search warning missing %q: %q", want, got)
		}
	}
}

func TestToolLoopGuard_BrowserFindNoMatchesStopsPageTextSearchLoop(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"query":"market cap","max_results":3}`)
	noMatch := "SourceAccess: browser_rendered_url=https://dash.example/subnets/120; snapshot_id=3; page_text_below=verified_page_evidence\nQUERY: \"market cap\"\nMATCHES: none\nNext: retry browser_find with a shorter or different visible phrase, call browser_snapshot to inspect current text, scroll once if the desired section is likely off-screen, or continue from existing evidence.\n"
	for i := 1; i < browserFindNoMatchThreshold; i++ {
		if got := g.recordAttempt("browser_find", args); got != "" {
			t.Fatalf("browser_find attempt %d should pass before no-match threshold: %q", i, got)
		}
		guard, ok := g.recordToolResult("browser_find", args, noMatch, false)
		if guard != "" || !ok {
			t.Fatalf("browser_find no-match %d should not guard yet; guard=%q ok=%v", i, guard, ok)
		}
	}
	if got := g.recordAttempt("browser_find", json.RawMessage(`{"query":"validators","max_results":3}`)); got != "" {
		t.Fatalf("attempt at threshold should still run so it can record its result: %q", got)
	}
	guard, ok := g.recordToolResult("browser_find", json.RawMessage(`{"query":"validators","max_results":3}`), noMatch, false)
	if ok {
		t.Fatal("threshold no-match should count as no new evidence")
	}
	for _, want := range []string{
		"browser_find returned no matches",
		"https://dash.example/subnets/120",
		"browser_snapshot",
		"browser_network/browser_network_read",
		"not visible in the inspected page",
		"Failure: kind=loop_guard_no_new_evidence",
	} {
		if !strings.Contains(guard, want) {
			t.Fatalf("browser_find no-new-evidence guard missing %q: %q", want, guard)
		}
	}
}

func TestToolLoopGuard_BrowserFindMatchResetsNoMatchLoop(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"query":"market cap","max_results":3}`)
	noMatch := "SourceAccess: browser_rendered_url=https://dash.example/subnets/120; snapshot_id=3; page_text_below=verified_page_evidence\nQUERY: \"market cap\"\nMATCHES: none\n"
	match := "SourceAccess: browser_rendered_url=https://dash.example/subnets/120; snapshot_id=4; page_text_below=verified_page_evidence\nQUERY: \"market\"\nMATCHES:\n- [text] Market Cap 201.04K T\n"
	for i := 0; i < browserFindNoMatchThreshold-1; i++ {
		g.recordToolResult("browser_find", args, noMatch, false)
	}
	if guard, ok := g.recordToolResult("browser_find", json.RawMessage(`{"query":"market"}`), match, false); guard != "" || !ok {
		t.Fatalf("browser_find match should reset no-match loop; guard=%q ok=%v", guard, ok)
	}
	if guard, ok := g.recordToolResult("browser_find", args, noMatch, false); guard != "" || !ok {
		t.Fatalf("post-match no-match should restart count, not guard; guard=%q ok=%v", guard, ok)
	}
}

func TestToolLoopGuard_BrowserNetworkNoMatchesStopsNetworkSearchLoop(t *testing.T) {
	g := newToolLoopGuard()
	noMatch := "BROWSER NETWORK EVIDENCE\nCURRENT_PAGE: https://dash.example/subnets/120\nquery: \"market_cap\"\nMATCHES: none\nNext: wait for the page to load dynamic data, try a shorter label/entity/API-path query, interact with the relevant tab, or mark hidden fields unverified.\n"
	for i := 1; i < browserNetworkNoMatchThreshold; i++ {
		args := json.RawMessage(`{"query":"metric-` + fmt.Sprintf("%d", i) + `","max_results":3}`)
		if got := g.recordAttempt("browser_network", args); got != "" {
			t.Fatalf("browser_network attempt %d should pass before no-match threshold: %q", i, got)
		}
		guard, ok := g.recordToolResult("browser_network", args, noMatch, false)
		if guard != "" || !ok {
			t.Fatalf("browser_network no-match %d should not guard yet; guard=%q ok=%v", i, guard, ok)
		}
	}
	args := json.RawMessage(`{"query":"validators","max_results":3}`)
	if got := g.recordAttempt("browser_network", args); got != "" {
		t.Fatalf("attempt at threshold should still run so it can record its result: %q", got)
	}
	guard, ok := g.recordToolResult("browser_network", args, noMatch, false)
	if ok {
		t.Fatal("threshold network no-match should count as no new evidence")
	}
	for _, want := range []string{
		"browser_network returned no captured response matches",
		"https://dash.example/subnets/120",
		"browser_snapshot",
		"relevant tab or wait once",
		"mark hidden fields unverified",
		"Failure: kind=loop_guard_no_new_evidence",
	} {
		if !strings.Contains(guard, want) {
			t.Fatalf("browser_network no-new-evidence guard missing %q: %q", want, guard)
		}
	}
}

func TestToolLoopGuard_BrowserNetworkMatchResetsNoMatchLoop(t *testing.T) {
	g := newToolLoopGuard()
	noMatch := "BROWSER NETWORK EVIDENCE\nCURRENT_PAGE: https://dash.example/subnets/120\nquery: \"market_cap\"\nMATCHES: none\n"
	match := "BROWSER NETWORK EVIDENCE\nCURRENT_PAGE: https://dash.example/subnets/120\nquery: \"market\"\nMATCHES:\n- n1 status=200 resource=fetch content_type=application/json url=https://dash.example/api\n"
	for i := 0; i < browserNetworkNoMatchThreshold-1; i++ {
		g.recordToolResult("browser_network", json.RawMessage(`{"query":"q"}`), noMatch, false)
	}
	if guard, ok := g.recordToolResult("browser_network", json.RawMessage(`{"query":"market"}`), match, false); guard != "" || !ok {
		t.Fatalf("browser_network match should reset no-match loop; guard=%q ok=%v", guard, ok)
	}
	if guard, ok := g.recordToolResult("browser_network", json.RawMessage(`{"query":"again"}`), noMatch, false); guard != "" || !ok {
		t.Fatalf("post-match no-match should restart count, not guard; guard=%q ok=%v", guard, ok)
	}
}

// TestToolLoopGuard_PerTurnCallCapForRunTask pins the
// over-delegation mitigation: a model can keep varying run_task's
// arguments (different task_type / objective / max_turns each call)
// and the same-args guard would NEVER fire. Without the per-turn cap
// the parent's MaxToolCalls is the only ceiling, which lets a bad
// prompt drain the parent budget on three or four shallow focused
// tasks in a row. The cap belongs in the guard because that's the
// single place every tool dispatch funnels through.
//
// The 4th attempt is the canonical boundary case: 3 prior calls are
// already a strong signal of over-delegation; the 4th gets rejected
// with a message the model can act on.
func TestToolLoopGuard_PerTurnCallCapForRunTask(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < 3; i++ {
		// Distinct args each iteration so the args-hash guard is NOT
		// what triggers; we're isolating the per-turn count cap.
		args := json.RawMessage(`{"task_type":"recall","objective":"q-` + fmt.Sprintf("%d", i) + `"}`)
		if got := g.recordAttempt(FocusedTaskToolName, args); got != "" {
			t.Fatalf("call %d should be allowed (cap=3 allows three calls), got %q", i, got)
		}
	}
	args := json.RawMessage(`{"task_type":"recall","objective":"q-fourth"}`)
	got := g.recordAttempt(FocusedTaskToolName, args)
	if got == "" {
		t.Fatal("4th run_task attempt must be blocked by per-turn cap")
	}
	if !strings.Contains(got, "per-turn delegation cap") {
		t.Errorf("rejection should name the cap concept, got %q", got)
	}
	if !strings.Contains(got, "Next:") || !strings.Contains(got, "Failure: kind=loop_guard_call_cap") {
		t.Errorf("rejection should include a corrective Next step the model can act on, got %q", got)
	}
}

func TestToolLoopGuard_PerTurnCallCapForPlan(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < perTurnCallCaps[PlanToolName]; i++ {
		args := json.RawMessage(`{"action":"update","index":1,"note":"step-` + fmt.Sprintf("%d", i) + `"}`)
		if got := g.recordAttempt(PlanToolName, args); got != "" {
			t.Fatalf("plan call %d should be allowed, got %q", i+1, got)
		}
	}
	got := g.recordAttempt(PlanToolName, json.RawMessage(`{"action":"view"}`))
	if got == "" {
		t.Fatal("plan call over cap must be blocked")
	}
	if !strings.Contains(got, "per-turn planning cap") {
		t.Fatalf("plan cap message should name planning cap, got %q", got)
	}
	if strings.Contains(got, "focused task") || strings.Contains(got, "delegation cap") {
		t.Fatalf("plan cap message should not use focused-task delegation wording, got %q", got)
	}
	if !strings.Contains(got, "Next:") || !strings.Contains(got, "execute the next concrete step") {
		t.Fatalf("plan cap message should include useful recovery guidance, got %q", got)
	}
}

func TestToolLoopGuard_PerTurnCallCapForExternalResearchTools(t *testing.T) {
	cases := []struct {
		tool string
		cap  int
		want string
	}{
		{tool: "web_fetch", cap: perTurnCallCaps["web_fetch"], want: "external-research cap"},
		{tool: "web_search", cap: perTurnCallCaps["web_search"], want: "external-research cap"},
		{tool: "browser_navigate", cap: perTurnCallCaps["browser_navigate"], want: "browser cap"},
		{tool: "browser_snapshot", cap: perTurnCallCaps["browser_snapshot"], want: "browser cap"},
		{tool: "browser_find", cap: perTurnCallCaps["browser_find"], want: "browser cap"},
		{tool: "browser_network", cap: perTurnCallCaps["browser_network"], want: "browser cap"},
		{tool: "browser_network_read", cap: perTurnCallCaps["browser_network_read"], want: "browser cap"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			g := newToolLoopGuard()
			for i := 0; i < c.cap; i++ {
				args := json.RawMessage(`{"url":"https://example.com/page-` + fmt.Sprintf("%d", i) + `","query":"q-` + fmt.Sprintf("%d", i) + `"}`)
				if got := g.recordAttempt(c.tool, args); got != "" {
					t.Fatalf("call %d should be allowed, got %q", i+1, got)
				}
			}
			got := g.recordAttempt(c.tool, json.RawMessage(`{"url":"https://example.com/over","query":"over"}`))
			for _, want := range []string{c.want, "Next:", "verified", "Failure: kind=loop_guard_call_cap"} {
				if !strings.Contains(got, want) {
					t.Fatalf("%s cap message missing %q: %q", c.tool, want, got)
				}
			}
		})
	}
}

// TestToolLoopGuard_PerTurnCapDoesNotAffectOtherTools guards against a
// regression where the cap mechanism leaks across tool names. read_file
// gets called many times per turn legitimately; capping it would break
// every realistic exploration session.
func TestToolLoopGuard_PerTurnCapDoesNotAffectOtherTools(t *testing.T) {
	g := newToolLoopGuard()
	for i := 0; i < 10; i++ {
		args := json.RawMessage(`{"path":"file-` + fmt.Sprintf("%d", i) + `.go"}`)
		if got := g.recordAttempt("read_file", args); got != "" {
			t.Fatalf("read_file call %d must not be capped, got %q", i, got)
		}
	}
}

// TestToolLoopGuard_PerTurnCapMessageBeatsArgsHashMessage ensures the
// model gets the right corrective message when both guards would
// trigger. A model that calls run_task with the SAME args three times
// would hit both: the args-hash guard at attempt 3 AND the per-turn
// cap eventually. The per-turn cap is the higher-signal message
// (over-delegation across the whole turn vs. one repeated input), so
// when both apply we want the cap message to win, which is also why
// the cap check sits before the args-hash check in recordAttempt.
func TestToolLoopGuard_PerTurnCapMessageBeatsArgsHashMessage(t *testing.T) {
	g := newToolLoopGuard()
	args := json.RawMessage(`{"task_type":"recall","objective":"q"}`)
	// First two attempts go through.
	if got := g.recordAttempt(FocusedTaskToolName, args); got != "" {
		t.Fatalf("attempt 1: %q", got)
	}
	if got := g.recordAttempt(FocusedTaskToolName, args); got != "" {
		t.Fatalf("attempt 2: %q", got)
	}
	// Third call: under the args-hash threshold (3) is met; that guard
	// would normally fire. But the per-turn cap (3) is also at its
	// boundary AFTER this call increments. The behavior here is that
	// the args-hash guard fires first because the cap is checked
	// before the increment: attempt 3 increments perToolCounts to 3,
	// then callCounts to 3, and only THEN compares >=3. We accept
	// either message here as correct; the design pin is just that the
	// 3rd same-args call is blocked.
	got := g.recordAttempt(FocusedTaskToolName, args)
	if got == "" {
		t.Fatal("3rd same-args attempt must be blocked")
	}
	// The 4th attempt with DIFFERENT args must hit the per-turn cap
	// message; the args-hash key is different so the same-args guard
	// can't fire here.
	args2 := json.RawMessage(`{"task_type":"recall","objective":"different"}`)
	got4 := g.recordAttempt(FocusedTaskToolName, args2)
	if !strings.Contains(got4, "per-turn delegation cap") {
		t.Errorf("4th call with new args must surface the per-turn cap message, got %q", got4)
	}
}

func TestRegistryDispatch_SuggestsUnknownToolNames(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", nil
	}})
	out, isErr := reg.dispatch(context.Background(), "read_flie", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("unknown tool should be an error")
	}
	if !strings.Contains(out, `Did you mean: read_file?`) {
		t.Fatalf("expected suggestion, got %q", out)
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "exact tool names") {
		t.Fatalf("unknown tool suggestion should include corrective Next step, got %q", out)
	}
}

func TestRegistryDispatch_UnknownToolWithoutSuggestionGivesNextStep(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", nil
	}})
	out, isErr := reg.dispatch(context.Background(), "browser_use", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("unknown tool should be an error")
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "advertised tool list") {
		t.Fatalf("unknown tool without suggestion should include recovery guidance, got %q", out)
	}
}

func TestRegistryDispatch_CanonicalizesToolNameAliases(t *testing.T) {
	reg := NewRegistry()
	called := false
	reg.Add(&Tool{
		Name:   "read_file",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			called = true
			return string(args), nil
		},
	})
	out, isErr := reg.dispatch(context.Background(), "readFile", json.RawMessage(`{"path":"README.md"}`))
	if isErr {
		t.Fatalf("canonicalized call should succeed: %s", out)
	}
	if !called {
		t.Fatal("canonicalized tool was not executed")
	}
}

func TestRegistryDispatch_CanonicalizesCommonWeakModelToolNames(t *testing.T) {
	cases := []struct {
		registered string
		called     string
	}{
		{registered: "read_file", called: "read_file_tool"},
		{registered: "read_file", called: "file_read"},
		{registered: "shell", called: "run_command"},
		{registered: "list_files", called: "list_dir"},
		{registered: "subagent_run", called: "subagent"},
		{registered: "run_task", called: "focused_task"},
	}
	for _, tc := range cases {
		t.Run(tc.registered+"/"+tc.called, func(t *testing.T) {
			reg := NewRegistry()
			called := false
			reg.Add(&Tool{
				Name:   tc.registered,
				Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
				Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
					called = true
					return string(args), nil
				},
			})
			out, isErr := reg.dispatch(context.Background(), tc.called, json.RawMessage(`{"path":"README.md"}`))
			if isErr {
				t.Fatalf("canonicalized call should succeed: %s", out)
			}
			if !called {
				t.Fatal("canonicalized tool was not executed")
			}
		})
	}
}

func TestRegistryDispatch_CommonAliasSuggestions(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		return "", nil
	}})
	out, isErr := reg.dispatch(context.Background(), "opnfile", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("unknown tool should be an error")
	}
	if !strings.Contains(out, `Did you mean: read_file?`) {
		t.Fatalf("expected read_file suggestion for common alias, got %q", out)
	}
}

func TestRegistryDispatch_SchemaLessToolErrorGetsNextStep(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{
		Name: "remote_tool",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "", errors.New("remote failed")
		},
	})

	out, isErr := reg.dispatch(context.Background(), "remote_tool", json.RawMessage(`{"q":"x"}`))
	if !isErr {
		t.Fatal("tool failure should be an error")
	}
	if !strings.Contains(out, "Error: remote failed") {
		t.Fatalf("expected tool error, got %q", out)
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "do not repeat the same failing call unchanged") {
		t.Fatalf("schema-less tool error should include recovery guidance, got %q", out)
	}
}

func TestRegistryDispatch_SchemaLessToolErrorKeepsExistingNextStep(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{
		Name: "remote_tool",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "", errors.New("bad input\nNext: retry with a query")
		},
	})

	out, isErr := reg.dispatch(context.Background(), "remote_tool", json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("tool failure should be an error")
	}
	if got := strings.Count(out, "Next:"); got != 1 {
		t.Fatalf("expected one Next step, got %d in %q", got, out)
	}
	if strings.Contains(out, "do not repeat the same failing call unchanged") {
		t.Fatalf("existing Next step should not get fallback guidance, got %q", out)
	}
}
