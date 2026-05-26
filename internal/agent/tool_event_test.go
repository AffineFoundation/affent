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
}

func TestRecordSourceAccessStats(t *testing.T) {
	var stats sse.ToolRuntimeStats
	recordSourceAccessStats(&stats, "SourceAccess: browser_rendered_url=https://example.com/page; page_text_below=verified_page_evidence\nPAGE TEXT:\nok")
	recordSourceAccessStats(&stats, "SourceAccess: browser_rendered_url=https://example.com/search; page_text_below=search_results_discovery_only\nPAGE TEXT:\nresult")
	recordSourceAccessStats(&stats, "SourceAccess: browser_network_url=https://example.com/api; source_method=network_xhr_fetch\n{\"ok\":true}")
	recordSourceAccessStats(&stats, "plain tool output")

	if stats.SourceAccessResults != 3 {
		t.Fatalf("SourceAccessResults = %d, want 3", stats.SourceAccessResults)
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
