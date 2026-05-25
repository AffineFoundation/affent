package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserFindResultsReturnCompactMatches(t *testing.T) {
	result := &BrowserFindResult{
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
	got := formatBrowserFindResults(result, "market", 4)
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
	out := formatBrowserFindResults(&BrowserFindResult{URL: "https://example.test"}, "volume", 8)
	for _, want := range []string{"MATCHES: none", "Next:", "browser_snapshot", "scroll once"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no-match output missing %q:\n%s", want, out)
		}
	}
}

func TestBrowserFindChallengePageIsBlocked(t *testing.T) {
	result := &BrowserFindResult{
		URL:   "https://www.google.com/sorry/index?continue=https://www.google.com/search%3Fq%3Daffine",
		Title: "Before you continue",
		TextBlocks: []TextBlock{
			{Type: "p", Text: "Our systems have detected unusual traffic from your computer network."},
		},
	}
	out, err := formatBrowserFindResult(result, "affine", 8)
	if err == nil {
		t.Fatal("expected browser_find challenge page to return a blocked error")
	}
	for _, want := range []string{"Failure: kind=blocked", "bot/challenge page", "use a different search provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("blocked error missing %q:\n%s", want, err.Error())
		}
	}
	if !strings.Contains(out, "URL: https://www.google.com/sorry/index") {
		t.Fatalf("blocked output should retain page metadata:\n%s", out)
	}
}

func TestBrowserFindNormalPageIsNotBlocked(t *testing.T) {
	out, err := formatBrowserFindResult(&BrowserFindResult{
		URL: "https://example.test",
		TextBlocks: []TextBlock{
			{Type: "p", Text: "Affine subnet market cap and emissions"},
		},
	}, "market cap", 8)
	if err != nil {
		t.Fatalf("normal page should not be blocked: %v", err)
	}
	if !strings.Contains(out, "Affine subnet market cap") {
		t.Fatalf("normal output missing expected match:\n%s", out)
	}
}

func TestBrowserFindDeduplicatesEquivalentTextMatches(t *testing.T) {
	result := &BrowserFindResult{
		URL: "https://example.test",
		TextBlocks: []TextBlock{
			{Type: "div", Text: "Market cap $55.4M Liquidity $44.8M"},
			{Type: "span", Text: "Market cap $55.4M Liquidity $44.8M"},
		},
	}
	got := formatBrowserFindResults(result, "market", 8)
	if strings.Count(got, "Market cap $55.4M Liquidity $44.8M") != 1 {
		t.Fatalf("equivalent text matches should be deduplicated:\n%s", got)
	}
}

func TestBrowserFindMatchesCompoundQueryByTerms(t *testing.T) {
	result := &BrowserFindResult{
		URL: "https://example.test",
		TextBlocks: []TextBlock{
			{Type: "div", Text: "SN 120 Rank 125 0.0637 TAO Volume 29.01 TAO"},
		},
	}
	got := formatBrowserFindResults(result, "Market Cap Volume Price TAO Holders", 8)
	if !strings.Contains(got, "Volume 29.01 TAO") {
		t.Fatalf("compound query should match by meaningful terms:\n%s", got)
	}
}

func TestBrowserFindTimeoutErrorHasRecoveryHint(t *testing.T) {
	err := browserFindTimeoutError("Market Cap Volume Price TAO Holders", browserFindTimeout, context.DeadlineExceeded)
	for _, want := range []string{"browser_find", "timed out", "Failure: kind=timeout", "Next:", "shorter visible keyword", "browser_snapshot"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("timeout error missing %q:\n%s", want, err.Error())
		}
	}
}

func TestBrowserFindResultDecodesDOMShape(t *testing.T) {
	var result BrowserFindResult
	raw := []byte(`{"url":"https://example.test","title":"Example","interactive":[{"ref":2,"role":"link","name":"Market"}],"text_blocks":[{"type":"p","text":"Market cap"}]}`)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://example.test" || result.Title != "Example" {
		t.Fatalf("decoded metadata = %+v", result)
	}
	if len(result.Interactive) != 1 || result.Interactive[0].Ref != 2 {
		t.Fatalf("decoded interactive = %+v", result.Interactive)
	}
	if len(result.TextBlocks) != 1 || result.TextBlocks[0].Text != "Market cap" {
		t.Fatalf("decoded text blocks = %+v", result.TextBlocks)
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
