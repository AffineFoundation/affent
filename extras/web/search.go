package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/websource"
	"golang.org/x/net/html"
)

// SearchResult is one hit returned by SearchProvider.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SearchProvider abstracts the search backend so callers can plug in
// Tavily, Brave, SearXNG, an internal index, or a stub for tests
// without changing the tool wiring.
type SearchProvider interface {
	Search(ctx context.Context, query string, n int) ([]SearchResult, error)
}

// SearchConfig tunes WebSearchTool.
type SearchConfig struct {
	// Provider is required.
	Provider SearchProvider
	// MaxResults caps the per-query result count. Default 8. Values
	// above 20 are clamped to the tool's hard cap so schema and
	// runtime behavior stay aligned.
	MaxResults int
}

const (
	defaultSearchResults  = 8
	maxSearchResults      = 20
	maxSearchQueryBytes   = 2048
	maxSearchTitleBytes   = 300
	maxSearchSnippetBytes = 1000
	maxSearchSourceHints  = 3
)

var searchSnippetURLPattern = regexp.MustCompile(`https?://[^\s<>"'()\[\]{}]+`)

// SearchTool returns an agent.Tool that runs a web search and returns
// a compact list of {title, url, snippet}. The model decides which URL
// to follow up on with web_fetch.
func SearchTool(cfg SearchConfig) (*agent.Tool, error) {
	if cfg.Provider == nil {
		return nil, errors.New("SearchConfig.Provider is required")
	}
	max := cfg.MaxResults
	if max <= 0 {
		max = defaultSearchResults
	}
	if max > maxSearchResults {
		max = maxSearchResults
	}
	defaultN := min(defaultSearchResults, max)

	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["query"],
        "properties": {
            "query": {"type": "string", "minLength": 1, "maxLength": %d, "description": "Search query. Include user-provided disambiguators such as ecosystem, parent project, ticker, network/subnet id, official domain, version, geography, or date range when they matter."},
            "num_results": {"type": "integer", "description": "How many results to return. Default %d, max %d.", "minimum": 1, "maximum": %d, "default": %d}
        }
    }`, maxSearchQueryBytes, defaultN, max, max, defaultN))

	return &agent.Tool{
		Name: "web_search",
		Description: "Run a web search and return ranked results as " +
			"{title, url, snippet}. Use to discover candidate sources; " +
			"read authoritative result URLs with an available page-reading " +
			"tool before relying on them when full-page reading is available.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Query      string `json:"query"`
				NumResults int    `json:"num_results"`
			}
			if err := decodeWebToolArgs(raw, &args, "retry web_search with only documented fields: query and num_results"); err != nil {
				return "", err
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", errors.New("query is required\nFailure: kind=invalid_args\nNext: retry with 2-6 specific keywords, named entities, and any user-provided disambiguators such as ecosystem, ticker, network/subnet id, official domain, or date range")
			}
			if len(query) > maxSearchQueryBytes {
				return "", fmt.Errorf("query is %d bytes; web_search supports queries up to %d bytes\nFailure: kind=invalid_args\nNext: retry with 2-6 specific keywords, named entities, and the shortest useful disambiguators such as ecosystem, ticker, network/subnet id, official domain, or date range", len(query), maxSearchQueryBytes)
			}
			n := args.NumResults
			if n <= 0 {
				n = defaultN
			}
			if n > max {
				n = max
			}

			results, err := cfg.Provider.Search(ctx, query, n)
			if err != nil {
				return "", recoverableSearchError(err)
			}
			return formatResults(results, n), nil
		},
	}, nil
}

func formatResults(results []SearchResult, limit int) string {
	if len(results) == 0 {
		return "(no results)\nFailure: kind=no_results\nNext: retry web_search with fewer or different keywords, preserve user-provided disambiguators such as ecosystem, ticker, network/subnet id, official domain, or date range, or use another available source URL if already known."
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	var b strings.Builder
	displayed := 0
	hasDirectReaderWarning := false
	for _, r := range results {
		if displayed >= limit {
			break
		}
		url := strings.TrimSpace(r.URL)
		if url == "" {
			continue
		}
		if len(url) > maxFetchURLBytes {
			continue
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = url
		}
		title = truncateSearchField(title, maxSearchTitleBytes)
		snippet := strings.TrimSpace(r.Snippet)
		if snippet == "" {
			snippet = "(snippet unavailable)"
		}
		snippet = truncateSearchField(snippet, maxSearchSnippetBytes)
		displayed++
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s",
			displayed, title, url, snippet)
		hints := sourceHintsFromSearchResult(r, url)
		if note := directFetchCautionForResult(r); note != "" {
			label := "Direct-reader caution"
			if directFetchShouldSkip(url) || (dynamicResultReason(r) != "" && len(hints) > 0) {
				label = "Direct-reader warning"
				hasDirectReaderWarning = true
			}
			fmt.Fprintf(&b, "\n   %s: %s", label, note)
		}
		for _, hint := range hints {
			fmt.Fprintf(&b, "\n   Source hint: snippet mentions readable endpoint %s", hint)
		}
		b.WriteString("\n\n")
	}
	if displayed == 0 {
		return "(no usable results: search provider returned no URLs)\nFailure: kind=no_results\nNext: retry web_search with more distinctive keywords and user-provided disambiguators such as ecosystem, ticker, network/subnet id, official domain, or date range, or use another available source URL if already known."
	}
	b.WriteString("Next: choose the 1-3 most authoritative/current result URLs, prefer official or primary sources, and read them with an available page-reading tool before answering. If no full-page reading tool is available, compare snippets and say that full-page verification was unavailable.")
	if hasDirectReaderWarning {
		b.WriteString(" Do not spend direct page-reading calls on URLs marked with Direct-reader warning; use their snippets only as weak discovery, sentiment, or claim evidence unless a readable canonical source is available.")
	}
	return strings.TrimSpace(b.String())
}

func sourceHintsFromSearchResult(r SearchResult, resultURL string) []string {
	seen := map[string]bool{}
	if normalized := canonicalSearchHintURL(resultURL); normalized != "" {
		seen[normalized] = true
	}
	text := r.Title + " " + r.Snippet
	matches := searchSnippetURLPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	hints := make([]string, 0, min(len(matches), maxSearchSourceHints))
	for _, raw := range matches {
		raw = strings.TrimRight(raw, ".,;:!?")
		normalized := canonicalSearchHintURL(raw)
		if normalized == "" || seen[normalized] {
			continue
		}
		if !isReadableSourceHintURL(raw) {
			continue
		}
		seen[normalized] = true
		hints = append(hints, normalized)
		if len(hints) >= maxSearchSourceHints {
			break
		}
	}
	return hints
}

func canonicalSearchHintURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	port := u.Port()
	u.Scheme = scheme
	u.Host = websource.NormalizeHost(u.Hostname())
	if port != "" {
		u.Host += ":" + port
	}
	u.Fragment = ""
	return u.String()
}

func isReadableSourceHintURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	path := strings.ToLower(u.EscapedPath())
	return websource.IsLikelyTextOrAPIPath(path)
}

func directFetchShouldSkip(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	host := websource.NormalizeHost(u.Hostname())
	path := strings.ToLower(u.EscapedPath())
	return websource.IsSearchResultPage(host, path) || websource.IsKnownDirectReaderTrapHost(host)
}

func directFetchCaution(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	host := websource.NormalizeHost(u.Hostname())
	path := strings.ToLower(u.EscapedPath())
	if websource.IsSearchResultPage(host, path) {
		return "open the target/source URL, not the search-results page; search snippets are discovery evidence only."
	}
	if websource.IsRedirectorHost(host) {
		return "this is often a redirect or short-link wrapper; prefer the final canonical URL from an authoritative source before reading it."
	}
	if websource.IsKnownDirectReaderTrapHost(host) {
		return "do not open this URL as source evidence unless a readable page is already available; this host usually blocks page readers or shows login/challenge pages. Use the snippet only as weak sentiment/claim evidence, find a mirrored/source URL, or mark this source as blocked/unverified."
	}
	if websource.IsSocialOrDiscussionHost(host) {
		return "social/discussion pages often block page readers, require JavaScript, or require login; use them as sentiment/claim evidence only unless a readable page source is returned."
	}
	if websource.IsLikelyCollectionPage(path) {
		return "this looks like a broad collection/listing page; prefer a specific detail page, official API/text/export endpoint, docs page, or source repository before spending a direct page-reading call."
	}
	return ""
}

func directFetchCautionForResult(r SearchResult) string {
	if note := directFetchCaution(r.URL); note != "" {
		return note
	}
	if dynamicResultReason(r) != "" {
		return "result appears to be a dynamic or JavaScript-rendered page; prefer an official API/text/source URL before spending a direct page-reading call."
	}
	return ""
}

func dynamicResultReason(r SearchResult) string {
	text := strings.ToLower(r.Title + " " + r.Snippet + " " + r.URL)
	switch {
	case strings.Contains(text, "requires javascript"),
		strings.Contains(text, "enable javascript"),
		strings.Contains(text, "client-rendered"),
		strings.Contains(text, "dynamic dashboard"),
		strings.Contains(text, "live dashboard"),
		strings.Contains(text, "app shell"):
		return "explicit dynamic page wording"
	default:
		return ""
	}
}

func truncateSearchField(s string, maxBytes int) string {
	return textutil.Preview(s, maxBytes, "...(truncated)")
}

func recoverableSearchError(err error) error {
	if err == nil || strings.Contains(err.Error(), "\nNext:") {
		return err
	}
	lower := strings.ToLower(err.Error())
	next := "retry once with fewer/different keywords or a more distinctive entity; if search remains unavailable, use known official URLs with available reading tools or say what could not be verified."
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		next = "search provider timed out; retry once with a shorter query, then use known official URLs or answer with clearly marked gaps."
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit"):
		next = "search provider is rate-limited; do not retry repeatedly. Use already returned sources, known official URLs, or answer with clearly marked gaps."
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden"):
		next = "search provider credentials/access failed; do not retry the same search. Use known official URLs or say search is unavailable."
	case strings.Contains(lower, "tavily") && (strings.Contains(lower, "decode") || strings.Contains(lower, "http")):
		next = "search backend failed; retry once with a simpler query or switch to known official URLs/search snippets already available."
	}
	return fmt.Errorf("%w\nFailure: kind=%s\nNext: %s", err, searchFailureKind(err), next)
}

func searchFailureKind(err error) string {
	lower := ""
	if err != nil {
		lower = strings.ToLower(err.Error())
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "timeout"
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit"):
		return "rate_limited"
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden"):
		return "blocked"
	case containsAny(lower, "http 500", "http 502", "http 503", "http 504", "status 500", "status 502", "status 503", "status 504", "status=500", "status=502", "status=503", "status=504"):
		return "server_error"
	case strings.Contains(lower, "http"):
		return "http_error"
	default:
		return "search_error"
	}
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// ---- Tavily provider --------------------------------------------------

// TavilyProvider hits api.tavily.com/search. Tavily is the default
// because it ships an "agent-friendly" snippet field (already
// summarized for retrieval), free tier, no scraping fragility.
//
// Env var TAVILY_API_KEY is read by NewTavilyProvider when APIKey
// isn't set explicitly.
type TavilyProvider struct {
	APIKey string
	HTTP   *http.Client
}

// NewDefaultSearchProvider wires the configured search backend from
// environment. AFFENT_WEB_SEARCH_PROVIDER accepts "tavily", "google", or
// "auto" (default). Auto keeps the historical Tavily behavior when
// TAVILY_API_KEY is present, otherwise uses Google when its credentials are
// configured. Google accepts GOOGLE_CSE_API_KEY/GOOGLE_CSE_ID, plus common
// aliases such as GOOGLE_API_KEY and GOOGLE_SEARCH_ENGINE_ID.
func NewDefaultSearchProvider() (SearchProvider, error) {
	return NewDefaultSearchProviderWithFallback(nil)
}

// NewDefaultSearchProviderWithFallback wires the configured search backend
// from environment, falling back to direct public search pages when no
// API-backed provider is configured. A rendered fallback can be supplied to
// recover from challenge pages or JavaScript-heavy search shells.
func NewDefaultSearchProviderWithFallback(renderedFallback RenderedFallbackFunc) (SearchProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("AFFENT_WEB_SEARCH_PROVIDER")))
	if provider == "" {
		provider = "auto"
	}
	switch provider {
	case "auto":
		if strings.TrimSpace(os.Getenv("TAVILY_API_KEY")) != "" {
			return NewTavilyProvider()
		}
		if googleSearchCredentialsConfigured() {
			return NewGoogleProvider()
		}
		return NewHTMLSearchProvider(renderedFallback)
	case "tavily":
		return NewTavilyProvider()
	case "google":
		return NewGoogleProvider()
	default:
		return nil, fmt.Errorf("unsupported AFFENT_WEB_SEARCH_PROVIDER=%q; valid values are auto, tavily, google", provider)
	}
}

// NewTavilyProvider wires a Tavily-backed provider. Returns an error
// if no API key is reachable (explicit or via TAVILY_API_KEY env).
func NewTavilyProvider() (*TavilyProvider, error) {
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		return nil, errors.New("TAVILY_API_KEY env var is required for the default Tavily search provider; supply your own SearchProvider to use a different backend")
	}
	return &TavilyProvider{
		APIKey: key,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Search implements SearchProvider against the Tavily REST API.
func (p *TavilyProvider) Search(ctx context.Context, query string, n int) ([]SearchResult, error) {
	body := map[string]any{
		"api_key":     p.APIKey,
		"query":       query,
		"max_results": n,
		// "basic" is the cheaper depth; enough for "find me a URL".
		"search_depth": "basic",
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.tavily.com/search", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	hc := p.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tavily http %d: %s", resp.StatusCode, preview)
	}

	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	// Cap the body read. Real Tavily responses are tens of KiB at
	// most; an unbounded decode lets a misbehaving (or proxy-
	// intercepted) endpoint OOM the agent process by streaming
	// indefinitely. 1 MiB is well past any realistic response and
	// small enough that pathological streams can't dominate memory.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("tavily decode: %w", err)
	}

	hits := make([]SearchResult, 0, len(out.Results))
	for _, r := range out.Results {
		hits = append(hits, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return hits, nil
}

// ---- Google Programmable Search provider ------------------------------

const googleSearchEndpoint = "https://www.googleapis.com/customsearch/v1"

// GoogleProvider hits Google's Programmable Search JSON API. It is the
// supported Google path for agents; scraping google.com/search from a browser
// often triggers anti-abuse challenge pages on datacenter IPs.
type GoogleProvider struct {
	APIKey   string
	EngineID string
	HTTP     *http.Client
	Endpoint string
}

func NewGoogleProvider() (*GoogleProvider, error) {
	key := googleSearchAPIKeyFromEnv()
	cx := googleSearchEngineIDFromEnv()
	if key == "" || cx == "" {
		return nil, errors.New("Google search requires GOOGLE_CSE_API_KEY or GOOGLE_API_KEY, plus GOOGLE_CSE_ID or GOOGLE_SEARCH_ENGINE_ID")
	}
	return &GoogleProvider{
		APIKey:   key,
		EngineID: cx,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		Endpoint: googleSearchEndpoint,
	}, nil
}

func googleSearchCredentialsConfigured() bool {
	return googleSearchAPIKeyFromEnv() != "" && googleSearchEngineIDFromEnv() != ""
}

func googleSearchAPIKeyFromEnv() string {
	return firstEnv("GOOGLE_CSE_API_KEY", "GOOGLE_CUSTOM_SEARCH_API_KEY", "GOOGLE_API_KEY")
}

func googleSearchEngineIDFromEnv() string {
	return firstEnv("GOOGLE_CSE_ID", "GOOGLE_CUSTOM_SEARCH_ENGINE_ID", "GOOGLE_SEARCH_ENGINE_ID", "GOOGLE_CSE_CX", "GOOGLE_CUSTOM_SEARCH_CX")
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

func (p *GoogleProvider) Search(ctx context.Context, query string, n int) ([]SearchResult, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = googleSearchEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("key", p.APIKey)
	q.Set("cx", p.EngineID)
	q.Set("q", query)
	q.Set("num", fmt.Sprint(n))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	hc := p.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("google search http %d: %s", resp.StatusCode, preview)
	}

	var out struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("google search decode: %w", err)
	}
	hits := make([]SearchResult, 0, len(out.Items))
	for _, item := range out.Items {
		hits = append(hits, SearchResult{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
		})
	}
	return hits, nil
}

// ---- HTML search provider ----------------------------------------------

type htmlSearchProvider struct {
	HTTP             *http.Client
	RenderedFallback RenderedFallbackFunc
	engines          []searchEngineConfig
}

type searchEngineConfig struct {
	Name      string
	URL       func(string) string
	ParseHTML func([]byte, string, int) []SearchResult
}

// NewHTMLSearchProvider wires a browser-friendly search backend that uses
// ordinary public search-result pages. It requires no API keys. If a rendered
// fallback is configured, blocked or JS-heavy pages are retried through the
// caller's browser and parsed from the rendered snapshot text.
func NewHTMLSearchProvider(renderedFallback RenderedFallbackFunc) (SearchProvider, error) {
	return &htmlSearchProvider{
		HTTP:             &http.Client{Timeout: 30 * time.Second},
		RenderedFallback: renderedFallback,
		engines:          defaultHTMLSearchEngines(),
	}, nil
}

func defaultHTMLSearchEngines() []searchEngineConfig {
	return []searchEngineConfig{
		{
			Name: "bing",
			URL: func(query string) string {
				return "https://www.bing.com/search?q=" + url.QueryEscape(query)
			},
			ParseHTML: parseBingSearchResults,
		},
		{
			Name: "duckduckgo",
			URL: func(query string) string {
				return "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
			},
			ParseHTML: parseDuckDuckGoSearchResults,
		},
	}
}

func (p *htmlSearchProvider) Search(ctx context.Context, query string, n int) ([]SearchResult, error) {
	if p == nil {
		return nil, errors.New("html search provider is nil")
	}
	if n <= 0 {
		n = defaultSearchResults
	}
	if n > maxSearchResults {
		n = maxSearchResults
	}
	hits := make([]SearchResult, 0, n)
	seen := map[string]bool{}
	var lastErr error
	for _, engine := range p.engines {
		if len(hits) >= n {
			break
		}
		results, err := p.searchEngine(ctx, engine, query, n-len(hits))
		if err != nil {
			lastErr = err
			continue
		}
		for _, r := range results {
			url := strings.TrimSpace(r.URL)
			if url == "" || seen[url] {
				continue
			}
			seen[url] = true
			hits = append(hits, r)
			if len(hits) >= n {
				break
			}
		}
	}
	if len(hits) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("html search returned no usable results")
	}
	return hits, nil
}

func (p *htmlSearchProvider) searchEngine(ctx context.Context, engine searchEngineConfig, query string, limit int) ([]SearchResult, error) {
	searchURL := engine.URL(query)
	body, finalURL, err := p.fetch(ctx, searchURL)
	if err == nil {
		if results := engine.ParseHTML(body, finalURL, limit); len(results) > 0 {
			return results, nil
		}
		if rendered := p.renderedSearchResults(ctx, searchURL, engine.Name); len(rendered) > 0 {
			return rendered, nil
		}
		return nil, fmt.Errorf("%s search returned no usable results", engine.Name)
	}
	if rendered := p.renderedSearchResults(ctx, searchURL, engine.Name); len(rendered) > 0 {
		return rendered, nil
	}
	return nil, err
}

func (p *htmlSearchProvider) fetch(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")

	hc := p.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, finalURL, readErr
	}
	if resp.StatusCode/100 != 2 {
		return nil, finalURL, fmt.Errorf("search engine http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, finalURL, nil
}

func (p *htmlSearchProvider) renderedSearchResults(ctx context.Context, requestURL, engineName string) []SearchResult {
	if p.RenderedFallback == nil {
		return nil
	}
	rendered, err := p.RenderedFallback(ctx, requestURL, FetchFallbackReason{
		Kind:     "search_results_page",
		Detail:   engineName + " search results page",
		FinalURL: requestURL,
	})
	if err != nil {
		return nil
	}
	return parseSearchResultsFromRenderedSnapshot(rendered, requestURL)
}

func parseBingSearchResults(body []byte, baseURL string, limit int) []SearchResult {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	containers := findNodes(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "li" && hasClass(n, "b_algo")
	}, limit*2)
	return extractSearchResultsFromContainers(containers, baseURL, limit, func(container *html.Node) string {
		return firstTextByClass(container, []string{"b_caption", "b_snippet"}, []string{"p", "div"})
	})
}

func parseDuckDuckGoSearchResults(body []byte, baseURL string, limit int) []SearchResult {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	containers := findNodes(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		if n.Data == "article" && attrHasValue(n, "data-testid", "result") {
			return true
		}
		return hasClass(n, "result") || hasClass(n, "results_links")
	}, limit*2)
	return extractSearchResultsFromContainers(containers, baseURL, limit, func(container *html.Node) string {
		if s := firstTextByClass(container, []string{"result__snippet", "result__a", "snippet"}, []string{"p", "div", "span"}); s != "" {
			return s
		}
		return firstTextByClass(container, nil, []string{"p", "div", "span"})
	})
}

func extractSearchResultsFromContainers(containers []*html.Node, baseURL string, limit int, snippetFn func(*html.Node) string) []SearchResult {
	results := make([]SearchResult, 0, min(limit, len(containers)))
	for _, container := range containers {
		if len(results) >= limit {
			break
		}
		titleNode := firstAnchorDescendant(container)
		if titleNode == nil {
			continue
		}
		title := strings.TrimSpace(textContent(titleNode))
		href := strings.TrimSpace(attrValue(titleNode, "href"))
		if href == "" {
			continue
		}
		u, err := url.Parse(href)
		if err != nil {
			continue
		}
		if u.Scheme == "" {
			base, err := url.Parse(baseURL)
			if err != nil {
				continue
			}
			u = base.ResolveReference(u)
		}
		u.Fragment = ""
		snippet := strings.TrimSpace(snippetFn(container))
		if title == "" {
			title = u.String()
		}
		if snippet == "" {
			snippet = "(snippet unavailable)"
		}
		results = append(results, SearchResult{
			Title:   title,
			URL:     u.String(),
			Snippet: snippet,
		})
	}
	return results
}

func parseSearchResultsFromRenderedSnapshot(rendered, baseURL string) []SearchResult {
	lines := strings.Split(rendered, "\n")
	results := make([]SearchResult, 0, defaultSearchResults)
	seen := map[string]bool{}
	inInteractive := false
	var pageText []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "INTERACTIVE ELEMENTS:":
			inInteractive = true
		case line == "PAGE TEXT:":
			inInteractive = false
		case inInteractive:
			title, href, ok := parseRenderedSearchLinkLine(line)
			if !ok {
				continue
			}
			u, err := url.Parse(href)
			if err != nil {
				continue
			}
			if u.Scheme == "" {
				base, err := url.Parse(baseURL)
				if err != nil {
					continue
				}
				u = base.ResolveReference(u)
			}
			u.Fragment = ""
			finalURL := u.String()
			if seen[finalURL] {
				continue
			}
			seen[finalURL] = true
			if title == "" {
				title = finalURL
			}
			results = append(results, SearchResult{
				Title:   title,
				URL:     finalURL,
				Snippet: "(browser-rendered search result)",
			})
			if len(results) >= defaultSearchResults {
				break
			}
		case strings.HasPrefix(line, "PAGE TEXT:"):
			continue
		default:
			if inInteractive {
				continue
			}
			if line != "" {
				pageText = append(pageText, line)
			}
		}
	}
	if len(results) == 0 && len(pageText) > 0 {
		_ = pageText
	}
	return results
}

func parseRenderedSearchLinkLine(line string) (title, href string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "[") {
		return "", "", false
	}
	// Format emitted by browser snapshot:
	// [1] link "Title" → https://example.com
	parts := strings.SplitN(line, "→", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	left := strings.TrimSpace(parts[0])
	right := strings.TrimSpace(parts[1])
	if right == "" {
		return "", "", false
	}
	q1 := strings.Index(left, `"`)
	q2 := strings.LastIndex(left, `"`)
	if q1 >= 0 && q2 > q1 {
		title = left[q1+1 : q2]
	}
	return title, right, true
}

func findNodes(root *html.Node, match func(*html.Node) bool, limit int) []*html.Node {
	if root == nil || limit <= 0 {
		return nil
	}
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || len(out) >= limit {
			return
		}
		if match(n) {
			out = append(out, n)
			if len(out) >= limit {
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if len(out) >= limit {
				return
			}
		}
	}
	walk(root)
	return out
}

func firstAnchorDescendant(root *html.Node) *html.Node {
	return firstDescendant(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && attrValue(n, "href") != ""
	})
}

func firstTextByClass(root *html.Node, classes []string, tags []string) string {
	if root == nil {
		return ""
	}
	if len(classes) > 0 {
		if n := firstDescendant(root, func(n *html.Node) bool {
			if n.Type != html.ElementNode {
				return false
			}
			for _, class := range classes {
				if hasClass(n, class) {
					return true
				}
			}
			return false
		}); n != nil {
			if s := strings.TrimSpace(textContent(n)); s != "" {
				return s
			}
		}
	}
	if len(tags) == 0 {
		tags = []string{"p", "div", "span"}
	}
	for _, tag := range tags {
		if n := firstDescendant(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == tag
		}); n != nil {
			if s := strings.TrimSpace(textContent(n)); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstDescendant(root *html.Node, match func(*html.Node) bool) *html.Node {
	if root == nil {
		return nil
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if match(c) {
			return c
		}
		if found := firstDescendant(c, match); found != nil {
			return found
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil {
			return
		}
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
			b.WriteByte(' ')
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return textutil.CompactWhitespace(b.String())
}

func hasClass(n *html.Node, want string) bool {
	return strings.Contains(" "+attrValue(n, "class")+" ", " "+want+" ")
}

func attrHasValue(n *html.Node, key, want string) bool {
	return strings.EqualFold(attrValue(n, key), want)
}

func attrValue(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}
