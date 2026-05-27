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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/rs/zerolog"
)

func newTestConv(t *testing.T) *Conversation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	return c
}

// newTestStore returns a FileMemoryStore wired to a temp dir with
// tight caps suitable for loop-side tests. The internal/memory
// package has its own copy with more knobs; this is the minimal
// helper for the root package's tests.
func newTestStore(t *testing.T) *memory.FileMemoryStore {
	t.Helper()
	dir := t.TempDir()
	s := memory.NewFileMemoryStore(dir)
	s.UserPath = filepath.Join(dir, "USER.md")
	return s
}

func TestDefaultSystemPromptReflectsRuntimeBudgets(t *testing.T) {
	for _, want := range []string{
		fmt.Sprintf("~%d tool calls", DefaultMaxTurnSteps),
		fmt.Sprintf("After %d tool calls", DefaultMaxTurnSteps/2),
		fmt.Sprintf("past %d calls", DefaultMaxTurnSteps*4/5),
		fmt.Sprintf("~%dKB", MaxToolResultBytesInContext/1024),
		"symbol_context",
		"repo_search",
		"Match the user's language",
		"Chinese, answer in Chinese",
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("DefaultSystemPrompt missing %q:\n%s", want, DefaultSystemPrompt)
		}
	}
}

func TestBaseSystemPromptsMatchUserLanguage(t *testing.T) {
	for name, prompt := range map[string]string{
		"default":     DefaultSystemPrompt,
		"limited":     LimitedToolSystemPrompt,
		"memory-only": MemoryOnlySystemPrompt,
	} {
		if !strings.Contains(prompt, "Match the user's language") {
			t.Fatalf("%s prompt should tell the model to match the user's language:\n%s", name, prompt)
		}
	}
}

func TestLimitedToolSystemPromptReflectsToolBudget(t *testing.T) {
	for _, want := range []string{
		fmt.Sprintf("~%d tool calls", DefaultMaxTurnSteps),
		fmt.Sprintf("after %d calls", DefaultMaxTurnSteps/2),
		fmt.Sprintf("Past %d calls", DefaultMaxTurnSteps*4/5),
		"prefer browser_find",
		"not-interactable error",
		"answer with a marked gap",
	} {
		if !strings.Contains(LimitedToolSystemPrompt, want) {
			t.Fatalf("LimitedToolSystemPrompt missing %q:\n%s", want, LimitedToolSystemPrompt)
		}
	}
}

func TestBaseSystemPromptsAvoidCapabilityOverclaims(t *testing.T) {
	for name, prompt := range map[string]string{
		"default":     DefaultSystemPrompt,
		"limited":     LimitedToolSystemPrompt,
		"memory-only": MemoryOnlySystemPrompt,
	} {
		for _, want := range []string{
			"Do not claim specific model/runtime capabilities",
			"listed in the available tools",
			"observed from tool results",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s prompt should constrain capability claims; missing %q:\n%s", name, want, prompt)
			}
		}
	}
}

func TestBaseSystemPromptsRequireExactEvidenceIdentifiers(t *testing.T) {
	for name, prompt := range map[string]string{
		"default":     DefaultSystemPrompt,
		"limited":     LimitedToolSystemPrompt,
		"memory-only": MemoryOnlySystemPrompt,
	} {
		for _, want := range []string{
			"copy them exactly",
			"Do not rewrite",
			"reconstruct identifiers from memory",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s prompt should require exact evidence identifiers; missing %q:\n%s", name, want, prompt)
			}
		}
	}
}

func TestBaseSystemPromptForSurface(t *testing.T) {
	if got := BaseSystemPromptForSurface(SystemPromptSurface{Builtins: true}); got != DefaultSystemPrompt {
		t.Fatal("builtins surface should use default workspace prompt")
	}
	if got := BaseSystemPromptForSurface(SystemPromptSurface{Memory: true}); got != MemoryOnlySystemPrompt {
		t.Fatal("memory-only surface should use memory-only prompt")
	}
	got := BaseSystemPromptForSurface(SystemPromptSurface{Memory: true, OtherTools: true})
	if got != LimitedToolSystemPrompt {
		t.Fatal("mixed non-builtin surface should use limited-tool prompt")
	}
	if got := BaseSystemPromptForSurface(SystemPromptSurface{}); got != LimitedToolSystemPrompt {
		t.Fatal("empty tool surface should use limited-tool prompt")
	}
}

func TestWithMemorySystemGuidance_AppendsOnce(t *testing.T) {
	base := "be helpful"
	once := WithMemorySystemGuidance(base)
	for _, want := range []string{"Memory retrieval:", "action=list", "action=search", "target=user", "target=memory", "topic=core"} {
		if !strings.Contains(once, want) {
			t.Fatalf("memory guidance missing %q:\n%s", want, once)
		}
	}
	if twice := WithMemorySystemGuidance(once); twice != once {
		t.Fatal("memory guidance should be idempotent")
	}
	if got := WithMemorySystemGuidance(""); !strings.Contains(got, DefaultSystemPrompt) || !strings.Contains(got, "Memory retrieval:") {
		t.Fatalf("empty prompt should fall back to default + memory guidance:\n%s", got)
	}
}

func TestWithExternalResearchSystemGuidance_AppendsOnce(t *testing.T) {
	base := "be helpful"
	surface := externalResearchToolSurface{WebSearch: true, WebFetch: true, Browser: true, BrowserFind: true, BrowserNetwork: true}
	once := WithExternalResearchSystemGuidance(base, surface)
	for _, want := range []string{"External research:", "web_search", "authoritative", "Do not open every search result", "weak sentiment", "Source hint", "llms.txt", "Direct-reader warning", "browser_navigate", "browser_find", "browser_network", "browser_network_read", "same-site XHR/fetch", "sibling API subdomains", "app.example.com -> api.example.com", "network_evidence_capture_pending", "browser_snapshot once more if the capture is still settling", "repeated scrolling", "dynamic dashboards", "field-label queries", "price market cap FDV volume supply TVL", "24h 7d volume market cap", "validators miners stake emission", "Do not repeat browser_find with only the entity name", "If the current page already shows the target entity in a visible list or row", "exact row label, ticker, or id", "Dashboard text can interleave global header metrics", "label/value adjacency", "bot/challenge", "social posts", "dates/freshness", "Embedded data preview", "page-source evidence", "If web_fetch fails", "Do not keep retrying the same failing URL", "If web_search returns no results", "distinctive entities", "stale_ref", "fresh visible ref", "open 1-3 high-value visible result URLs", "before refining the search", "Preserve user-provided disambiguators", "network/subnet id", "parent ecosystem, the entity name or ticker, and the metric intent", "same-name standalone product", "searched the asserted parent ecosystem", "absent from one visible list", "parent ecosystem plus known ids/synonyms", "successfully accessed only when a tool actually read that URL", "actual fetched_url/browser_rendered_url", "requested_url only records what you asked for", "preserve ref=...", "browser_find no-match only means", "current rendered page text", "Do not say a field was unavailable", "PAGE TEXT", "discovered/unverified", "API/text/export endpoints"} {
		if !strings.Contains(once, want) {
			t.Fatalf("external research guidance missing %q:\n%s", want, once)
		}
	}
	if twice := WithExternalResearchSystemGuidance(once, surface); twice != once {
		t.Fatal("external research guidance should be idempotent")
	}
	if got := WithExternalResearchSystemGuidance("", surface); !strings.Contains(got, DefaultSystemPrompt) || !strings.Contains(got, "External research:") {
		t.Fatalf("empty prompt should fall back to default + external research guidance:\n%s", got)
	}

	browserOnly := WithExternalResearchSystemGuidance("be helpful", externalResearchToolSurface{Browser: true})
	for _, forbidden := range []string{"web_search", "web_fetch"} {
		if strings.Contains(browserOnly, forbidden) {
			t.Fatalf("browser-only guidance should not mention unavailable %q:\n%s", forbidden, browserOnly)
		}
	}
	for _, want := range []string{"browser_navigate", "browser_snapshot", "unavailable discovery tools", "stale_ref", "not_interactable", "fresh visible ref"} {
		if !strings.Contains(browserOnly, want) {
			t.Fatalf("browser-only guidance missing %q:\n%s", want, browserOnly)
		}
	}
	if !strings.Contains(browserOnly, "public search result pages") || !strings.Contains(browserOnly, "bot challenge") {
		t.Fatalf("browser-only guidance should explain browser-based discovery:\n%s", browserOnly)
	}
	for _, want := range []string{"Prefer Bing, DuckDuckGo, or site search over Google", "Google's bot/sorry page"} {
		if !strings.Contains(browserOnly, want) {
			t.Fatalf("browser-only guidance should steer away from Google challenge pages, missing %q:\n%s", want, browserOnly)
		}
	}
	if !strings.Contains(browserOnly, "Do not guess URL paths") || !strings.Contains(browserOnly, "subnet numbers") {
		t.Fatalf("browser-only guidance should discourage guessed routes and ids:\n%s", browserOnly)
	}

	webOnly := WithExternalResearchSystemGuidance("be helpful", externalResearchToolSurface{WebSearch: true, WebFetch: true})
	for _, forbidden := range []string{"browser_navigate", "browser_snapshot", "browser tools"} {
		if strings.Contains(webOnly, forbidden) {
			t.Fatalf("web-only guidance should not mention unavailable %q:\n%s", forbidden, webOnly)
		}
	}
}

func TestFinalNoToolsPromptsRequireEvidenceRescan(t *testing.T) {
	for name, prompt := range map[string]string{
		"length":       lengthRecoveryPrompt,
		"max-turns":    maxTurnsFinalPrompt,
		"tool-budget":  toolBudgetFinalPrompt,
		"forced-tools": forceNoToolsFinalPrompt,
	} {
		for _, want := range []string{
			"Do not call tools",
			"Re-scan the latest successful SourceAccess outputs",
			"prices, counts, dates",
			"before declaring any field unavailable",
			"Discovery-only pages (search results, 404/not-found pages",
			"actual fetched_url/browser_rendered_url",
			"preserve ref=...",
			"requested_url and discovered links as unverified",
			"Do not infer project maturity, scale, ranking quality",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s final prompt missing %q:\n%s", name, want, prompt)
			}
		}
	}
}

func TestFinalEvidenceDisciplineRejectsDiscoveryOnlyPages(t *testing.T) {
	for _, want := range []string{
		"Discovery-only pages (search results, 404/not-found pages",
		"Re-scan the latest successful SourceAccess outputs",
		"before declaring any field unavailable",
	} {
		if !strings.Contains(finalEvidenceDiscipline, want) {
			t.Fatalf("final evidence discipline missing %q:\n%s", want, finalEvidenceDiscipline)
		}
	}
}

func TestFinalEvidenceDigestExtractsRecentVerifiedMetrics(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role:    "tool",
			Name:    "browser_navigate",
			Content: "SourceAccess: browser_rendered_url=https://duckduckgo.com/?q=affine; snapshot_id=1; page_text_below=search_results_discovery_only\nURL: https://duckduckgo.com/?q=affine\nTITLE: search\n[text span] ignored result snippet with Price $999\n",
		},
		{
			Role: "tool",
			Name: "browser_navigate",
			Content: "SourceAccess: browser_rendered_url=https://example.test/missing; snapshot_id=2; page_text_below=not_found_page_discovery_only; links_in_snapshot=discovered_unverified_until_opened\n" +
				"URL: https://example.test/missing\n" +
				"TITLE: 404 - Page Not Found\n" +
				"PAGE TEXT:\n" +
				"[text p] use the navigation links to reach /docs or /subnets\n",
		},
		{
			Role: "tool",
			Name: "browser_find",
			Content: "SourceAccess: browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about; requested_url=https://www.tao.app/subnets; snapshot_id=14; page_text_below=verified_page_evidence\n" +
				"URL: https://www.tao.app/subnets/120?active_tab=about\n" +
				"TITLE: SN120 - Affine | TAO.app | Your Gateway to Bittensor\n" +
				"QUERY: \"Affine metric price market cap stake TAO\"\n" +
				"[text span] TAO Price $ 277.32 -1.02 % 1D Vol $ 168.66M -39 % MC $ 3.03B FDV $ 5.82B Circ. Supply 10.94M Block 8,260,180\n" +
				"[text div] Price 0.06342 T/ d L: 0.060 T H: 0.086 T Market Cap 201.04K T FDV 1.32M T\n",
		},
	}
	got := finalEvidenceDigest(msgs)
	for _, want := range []string{
		"Final evidence digest",
		"Metric caution",
		"Source status caution",
		"label when the adjacency",
		"Links in page text are discovered/unverified",
		"browser_find",
		"browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about",
		"Accessed URL: https://www.tao.app/subnets/120?active_tab=about",
		"Requested URL only: https://www.tao.app/subnets",
		"requested_url=https://www.tao.app/subnets",
		"TAO Price $ 277.32",
		"MC $ 3.03B",
		"Market Cap 201.04K T",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ignored result snippet") {
		t.Fatalf("digest should skip discovery-only search result pages:\n%s", got)
	}
	if strings.Contains(got, "page_text_below=not_found_page_discovery_only") || strings.Contains(got, "use the navigation links to reach /docs or /subnets") {
		t.Fatalf("digest should skip discovery-only 404 pages:\n%s", got)
	}
}

func TestFinalEvidenceDigestPreservesNetworkRef(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			Name: "browser_network_read",
			Content: "SourceAccess: browser_network_url=https://api.taostats.io/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n3; status=200; content_type=application/json; source_method=network_xhr_fetch\n" +
				"JSON_PATH: $.data.market_cap\n" +
				"BODY_BYTES: 28\n" +
				"{\"market_cap\":\"201.04K T\"}",
		},
	}
	got := finalEvidenceDigest(msgs)
	for _, want := range []string{
		"browser_network_read",
		"browser_network_url=https://api.taostats.io/subnets/120",
		"ref=n3",
		"Network ref: n3",
		"Requested URL only: https://taostats.io/subnets/120",
		"browser_network_read returns a SourceAccess line; preserve ref=...",
		"market_cap",
		"201.04K T",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
}

func TestFinalEvidenceDigestSkipsRenderedBrowserDiscoveryOnlyFallbacks(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			Name: "web_fetch",
			Content: "SourceAccess: fetched_url=https://example.test/missing; requested_url=https://example.test/missing?q=affine; mode=rendered_browser_fallback; linked_urls_in_content=discovered_unverified_until_fetched; rendered_browser_source_status=not_found_page_discovery_only\n" +
				"[rendered browser fallback succeeded: URL=https://example.test/missing, DirectFetchReason=\"not_found\"]\n" +
				"SourceAccess: browser_rendered_url=https://example.test/missing; snapshot_id=21; page_text_below=not_found_page_discovery_only; links_in_snapshot=discovered_unverified_until_opened\n" +
				"URL: https://example.test/missing\n" +
				"TITLE: 404 - Page Not Found\n" +
				"PAGE TEXT:\n" +
				"[text p] use the navigation links to reach /docs or /subnets\n",
		},
	}
	got := finalEvidenceDigest(msgs)
	if got != "" {
		t.Fatalf("digest should skip rendered-browser discovery-only fallbacks entirely, got:\n%s", got)
	}
}

func TestNormalizeFinalEvidenceLine_CompactsWhitespaceAndTruncates(t *testing.T) {
	got := normalizeFinalEvidenceLine("  hello \n\t world  " + strings.Repeat("你", 300))
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("normalizeFinalEvidenceLine should compact whitespace, got %q", got)
	}
	if !strings.HasPrefix(got, "hello world") {
		t.Fatalf("normalizeFinalEvidenceLine lost leading content: %q", got)
	}
	if len(got) > 523 {
		t.Fatalf("normalizeFinalEvidenceLine too long: %d", len(got))
	}
}

func TestFinalEvidenceDigestPrioritizesMetricEvidenceOverRecentLowValuePages(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			Name: "browser_find",
			Content: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120/statistics; snapshot_id=8; page_text_below=verified_page_evidence\n" +
				"URL: https://taostats.io/subnets/120/statistics\n" +
				"TITLE: Affine SN120 statistics\n" +
				"QUERY: \"TAO Emission Stake Validators Miners UID Capacity\"\n" +
				"[text div] Subnet Price 0.0639 TAO | Subnet Emission 8260267 | Validators 64 | Miners 192 | Stake 44.2K TAO\n",
		},
		{
			Role: "tool",
			Name: "browser_scroll",
			Content: "SourceAccess: browser_rendered_url=https://hub.docker.com/r/affinefoundation/affine; snapshot_id=17; page_text_below=verified_page_evidence\n" +
				"URL: https://hub.docker.com/r/affinefoundation/affine\n" +
				"TITLE: affinefoundation/affine - Docker Hub\n" +
				"[text span] affinefoundation/affine latest image pull instructions\n",
		},
	}
	got := finalEvidenceDigest(msgs)
	metricIdx := strings.Index(got, "https://taostats.io/subnets/120/statistics")
	dockerIdx := strings.Index(got, "https://hub.docker.com/r/affinefoundation/affine")
	if metricIdx < 0 {
		t.Fatalf("digest missing metric evidence:\n%s", got)
	}
	if dockerIdx >= 0 && metricIdx > dockerIdx {
		t.Fatalf("metric evidence should rank before recent low-value Docker page:\n%s", got)
	}
	for _, want := range []string{"Subnet Price 0.0639 TAO", "Validators 64", "Metric caution"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
}

func TestFinalEvidenceDigestPreservesMultiplePriceLikeValuesSeparately(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			Name: "browser_snapshot",
			Content: "SourceAccess: browser_rendered_url=https://www.tao.app/subnets/120?active_tab=about; snapshot_id=14; page_text_below=verified_page_evidence\n" +
				"URL: https://www.tao.app/subnets/120?active_tab=about\n" +
				"TITLE: SN120 - Affine | TAO.app | Your Gateway to Bittensor\n" +
				"[text span] TAO Price $ 277.32 -1.02 % 1D Vol $ 168.66M -39 % MC $ 3.03B FDV $ 5.82B Circ. Supply 10.94M\n" +
				"[text div] Price 0.06342 T/ d L: 0.060 T H: 0.086 T Market Cap 201.04K T FDV 1.32M T\n",
		},
	}
	got := finalEvidenceDigest(msgs)
	for _, want := range []string{"TAO Price $ 277.32", "Price 0.06342 T", "Market Cap 201.04K T", "MC $ 3.03B", "FDV 1.32M T"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Metric caution: multiple price-like values are visible in this source") {
		t.Fatalf("digest should explicitly warn about multiple price-like values:\n%s", got)
	}
	if strings.Contains(got, "Price 277.32") && !strings.Contains(got, "TAO Price $ 277.32") {
		t.Fatalf("digest should preserve the visible label on the larger price-like value:\n%s", got)
	}
}

func TestFinalEvidenceDigestDownranksFindMisses(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "tool",
			Name: "web_fetch",
			Content: "SourceAccess: fetched_url=https://github.com/ysjprojects/affine-sn120; linked_urls_in_content=discovered_unverified_until_fetched\n" +
				"TITLE: GitHub - ysjprojects/affine-sn120\n" +
				"Affine is an incentivized RL environment for Bittensor SN120.\n",
		},
		{
			Role: "tool",
			Name: "browser_find",
			Content: "SourceAccess: browser_rendered_url=https://taostats.io/subnets; snapshot_id=3; page_text_below=verified_page_evidence\n" +
				"URL: https://taostats.io/subnets\n" +
				"TITLE: Subnets · taostats\n" +
				"QUERY: \"affine\"\n" +
				"MATCHES: none\n",
		},
	}
	got := finalEvidenceDigest(msgs)
	githubIdx := strings.Index(got, "https://github.com/ysjprojects/affine-sn120")
	findIdx := strings.Index(got, "MATCHES: none")
	if githubIdx < 0 {
		t.Fatalf("digest missing successful source:\n%s", got)
	}
	if findIdx >= 0 && githubIdx > findIdx {
		t.Fatalf("successful source should rank before find miss:\n%s", got)
	}
	for _, want := range []string{"browser_find no-match", "not that the entity is absent from the whole site"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
}

func TestFinalAnswerNeedsEvidenceRecovery(t *testing.T) {
	if !finalAnswerNeedsEvidenceRecovery("让我尝试多种来源搜索。", 3) {
		t.Fatal("short process narration after tool use should trigger recovery")
	}
	if finalAnswerNeedsEvidenceRecovery("让我尝试多种来源搜索。", 0) {
		t.Fatal("no-tool answers should not trigger recovery")
	}
	if finalAnswerNeedsEvidenceRecovery("已验证：Affine 是 Bittensor 的 SN120，来源是 taostats.io/subnets/120。", 3) {
		t.Fatal("substantive short answer should not trigger recovery")
	}
	if finalAnswerNeedsEvidenceRecovery(strings.Repeat("让我继续搜索。", 40), 3) {
		t.Fatal("long response should not be treated as the short process-narration recovery case")
	}
}

func TestWithRuntimeContextSystemGuidance_IncludesDateAndFreshnessRule(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 34, 56, 0, time.FixedZone("test", 8*60*60))
	got := WithRuntimeContextSystemGuidance("be helpful", now)
	for _, want := range []string{
		"Runtime context:",
		"Current UTC date: 2026-05-25.",
		"access date",
		"Do not invent source dates",
		"distinguish source publication/update dates from access date",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runtime context guidance missing %q:\n%s", want, got)
		}
	}
}

func TestExternalResearchGuidanceMatchesToolSurface(t *testing.T) {
	cases := []struct {
		name      string
		surface   externalResearchToolSurface
		want      []string
		forbidden []string
	}{
		{
			name:    "search fetch browser",
			surface: externalResearchToolSurface{WebSearch: true, WebFetch: true, Browser: true},
			want: []string{
				"web_search for discovery",
				"Do not open every search result",
				"Source hint",
				"Direct-reader warning",
				"Preserve user-provided disambiguators",
				"Use web_fetch",
				"browser_navigate/browser_snapshot",
				"dynamic dashboards",
				"bot/challenge",
				"from search results",
				"If web_search returns no results",
				"stale_ref",
			},
		},
		{
			name:    "search fetch only",
			surface: externalResearchToolSurface{WebSearch: true, WebFetch: true},
			want: []string{
				"web_search for discovery",
				"Do not open every search result",
				"Source hint",
				"Direct-reader warning",
				"Preserve user-provided disambiguators",
				"Use web_fetch",
				"Avoid using web_fetch on result-list pages",
				"from search results",
				"If web_search returns no results",
			},
			forbidden: []string{"browser_navigate", "browser_snapshot", "browser tools"},
		},
		{
			name:    "fetch only",
			surface: externalResearchToolSurface{WebFetch: true},
			want: []string{
				"Use web_fetch",
				"Preserve user-provided disambiguators",
				"Avoid using web_fetch on result-list pages",
				"direct-reader warning",
				"try another known public URL",
			},
			forbidden: []string{"web_search", "browser_navigate", "browser_snapshot", "browser tools"},
		},
		{
			name:    "fetch browser",
			surface: externalResearchToolSurface{WebFetch: true, Browser: true},
			want: []string{
				"Use web_fetch",
				"browser_navigate/browser_snapshot for rendered pages",
				"Preserve user-provided disambiguators",
				"try another known public URL",
				"Do not guess URL paths",
				"Prefer Bing, DuckDuckGo, or site search over Google",
				"stale_ref",
			},
			forbidden: []string{"web_search", "browser tools"},
		},
		{
			name:    "browser only",
			surface: externalResearchToolSurface{Browser: true},
			want: []string{
				"browser_navigate/browser_snapshot for page inspection",
				"unavailable discovery tools",
				"Preserve user-provided disambiguators",
				"stale_ref",
			},
			forbidden: []string{"web_search", "web_fetch"},
		},
		{
			name:    "search only",
			surface: externalResearchToolSurface{WebSearch: true},
			want: []string{
				"web_search to discover and compare source snippets",
				"full-page reading is unavailable",
				"Preserve user-provided disambiguators",
				"If web_search returns no results",
			},
			forbidden: []string{"web_fetch", "browser_navigate", "browser_snapshot", "browser tools"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WithExternalResearchSystemGuidance("be helpful", c.surface)
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Fatalf("guidance missing %q:\n%s", want, got)
				}
			}
			for _, forbidden := range c.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("guidance should not mention unavailable %q:\n%s", forbidden, got)
				}
			}
		})
	}
}

func TestExternalResearchSurfaceForRegistry(t *testing.T) {
	cases := []struct {
		name   string
		tools  []string
		want   externalResearchToolSurface
		wantOK bool
	}{
		{
			name: "empty",
		},
		{
			name:  "unrelated",
			tools: []string{"shell"},
		},
		{
			name:   "web search",
			tools:  []string{"web_search"},
			want:   externalResearchToolSurface{WebSearch: true},
			wantOK: true,
		},
		{
			name:   "web fetch",
			tools:  []string{"web_fetch"},
			want:   externalResearchToolSurface{WebFetch: true},
			wantOK: true,
		},
		{
			name:   "browser navigate",
			tools:  []string{"browser_navigate"},
			want:   externalResearchToolSurface{Browser: true},
			wantOK: true,
		},
		{
			name:   "browser snapshot",
			tools:  []string{"browser_snapshot"},
			want:   externalResearchToolSurface{Browser: true},
			wantOK: true,
		},
		{
			name:   "browser find",
			tools:  []string{"browser_find"},
			want:   externalResearchToolSurface{Browser: true, BrowserFind: true},
			wantOK: true,
		},
		{
			name:   "browser network",
			tools:  []string{"browser_network"},
			want:   externalResearchToolSurface{Browser: true, BrowserNetwork: true},
			wantOK: true,
		},
		{
			name:   "all",
			tools:  []string{"web_search", "web_fetch", "browser_navigate", "browser_snapshot", "browser_find", "browser_network", "browser_network_read"},
			want:   externalResearchToolSurface{WebSearch: true, WebFetch: true, Browser: true, BrowserFind: true, BrowserNetwork: true},
			wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := NewRegistry()
			for _, name := range c.tools {
				reg.Add(&Tool{Name: name})
			}

			got, ok := externalResearchSurfaceForRegistry(reg)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if got != c.want {
				t.Fatalf("surface = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestRegistrySystemPromptComposition(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: MemoryToolName})
	if got := BaseSystemPromptForRegistry(reg); got != MemoryOnlySystemPrompt {
		t.Fatal("memory-only registry should use memory-only base prompt")
	}
	prompt := WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	if !strings.Contains(prompt, "Memory retrieval:") {
		t.Fatalf("memory registry prompt missing memory guidance:\n%s", prompt)
	}
	for _, forbidden := range []string{"'shell' tool", "read_file", "Session history retrieval:", "External research:", "Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("memory-only registry prompt should not include %q:\n%s", forbidden, prompt)
		}
	}
	emptyPrompt := WithRegistrySystemGuidance("", reg)
	if !strings.Contains(emptyPrompt, "only tool is 'memory'") || !strings.Contains(emptyPrompt, "Memory retrieval:") {
		t.Fatalf("empty prompt should compose memory-only base + guidance:\n%s", emptyPrompt)
	}
	if strings.Contains(emptyPrompt, "'shell' tool") || strings.Contains(emptyPrompt, "read_file") {
		t.Fatalf("empty memory-only prompt should not fall back to default workspace prompt:\n%s", emptyPrompt)
	}

	reg.Add(&Tool{Name: PlanToolName})
	reg.Add(&Tool{Name: SubagentToolName})
	reg.Add(&Tool{Name: FocusedTaskToolName})
	reg.Add(&Tool{Name: SessionSearchToolName})
	reg.Add(&Tool{Name: "web_search"})
	prompt = WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	for _, want := range []string{"Memory retrieval:", "Session history retrieval:", "External research:", "Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("registry prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Count(WithRegistrySystemGuidance(prompt, reg), "Memory retrieval:") != 1 {
		t.Fatal("registry guidance should be idempotent")
	}
	if strings.Count(WithRegistrySystemGuidance(prompt, reg), "Session history retrieval:") != 1 {
		t.Fatal("session search guidance should be idempotent")
	}
	if strings.Count(WithRegistrySystemGuidance(prompt, reg), "External research:") != 1 {
		t.Fatal("external research guidance should be idempotent")
	}
	if strings.Contains(prompt, "Subagent browser delegation:") {
		t.Fatalf("registry prompt without browser tools should not include browser-specific subagent guidance:\n%s", prompt)
	}

	reg.Add(&Tool{Name: "browser_navigate"})
	prompt = WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	if !strings.Contains(prompt, "Subagent browser delegation:") {
		t.Fatalf("registry prompt with subagent and browser should include browser delegation guidance:\n%s", prompt)
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: FocusedTaskToolName, Schema: focusedTaskToolSchema([]FocusedTaskProfile{exploreProfile(), verifyProfile()})})
	prompt = WithRegistrySystemGuidance("", reg)
	if !strings.Contains(prompt, "Focused tasks (run_task):") {
		t.Fatalf("focused-task registry prompt missing guidance:\n%s", prompt)
	}
	if strings.Contains(prompt, "Trigger research") || strings.Contains(prompt, "research external facts") {
		t.Fatalf("focused-task registry prompt should not mention filtered research:\n%s", prompt)
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: MemoryToolName})
	reg.Add(&Tool{Name: "read_file"})
	if got := BaseSystemPromptForRegistry(reg); got != LimitedToolSystemPrompt {
		t.Fatal("memory plus partial file tools must not use the memory-only prompt")
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: SessionSearchToolName})
	emptyPrompt = WithRegistrySystemGuidance("", reg)
	if !strings.Contains(emptyPrompt, "limited-tool runtime") || !strings.Contains(emptyPrompt, "Session history retrieval:") {
		t.Fatalf("empty session-search prompt should compose limited base + guidance:\n%s", emptyPrompt)
	}
	if strings.Contains(emptyPrompt, "'shell' tool") || strings.Contains(emptyPrompt, "read_file") {
		t.Fatalf("empty session-search prompt should not fall back to default workspace prompt:\n%s", emptyPrompt)
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: "browser_navigate"})
	emptyPrompt = WithRegistrySystemGuidance("", reg)
	if !strings.Contains(emptyPrompt, "limited-tool runtime") || !strings.Contains(emptyPrompt, "External research:") {
		t.Fatalf("empty browser prompt should compose limited base + external research guidance:\n%s", emptyPrompt)
	}
	for _, forbidden := range []string{"web_search", "web_fetch"} {
		if strings.Contains(emptyPrompt, forbidden) {
			t.Fatalf("browser-only registry prompt should not mention unavailable %q:\n%s", forbidden, emptyPrompt)
		}
	}
}

func TestAnnotateLLMCallErrorAddsActionableContext(t *testing.T) {
	loop := &Loop{LLM: NewLLMClient("https://llm.example/v1", "", "reasoning-model")}

	timeoutErr := loop.annotateLLMCallError("llm_stream", context.DeadlineExceeded, 4*time.Minute)
	if !errors.Is(timeoutErr, context.DeadlineExceeded) || !isTransient(timeoutErr) {
		t.Fatalf("annotated timeout must preserve deadline/transient classification: %v", timeoutErr)
	}
	for _, want := range []string{"LLM llm_stream timed out", "reasoning-model", "https://llm.example/v1/chat/completions", "max-call-timeout/per-call-timeout=4m0s", "stream-idle-timeout", "first-token latency", "reasoning model", "Next:", "TTFT", "inter-chunk gaps"} {
		if !strings.Contains(timeoutErr.Error(), want) {
			t.Fatalf("timeout diagnostic missing %q:\n%s", want, timeoutErr.Error())
		}
	}

	idleErr := loop.annotateLLMCallError("llm_stream", &RetryableError{Err: streamIdleTimeoutError(30 * time.Second)}, 4*time.Minute)
	if !errors.Is(idleErr, errStreamIdleTimeout) || !isTransient(idleErr) {
		t.Fatalf("annotated idle timeout must preserve retryable/sentinel classification: %v", idleErr)
	}
	for _, want := range []string{"stream idle timeout", "stream-idle-timeout", "before finish_reason", "proxy buffering", "max-call-timeout/per-call-timeout=4m0s", "Next:", "worker health"} {
		if !strings.Contains(idleErr.Error(), want) {
			t.Fatalf("idle timeout diagnostic missing %q:\n%s", want, idleErr.Error())
		}
	}

	finishErr := loop.annotateLLMCallError("llm_stream", &RetryableError{Err: errStreamEndedWithoutFinish}, 4*time.Minute)
	if !errors.Is(finishErr, errStreamEndedWithoutFinish) || !isTransient(finishErr) {
		t.Fatalf("annotated finish error must preserve retryable/sentinel classification: %v", finishErr)
	}
	for _, want := range []string{"incomplete SSE stream", "finish_reason", "sglang/vLLM", "reverse-proxy reset", "OOM kill", "Next:", "upstream incomplete-stream error", "model server/proxy logs"} {
		if !strings.Contains(finishErr.Error(), want) {
			t.Fatalf("finish diagnostic missing %q:\n%s", want, finishErr.Error())
		}
	}
}

func TestLLMErrorFailureKind(t *testing.T) {
	loop := &Loop{LLM: NewLLMClient("https://llm.example/v1", "", "reasoning-model")}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "deadline", err: loop.annotateLLMCallError("llm_stream", context.DeadlineExceeded, time.Minute), want: "llm_timeout"},
		{name: "idle", err: loop.annotateLLMCallError("llm_stream", &RetryableError{Err: streamIdleTimeoutError(time.Second)}, time.Minute), want: "llm_timeout"},
		{name: "incomplete", err: loop.annotateLLMCallError("llm_stream", &RetryableError{Err: errStreamEndedWithoutFinish}, time.Minute), want: "llm_incomplete_stream"},
		{name: "context overflow", err: loop.annotateLLMCallError("llm_request", errors.New("maximum context length is 4096 tokens"), time.Minute), want: "context_overflow"},
		{name: "other", err: loop.annotateLLMCallError("llm_request", errors.New("bad gateway"), time.Minute), want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := llmErrorFailureKind(c.err); got != c.want {
				t.Fatalf("llmErrorFailureKind() = %q, want %q for %v", got, c.want, c.err)
			}
		})
	}
}

func TestRunTurnPublishesLLMErrorFailureKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	events := make(chan sse.Event, 16)
	loop := &Loop{
		LLM:                 NewLLMClient(srv.URL, "", "fake-model"),
		Conv:                newTestConv(t),
		Events:              events,
		MaxTurnSteps:        1,
		MaxTransientRetries: -1,
		PerCallTimeout:      time.Second,
	}
	if err := loop.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.SendUser(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != sse.TypeError {
				continue
			}
			var p sse.ErrorPayload
			if err := json.Unmarshal(ev.Data, &p); err != nil {
				t.Fatalf("decode error payload: %v", err)
			}
			if p.FailureKind != "llm_incomplete_stream" {
				t.Fatalf("FailureKind = %q, want llm_incomplete_stream; payload=%+v", p.FailureKind, p)
			}
			if p.Code != "llm_stream" || p.Recoverable {
				t.Fatalf("unexpected error payload: %+v", p)
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for error event")
		}
	}
}

func TestLoopTurnOptionsOverrideToolSurfaceAndPolicies(t *testing.T) {
	baseTools := NewRegistry()
	baseTools.Add(&Tool{Name: "shell"})
	baseTools.Add(&Tool{Name: PlanToolName})
	planOnlyTools := NewRegistry()
	planOnlyTools.Add(&Tool{Name: PlanToolName})

	loop := &Loop{
		Tools:                  baseTools,
		FirstToolPolicy:        &FirstToolPolicy{ToolName: "shell"},
		ToolCallPolicies:       []*ToolCallPolicy{{ToolName: "shell", Reject: func(ToolCallPolicyContext) (string, bool) { return "base shell policy", true }}},
		MaxToolCalls:           8,
		FinalNoToolsOnMaxTurns: false,
	}
	opts := TurnOptions{
		Tools:                  planOnlyTools,
		FirstToolPolicy:        PlanFirstToolPolicy(),
		MaxToolCalls:           2,
		FinalNoToolsOnMaxTurns: true,
		ToolCallPolicies:       []*ToolCallPolicy{{ToolName: PlanToolName, Reject: func(ToolCallPolicyContext) (string, bool) { return "turn plan policy", true }}},
	}

	defs := loop.toolDefs(opts)
	if len(defs) != 1 || defs[0].Function.Name != PlanToolName {
		t.Fatalf("turn tool defs = %+v, want only plan", defs)
	}
	if got := loop.activeFirstToolPolicy("draft a plan", opts); got == nil || got.ToolName != PlanToolName {
		t.Fatalf("turn first-tool policy = %+v, want plan", got)
	}
	if got := loop.maxToolCallsForTurn(opts); got != 2 {
		t.Fatalf("turn max tool calls = %d, want 2", got)
	}
	if !loop.finalNoToolsOnMaxTurnsForTurn(opts) {
		t.Fatal("turn should request final no-tool answer on max turns")
	}
	if got, ok := loop.toolCallPolicyRejection("draft a plan", PlanToolName, json.RawMessage(`{"action":"view"}`), 0, opts); !ok || !strings.Contains(got, "turn plan policy") {
		t.Fatalf("turn tool-call policy = %q ok=%t, want plan policy", got, ok)
	}

	baseDefs := loop.toolDefs(TurnOptions{})
	if len(baseDefs) != 2 {
		t.Fatalf("base tool defs changed = %+v, want original two tools", baseDefs)
	}
	if got := loop.activeFirstToolPolicy("draft a plan", TurnOptions{}); got == nil || got.ToolName != "shell" {
		t.Fatalf("base first-tool policy changed = %+v, want shell", got)
	}
	if got, ok := loop.toolCallPolicyRejection("run shell", "shell", json.RawMessage(`{}`), 0, TurnOptions{}); !ok || !strings.Contains(got, "base shell policy") {
		t.Fatalf("base tool-call policy changed = %q ok=%t, want shell policy", got, ok)
	}
}

func TestEnsureSystemPrompt_EmptyConv_NoMemory(t *testing.T) {
	conv := newTestConv(t)
	l := &Loop{Conv: conv}
	if err := l.EnsureSystemPrompt("custom prompt"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "custom prompt" {
		t.Fatalf("system message wrong: %+v", msgs[0])
	}
}

func TestConsumeAndPersist_ReasoningOnlyTerminalEmitsMessageDone(t *testing.T) {
	conv := newTestConv(t)
	events := make(chan sse.Event, 8)
	l := &Loop{Conv: conv, Events: events, Log: zerolog.Nop()}

	stream := make(chan StreamEvent, 1)
	stream <- StreamEvent{Finish: &FinishInfo{
		Reason: "stop",
		Final: ChatMessage{
			Role:             "assistant",
			ReasoningContent: "  final answer from reasoning channel  ",
		},
	}}
	close(stream)

	finish, sawText, err := l.consumeAndPersist(context.Background(), "turn-1", stream)
	if err != nil {
		t.Fatal(err)
	}
	if sawText {
		t.Fatal("reasoning-only output must not count as streamed visible text")
	}
	if finish.Final.Content != "final answer from reasoning channel" {
		t.Fatalf("reasoning fallback did not populate visible content: %+v", finish.Final)
	}

	var gotMessageDone string
	var gotThinkingDone string
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			switch ev.Type {
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatal(err)
				}
				gotMessageDone = p.Text
			case sse.TypeThinkingDone:
				var p sse.ThinkingDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatal(err)
				}
				gotThinkingDone = p.Text
			}
		default:
			t.Fatal("expected thinking.done and message.done events")
		}
	}
	if gotThinkingDone != "  final answer from reasoning channel  " {
		t.Fatalf("thinking.done changed reasoning payload: %q", gotThinkingDone)
	}
	if gotMessageDone != "final answer from reasoning channel" {
		t.Fatalf("message.done fallback = %q", gotMessageDone)
	}

	msgs := conv.Snapshot()
	if len(msgs) != 1 || msgs[0].Content != "final answer from reasoning channel" {
		t.Fatalf("conversation did not persist fallback visible content: %+v", msgs)
	}
}

func TestEnsureSystemPrompt_EmptyConv_WithMemory(t *testing.T) {
	conv := newTestConv(t)
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "User uses Go 1.22 + sqlc"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("base prompt"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", len(msgs))
	}
	c := msgs[0].Content
	if !strings.HasPrefix(c, "base prompt") {
		t.Fatalf("system message should start with base prompt: %q", c)
	}
	if !strings.Contains(c, "User uses Go 1.22") {
		t.Fatalf("system message should contain memory entry: %q", c)
	}
	if !strings.Contains(c, "MEMORY") {
		t.Fatalf("system message should contain memory header: %q", c)
	}
}

func TestEnsureSystemPrompt_ResumedConv_RewritesCurrentRuntimePrompt(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old prompt\n\nSubagent delegation:\nstale guidance\n\nAffent plan tool guidance:\nstale plan guidance"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv}
	if err := l.EnsureSystemPrompt("new prompt without disabled feature guidance"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("resumed conv must not gain a message, got %d", len(msgs))
	}
	if msgs[0].Content != "new prompt without disabled feature guidance" {
		t.Fatalf("resumed conv must rewrite system msg to current runtime prompt, got %q", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "Subagent delegation:") || strings.Contains(msgs[0].Content, "Affent plan tool guidance:") {
		t.Fatalf("disabled feature guidance leaked after prompt rewrite:\n%s", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Fatalf("user message must survive rewrite, got %+v", msgs[1])
	}
}

func TestEnsureSystemPrompt_ResumedConv_WithMemory_Rewritten(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old base + old memory block"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "assistant", Content: "hello"}); err != nil {
		t.Fatal(err)
	}

	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "Fresh fact for this session"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("fresh base"); err != nil {
		t.Fatal(err)
	}

	msgs := conv.Snapshot()
	if len(msgs) != 3 {
		t.Fatalf("message count must be preserved, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("first message must remain a system message, got role=%q", msgs[0].Role)
	}
	if !strings.HasPrefix(msgs[0].Content, "fresh base") {
		t.Fatalf("system msg must start with new base prompt, got %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Fresh fact for this session") {
		t.Fatalf("system msg must include current memory entry, got %q", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Fatalf("user message must survive rewrite, got %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" {
		t.Fatalf("assistant message must survive rewrite, got %+v", msgs[2])
	}
}

func TestEnsureSystemPrompt_ResumedConv_WithMemory_AlreadyEqual_NoOp(t *testing.T) {
	conv := newTestConv(t)
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "stable fact"); err != nil {
		t.Fatal(err)
	}
	// Compute what EnsureSystemPrompt would produce and pre-seed the
	// conversation with exactly that.
	want := "base" + "\n\n" + mem.Snapshot()
	if err := conv.Append(ChatMessage{Role: "system", Content: want}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "earlier"}); err != nil {
		t.Fatal(err)
	}

	// Capture file mtime to assert no Replace happened.
	path := conv.path
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatalf("expected no-op when system msg already equals composition; file was rewritten")
	}
	msgs := conv.Snapshot()
	if msgs[0].Content != want {
		t.Fatalf("system message changed unexpectedly")
	}
}

func TestEnsureSystemPrompt_ProjectContext_EmptyConv(t *testing.T) {
	conv := newTestConv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Project uses Go 1.22"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	c := msgs[0].Content
	if !strings.HasPrefix(c, "base") {
		t.Fatalf("system msg should start with base: %q", c)
	}
	if !strings.Contains(c, "PROJECT CONTEXT") || !strings.Contains(c, "Project uses Go 1.22") {
		t.Fatalf("project context missing:\n%s", c)
	}
}

func TestEnsureSystemPrompt_ProjectContextPlusMemory_Order(t *testing.T) {
	conv := newTestConv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("user-authored fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "agent-authored fact"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir, Memory: mem}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	c := conv.Snapshot()[0].Content

	basePos := strings.Index(c, "base")
	projPos := strings.Index(c, "user-authored fact")
	memPos := strings.Index(c, "agent-authored fact")
	if basePos < 0 || projPos < 0 || memPos < 0 {
		t.Fatalf("missing pieces in composed prompt:\n%s", c)
	}
	if !(basePos < projPos && projPos < memPos) {
		t.Fatalf("expected order base → project-context → memory; got positions %d %d %d",
			basePos, projPos, memPos)
	}
}

func TestEnsureSystemPrompt_ProjectContext_ResumeRewrites(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old prompt without project context"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("freshly added project rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages preserved, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "freshly added project rule") {
		t.Fatalf("project context not refreshed on resume:\n%s", msgs[0].Content)
	}
}

func TestEnsureSystemPrompt_ProjectContext_DirEmptyOrMissing_NoOp(t *testing.T) {
	conv := newTestConv(t)
	l := &Loop{Conv: conv, ProjectContextDir: t.TempDir()} // dir exists but no files
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if got := conv.Snapshot()[0].Content; got != "base" {
		t.Fatalf("with no project files, system msg should equal base, got %q", got)
	}
}

func TestEnsureSystemPrompt_SnapshotLiveAcrossSessions(t *testing.T) {
	// One store, two sessions: each session's system message reflects
	// store state at that session's start.
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "session-1 fact"); err != nil {
		t.Fatal(err)
	}

	conv1 := newTestConv(t)
	l1 := &Loop{Conv: conv1, Memory: mem}
	if err := l1.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conv1.Snapshot()[0].Content, "session-1 fact") {
		t.Fatalf("session 1 system msg missing the fact")
	}

	if _, err := mem.Add(memory.TargetMemory, "", "session-2 fact"); err != nil {
		t.Fatal(err)
	}
	conv2 := newTestConv(t)
	l2 := &Loop{Conv: conv2, Memory: mem}
	if err := l2.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	sys2 := conv2.Snapshot()[0].Content
	if !strings.Contains(sys2, "session-1 fact") || !strings.Contains(sys2, "session-2 fact") {
		t.Fatalf("session 2 system msg must reflect current store state, got %q", sys2)
	}

	// And session 1's prompt must NOT have been retroactively changed.
	if strings.Contains(conv1.Snapshot()[0].Content, "session-2 fact") {
		t.Fatalf("session 1 prompt must not see session-2 fact retroactively")
	}
}

// TestTruncateForContext_UTF8Safe verifies the helper that clamps
// oversized tool results to the in-context budget doesn't split a
// multi-byte UTF-8 rune. Before the fix it byte-sliced the input at
// the raw `max` offset; if that offset landed inside a Cyrillic /
// Greek / emoji rune the model received invalid UTF-8.
func TestTruncateForContext_UTF8Safe(t *testing.T) {
	// Each Cyrillic rune is 2 UTF-8 bytes. Sweeping all sub-rune
	// offsets exercises both the "lands mid-rune" and "lands on
	// boundary" paths.
	in := "приветприветпривет"
	for n := 1; n < len(in); n++ {
		out := truncateForContext(in, n)
		// truncateForContext appends a banner starting with "\n\n[...";
		// the prefix is everything before that.
		prefix := strings.SplitN(out, "\n\n[", 2)[0]
		if !utf8.ValidString(prefix) {
			t.Fatalf("truncateForContext(_, %d) produced invalid UTF-8 prefix: %q", n, prefix)
		}
	}
}

// TestPublish_NilEventsIsSilent pins the no-allocation, no-log path
// when an caller opts out of the event stream by leaving
// Loop.Events nil. Pre-fix the publish call hit `case nil <- ev:
// default:` which never proceeds, so every event triggered a
// misleading "event channel full" warning.
func TestPublish_NilEventsIsSilent(t *testing.T) {
	var buf strings.Builder
	loop := &Loop{
		Log:    zerolog.New(&buf),
		Events: nil,
	}
	// Spam a batch of varied events; none of them should log or panic.
	for i := 0; i < 50; i++ {
		loop.publish("message.delta", map[string]any{"delta": "x"})
		loop.publish("turn.end", map[string]any{"reason": "completed"})
	}
	if strings.Contains(buf.String(), "channel full") {
		t.Fatalf("nil Events must not produce \"channel full\" logs: %s", buf.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("nil Events must produce no log output, got %q", buf.String())
	}
}

func TestPublishRuntimeSurfaceCapturesEffectiveTools(t *testing.T) {
	events := make(chan sse.Event, 1)
	reg := NewRegistry()
	reg.Add(&Tool{Name: "read_file", CatalogGroup: "Workspace"})
	reg.Add(&Tool{Name: "web_fetch", CatalogGroup: "Web"})
	reg.Add(&Tool{Name: "web_search", CatalogGroup: "Web"})
	reg.Add(&Tool{Name: MemoryToolName, CatalogGroup: "Memory"})
	loop := &Loop{
		Tools:                        reg,
		Events:                       events,
		MaxTurnSteps:                 7,
		MaxToolCalls:                 5,
		ToolResultMaxBytesInContext:  1234,
		ToolResultContextBudgetBytes: 5678,
		ToolResultArtifactPathPrefix: ".affent/custom",
	}
	loop.publishRuntimeSurface("turn_surface", TurnOptions{})
	ev := <-events
	if ev.Type != sse.TypeRuntimeSurface {
		t.Fatalf("event type = %q, want %q", ev.Type, sse.TypeRuntimeSurface)
	}
	var payload sse.RuntimeSurfacePayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("decode runtime surface: %v", err)
	}
	if payload.TurnID != "turn_surface" || payload.ToolCount != 4 || len(payload.Tools) != 4 {
		t.Fatalf("payload identity = %+v", payload)
	}
	if !payload.Capabilities.WebFetch || !payload.Capabilities.WebSearch || !payload.Capabilities.Memory {
		t.Fatalf("capabilities missing expected tools: %+v", payload.Capabilities)
	}
	if payload.Capabilities.Builtins {
		t.Fatalf("partial workspace surface should not claim full builtins: %+v", payload.Capabilities)
	}
	if len(payload.Capabilities.WorkspaceTools) != 1 || payload.Capabilities.WorkspaceTools[0] != "read_file" {
		t.Fatalf("workspace tools = %#v, want read_file", payload.Capabilities.WorkspaceTools)
	}
	if payload.Capabilities.Browser || payload.Capabilities.Plan {
		t.Fatalf("capabilities should not invent unavailable surfaces: %+v", payload.Capabilities)
	}
	if payload.MaxTurnSteps != 7 || payload.MaxToolCalls != 5 ||
		payload.ToolResultEventCapBytes != MaxToolResultBytesInEvent ||
		payload.ToolResultContextMaxBytes != 1234 ||
		payload.ToolResultContextBudgetBytes != 5678 ||
		payload.ToolResultArtifactPrefix != ".affent/custom" {
		t.Fatalf("limits = %+v", payload)
	}
}

// TestPreviewN_UTF8Safe covers the event-bus preview path the same way.
func TestPreviewN_UTF8Safe(t *testing.T) {
	in := "héllo wörld" // 'é' and 'ö' are each 2 bytes
	for n := 1; n < len(in); n++ {
		out := textutil.Preview(in, n)
		cut := strings.TrimSuffix(out, "...")
		if !utf8.ValidString(cut) {
			t.Fatalf("textutil.Preview(%q, %d) produced invalid UTF-8 prefix: %q", in, n, cut)
		}
	}
}

func TestLoopToolResultContextCapsByTool(t *testing.T) {
	loop := &Loop{}
	cases := map[string]int{
		"read_file":            12 * 1024,
		"shell":                6 * 1024,
		"web_fetch":            5 * 1024,
		"browser_navigate":     3 * 1024,
		"browser_snapshot":     3 * 1024,
		"browser_find":         2 * 1024,
		"browser_network":      2 * 1024,
		"browser_network_read": 4 * 1024,
		"browser_scroll":       2 * 1024,
		"browser_wait":         2 * 1024,
		"browser_click":        2 * 1024,
		"browser_type":         2 * 1024,
		MemoryToolName:         4 * 1024,
		SessionSearchToolName:  4 * 1024,
		"web_search":           3 * 1024,
		"list_files":           4 * 1024,
		"edit_file":            2 * 1024,
		"browser_screenshot":   2 * 1024,
		"unknown":              MaxToolResultBytesInContext,
	}
	for tool, want := range cases {
		if got := loop.toolResultMaxBytesInContextFor(tool); got != want {
			t.Fatalf("%s cap = %d, want %d", tool, got, want)
		}
	}
	loop.ToolResultMaxBytesInContext = 123
	if got := loop.toolResultMaxBytesInContextFor("read_file"); got != 123 {
		t.Fatalf("explicit cap should win, got %d", got)
	}
}

func TestLoopToolResultContextBudgetDefaultAndOverride(t *testing.T) {
	loop := &Loop{}
	if got := loop.toolResultContextBudgetBytes(); got != 32*1024 {
		t.Fatalf("default tool context budget = %d, want %d", got, 32*1024)
	}
	loop.ToolResultContextBudgetBytes = 321
	if got := loop.toolResultContextBudgetBytes(); got != 321 {
		t.Fatalf("explicit tool context budget = %d, want 321", got)
	}
}

func TestToolResultContextBudgetExhaustionPreservesEvidenceHead(t *testing.T) {
	budget := newToolResultContextBudget(1)
	first, omitted := budget.truncateToolResult("browser_navigate", "x", 1024, "")
	if first != "x" || omitted != 0 {
		t.Fatalf("first result = %q omitted=%d, want exact fit", first, omitted)
	}
	payload := "SourceAccess: browser_rendered_url=https://example.com/report; snapshot_id=4; page_text_below=verified_page_evidence; links_in_snapshot=discovered_unverified_until_opened\nURL: https://example.com/report\nTITLE: Report\nSNAPSHOT_ID: 4\n\nPAGE TEXT:\np: useful evidence\n" + strings.Repeat("z", 2048)
	second, omitted := budget.truncateToolResult("browser_navigate", payload, 4096, "")
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://example.com/report",
		"URL: https://example.com/report",
		"TITLE: Report",
		"per-turn tool-result context budget",
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("exhausted budget result missing %q:\n%s", want, second)
		}
	}
	if omitted <= 0 || omitted >= len(payload) {
		t.Fatalf("omitted bytes = %d, want partial head preserved from %d-byte payload", omitted, len(payload))
	}
}

func TestToolResultContextBudgetCompactsRepeatedBrowserPageReads(t *testing.T) {
	budget := newToolResultContextBudget(16 * 1024)
	payload := "SourceAccess: browser_rendered_url=https://example.com/report; snapshot_id=4; page_text_below=verified_page_evidence; links_in_snapshot=discovered_unverified_until_opened\nURL: https://example.com/report\nTITLE: Report\nSNAPSHOT_ID: 4\n\nPAGE TEXT:\np: useful evidence\n" + strings.Repeat("first ", 1000)
	first, omitted := budget.truncateToolResult("browser_navigate", payload, 4*1024, "")
	if omitted <= 0 || !strings.Contains(first, "first first") {
		t.Fatalf("first browser read should use normal per-tool truncation, omitted=%d:\n%s", omitted, first)
	}

	repeatedPayload := "SourceAccess: browser_rendered_url=https://example.com/report; snapshot_id=5; page_text_below=verified_page_evidence; links_in_snapshot=discovered_unverified_until_opened\nURL: https://example.com/report\nTITLE: Report\nSNAPSHOT_ID: 5\n\nPAGE TEXT:\np: useful evidence again\n" + strings.Repeat("repeat ", 1000)
	second, omitted := budget.truncateToolResult("browser_snapshot", repeatedPayload, 4*1024, "")
	for _, want := range []string{
		"SourceAccess: browser_rendered_url=https://example.com/report",
		"URL: https://example.com/report",
		"browser page already read this turn",
		"browser_find for targeted text",
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("repeated browser read missing %q:\n%s", want, second)
		}
	}
	if omitted <= 0 || strings.Contains(second, strings.Repeat("repeat ", 200)) {
		t.Fatalf("repeated browser read should be compacted, omitted=%d:\n%s", omitted, second)
	}
}

func TestTruncateToolResultForContextGuidanceByTool(t *testing.T) {
	payload := strings.Repeat("x", 256)

	browser := truncateToolResultForContext("browser_snapshot", payload, 32, "")
	for _, want := range []string{"browser_snapshot", "browser_find", "broad page snapshots"} {
		if !strings.Contains(browser, want) {
			t.Fatalf("browser truncation guidance missing %q:\n%s", want, browser)
		}
	}
	if strings.Contains(browser, "grep") || strings.Contains(browser, "head/tail") {
		t.Fatalf("browser truncation guidance should not suggest shell piping:\n%s", browser)
	}

	web := truncateToolResultForContext("web_fetch", payload, 32, "")
	for _, want := range []string{"web_fetch", "specific API/text/source URL", "verified evidence"} {
		if !strings.Contains(web, want) {
			t.Fatalf("web_fetch truncation guidance missing %q:\n%s", want, web)
		}
	}

	shell := truncateToolResultForContext("shell", payload, 32, "")
	if !strings.Contains(shell, "head/tail/grep/sed") {
		t.Fatalf("shell truncation should keep command-oriented guidance:\n%s", shell)
	}
}
