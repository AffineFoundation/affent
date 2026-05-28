package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/go-rod/rod/lib/proto"
	"golang.org/x/net/publicsuffix"
)

const (
	maxNetworkEvidenceEntries   = 200
	maxNetworkEvidenceBodyBytes = 512 * 1024
	maxNetworkPreviewBytes      = 1200
	defaultNetworkReadBytes     = 16 * 1024
	maxNetworkReadBytes         = 64 * 1024
	maxNetworkQueryBytes        = 512
	maxNetworkJSONPathBytes     = 512
	maxNetworkJSONPathHints     = 8
	maxNetworkRecentPathHints   = 3
	maxNetworkJSONPathHintValue = 96
	maxNetworkJSONPathScanNodes = 240
	defaultNetworkMaxResults    = 8
	maxNetworkMaxResults        = 20
)

type NetworkEvidenceEntry struct {
	Ref         string `json:"ref"`
	URL         string `json:"url"`
	PageURL     string `json:"page_url,omitempty"`
	StatusCode  int    `json:"status_code"`
	Resource    string `json:"resource"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"-"`
}

type NetworkEvidenceLog struct {
	mu           sync.Mutex
	next         int
	pageHost     string
	pageURL      string
	entries      []NetworkEvidenceEntry
	pendingReads int
	lastActivity time.Time
}

type scoredNetworkEvidenceEntry struct {
	entry NetworkEvidenceEntry
	score int
	index int
}

func NewNetworkEvidenceLog() *NetworkEvidenceLog {
	return &NetworkEvidenceLog{}
}

func (l *NetworkEvidenceLog) BeginRead() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.pendingReads++
	l.lastActivity = time.Now()
	l.mu.Unlock()
}

func (l *NetworkEvidenceLog) EndRead() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.pendingReads > 0 {
		l.pendingReads--
	}
	l.lastActivity = time.Now()
	l.mu.Unlock()
}

func (l *NetworkEvidenceLog) WaitIdle(ctx context.Context, maxWait, quietFor time.Duration) bool {
	if l == nil || maxWait <= 0 {
		return true
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if l.isIdle(quietFor) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (l *NetworkEvidenceLog) isIdle(quietFor time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pendingReads > 0 {
		return false
	}
	if quietFor <= 0 || l.lastActivity.IsZero() {
		return true
	}
	return time.Since(l.lastActivity) >= quietFor
}

func (l *NetworkEvidenceLog) ObserveResponse(rawURL string, resource proto.NetworkResourceType) {
	if l == nil || strings.ToLower(string(resource)) != "document" {
		return
	}
	host := normalizedURLHost(rawURL)
	if host == "" {
		return
	}
	l.mu.Lock()
	l.pageHost = host
	l.pageURL = strings.TrimSpace(rawURL)
	l.lastActivity = time.Now()
	l.mu.Unlock()
}

func (l *NetworkEvidenceLog) CurrentPageURL() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pageURL
}

func (l *NetworkEvidenceLog) Add(rawURL string, status int, resource proto.NetworkResourceType, headers http.Header, body []byte) (NetworkEvidenceEntry, bool) {
	if l == nil {
		return NetworkEvidenceEntry{}, false
	}
	contentType := headers.Get("Content-Type")
	if !networkEvidenceCandidate(rawURL, status, resource, contentType, body) {
		return NetworkEvidenceEntry{}, false
	}
	host := normalizedURLHost(rawURL)
	if host == "" {
		return NetworkEvidenceEntry{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pageHost != "" && !sameSiteOrSubdomain(host, l.pageHost) {
		return NetworkEvidenceEntry{}, false
	}
	if len(body) > maxNetworkEvidenceBodyBytes {
		body = body[:maxNetworkEvidenceBodyBytes]
	}
	l.next++
	l.lastActivity = time.Now()
	entry := NetworkEvidenceEntry{
		Ref:         fmt.Sprintf("n%d", l.next),
		URL:         rawURL,
		PageURL:     l.pageURL,
		StatusCode:  status,
		Resource:    strings.ToLower(string(resource)),
		ContentType: compactContentType(contentType),
		Body:        append([]byte(nil), body...),
	}
	l.entries = append(l.entries, entry)
	if len(l.entries) > maxNetworkEvidenceEntries {
		copy(l.entries, l.entries[len(l.entries)-maxNetworkEvidenceEntries:])
		l.entries = l.entries[:maxNetworkEvidenceEntries]
	}
	return entry, true
}

func (l *NetworkEvidenceLog) Search(query string, maxResults int) []NetworkEvidenceEntry {
	if l == nil {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if maxResults <= 0 {
		maxResults = defaultNetworkMaxResults
	}
	if maxResults > maxNetworkMaxResults {
		maxResults = maxNetworkMaxResults
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if query == "" {
		var out []NetworkEvidenceEntry
		for i := len(l.entries) - 1; i >= 0 && len(out) < maxResults; i-- {
			entry := l.entries[i]
			if !networkEntryMatchesPageHost(entry, l.pageHost) {
				continue
			}
			out = append(out, cloneNetworkEntry(entry))
		}
		return out
	}
	terms := networkQueryTerms(query)
	var scored []scoredNetworkEvidenceEntry
	for i := len(l.entries) - 1; i >= 0; i-- {
		entry := l.entries[i]
		if !networkEntryMatchesPageHost(entry, l.pageHost) {
			continue
		}
		score := networkEntryScore(entry, query, terms)
		if score > 0 {
			scored = append(scored, scoredNetworkEvidenceEntry{
				entry: cloneNetworkEntry(entry),
				score: score,
				index: i,
			})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].index > scored[j].index
	})
	if len(scored) > maxResults {
		scored = scored[:maxResults]
	}
	out := make([]NetworkEvidenceEntry, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.entry)
	}
	return out
}

func (l *NetworkEvidenceLog) Recent(maxResults int) []NetworkEvidenceEntry {
	if l == nil {
		return nil
	}
	if maxResults <= 0 {
		maxResults = defaultNetworkMaxResults
	}
	if maxResults > maxNetworkMaxResults {
		maxResults = maxNetworkMaxResults
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []NetworkEvidenceEntry
	for i := len(l.entries) - 1; i >= 0 && len(out) < maxResults; i-- {
		entry := l.entries[i]
		if !networkEntryMatchesPageHost(entry, l.pageHost) {
			continue
		}
		out = append(out, cloneNetworkEntry(entry))
	}
	return out
}

func (l *NetworkEvidenceLog) Get(refOrURL string) (NetworkEvidenceEntry, bool) {
	if l == nil {
		return NetworkEvidenceEntry{}, false
	}
	refOrURL = strings.TrimSpace(refOrURL)
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		entry := l.entries[i]
		if !networkEntryMatchesPageHost(entry, l.pageHost) {
			continue
		}
		if entry.Ref == refOrURL || entry.URL == refOrURL {
			return cloneNetworkEntry(entry), true
		}
	}
	return NetworkEvidenceEntry{}, false
}

func networkEntryMatchesPageHost(entry NetworkEvidenceEntry, pageHost string) bool {
	if pageHost == "" {
		return true
	}
	host := normalizedURLHost(entry.URL)
	return host != "" && sameSiteOrSubdomain(host, pageHost)
}

func networkEntryScore(entry NetworkEvidenceEntry, query string, terms []string) int {
	urlText := strings.ToLower(entry.URL)
	metaText := strings.ToLower(entry.ContentType + " " + entry.Resource)
	bodyText := strings.ToLower(string(entry.Body))
	combined := urlText + " " + metaText + " " + bodyText
	score := 0
	if query != "" {
		if strings.Contains(urlText, query) {
			score += 90
		}
		if strings.Contains(bodyText, query) {
			score += 70
		}
		if strings.Contains(metaText, query) {
			score += 20
		}
		normalizedQuery := normalizeNetworkSearchText(query)
		normalizedCombined := normalizeNetworkSearchText(combined)
		if normalizedQuery != "" && strings.Contains(normalizedCombined, normalizedQuery) {
			score += 45
		}
	}
	if len(terms) > 0 {
		matched := 0
		normalizedURL := normalizeNetworkSearchText(urlText)
		normalizedMeta := normalizeNetworkSearchText(metaText)
		normalizedBody := normalizeNetworkSearchText(bodyText)
		for _, term := range terms {
			termScore := 0
			if networkNormalizedTextContainsTerm(normalizedURL, term) {
				termScore += 18
			}
			if networkNormalizedTextContainsTerm(normalizedBody, term) {
				termScore += 12
			}
			if networkNormalizedTextContainsTerm(normalizedMeta, term) {
				termScore += 5
			}
			if termScore > 0 {
				matched++
				score += termScore
			}
		}
		if matched == len(terms) {
			score += 40
		} else if matched > 1 {
			score += 8 * matched
		}
	}
	if len(networkJSONPathHints(entry.Body, query)) > 0 {
		score += 35
	}
	return score
}

func cloneNetworkEntry(entry NetworkEvidenceEntry) NetworkEvidenceEntry {
	entry.Body = append([]byte(nil), entry.Body...)
	return entry
}

func networkEvidenceCandidate(rawURL string, status int, resource proto.NetworkResourceType, contentType string, body []byte) bool {
	if status < 200 || status >= 400 {
		return false
	}
	switch strings.ToLower(string(resource)) {
	case "fetch", "xhr":
	default:
		return false
	}
	if isChallengePathURL(rawURL) {
		return false
	}
	if normalizedURLHost(rawURL) == "" {
		return false
	}
	if len(body) == 0 {
		return false
	}
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "json") ||
		strings.Contains(ct, "text/") ||
		strings.Contains(ct, "csv") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "javascript") {
		return true
	}
	return looksLikeText(body)
}

func normalizedURLHost(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func sameSiteOrSubdomain(host, pageHost string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	pageHost = strings.TrimPrefix(strings.ToLower(pageHost), "www.")
	if host == pageHost || strings.HasSuffix(host, "."+pageHost) || strings.HasSuffix(pageHost, "."+host) {
		return true
	}
	site, siteOK := registrableDomain(host)
	pageSite, pageSiteOK := registrableDomain(pageHost)
	return siteOK && pageSiteOK && site == pageSite
}

func registrableDomain(host string) (string, bool) {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || net.ParseIP(host) != nil {
		return "", false
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil || site == "" {
		return "", false
	}
	return site, true
}

func compactContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

func looksLikeText(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	sample := body
	if len(sample) > 1024 {
		sample = sample[:1024]
	}
	for _, b := range sample {
		if b == 0 {
			return false
		}
		if b < 0x09 {
			return false
		}
	}
	return true
}

func NetworkSearchTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "properties": {
            "query": {
                "type": "string",
                "maxLength": %d,
                "description": "Case-insensitive text to search in captured same-site XHR/fetch URLs and response bodies. Use labels, entity names, subnet ids, metric names, or API path fragments."
            },
            "max_results": {
                "type": "integer",
                "minimum": 1,
                "maximum": %d,
                "default": %d,
                "description": "Maximum matching network responses to return."
            }
        }
    }`, maxNetworkQueryBytes, maxNetworkMaxResults, defaultNetworkMaxResults))
	return &agent.Tool{
		Name:        "browser_network",
		Description: "Search captured same-site browser XHR/fetch JSON or text responses from the current session, including sibling API subdomains under the same registrable domain. Use after browser_navigate/browser_snapshot reports partial dynamic content, empty metric widgets, or labels without values. Returns compact refs; read a selected response with browser_network_read.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_network with only documented fields: query and max_results"); err != nil {
				return "", err
			}
			query := strings.TrimSpace(args.Query)
			if len(query) > maxNetworkQueryBytes {
				return "", browserInvalidArgs(fmt.Sprintf("query is %d bytes; browser_network supports queries up to %d bytes", len(query), maxNetworkQueryBytes), "retry with a shorter metric label, entity id, API path, or distinctive value")
			}
			pageURL := ""
			if s != nil && s.network != nil {
				pageURL = s.network.CurrentPageURL()
			}
			var entries []NetworkEvidenceEntry
			var recent []NetworkEvidenceEntry
			if s != nil && s.network != nil {
				entries = s.network.Search(query, args.MaxResults)
				if len(entries) == 0 && query != "" {
					recent = s.network.Recent(args.MaxResults)
				}
			}
			return formatNetworkSearchResults(query, pageURL, entries, recent), nil
		},
	}
}

func NetworkReadTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["ref"],
        "properties": {
            "ref": {
                "type": "string",
                "minLength": 1,
                "maxLength": 4096,
                "description": "Network response ref from browser_network, such as n3, or the exact captured response URL."
            },
            "json_path": {
                "type": "string",
                "maxLength": %d,
                "description": "Optional JSON subtree path to return, using a bounded subset such as $.data.items[0].price, data.items[0], or [0].metrics. Use this to avoid dumping large API responses."
            },
            "offset": {
                "type": "integer",
                "minimum": 0,
                "maximum": %d,
                "default": 0,
                "description": "Byte offset into the selected response body. Use the next_offset from a truncated read to continue inspecting a large response."
            },
            "max_bytes": {
                "type": "integer",
                "minimum": 1,
                "maximum": %d,
                "default": %d,
                "description": "Maximum response body bytes to return."
            }
        }
    }`, maxNetworkJSONPathBytes, maxNetworkEvidenceBodyBytes, maxNetworkReadBytes, defaultNetworkReadBytes))
	return &agent.Tool{
		Name:        "browser_network_read",
		Description: "Read a captured same-site browser XHR/fetch response by ref or exact URL. Returns bounded JSON/text evidence with the source URL; pass json_path to extract one JSON subtree from large responses, or offset to continue a truncated read. Use this instead of guessing values from rendered labels when a dynamic dashboard hides metric text.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Ref      string `json:"ref"`
				JSONPath string `json:"json_path"`
				Offset   int    `json:"offset"`
				MaxBytes int    `json:"max_bytes"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_network_read with only documented fields: ref, json_path, offset, and max_bytes"); err != nil {
				return "", err
			}
			ref := strings.TrimSpace(args.Ref)
			if ref == "" {
				return "", browserInvalidArgs("ref is required", "retry with a ref returned by browser_network, for example n1")
			}
			jsonPath := strings.TrimSpace(args.JSONPath)
			if len(jsonPath) > maxNetworkJSONPathBytes {
				return "", browserInvalidArgs(fmt.Sprintf("json_path is %d bytes; browser_network_read supports paths up to %d bytes", len(jsonPath), maxNetworkJSONPathBytes), "retry with a shorter path such as data.items[0].price")
			}
			offset := args.Offset
			if offset < 0 {
				return "", browserInvalidArgs("offset must be non-negative", "retry with offset 0 or the next_offset from a previous browser_network_read result")
			}
			maxBytes := args.MaxBytes
			if maxBytes <= 0 {
				maxBytes = defaultNetworkReadBytes
			}
			if maxBytes > maxNetworkReadBytes {
				return "", browserInvalidArgs(fmt.Sprintf("max_bytes %d exceeds browser_network_read cap %d", maxBytes, maxNetworkReadBytes), "retry with a smaller max_bytes value")
			}
			if s == nil || s.network == nil {
				return "", fmt.Errorf("network response %q was not found because this browser session has no captured network evidence log\nFailure: kind=not_found\nNext: call browser_navigate on the target page, wait for the dashboard to load, then call browser_network with a distinctive query before browser_network_read", ref)
			}
			entry, ok := s.network.Get(ref)
			if !ok {
				return "", fmt.Errorf("network response %q was not found in the current browser session\nFailure: kind=not_found\nNext: call browser_network with a distinctive query from the current page or navigate/wait until the dashboard has loaded its XHR/fetch responses", ref)
			}
			return formatNetworkReadResult(entry, offset, maxBytes, jsonPath)
		},
	}
}

func formatNetworkSearchResults(query, pageURL string, entries, recent []NetworkEvidenceEntry) string {
	var b strings.Builder
	b.WriteString("BROWSER NETWORK EVIDENCE\n")
	if strings.TrimSpace(pageURL) != "" {
		fmt.Fprintf(&b, "CURRENT_PAGE: %s\n", pageURL)
	} else {
		b.WriteString("CURRENT_PAGE: none\n")
	}
	if query != "" {
		fmt.Fprintf(&b, "query: %q\n", query)
	}
	b.WriteString("EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n")
	if len(entries) == 0 {
		b.WriteString("MATCHES: none\n")
		if strings.TrimSpace(pageURL) == "" && len(recent) == 0 {
			b.WriteString("CAPTURED_RESPONSES: none\n")
			b.WriteString("Failure: kind=no_matches\n")
			b.WriteString("Next: call browser_navigate with the target http:// or https:// URL, wait or snapshot after the page loads, then call browser_network with a distinctive metric label, entity id, or API path before browser_network_read.\n")
			return b.String()
		}
		if len(recent) > 0 {
			b.WriteString("RECENT_CAPTURED_RESPONSES:\n")
			for _, entry := range recent {
				fmt.Fprintf(&b, "- %s status=%d resource=%s content_type=%s url=%s\n", entry.Ref, entry.StatusCode, entry.Resource, entry.ContentType, entry.URL)
				if hints := networkRecentJSONPathHints(entry.Body); len(hints) > 0 {
					fmt.Fprintf(&b, "  json_paths: %s\n", strings.Join(hints, "; "))
				}
			}
		}
		b.WriteString("Failure: kind=no_matches\n")
		b.WriteString("Next: if recent captured responses look relevant, call browser_network_read with one ref before citing values; otherwise wait for the page to load dynamic data, try a shorter label/entity/API-path query, interact with the relevant tab, or mark hidden fields unverified.\n")
		return b.String()
	}
	b.WriteString("MATCHES:\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "- %s status=%d resource=%s content_type=%s url=%s\n", entry.Ref, entry.StatusCode, entry.Resource, entry.ContentType, entry.URL)
		preview := textutil.Preview(textutil.CompactWhitespace(string(entry.Body)), maxNetworkPreviewBytes)
		if preview != "" {
			fmt.Fprintf(&b, "  preview: %s\n", preview)
		}
		if hints := networkJSONPathHints(entry.Body, query); len(hints) > 0 {
			fmt.Fprintf(&b, "  json_paths: %s\n", strings.Join(hints, "; "))
		}
	}
	b.WriteString("Next: call browser_network_read with the most relevant ref and json_path before citing values.\n")
	return b.String()
}

func networkRecentJSONPathHints(body []byte) []string {
	hints := networkJSONPathHints(body, "")
	if len(hints) > maxNetworkRecentPathHints {
		hints = hints[:maxNetworkRecentPathHints]
	}
	return hints
}

func formatNetworkReadResult(entry NetworkEvidenceEntry, offset, maxBytes int, jsonPath string) (string, error) {
	if offset < 0 {
		offset = 0
	}
	if maxBytes <= 0 {
		maxBytes = defaultNetworkReadBytes
	}
	body := entry.Body
	if jsonPath != "" {
		selected, err := selectNetworkJSONPath(entry.Body, jsonPath)
		if err != nil {
			return "", err
		}
		body = selected
	}
	bodyBytes := len(body)
	if offset > bodyBytes {
		offset = bodyBytes
	}
	end := offset + maxBytes
	if end > bodyBytes {
		end = bodyBytes
	}
	chunk := body[offset:end]
	omittedBefore := offset
	omittedAfter := bodyBytes - end
	var b strings.Builder
	b.WriteString(sourceaccess.FormatSourceAccessLine(
		"browser_network_url",
		entry.URL,
		entry.PageURL,
		fmt.Sprintf("ref=%s", entry.Ref),
		fmt.Sprintf("status=%d", entry.StatusCode),
		fmt.Sprintf("content_type=%s", entry.ContentType),
		"source_method=network_xhr_fetch",
	))
	if jsonPath != "" {
		fmt.Fprintf(&b, "JSON_PATH: %s\n", jsonPath)
	}
	fmt.Fprintf(&b, "BODY_BYTES: %d", bodyBytes)
	if omittedBefore > 0 || omittedAfter > 0 {
		fmt.Fprintf(&b, " (offset %d, showing %d", offset, len(chunk))
		if omittedBefore > 0 {
			fmt.Fprintf(&b, ", omitted_before %d", omittedBefore)
		}
		if omittedAfter > 0 {
			fmt.Fprintf(&b, ", omitted_after %d, next_offset %d", omittedAfter, end)
		}
		b.WriteString(")")
	}
	b.WriteString("\n")
	b.Write(chunk)
	if omittedAfter > 0 {
		fmt.Fprintf(&b, "\n[... %d bytes omitted after this chunk; retry with offset=%d, a narrower json_path, or max_bytes up to %d ...]\n", omittedAfter, end, maxNetworkReadBytes)
	}
	return b.String(), nil
}

type networkJSONPathStep struct {
	key   *string
	index *int
	raw   string
}

func selectNetworkJSONPath(body []byte, jsonPath string) ([]byte, error) {
	steps, err := parseNetworkJSONPath(jsonPath)
	if err != nil {
		return nil, browserInvalidArgsWrap(err, "retry with a supported JSON path such as $.data.items[0].price, data.items[0], or [0].metrics")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var current any
	if err := dec.Decode(&current); err != nil {
		return nil, fmt.Errorf("network response is not valid JSON for json_path %q: %w\nFailure: kind=invalid_args\nNext: retry browser_network_read without json_path, or select a JSON response returned by browser_network", jsonPath, err)
	}
	for _, step := range steps {
		if step.key != nil {
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, networkJSONPathNotFound(jsonPath, step.raw, body)
			}
			next, ok := obj[*step.key]
			if !ok {
				return nil, networkJSONPathNotFound(jsonPath, step.raw, body)
			}
			current = next
			continue
		}
		if step.index != nil {
			arr, ok := current.([]any)
			if !ok || *step.index < 0 || *step.index >= len(arr) {
				return nil, networkJSONPathNotFound(jsonPath, step.raw, body)
			}
			current = arr[*step.index]
		}
	}
	selected, err := json.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("encode json_path %q result: %w\nFailure: kind=invalid_args\nNext: retry browser_network_read without json_path", jsonPath, err)
	}
	return selected, nil
}

func networkJSONPathNotFound(jsonPath, step string, body []byte) error {
	hints := networkJSONPathHints(body, jsonPath)
	if len(hints) == 0 {
		hints = networkJSONPathHints(body, "")
	}
	hintLine := ""
	if len(hints) > 0 {
		hintLine = "\nCandidate JSON paths: " + strings.Join(hints, "; ")
	}
	return fmt.Errorf("json_path %q was not found at %q in the captured network response%s\nFailure: kind=not_found\nNext: retry browser_network_read with one candidate JSON path above, call browser_network with a distinctive key/value query, or read without json_path", jsonPath, step, hintLine)
}

func networkJSONPathHints(body []byte, query string) []string {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	queryTerms := networkQueryTerms(query)
	seen := map[string]bool{}
	visited := 0
	var hints []string
	collectNetworkJSONPathHints(root, "$", query, queryTerms, seen, &visited, &hints)
	return hints
}

func collectNetworkJSONPathHints(v any, path string, query string, queryTerms []string, seen map[string]bool, visited *int, hints *[]string) {
	if len(*hints) >= maxNetworkJSONPathHints || *visited >= maxNetworkJSONPathScanNodes {
		return
	}
	*visited++
	switch value := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextPath, ok := joinNetworkJSONPath(path, key)
			if !ok {
				continue
			}
			collectNetworkJSONPathHints(value[key], nextPath, query, queryTerms, seen, visited, hints)
			if len(*hints) >= maxNetworkJSONPathHints || *visited >= maxNetworkJSONPathScanNodes {
				return
			}
		}
	case []any:
		for i, item := range value {
			collectNetworkJSONPathHints(item, path+"["+strconv.Itoa(i)+"]", query, queryTerms, seen, visited, hints)
			if len(*hints) >= maxNetworkJSONPathHints || *visited >= maxNetworkJSONPathScanNodes {
				return
			}
		}
	default:
		hint, ok := formatNetworkJSONPathHint(path, value, query, queryTerms)
		if !ok || seen[path] {
			return
		}
		seen[path] = true
		*hints = append(*hints, hint)
	}
}

func formatNetworkJSONPathHint(path string, value any, query string, queryTerms []string) (string, bool) {
	rendered, ok := renderNetworkJSONHintValue(value)
	if !ok {
		return "", false
	}
	search := path + " " + rendered
	if query != "" && !strings.Contains(strings.ToLower(search), query) && !networkTextMatchesAnyTerm(search, queryTerms) {
		return "", false
	}
	rendered = textutil.Preview(textutil.CompactWhitespace(rendered), maxNetworkJSONPathHintValue)
	if rendered == "" {
		return "", false
	}
	return path + "=" + rendered, true
}

func renderNetworkJSONHintValue(value any) (string, bool) {
	switch value.(type) {
	case nil, string, bool, json.Number, float64, int, int64, uint64:
	default:
		return "", false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func networkQueryTerms(query string) []string {
	normalized := normalizeNetworkSearchText(query)
	if normalized == "" {
		return nil
	}
	seen := map[string]bool{}
	var terms []string
	for _, term := range strings.Fields(normalized) {
		if len(term) < 2 || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func networkTextMatchesAnyTerm(text string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	normalized := normalizeNetworkSearchText(text)
	if normalized == "" {
		return false
	}
	fields := map[string]bool{}
	for _, field := range strings.Fields(normalized) {
		fields[field] = true
	}
	for _, term := range terms {
		if fields[term] {
			return true
		}
	}
	return false
}

func networkNormalizedTextContainsTerm(normalized, term string) bool {
	if normalized == "" || term == "" {
		return false
	}
	for _, field := range strings.Fields(normalized) {
		if field == term {
			return true
		}
	}
	return false
}

func normalizeNetworkSearchText(text string) string {
	var out []string
	seen := map[string]bool{}
	emit := func(token string) {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || seen[token] {
			return
		}
		seen[token] = true
		out = append(out, token)
	}
	for _, token := range networkAlnumTokens(text) {
		emit(token)
		parts := splitNetworkIdentifierToken(token)
		for i, part := range parts {
			emit(part)
			if isNetworkNumericToken(part) && i+1 < len(parts) && len([]rune(parts[i+1])) == 1 && isNetworkAlphaToken(parts[i+1]) {
				emit(part + parts[i+1])
			}
		}
	}
	return strings.Join(out, " ")
}

func networkAlnumTokens(text string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range text {
		if isNetworkAlphaNumRune(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func splitNetworkIdentifierToken(token string) []string {
	runes := []rune(token)
	if len(runes) == 0 {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		cur := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if networkIdentifierBoundary(prev, cur, next) {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	if len(parts) == 1 && strings.EqualFold(parts[0], token) {
		return nil
	}
	return parts
}

func networkIdentifierBoundary(prev, cur, next rune) bool {
	if isNetworkLowerAlpha(prev) && isNetworkUpperAlpha(cur) {
		return true
	}
	if isNetworkDigit(prev) != isNetworkDigit(cur) {
		return true
	}
	return isNetworkUpperAlpha(prev) && isNetworkUpperAlpha(cur) && next != 0 && isNetworkLowerAlpha(next)
}

func isNetworkAlphaNumRune(r rune) bool {
	return isNetworkAlphaRune(r) || isNetworkDigit(r)
}

func isNetworkAlphaRune(r rune) bool {
	return isNetworkLowerAlpha(r) || isNetworkUpperAlpha(r)
}

func isNetworkLowerAlpha(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isNetworkUpperAlpha(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isNetworkDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isNetworkNumericToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !isNetworkDigit(r) {
			return false
		}
	}
	return true
}

func isNetworkAlphaToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !isNetworkAlphaRune(r) {
			return false
		}
	}
	return true
}

func joinNetworkJSONPath(base, key string) (string, bool) {
	if networkJSONSimpleKey(key) {
		return base + "." + key, true
	}
	if !strings.ContainsAny(key, `'\`) {
		return base + "['" + key + "']", true
	}
	if !strings.ContainsAny(key, `"\\`) {
		return base + `["` + key + `"]`, true
	}
	return "", false
}

func networkJSONSimpleKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func parseNetworkJSONPath(raw string) ([]networkJSONPathStep, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return nil, fmt.Errorf("json_path is blank")
	}
	if p == "$" {
		return nil, nil
	}
	if strings.HasPrefix(p, "$.") {
		p = p[2:]
	} else if strings.HasPrefix(p, "$[") {
		p = p[1:]
	} else if strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("json_path %q has unsupported syntax after $", raw)
	}
	var steps []networkJSONPathStep
	for i := 0; i < len(p); {
		switch p[i] {
		case '.':
			i++
			field, next, err := parseNetworkJSONField(p, i)
			if err != nil {
				return nil, err
			}
			steps = append(steps, networkJSONPathKey(field))
			i = next
		case '[':
			step, next, err := parseNetworkJSONBracket(p, i)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
			i = next
		default:
			field, next, err := parseNetworkJSONField(p, i)
			if err != nil {
				return nil, err
			}
			steps = append(steps, networkJSONPathKey(field))
			i = next
		}
	}
	return steps, nil
}

func parseNetworkJSONField(p string, start int) (string, int, error) {
	if start >= len(p) {
		return "", start, fmt.Errorf("json_path field is missing")
	}
	end := start
	for end < len(p) && p[end] != '.' && p[end] != '[' {
		end++
	}
	field := strings.TrimSpace(p[start:end])
	if field == "" {
		return "", start, fmt.Errorf("json_path field is missing")
	}
	if strings.ContainsAny(field, `]"'`) {
		return "", start, fmt.Errorf("json_path field %q uses unsupported characters; use bracket-quoted keys for complex names", field)
	}
	return field, end, nil
}

func parseNetworkJSONBracket(p string, start int) (networkJSONPathStep, int, error) {
	end := strings.IndexByte(p[start:], ']')
	if end < 0 {
		return networkJSONPathStep{}, start, fmt.Errorf("json_path bracket is missing closing ]")
	}
	end += start
	body := strings.TrimSpace(p[start+1 : end])
	if body == "" {
		return networkJSONPathStep{}, start, fmt.Errorf("json_path bracket is empty")
	}
	if body[0] == '"' || body[0] == '\'' {
		key, err := parseNetworkJSONQuotedKey(body)
		if err != nil {
			return networkJSONPathStep{}, start, err
		}
		step := networkJSONPathKey(key)
		step.raw = p[start : end+1]
		return step, end + 1, nil
	}
	index, err := strconv.Atoi(body)
	if err != nil || index < 0 {
		return networkJSONPathStep{}, start, fmt.Errorf("json_path bracket %q must be a non-negative array index or quoted key", body)
	}
	step := networkJSONPathIndex(index)
	step.raw = p[start : end+1]
	return step, end + 1, nil
}

func parseNetworkJSONQuotedKey(body string) (string, error) {
	if len(body) < 2 || body[0] != body[len(body)-1] {
		return "", fmt.Errorf("json_path quoted key %q is missing a matching quote", body)
	}
	if body[0] != '"' && body[0] != '\'' {
		return "", fmt.Errorf("json_path quoted key must use single or double quotes")
	}
	key := body[1 : len(body)-1]
	if strings.ContainsAny(key, `\`) {
		return "", fmt.Errorf("json_path quoted key %q uses unsupported escapes", body)
	}
	if key == "" {
		return "", fmt.Errorf("json_path quoted key is blank")
	}
	return key, nil
}

func networkJSONPathKey(key string) networkJSONPathStep {
	return networkJSONPathStep{key: &key, raw: key}
}

func networkJSONPathIndex(index int) networkJSONPathStep {
	return networkJSONPathStep{index: &index, raw: "[" + strconv.Itoa(index) + "]"}
}

func sortedNetworkRefs(entries []NetworkEvidenceEntry) []string {
	refs := make([]string, 0, len(entries))
	for _, entry := range entries {
		refs = append(refs, entry.Ref)
	}
	sort.Strings(refs)
	return refs
}
