package agent

import (
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
	recordToolFailureKind(&stats, "Failure: kind=invalid_args", false)
	if len(stats.ToolFailureByKind) != 0 {
		t.Fatalf("successful outcome should not record failure kind: %+v", stats.ToolFailureByKind)
	}

	recordToolFailureKind(&stats, "Failure: kind=invalid_args", true)
	recordToolFailureKind(&stats, "Failure: kind=invalid_args", true)
	recordToolFailureKind(&stats, "Failure: kind=timeout", true)
	if stats.ToolFailureByKind["invalid_args"] != 2 || stats.ToolFailureByKind["timeout"] != 1 {
		t.Fatalf("ToolFailureByKind = %+v", stats.ToolFailureByKind)
	}
}

func TestToolFailureKindForOutcome(t *testing.T) {
	if got := toolFailureKindForOutcome("web_fetch", "fetch failed\nFailure: kind=blocked, status=403\nNext: use another source", true); got != "blocked" {
		t.Fatalf("hard failure kind = %q, want blocked", got)
	}
	if got := toolFailureKindForOutcome("web_fetch", "[empty response: URL=https://example]\nFailure: kind=empty_response", false); got != "empty_response" {
		t.Fatalf("no-evidence failure kind = %q, want empty_response", got)
	}
	if got := toolFailureKindForOutcome("read_file", "Failure: kind=blocked", false); got != "" {
		t.Fatalf("successful read_file content should not set FailureKind, got %q", got)
	}
}
