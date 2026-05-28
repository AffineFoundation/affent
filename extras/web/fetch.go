package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/metrictext"
	"github.com/affinefoundation/affent/internal/netguard"
	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/websource"
	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"
)

var markdownLinkForPreviewPattern = regexp.MustCompile(`!?\[([^\]\n]{1,120})\]\([^)\n]{1,512}\)`)

// FetchConfig tunes WebFetchTool. Zero values pick sane defaults.
type FetchConfig struct {
	// HTTP is reused across calls. When nil FetchTool builds a client
	// with a 30s timeout AND an SSRF guard that blocks dialing to
	// private / loopback / link-local / unspecified / multicast IPs
	// (covers RFC1918, AWS-metadata at 169.254.169.254, 127.0.0.1,
	// IPv6 ULA / link-local, etc.). When non-nil, the caller owns
	// the safety story — pass in a hardened client or be sure your
	// network can't reach anything sensitive.
	HTTP *http.Client
	// MaxBytes caps the response body the tool reads. Default 2 MiB —
	// enough for most articles without letting a misconfigured server
	// stream gigabytes into memory. Pages larger than this are
	// truncated (bytes) before HTML→markdown. Values above the hard
	// cap are clamped so callers cannot accidentally disable the
	// memory guard.
	MaxBytes int64
	// MaxResultChars caps the markdown output handed back to the LLM.
	// Default 8000. Values above the hard cap are clamped. Truncated
	// output gets a "...(truncated)" marker.
	MaxResultChars int
	// UserAgent is sent on every request. Defaults to a browser-shaped
	// UA because many public sites reject library/bot-looking clients
	// even for ordinary article/doc pages. Override if a deployment
	// needs a stricter identity string.
	UserAgent string
	// AllowPrivateNetwork disables the default SSRF guard. Off by
	// default — a model that decides to fetch http://127.0.0.1:7777
	// (affentserve itself) or http://169.254.169.254 (cloud-metadata
	// IMDSv1) shouldn't be able to without operator opt-in. Flip on
	// only for development against local services, or when the agent
	// is running inside a network namespace that already isolates it.
	AllowPrivateNetwork bool
	// RenderedFallback, when set, is called after web_fetch determines
	// that a URL is probably not readable through direct HTTP but may be
	// readable in a real browser: anti-bot/challenge responses, direct
	// reader trap hosts, and client-rendered app shells. This is an
	// injection point instead of a browser dependency so extras/web
	// stays lightweight; callers that wire it must preserve their own
	// browser security policy.
	RenderedFallback RenderedFallbackFunc
}

// RenderedFallbackFunc reads requestURL through a caller-provided rendered
// page backend, usually the same session-scoped Chromium browser exposed as
// browser_* tools. The reason describes why direct fetch was not enough.
type RenderedFallbackFunc func(ctx context.Context, requestURL string, reason FetchFallbackReason) (string, error)

// FetchFallbackReason is passed to RenderedFallbackFunc so adapters can log,
// choose wait strategy, or reject cases they do not want to render.
type FetchFallbackReason struct {
	Kind        string
	Status      int
	ContentType string
	Detail      string
	FinalURL    string
}

const (
	maxFetchURLBytes             = 4096
	defaultMaxBytes              = 2 * 1024 * 1024
	maxFetchBytes                = 8 * 1024 * 1024
	defaultMaxResultChars        = 8000
	maxFetchResultChars          = 64 * 1024
	maxDynamicShellPreviewChars  = 600
	maxDynamicShellLinkScanBytes = 512 * 1024
	maxDynamicShellLinks         = 5
	maxDynamicShellLinkText      = 80
	maxHTMLTextFallbackChars     = 64 * 1024
	defaultUserAgent             = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36 AffentWebFetch/0.1"
	defaultAcceptHeader          = "text/html,application/xhtml+xml,application/json;q=0.95,application/ld+json;q=0.95,application/*+json;q=0.9,application/xml;q=0.9,application/rss+xml;q=0.9,application/atom+xml;q=0.9,application/*+xml;q=0.85,application/x-ndjson;q=0.85,text/plain;q=0.8,application/yaml;q=0.75,application/x-yaml;q=0.75,*/*;q=0.5"
)

// FetchTool returns an agent.Tool that fetches a URL and returns its
// text content. HTML is converted to markdown; other text/* types
// (text/plain, application/json, …) are passed through; non-text bodies
// get a placeholder. Redirects are followed by net/http's default
// behaviour (10 hops max).
func FetchTool(cfg FetchConfig) *agent.Tool {
	cfg = normalizeFetchConfig(cfg)
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["url"],
        "properties": {
            "url": {"type": "string", "minLength": 1, "maxLength": %d, "description": "The fully-qualified URL to fetch (http:// or https://)."}
        }
    }`, maxFetchURLBytes))

	description := "Fetch a URL and return its text content. HTML is " +
		"converted to compact markdown; text/plain, application/json, " +
		"and similar text types are returned as-is. Output is capped " +
		"and truncated with a marker. Redirects are followed. Best for " +
		"official, raw, API, repository, or text pages; avoid search/result " +
		"lists, social pages, short links, and dynamic dashboards when a " +
		"canonical API/text/source URL is available."
	if cfg.RenderedFallback != nil {
		description += " In browser-enabled runtimes, pages that block direct readers or require JavaScript are automatically retried through the session browser and returned as rendered snapshot text."
	}

	return &agent.Tool{
		Name:        "web_fetch",
		Description: description,
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				URL string `json:"url"`
			}
			if err := decodeWebToolArgs(raw, &args, "retry web_fetch with only the documented field: url"); err != nil {
				return "", err
			}
			args.URL = strings.TrimSpace(args.URL)
			if args.URL == "" {
				return "", errors.New("url is required\nFailure: kind=invalid_args\nNext: retry web_fetch with a fully-qualified http:// or https:// URL")
			}
			if len(args.URL) > maxFetchURLBytes {
				return "", fmt.Errorf("url is %d bytes; web_fetch supports URLs up to %d bytes\nFailure: kind=invalid_args\nNext: retry web_fetch with the canonical page URL, or use an available discovery tool/source to find a shorter result URL", len(args.URL), maxFetchURLBytes)
			}
			if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
				return "", fmt.Errorf("url must start with http:// or https:// (got %q)\nFailure: kind=invalid_args\nNext: retry web_fetch with the full URL including the http:// or https:// scheme", args.URL)
			}
			if out := directFetchPreflightResult(ctx, cfg, args.URL); out != "" {
				return out, nil
			}
			return fetch(ctx, cfg, args.URL)
		},
	}
}

func decodeWebToolArgs(raw json.RawMessage, dst any, next string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode args: %w\nFailure: kind=invalid_args\nNext: %s", err, next)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode args: multiple JSON values\nFailure: kind=invalid_args\nNext: %s", next)
	}
	return nil
}

func normalizeFetchConfig(cfg FetchConfig) FetchConfig {
	if cfg.HTTP == nil {
		cfg.HTTP = newGuardedClient(cfg.AllowPrivateNetwork)
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	} else if cfg.MaxBytes > maxFetchBytes {
		cfg.MaxBytes = maxFetchBytes
	}
	if cfg.MaxResultChars <= 0 {
		cfg.MaxResultChars = defaultMaxResultChars
	} else if cfg.MaxResultChars > maxFetchResultChars {
		cfg.MaxResultChars = maxFetchResultChars
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}
	return cfg
}

func fetch(ctx context.Context, cfg FetchConfig, requestURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	// Hint we want text-ish content, including structured formats that
	// web_fetch can return directly for API, feed, and metrics sources.
	req.Header.Set("Accept", defaultAcceptHeader)
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")

	resp, err := cfg.HTTP.Do(req)
	if err != nil {
		return "", recoverableFetchError(requestURL, "", 0, fmt.Errorf("http get: %w", err))
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if finalURL == "" {
		finalURL = requestURL
	}

	if resp.StatusCode/100 != 2 {
		// Read a little so the error is informative.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		err := recoverableFetchError(requestURL, finalURL, resp.StatusCode, fmt.Errorf("http %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(preview))))
		reason := FetchFallbackReason{Kind: fetchFailureKind(resp.StatusCode, err), Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Detail: strings.TrimSpace(string(preview)), FinalURL: finalURL}
		if shouldUseRenderedFallback(reason) {
			if out, fallbackErr := renderedFallbackResult(ctx, cfg, requestURL, reason); fallbackErr == nil {
				return out, nil
			}
		}
		return "", err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxBytes+1))
	if err != nil {
		return "", recoverableFetchError(requestURL, finalURL, 0, fmt.Errorf("read body: %w", err))
	}
	bodyTruncated := int64(len(body)) > cfg.MaxBytes
	if bodyTruncated {
		body = body[:cfg.MaxBytes]
	}

	ct := resp.Header.Get("Content-Type")
	if len(bytes.TrimSpace(body)) == 0 {
		if shouldUseRenderedFallbackForEmpty(resp.StatusCode, ct) {
			if rendered, err := renderedFallbackResult(ctx, cfg, requestURL, FetchFallbackReason{Kind: "empty_response", Status: resp.StatusCode, ContentType: ct, Detail: "empty successful HTTP response", FinalURL: finalURL}); err == nil {
				return rendered, nil
			}
		}
		return emptyFetchResult(finalURL, ct), nil
	}
	out := renderBody(body, ct, finalURL)
	if strings.TrimSpace(out) == "" {
		return emptyFetchResult(finalURL, ct), nil
	}
	if reason := blockedPageReason(out, finalURL); reason != "" {
		if rendered, err := renderedFallbackResult(ctx, cfg, requestURL, FetchFallbackReason{Kind: "blocked", ContentType: ct, Detail: reason, FinalURL: finalURL}); err == nil {
			return rendered, nil
		}
		return blockedFetchResult(finalURL, ct, reason, renderedFallbackUnavailableNote(cfg)), nil
	}
	if reason := dynamicPageShellReason(body, ct, out); reason != "" {
		if rendered, err := renderedFallbackResult(ctx, cfg, requestURL, FetchFallbackReason{Kind: "dynamic_shell", ContentType: ct, Detail: reason, FinalURL: finalURL}); err == nil {
			return rendered, nil
		}
		return dynamicPageShellResult(finalURL, ct, reason, dynamicShellDiscoveryPreview(out), dynamicShellDiscoveryLinks(body, finalURL), embeddedDataSnippets(body, finalURL), renderedFallbackUnavailableNote(cfg)), nil
	}

	out = truncateFetchResult(out, cfg.MaxResultChars)
	out = addSourceAccessHeader(requestURL, finalURL, out)
	if bodyTruncated {
		out = strings.TrimSpace(out) + "\n\n...(response body truncated)"
	}
	return out, nil
}

func addSourceAccessHeader(requestURL, finalURL, out string) string {
	if finalURL == "" {
		finalURL = requestURL
	}
	var b strings.Builder
	b.WriteString(sourceaccess.FormatFetchedHeader(finalURL, requestURL))
	if metrictext.HasMultiplePriceLikeValues(out) {
		b.WriteString(metrictext.AmbiguityNote)
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimSpace(out))
	return b.String()
}

func truncateFetchResult(out string, maxChars int) string {
	if maxChars <= 0 || len(out) <= maxChars {
		return out
	}
	return textutil.Preview(out, maxChars, "\n\n...(truncated)")
}

func emptyFetchResult(finalURL, contentType string) string {
	return fmt.Sprintf("[empty response: URL=%s, Content-Type=%q]\nFailure: kind=empty_response\nNext: do not treat this as page evidence; use another available source, fetch a text/API/HTML version, or answer with this source marked as empty/unverified.", finalURL, contentType)
}

func blockedFetchResult(finalURL, contentType, reason, fallbackNote string) string {
	return fmt.Sprintf("[blocked response: URL=%s, Content-Type=%q, Reason=%q]\nFailure: kind=blocked\nNext: do not treat this challenge/error page as source evidence; use an available search result snippet only as weak evidence, switch to a canonical API/text/source page, or mark this source as blocked/unverified.%s", finalURL, contentType, reason, fallbackNote)
}

type dynamicShellLink struct {
	Text  string
	URL   string
	score int
	order int
}

func dynamicPageShellResult(finalURL, contentType, reason, preview string, links []dynamicShellLink, dataSnippets []string, fallbackNote string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[dynamic page shell: URL=%s, Content-Type=%q, Reason=%q]", finalURL, contentType, reason)
	if preview != "" {
		fmt.Fprintf(&b, "\nDiscovery preview (not source evidence): %s", preview)
	}
	if len(links) > 0 {
		b.WriteString("\nDiscovery links (not source evidence):")
		for _, link := range links {
			if link.Text != "" {
				fmt.Fprintf(&b, "\n- %s — %s", link.Text, link.URL)
			} else {
				fmt.Fprintf(&b, "\n- %s", link.URL)
			}
		}
	}
	if len(dataSnippets) > 0 {
		b.WriteString("\nEmbedded data preview (page source evidence; verify relevance before using):")
		for _, snippet := range dataSnippets {
			fmt.Fprintf(&b, "\n- %s", snippet)
		}
		b.WriteString("\nNext: the rendered page shell itself is not evidence; use the embedded data preview only when it directly matches the requested entity/URL, otherwise switch to a canonical API/text/source page or mark rendered-only fields as unverified.")
		b.WriteString(fallbackNote)
		return b.String()
	}
	b.WriteString("\nFailure: kind=dynamic_shell\nNext: do not treat this loading/app shell as source evidence; use the discovery preview/links only to choose a canonical API/text/source page, or answer with this source marked as dynamic/unverified.")
	b.WriteString(fallbackNote)
	return b.String()
}

func dynamicShellDiscoveryPreview(markdown string) string {
	text := textutil.CompactWhitespace(markdown)
	if text == "" {
		return ""
	}
	text = markdownLinkForPreviewPattern.ReplaceAllString(text, "$1")
	lower := strings.ToLower(text)
	if len(text) <= 80 && containsAny(lower, "loading", "loading...", "enable javascript", "please enable javascript") {
		return ""
	}
	return textutil.Preview(text, maxDynamicShellPreviewChars, "...(truncated)")
}

func dynamicShellDiscoveryLinks(body []byte, baseURL string) []dynamicShellLink {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	if len(body) > maxDynamicShellLinkScanBytes {
		body = body[:maxDynamicShellLinkScanBytes]
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	seen := map[string]bool{}
	var links []dynamicShellLink
	var current *dynamicShellLink
	depth := 0
	order := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if len(links) == 0 {
				return nil
			}
			sort.SliceStable(links, func(i, j int) bool {
				if links[i].score != links[j].score {
					return links[i].score > links[j].score
				}
				return links[i].order < links[j].order
			})
			if len(links) > maxDynamicShellLinks {
				links = links[:maxDynamicShellLinks]
			}
			return links
		case html.StartTagToken:
			token := z.Token()
			if token.Data == "a" {
				if link, ok := dynamicShellLinkFromToken(token, base, order); ok && !seen[link.URL] {
					current = &link
					depth = 1
				} else {
					current = nil
				}
			} else if current != nil {
				depth++
			}
		case html.EndTagToken:
			token := z.Token()
			if current == nil {
				continue
			}
			if token.Data == "a" {
				current.Text = truncateDiscoveryLinkText(current.Text)
				if current.score = dynamicShellLinkScore(current.Text, current.URL); current.score > 0 {
					seen[current.URL] = true
					current.order = order
					links = append(links, *current)
					order++
				}
				current = nil
				depth = 0
			} else if depth > 0 {
				depth--
			}
		case html.TextToken:
			if current != nil && len(current.Text) < maxDynamicShellLinkText {
				current.Text = strings.TrimSpace(current.Text + " " + string(z.Text()))
			}
		}
	}
}

func dynamicShellLinkFromToken(token html.Token, base *url.URL, order int) (dynamicShellLink, bool) {
	var href string
	for _, attr := range token.Attr {
		if strings.EqualFold(attr.Key, "href") {
			href = strings.TrimSpace(attr.Val)
			break
		}
	}
	if href == "" || strings.HasPrefix(href, "#") {
		return dynamicShellLink{}, false
	}
	u, err := url.Parse(href)
	if err != nil {
		return dynamicShellLink{}, false
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return dynamicShellLink{}, false
	}
	u = base.ResolveReference(u)
	u.Fragment = ""
	return dynamicShellLink{URL: u.String(), order: order}, true
}

func dynamicShellLinkScore(text, rawURL string) int {
	lower := strings.ToLower(text + " " + rawURL)
	score := 0
	for _, needle := range []string{"api", "docs", "documentation", "developer", "developers", "export", "download", "raw", "data", "dataset", "csv", "json", "rss", "feed", "github"} {
		if strings.Contains(lower, needle) {
			score += 2
		}
	}
	for _, needle := range []string{"login", "signin", "sign-in", "auth", "account", "portfolio", "swap", "stake", "claim", "api key", "api-key", "apikey", "keys", "billing", "pricing", "upgrade"} {
		if strings.Contains(lower, needle) {
			score -= 2
		}
	}
	return score
}

func truncateDiscoveryLinkText(text string) string {
	text = textutil.CompactWhitespace(text)
	return textutil.Preview(text, maxDynamicShellLinkText, "...(truncated)")
}

func directFetchPreflightResult(ctx context.Context, cfg FetchConfig, rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	host := websource.NormalizeHost(u.Hostname())
	path := strings.ToLower(u.EscapedPath())
	switch {
	case websource.IsSearchResultPage(host, path):
		if out, err := renderedFallbackResult(ctx, cfg, rawURL, FetchFallbackReason{Kind: "search_results_page", Detail: "search-results page", FinalURL: rawURL}); err == nil {
			return out
		}
		return skippedDirectFetchResult(rawURL, "search-results page", renderedFallbackUnavailableNote(cfg))
	case websource.IsKnownDirectReaderTrapHost(host):
		if out, err := renderedFallbackResult(ctx, cfg, rawURL, FetchFallbackReason{Kind: "direct_reader_trap", Detail: "site usually blocks direct HTTP readers", FinalURL: rawURL}); err == nil {
			return out
		}
		return skippedDirectFetchResult(rawURL, "site usually blocks direct HTTP readers", renderedFallbackUnavailableNote(cfg))
	default:
		return ""
	}
}

func renderedFallbackResult(ctx context.Context, cfg FetchConfig, requestURL string, reason FetchFallbackReason) (string, error) {
	if cfg.RenderedFallback == nil {
		return "", errors.New("rendered fallback is not configured")
	}
	if reason.FinalURL == "" {
		reason.FinalURL = requestURL
	}
	if err := validateRenderedFallbackURL(ctx, cfg, reason.FinalURL); err != nil {
		return "", err
	}
	out, err := cfg.RenderedFallback(ctx, requestURL, reason)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("rendered fallback returned empty content")
	}
	if fallbackBlock := blockedPageReason(out, reason.FinalURL); fallbackBlock != "" {
		return "", fmt.Errorf("rendered fallback returned blocked/challenge page: %s", fallbackBlock)
	}
	renderedStatus := ""
	if info, ok := sourceaccess.FirstInfoFromResult(out); ok {
		status := info.RenderedBrowserSourceStatus
		if status == "" {
			status = info.PageTextBelow
		}
		switch status {
		case "search_results_discovery_only", "not_found_page_discovery_only", "partial_dynamic_page_evidence":
			renderedStatus = "rendered_browser_source_status=" + status
		}
	}
	prefix := sourceaccess.FormatSourceAccessLine("fetched_url", reason.FinalURL, requestURL, "mode=rendered_browser_fallback", "linked_urls_in_content=discovered_unverified_until_fetched", renderedStatus)
	prefix += fmt.Sprintf("[rendered browser fallback succeeded: URL=%s, DirectFetchReason=%q", reason.FinalURL, reason.Kind)
	if reason.Status > 0 {
		prefix += fmt.Sprintf(", DirectFetchStatus=%d", reason.Status)
	}
	if reason.Detail != "" {
		prefix += fmt.Sprintf(", DirectFetchDetail=%q", reason.Detail)
	}
	prefix += "]\n"
	body := truncateFetchResult(out, cfg.MaxResultChars)
	if metrictext.HasMultiplePriceLikeValues(body) {
		return prefix + metrictext.AmbiguityNote + "\n" + body, nil
	}
	return prefix + body, nil
}

func validateRenderedFallbackURL(ctx context.Context, cfg FetchConfig, rawURL string) error {
	if cfg.AllowPrivateNetwork {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("rendered fallback URL is not fully qualified")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("rendered fallback URL scheme %q is not supported", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("rendered fallback URL has no hostname")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("ssrf-guard: refusing rendered fallback to localhost")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("ssrf-guard: refusing rendered fallback to %s (private / loopback / link-local / unspecified / multicast)", ip)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("ssrf-guard: resolve rendered fallback host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("ssrf-guard: rendered fallback host %q resolved no addresses", host)
	}
	for _, addr := range ips {
		if isBlockedIP(addr.IP) {
			return fmt.Errorf("ssrf-guard: refusing rendered fallback to %s resolved from %q (private / loopback / link-local / unspecified / multicast)", addr.IP, host)
		}
	}
	return nil
}

func shouldUseRenderedFallback(reason FetchFallbackReason) bool {
	switch reason.Kind {
	case "blocked", "rate_limited", "dynamic_shell", "direct_reader_trap", "search_results_page", "not_found":
		return true
	default:
		return false
	}
}

func shouldUseRenderedFallbackForEmpty(status int, contentType string) bool {
	switch status {
	case http.StatusNoContent, http.StatusResetContent:
		return false
	}
	mediaType := contentMediaType(contentType)
	return mediaType == "" || mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func renderedFallbackUnavailableNote(cfg FetchConfig) string {
	if cfg.RenderedFallback != nil {
		return ""
	}
	return " Rendered browser fallback is not configured for this web_fetch; start affentserve with browser support enabled, or use browser tools directly if this runtime exposes them."
}

func skippedDirectFetchResult(finalURL, reason, fallbackNote string) string {
	return fmt.Sprintf("[blocked response: URL=%s, Content-Type=%q, Reason=%q]\nFailure: kind=blocked\nNext: do not spend direct fetch calls on this page in this turn; use the search result target URL instead of a search-results page, use search snippets only as weak discovery/sentiment evidence, switch to an official API/text/source page, or mark this source as blocked/unverified.%s", finalURL, "", reason, fallbackNote)
}

func recoverableFetchError(requestURL, finalURL string, status int, err error) error {
	if err == nil || strings.Contains(err.Error(), "\nNext:") {
		return err
	}
	next := "retry only if the URL or transient network condition changed; otherwise use another available source, an alternate official URL, or answer with what could be verified"
	lower := strings.ToLower(err.Error())
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		next = "do not keep retrying this blocked URL; use another available source, a canonical public URL from discovery results, or mark this source as blocked/unverified"
	case status == http.StatusNotFound || status == http.StatusGone:
		next = "use available discovery results or the site's navigation to find the current canonical URL, then retry web_fetch with that URL"
	case status == http.StatusTooManyRequests:
		next = "do not hammer this host; use cached/search-result snippets or another authoritative source, and retry later only if needed"
	case status >= 500 && status <= 599:
		next = "server-side failure; retry once later or use another authoritative mirror/source instead of repeating the same failing URL"
	case strings.Contains(lower, "ssrf-guard"):
		next = "do not fetch private, loopback, link-local, or internal network URLs; use public sources or ask the operator to enable private-network fetch only for trusted local development"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		next = "network timeout; retry once with the same canonical URL, then switch to another available source or discovery tool if it fails again"
	}
	return fmt.Errorf("%w\nFailure: %s\nURL: %s%s\nNext: %s", err, fetchFailureLabel(status, err), requestURL, redirectedURLSuffix(requestURL, finalURL), next)
}

func fetchFailureLabel(status int, err error) string {
	kind := fetchFailureKind(status, err)
	if status > 0 {
		return fmt.Sprintf("kind=%s, status=%d", kind, status)
	}
	return fmt.Sprintf("kind=%s", kind)
}

func fetchFailureKind(status int, err error) string {
	kind := "network_error"
	lower := ""
	if err != nil {
		lower = strings.ToLower(err.Error())
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = "blocked"
	case status == http.StatusNotFound || status == http.StatusGone:
		kind = "not_found"
	case status == http.StatusTooManyRequests:
		kind = "rate_limited"
	case status >= 500 && status <= 599:
		kind = "server_error"
	case status > 0:
		kind = "http_error"
	case strings.Contains(lower, "ssrf-guard"):
		kind = "private_network_blocked"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		kind = "timeout"
	}
	return kind
}

func renderBody(body []byte, contentType, finalURL string) string {
	mediaType := contentMediaType(contentType)
	if shouldSniffBody(mediaType) {
		switch sniffReadableBody(body) {
		case "html":
			mediaType = "text/html"
		case "text":
			mediaType = "text/plain"
		}
	}
	switch {
	case mediaType == "text/html", mediaType == "application/xhtml+xml":
		// Standard reader pipeline: Readability extracts the article's
		// main content (drops nav/header/footer/sidebar/ads), then
		// html-to-markdown converts the cleaned HTML. Both libraries
		// are the de-facto Go choices:
		//   - go-shiori/go-readability — Mozilla Readability port
		//   - JohannesKaufmann/html-to-markdown — commonmark-spec converter
		// Falling back to direct conversion when readability can't
		// identify a main article (e.g. on a homepage, listing, or
		// non-article page) so the model still gets something useful.
		htmlText := string(body)
		var pageURL *url.URL
		if u, err := url.Parse(finalURL); err == nil {
			pageURL = u
		}
		articleHTML := htmlText
		if art, err := readability.FromReader(bytes.NewReader(body), pageURL); err == nil && art.Content != "" {
			articleHTML = art.Content
		}

		var opts []converter.ConvertOptionFunc
		if domain := domainOf(finalURL); domain != "" {
			opts = append(opts, converter.WithDomain(domain))
		}
		md, err := htmltomarkdown.ConvertString(articleHTML, opts...)
		if err != nil {
			return htmlTextFallback(body)
		}
		return md
	case isReadableTextMediaType(mediaType):
		return string(body)
	default:
		return fmt.Sprintf("[non-text response: URL=%s, Content-Type=%q, %d bytes]\nFailure: kind=non_text\nNext: do not treat this as readable page evidence; fetch an HTML/API/text version, or choose another authoritative source.", finalURL, contentType, len(body))
	}
}

func contentMediaType(contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	return mediaType
}

func isReadableTextMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json",
		"application/xml",
		"application/javascript",
		"application/x-javascript",
		"application/x-ndjson",
		"application/yaml",
		"application/x-yaml":
		return true
	}
	return strings.HasPrefix(mediaType, "application/") &&
		(strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml"))
}

func htmlTextFallback(body []byte) string {
	z := html.NewTokenizer(bytes.NewReader(body))
	var b strings.Builder
	skipDepth := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(b.String())
		case html.StartTagToken:
			name, _ := z.TagName()
			if htmlFallbackSkipsTag(string(name)) {
				skipDepth++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if skipDepth > 0 && htmlFallbackSkipsTag(string(name)) {
				skipDepth--
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			text := textutil.CompactWhitespace(string(z.Text()))
			if text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			if b.Len()+len(text) > maxHTMLTextFallbackChars {
				remaining := maxHTMLTextFallbackChars - b.Len()
				if remaining > 0 {
					b.WriteString(textutil.Preview(text, remaining, ""))
				}
				return strings.TrimSpace(b.String())
			}
			b.WriteString(text)
		}
	}
}

func htmlFallbackSkipsTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "script", "style", "template", "svg":
		return true
	default:
		return false
	}
}

func shouldSniffBody(mediaType string) bool {
	switch mediaType {
	case "", "application/octet-stream", "binary/octet-stream":
		return true
	default:
		return false
	}
}

func sniffReadableBody(body []byte) string {
	if looksLikeHTML(body) {
		return "html"
	}
	if looksLikeText(body) {
		return "text"
	}
	return ""
}

func looksLikeHTML(body []byte) bool {
	const sampleLimit = 4096
	sample := body
	if len(sample) > sampleLimit {
		sample = sample[:sampleLimit]
	}
	s := strings.TrimLeftFunc(string(bytes.TrimPrefix(sample, []byte{0xEF, 0xBB, 0xBF})), unicode.IsSpace)
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "<!doctype html") ||
		strings.HasPrefix(s, "<html") ||
		strings.HasPrefix(s, "<head") ||
		strings.HasPrefix(s, "<body")
}

func looksLikeText(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	const sampleLimit = 1024
	sample := body
	if len(sample) > sampleLimit {
		sample = sample[:textutil.AlignBackward(string(sample), sampleLimit)]
	}
	if len(sample) == 0 || !utf8.Valid(sample) {
		return false
	}
	for _, r := range string(sample) {
		if r == '\uFFFD' {
			return false
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func redirectedURLSuffix(requestURL, finalURL string) string {
	if finalURL == "" || finalURL == requestURL {
		return ""
	}
	return "\nFinal URL: " + finalURL
}

func blockedPageReason(markdown, finalURL string) string {
	lower := strings.ToLower(markdown)
	markers := []struct {
		needle string
		reason string
	}{
		{"unfortunately, bots use duckduckgo too", "anti-bot challenge"},
		{"please complete the following challenge to confirm", "anti-bot challenge"},
		{"our systems have detected unusual traffic", "anti-bot challenge"},
		{"if you're having trouble accessing google search", "search challenge page"},
		{"enable javascript and cookies to continue", "javascript/cookie challenge"},
		{"checking if the site connection is secure", "javascript/cookie challenge"},
		{"attention required! | cloudflare", "cloudflare challenge"},
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker.needle) {
			return marker.reason
		}
	}
	host := strings.ToLower(domainOf(finalURL))
	if (strings.Contains(host, "://x.com") || strings.Contains(host, "://twitter.com")) &&
		strings.Contains(lower, "something went wrong") &&
		strings.Contains(lower, "privacy related extensions") {
		return "social site error page"
	}
	return ""
}

func dynamicPageShellReason(body []byte, contentType, markdown string) string {
	mediaType := contentMediaType(contentType)
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		if !shouldSniffBody(mediaType) || !looksLikeHTML(body) {
			return ""
		}
	}

	sample := body
	const htmlSampleLimit = 256 * 1024
	if len(sample) > htmlSampleLimit {
		sample = sample[:htmlSampleLimit]
	}
	htmlLower := strings.ToLower(string(sample))
	if !hasClientRenderedAppMarker(htmlLower) {
		return ""
	}

	text := strings.ToLower(textutil.CompactWhitespace(markdown))
	switch {
	case text == "":
		return "client-rendered app shell with no readable text"
	case len(text) <= 900 && containsAny(text, "loading", "loading...", "enable javascript", "please enable javascript"):
		return "client-rendered loading/javascript shell"
	case len(body) >= 512*1024 && len(text) <= 6000 && strings.Count(htmlLower, "<script") >= 30:
		return "large client-rendered app shell with little readable text"
	case len(text) <= 400 && strings.Count(htmlLower, "<script") >= 8:
		return "client-rendered app shell with little readable text"
	default:
		return ""
	}
}

func hasClientRenderedAppMarker(htmlLower string) bool {
	return containsAny(htmlLower,
		"/_next/static/",
		"__next",
		"data-nextjs",
		"window.__nuxt__",
		"data-server-rendered=\"true\"",
		"id=\"root\"",
		"id=\"app\"",
		"vite/client",
		"webpackchunk",
	)
}

// newGuardedClient builds an http.Client whose Transport refuses to
// dial to any IP we don't want a model-driven URL to reach. The check
// runs in net.Dialer.Control, AFTER DNS resolution but BEFORE the TCP
// SYN, so we catch the actual IP the OS is about to connect to —
// even when the hostname has multiple A/AAAA records or the resolver
// returns a different answer than a separate "preflight" lookup
// would (defeats trivial DNS-rebinding attacks on a single connect).
// Redirects re-enter the same dialer per hop, so a public→private
// hop is blocked too.
//
// When allowPrivate is true the control hook is omitted entirely so
// dev / local-service fetching works as expected.
func newGuardedClient(allowPrivate bool) *http.Client {
	if allowPrivate {
		return &http.Client{Timeout: 30 * time.Second}
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			ip := net.ParseIP(host)
			if ip == nil {
				// Shouldn't happen — by the time Control fires, the
				// Dialer has resolved to a numeric address. Be
				// defensive: refuse rather than connect blind.
				return fmt.Errorf("ssrf-guard: unparseable dial target %q", address)
			}
			if isBlockedIP(ip) {
				return fmt.Errorf("ssrf-guard: refusing to dial %s (private / loopback / link-local / unspecified / multicast)", ip)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			// Match http.DefaultTransport's idle-pool bounds. A
			// zero-value Transport keeps idle conns forever (no
			// IdleConnTimeout) and the pool is effectively unbounded;
			// a long-running affentserve fetching many distinct
			// hosts would accumulate sockets until something killed
			// the process. 90 s / 100 conns are net/http's own
			// defaults.
			MaxIdleConns:    100,
			IdleConnTimeout: 90 * time.Second,
			// HTTP/2 needs an explicit opt-in once any field on the
			// Transport is set (Go's transport stops auto-upgrading
			// in that case). Without this, every fetch is HTTP/1.1,
			// missing out on multiplexing and ALPN for hosts that
			// only optimize the h2 path.
			ForceAttemptHTTP2: true,
		},
	}
}

// isBlockedIP collapses Go's net.IP category methods plus IPv4
// broadcast into one check. IsPrivate covers RFC1918 + IPv6 ULA;
// IsLoopback / IsLinkLocalUnicast / IsUnspecified / IsMulticast
// cover the rest of the families that a model has no business
// reaching through a fetch tool.
func isBlockedIP(ip net.IP) bool {
	return netguard.IsBlockedIP(ip)
}

// domainOf extracts "scheme://host" from a URL, used to resolve
// relative links/images against the page's origin.
func domainOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
