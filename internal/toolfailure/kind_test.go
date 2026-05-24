package toolfailure

import "testing"

func TestKind(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "Error\nFailure: kind=blocked\nNext: use another source", want: "blocked"},
		{name: "with status", in: "Failure: kind=server_error, status=502", want: "server_error"},
		{name: "later field", in: "first\nFailure: status=403, kind=blocked", want: "blocked"},
		{name: "invalid", in: "Failure: kind=blocked; rm -rf", want: ""},
		{name: "missing", in: "Next: retry", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Kind(c.in); got != c.want {
				t.Fatalf("Kind() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestKindForResult(t *testing.T) {
	if got := KindForResult("web_fetch", "Failure: kind=blocked", true); got != "blocked" {
		t.Fatalf("hard failure kind = %q, want blocked", got)
	}
	if got := KindForResult("web_fetch", "[empty response: URL=https://example]\nFailure: kind=empty_response", false); got != "empty_response" {
		t.Fatalf("no-evidence kind = %q, want empty_response", got)
	}
	if got := KindForResult("web_search", "(no results)\nFailure: kind=no_results", false); got != "no_results" {
		t.Fatalf("no-results kind = %q, want no_results", got)
	}
	if got := KindForResult("read_file", "Failure: kind=blocked", false); got != "" {
		t.Fatalf("successful read_file content kind = %q, want empty", got)
	}
}

func TestIsNoEvidenceResult(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		result string
		want   bool
	}{
		{name: "fetch empty", tool: "web_fetch", result: "[empty response: URL=https://example]", want: true},
		{name: "fetch non text", tool: "web_fetch", result: "[non-text response: URL=https://example]", want: true},
		{name: "search none", tool: "web_search", result: "(no results)\nFailure: kind=no_results", want: true},
		{name: "search unusable", tool: "web_search", result: "(no usable results: search provider returned no URLs)\nFailure: kind=no_results", want: true},
		{name: "search hits", tool: "web_search", result: "1. Result\n   https://example.com\n   snippet", want: false},
		{name: "other", tool: "shell", result: "(no results)\nFailure: kind=no_results", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsNoEvidenceResult(c.tool, c.result); got != c.want {
				t.Fatalf("IsNoEvidenceResult() = %t, want %t", got, c.want)
			}
		})
	}
}
