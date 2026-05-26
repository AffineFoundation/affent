package browser

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/affinefoundation/affent/internal/metrictext"
)

// Snapshot serialization unit tests. These don't need a real browser —
// they exercise Format() against constructed Snapshot values, which is
// what the LLM actually sees and where regressions hurt most.

func TestFormat_BasicShape(t *testing.T) {
	checked := true
	snap := &Snapshot{
		SnapshotID: 7,
		URL:        "https://example.com/",
		Title:      "Example",
		TextBlocks: []TextBlock{
			{Type: "h1", Text: "Welcome"},
			{Type: "p", Text: "Lorem ipsum dolor sit amet."},
		},
		Interactive: []InteractiveElement{
			{Ref: 1, Role: "link", Name: "More info", Href: "/info"},
			{Ref: 2, Role: "textbox", Name: "Search", Value: "old query"},
			{Ref: 3, Role: "checkbox", Name: "Subscribe", Checked: &checked},
		},
	}
	out := snap.Format()

	for _, want := range []string{
		"URL: https://example.com/",
		"TITLE: Example",
		"SNAPSHOT_ID: 7",
		"h1: Welcome",
		"p: Lorem ipsum",
		"[1] link \"More info\" → /info",
		"[2] textbox \"Search\" (value: \"old query\")",
		"[3] checkbox \"Subscribe\" (checked)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Format output missing %q\n---\n%s\n---", want, out)
		}
	}
}

func TestFormatSnapshotResultFlagsBotChallenges(t *testing.T) {
	cases := []struct {
		name string
		snap *Snapshot
		want string
	}{
		{
			name: "google sorry redirect",
			snap: &Snapshot{SnapshotID: 1, URL: "https://www.google.com/sorry/index?continue=https://www.google.com/search%3Fq%3Daffine"},
			want: "google sorry page",
		},
		{
			name: "cloudflare text",
			snap: &Snapshot{
				SnapshotID: 1,
				URL:        "https://example.com/",
				TextBlocks: []TextBlock{{Type: "h1", Text: "Checking if the site connection is secure"}},
			},
			want: "cloudflare challenge text",
		},
		{
			name: "turnstile diagnostic",
			snap: &Snapshot{
				SnapshotID:  1,
				URL:         "https://taostats.io/subnets/120",
				Title:       "0.0631 · SN120 · Affine",
				Diagnostics: []string{"cloudflare_turnstile_or_challenge_visible: page content may be gated; do not treat missing metric values as unavailable facts"},
				TextBlocks:  []TextBlock{{Type: "p", Text: "Market Cap"}},
				Interactive: []InteractiveElement{{Ref: 1, Role: "button", Name: "Connect Wallet"}},
			},
			want: "cloudflare turnstile/challenge widget",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := formatSnapshotResult(c.snap)
			if err == nil {
				t.Fatal("challenge snapshot should return a tool error")
			}
			for _, want := range []string{"Failure: kind=blocked", "Next:", c.want} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("blocked error missing %q:\n%s", want, err)
				}
			}
			if c.name == "google sorry redirect" {
				for _, want := range []string{"do not retry this Google search URL", "Bing", "DuckDuckGo"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("google blocked error missing %q:\n%s", want, err)
					}
				}
			}
			if !strings.Contains(out, "URL: "+c.snap.URL) {
				t.Fatalf("blocked snapshot should still include page evidence for UI/debugging:\n%s", out)
			}
			if len(c.snap.Diagnostics) > 0 && !strings.Contains(out, "PAGE DIAGNOSTICS:") {
				t.Fatalf("blocked snapshot should include diagnostics for UI/debugging:\n%s", out)
			}
			if strings.Contains(out, "SourceAccess:") {
				t.Fatalf("blocked snapshot must not be marked as verified source evidence:\n%s", out)
			}
		})
	}
}

func TestFormatSnapshotResultSurfacesDynamicMetricDiagnostics(t *testing.T) {
	out, err := formatSnapshotResult(&Snapshot{
		SnapshotID: 9,
		URL:        "https://taostats.io/subnets/120",
		Title:      "0.0631 · SN120 · Affine",
		Diagnostics: []string{
			"empty_dynamic_metric_widgets: 3 visible custom metric widget(s) exposed no text value; use browser_network/browser_network_read, API/text/source endpoint, or mark those fields unverified",
		},
		TextBlocks: []TextBlock{
			{Type: "p", Text: "Market Cap"},
			{Type: "p", Text: "24hr Volume"},
		},
	})
	if err != nil {
		t.Fatalf("empty metric widgets alone should warn but not block: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://taostats.io/subnets/120",
		"page_text_below=partial_dynamic_page_evidence",
		"rendered_browser_source_status=partial_dynamic_page_evidence",
		"visible page text is partial dynamic evidence",
		"PAGE DIAGNOSTICS:",
		"empty_dynamic_metric_widgets",
		"browser_network/browser_network_read",
		"mark those fields unverified",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dynamic metric diagnostic output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "page_text_below=verified_page_evidence") {
		t.Fatalf("dynamic metric snapshot must not be marked as fully verified evidence:\n%s", out)
	}
}

func TestApplyNetworkSettleDiagnosticMarksPartialDynamicEvidence(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 14,
		URL:        "https://taostats.io/subnets/120",
		Title:      "Affine SN120 · taostats",
		TextBlocks: []TextBlock{{Type: "p", Text: "Market Cap"}},
	}
	applyNetworkSettleDiagnostic(snap, false)
	applyNetworkSettleDiagnostic(snap, false)

	out, err := formatSnapshotResult(snap)
	if err != nil {
		t.Fatalf("pending network evidence diagnostic should warn but not block: %v", err)
	}
	if count := strings.Count(out, "network_evidence_capture_pending"); count != 1 {
		t.Fatalf("pending network evidence diagnostic count = %d, want 1:\n%s", count, out)
	}
	for _, want := range []string{
		"page_text_below=partial_dynamic_page_evidence",
		"visible page text is partial dynamic evidence (network evidence capture pending)",
		"call browser_snapshot again or browser_network",
		"before treating absent captured refs as unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pending network evidence output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatSnapshotResultIncludesNetworkEvidenceHints(t *testing.T) {
	out, err := formatSnapshotResult(&Snapshot{
		SnapshotID: 12,
		URL:        "https://taostats.io/subnets/120",
		Title:      "Affine SN120 · taostats",
		TextBlocks: []TextBlock{
			{Type: "p", Text: "Market Cap"},
		},
		Network: []NetworkEvidenceEntry{
			{
				Ref:         "n3",
				URL:         "https://taostats.io/api/subnets/120",
				StatusCode:  200,
				Resource:    "fetch",
				ContentType: "application/json",
				Body:        []byte(`{"market_cap":"201.04K T"}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("snapshot with network hints should not fail: %v", err)
	}
	for _, want := range []string{
		"CAPTURED NETWORK RESPONSES:",
		"n3 status=200 resource=fetch content_type=application/json url=https://taostats.io/api/subnets/120",
		"browser_network_read",
		"before citing hidden dashboard values",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("snapshot network hint missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"market_cap"`) {
		t.Fatalf("snapshot network hints must not expose response bodies before browser_network_read:\n%s", out)
	}
}

func TestFormatSnapshotResultAllowsNormalPages(t *testing.T) {
	out, err := formatSnapshotResult(&Snapshot{
		SnapshotID: 2,
		URL:        "https://example.com/docs",
		Title:      "Docs",
		TextBlocks: []TextBlock{{Type: "p", Text: "Readable evidence"}},
	})
	if err != nil {
		t.Fatalf("normal page should not be blocked: %v", err)
	}
	if !strings.Contains(out, "Readable evidence") {
		t.Fatalf("normal snapshot missing content:\n%s", out)
	}
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://example.com/docs",
		"page_text_below=verified_page_evidence",
		"links_in_snapshot=discovered_unverified_until_opened",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("normal snapshot missing source access marker %q:\n%s", want, out)
		}
	}
}

func TestFormatSnapshotResultMarksNotFoundPagesAsDiscoveryOnly(t *testing.T) {
	out, err := formatSnapshotResult(&Snapshot{
		SnapshotID: 8,
		URL:        "https://example.com/missing",
		Title:      "404 - Page Not Found",
		TextBlocks: []TextBlock{
			{Type: "h1", Text: "Page not found"},
			{Type: "p", Text: "Use the navigation links to reach /docs or /subnets."},
		},
		Interactive: []InteractiveElement{
			{Ref: 1, Role: "link", Name: "Docs", Href: "/docs"},
		},
	})
	if err != nil {
		t.Fatalf("404 pages should still return a snapshot body: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://example.com/missing",
		"page_text_below=not_found_page_discovery_only",
		"links_in_snapshot=discovered_unverified_until_opened",
		"Next: treat this page as a not-found page",
		"do not cite the page body as verified evidence",
		"Page not found",
		"[1] link \"Docs\" → /docs",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("404 snapshot missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "page_text_below=verified_page_evidence") {
		t.Fatalf("404 page must not be marked as verified evidence:\n%s", out)
	}
}

func TestFormatSnapshotResultWarnsOnMultiplePriceLikeValues(t *testing.T) {
	out, err := formatSnapshotResult(&Snapshot{
		SnapshotID: 11,
		URL:        "https://tao.app/subnets/120",
		Title:      "Affine SN120 | TAO.app",
		Interactive: []InteractiveElement{
			{Ref: 1, Role: "button", Name: "TAO Price $277.32"},
		},
		TextBlocks: []TextBlock{
			{Type: "p", Text: "TAO Price $277.32 MC $3.03B FDV $5.82B"},
			{Type: "p", Text: "Price 0.06342 T Market Cap 201.04K T FDV 1.32M T"},
		},
	})
	if err != nil {
		t.Fatalf("normal page should not be blocked: %v", err)
	}
	for _, want := range []string{
		metrictext.AmbiguityNote,
		"TAO Price $277.32",
		"Price 0.06342 T",
		"SourceAccess: browser_rendered_url=https://tao.app/subnets/120",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("snapshot missing %q:\n%s", want, out)
		}
	}
}

func TestFormatSnapshotResultRecordsRequestedURLWhenRedirected(t *testing.T) {
	out, err := formatSnapshotResultWithRequested(&Snapshot{
		SnapshotID: 4,
		URL:        "https://example.com/final",
		Title:      "Final",
		TextBlocks: []TextBlock{{Type: "p", Text: "Rendered final route"}},
	}, "https://example.com/start")
	if err != nil {
		t.Fatalf("redirected page should not be blocked: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://example.com/final",
		"requested_url=https://example.com/start",
		"page_text_below=verified_page_evidence",
		"Rendered final route",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("redirected snapshot missing %q:\n%s", want, out)
		}
	}
}

func TestFormatSnapshotResultMarksSearchPagesAsDiscoveryOnly(t *testing.T) {
	out, err := formatSnapshotResult(&Snapshot{
		SnapshotID: 3,
		URL:        "https://duckduckgo.com/?q=affine+bittensor+SN120",
		Title:      "affine bittensor SN120 at DuckDuckGo",
		TextBlocks: []TextBlock{{Type: "a", Text: "Affine SN120 official page"}},
	})
	if err != nil {
		t.Fatalf("search page should not be blocked: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://duckduckgo.com/?q=affine+bittensor+SN120",
		"page_text_below=search_results_discovery_only",
		"result_links_and_snippets=unverified_until_opened",
		"Next: treat this page as discovery only",
		"open 1-3 high-value result URLs",
		"do not cite snippets as verified facts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("search snapshot missing discovery-only marker %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "page_text_below=verified_page_evidence") {
		t.Fatalf("search result page must not be marked as ordinary verified page evidence:\n%s", out)
	}
}

func TestFormat_InteractiveBeforePageText(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 1,
		URL:        "https://example.com/",
		TextBlocks: []TextBlock{
			{Type: "p", Text: "later content"},
		},
		Interactive: []InteractiveElement{
			{Ref: 9, Role: "link", Name: "Important action", Href: "/go"},
		},
	}
	out := snap.Format()
	interactiveAt := strings.Index(out, "INTERACTIVE ELEMENTS:")
	textAt := strings.Index(out, "PAGE TEXT:")
	if interactiveAt < 0 || textAt < 0 || interactiveAt > textAt {
		t.Fatalf("interactive elements should appear before page text so refs survive context truncation:\n%s", out)
	}
}

func TestFormat_GroupsShortTextBlocksAndOmitDuplicates(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 1,
		URL:        "https://example.com/",
		Interactive: []InteractiveElement{
			{Ref: 1, Role: "link", Name: "AffineSN120", Href: "/subnets/120"},
		},
		TextBlocks: []TextBlock{
			{Type: "p", Text: "AffineSN120"},
			{Type: "p", Text: "Price"},
			{Type: "p", Text: "0.0634"},
			{Type: "p", Text: "Market Cap"},
		},
	}
	out := snap.Format()
	if strings.Contains(out, "p: AffineSN120") {
		t.Fatalf("duplicate interactive text should be omitted:\n%s", out)
	}
	if !strings.Contains(out, "p: Price | 0.0634 | Market Cap") {
		t.Fatalf("short adjacent text blocks should be grouped:\n%s", out)
	}
}

func TestFormat_CapsInteractiveElements(t *testing.T) {
	var interactive []InteractiveElement
	for i := 1; i <= maxFormattedInteractive+3; i++ {
		interactive = append(interactive, InteractiveElement{Ref: i, Role: "link", Name: "Item"})
	}
	snap := &Snapshot{SnapshotID: 1, URL: "https://example.com/", Interactive: interactive}
	out := snap.Format()
	if strings.Contains(out, "[123]") {
		t.Fatalf("formatted snapshot should cap interactive elements:\n%s", out)
	}
	if !strings.Contains(out, "interactive elements omitted") {
		t.Fatalf("formatted snapshot should report omitted interactive elements:\n%s", out)
	}
}

func TestFormatInteractive_TruncatesLongHrefAndValue(t *testing.T) {
	got := formatInteractive(InteractiveElement{
		Ref:   1,
		Role:  "link",
		Name:  "Long",
		Href:  "https://example.com/" + strings.Repeat("x", maxFormattedInteractiveURL+20),
		Value: strings.Repeat("v", maxFormattedValue+20),
	})
	if !strings.Contains(got, "...(truncated)") {
		t.Fatalf("expected long href/value truncation marker, got %q", got)
	}
	if len(got) > maxFormattedInteractiveURL+maxFormattedValue+80 {
		t.Fatalf("formatted interactive line too large: len=%d line=%q", len(got), got)
	}
}

func TestTruncateSnapshotField_SmallLimitKeepsUTF8(t *testing.T) {
	got := truncateSnapshotField("hello世界", 7)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateSnapshotField returned invalid UTF-8: %q", got)
	}
	if len(got) > 7 {
		t.Fatalf("truncateSnapshotField exceeded limit: len=%d value=%q", len(got), got)
	}
}

func TestFormat_CompactsDashboardLikeSnapshot(t *testing.T) {
	var text []TextBlock
	for i := 0; i < 200; i++ {
		text = append(text, TextBlock{Type: "p", Text: []string{
			"Subnets", "BittensorTAO", "USD", "Market Cap", "24hr Volume",
			"Affine", "SN120", "0.0634", "55.5M", "2.14M",
		}[i%10]})
	}
	var interactive []InteractiveElement
	for i := 1; i <= 150; i++ {
		name := "Subnet"
		href := fmt.Sprintf("https://taostats.io/subnets/%d", i)
		if i == 120 {
			name = "AffineSN120"
			href = "https://taostats.io/subnets/120"
		}
		interactive = append(interactive, InteractiveElement{Ref: i, Role: "link", Name: name, Href: href})
	}
	snap := &Snapshot{
		SnapshotID:      9,
		URL:             "https://taostats.io/subnets",
		Title:           "Subnets · taostats",
		TextBlocks:      text,
		Interactive:     interactive,
		TruncatedBlocks: true,
	}
	out := snap.Format()
	affineAt := strings.Index(out, `[120] link "AffineSN120"`)
	textAt := strings.Index(out, "PAGE TEXT:")
	if affineAt < 0 {
		t.Fatalf("formatted dashboard snapshot should keep Affine ref visible:\n%s", out)
	}
	if textAt < 0 || affineAt > textAt {
		t.Fatalf("critical table refs should appear before passive dashboard text:\n%s", out)
	}
	if affineAt > 7*1024 {
		t.Fatalf("critical table ref should survive the browser tool context cap; offset=%d", affineAt)
	}
	if len(out) > 20*1024 {
		t.Fatalf("dashboard-like snapshot should stay compact; len=%d", len(out))
	}
	if !strings.Contains(out, "interactive elements omitted") {
		t.Fatalf("large dashboard should report omitted interactive elements:\n%s", out)
	}
}

func TestFormat_NoInteractive(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 1,
		URL:        "about:blank",
		Title:      "",
	}
	out := snap.Format()
	if !strings.Contains(out, "(no interactive elements detected)") {
		t.Errorf("expected empty-page hint, got:\n%s", out)
	}
	if strings.Contains(out, "TITLE:") {
		t.Errorf("empty title should be omitted from output, got:\n%s", out)
	}
}

func TestFormat_TruncatedBlocks(t *testing.T) {
	snap := &Snapshot{
		SnapshotID: 1,
		URL:        "https://example.com",
		TextBlocks: []TextBlock{
			{Type: "p", Text: "first"},
		},
		TruncatedBlocks: true,
	}
	out := snap.Format()
	if !strings.Contains(out, "text blocks truncated") {
		t.Errorf("expected truncation marker, got:\n%s", out)
	}
}

func TestFormatInteractive_UncheckedRendersExplicitly(t *testing.T) {
	unchecked := false
	el := InteractiveElement{
		Ref: 1, Role: "checkbox", Name: "Opt in", Checked: &unchecked,
	}
	got := formatInteractive(el)
	if !strings.Contains(got, "(unchecked)") {
		t.Errorf("expected explicit (unchecked) marker, got %q", got)
	}
}

func TestStaleRefError_Message(t *testing.T) {
	err := &StaleRefError{Ref: 42}
	msg := err.Error()
	if !strings.Contains(msg, "ref 42") {
		t.Errorf("error message should mention ref number, got %q", msg)
	}
	if !strings.Contains(msg, "browser_snapshot") {
		t.Errorf("error message should hint at the recovery action, got %q", msg)
	}
	if !strings.Contains(msg, "Failure: kind=stale_ref") {
		t.Errorf("error message should expose stale_ref failure kind, got %q", msg)
	}
	if !strings.Contains(msg, "Next:") {
		t.Errorf("error message should include a Next step, got %q", msg)
	}
	if !strings.Contains(msg, "fresh ref") {
		t.Errorf("error message should ask for a fresh ref, got %q", msg)
	}
}

// TestSnapshotJS_IsValidJSFunction is a smoke check that the embedded
// JS at least parses as an arrow function body. It catches the common
// regression of breaking the JS with an unescaped backtick or a stray
// raw string.
func TestSnapshotJS_IsValidJSFunction(t *testing.T) {
	if !strings.HasPrefix(snapshotJS, "() => {") {
		t.Errorf("snapshotJS must begin with arrow-function header so rod's Eval treats it as an expression, got prefix %q", snapshotJS[:min(20, len(snapshotJS))])
	}
	if !strings.HasSuffix(strings.TrimSpace(snapshotJS), "}") {
		t.Errorf("snapshotJS must close its function body, got suffix %q", snapshotJS[max(0, len(snapshotJS)-20):])
	}
	// Pair-balance sanity check on braces.
	if strings.Count(snapshotJS, "{") != strings.Count(snapshotJS, "}") {
		t.Errorf("snapshotJS has unbalanced braces: opens=%d closes=%d",
			strings.Count(snapshotJS, "{"), strings.Count(snapshotJS, "}"))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
