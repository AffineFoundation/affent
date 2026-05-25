package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserFindResultsReturnCompactMatches(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 7,
		URL:        "https://example.test/asset",
		Title:      "Asset stats",
		Interactive: []InteractiveElement{
			{Ref: 3, Role: "link", Name: "Market data", Href: "/market"},
			{Ref: 4, Role: "button", Name: "Connect Wallet"},
		},
		TextBlocks: []TextBlock{
			{Type: "h1", Text: "Affine"},
			{Type: "p", Text: "The current market cap is $55.4M and liquidity is $44.8M on the main pool."},
		},
	}
	got := formatBrowserFindResults(snap, "market", 4)
	for _, want := range []string{
		"URL: https://example.test/asset",
		"TITLE: Asset stats",
		"SNAPSHOT_ID: 7",
		`QUERY: "market"`,
		`[interactive ref=3] link "Market data"`,
		"[text p] The current market cap is $55.4M",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("find output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Connect Wallet") {
		t.Fatalf("find output should omit non-matches:\n%s", got)
	}
}

func TestBrowserFindNoMatchesHasRecoveryHint(t *testing.T) {
	out := formatBrowserFindResults(&Snapshot{URL: "https://example.test"}, "volume", 8)
	for _, want := range []string{"MATCHES: none", "Next:", "browser_snapshot", "scroll once"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no-match output missing %q:\n%s", want, out)
		}
	}
}

func TestBrowserFindToolRejectsInvalidArgsBeforePageCheck(t *testing.T) {
	tool := FindTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maxLength": 256`) {
		t.Fatalf("schema should publish query maxLength: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("blank query error = %v, want query is required", err)
	}
	requireInvalidArgs(t, err)
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"market","max_results":26}`))
	if err == nil || !strings.Contains(err.Error(), "max_results must be between") {
		t.Fatalf("oversized max_results error = %v, want max_results error", err)
	}
	requireInvalidArgs(t, err)
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"market","unused":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "unused") {
		t.Fatalf("unknown arg error = %v, want unknown field", err)
	}
	requireInvalidArgs(t, err)
}
