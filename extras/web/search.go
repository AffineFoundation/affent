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
	"strings"
	"time"
	"unicode/utf8"

	agent "github.com/affinefoundation/affent/internal/agent"
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
)

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
            "query": {"type": "string", "minLength": 1, "maxLength": %d, "description": "Search query (plain English; the tool handles tokenization)."},
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
				return "", errors.New("query is required\nFailure: kind=invalid_args\nNext: retry with 2-6 specific keywords, named entities, error text, or the URL/topic you need to discover")
			}
			if len(query) > maxSearchQueryBytes {
				return "", fmt.Errorf("query is %d bytes; web_search supports queries up to %d bytes\nFailure: kind=invalid_args\nNext: retry with 2-6 specific keywords, named entities, error text, or the shortest precise topic", len(query), maxSearchQueryBytes)
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
		return "(no results)\nFailure: kind=no_results\nNext: retry web_search with fewer or different keywords, include distinctive entities or official domain names, or use another available source URL if already known."
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	var b strings.Builder
	displayed := 0
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
		if note := directFetchCaution(url); note != "" {
			fmt.Fprintf(&b, "\n   Direct-fetch caution: %s", note)
		}
		b.WriteString("\n\n")
	}
	if displayed == 0 {
		return "(no usable results: search provider returned no URLs)\nFailure: kind=no_results\nNext: retry web_search with more distinctive keywords or official domain names, or use another available source URL if already known."
	}
	b.WriteString("Next: choose the 1-3 most authoritative/current result URLs, prefer official or primary sources, and read them with an available page-reading tool before answering. If no full-page reading tool is available, compare snippets and say that full-page verification was unavailable.")
	return strings.TrimSpace(b.String())
}

func directFetchCaution(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	path := strings.ToLower(u.EscapedPath())
	if isSearchResultHost(host, path) {
		return "open the target/source URL, not the search-results page; search snippets are discovery evidence only."
	}
	if isRedirectorHost(host) {
		return "this is often a redirect or short-link wrapper; prefer the final canonical URL from an authoritative source before reading it."
	}
	if isSocialOrDiscussionHost(host) {
		return "social/discussion pages often block direct readers or require JavaScript; use them as sentiment/claim evidence only unless a readable page source is returned."
	}
	return ""
}

func isSearchResultHost(host, path string) bool {
	switch host {
	case "google.com", "bing.com", "duckduckgo.com", "search.brave.com", "search.yahoo.com", "yahoo.com", "baidu.com", "yandex.com":
		return path == "" || path == "/" || strings.HasPrefix(path, "/search") || strings.HasPrefix(path, "/html") || strings.HasPrefix(path, "/s")
	default:
		return false
	}
}

func isRedirectorHost(host string) bool {
	switch host {
	case "t.co", "bit.ly", "tinyurl.com", "goo.gl", "lnkd.in", "l.facebook.com", "out.reddit.com":
		return true
	default:
		return false
	}
}

func isSocialOrDiscussionHost(host string) bool {
	for _, suffix := range []string{
		"x.com",
		"twitter.com",
		"facebook.com",
		"instagram.com",
		"linkedin.com",
		"tiktok.com",
		"threads.net",
		"reddit.com",
		"medium.com",
		"discord.com",
		"t.me",
		"telegram.me",
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func truncateSearchField(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}

func recoverableSearchError(err error) error {
	if err == nil || strings.Contains(err.Error(), "\nNext:") {
		return err
	}
	lower := strings.ToLower(err.Error())
	next := "retry once with fewer/different keywords or a more distinctive entity; if search remains unavailable, use known official URLs with available page-reading/rendering tools or say what could not be verified."
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
