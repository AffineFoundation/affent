package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/go-rod/rod/lib/proto"
)

const (
	maxNetworkEvidenceEntries   = 200
	maxNetworkEvidenceBodyBytes = 512 * 1024
	maxNetworkPreviewBytes      = 1200
	defaultNetworkReadBytes     = 16 * 1024
	maxNetworkReadBytes         = 64 * 1024
	maxNetworkQueryBytes        = 512
	defaultNetworkMaxResults    = 8
	maxNetworkMaxResults        = 20
)

type NetworkEvidenceEntry struct {
	Ref         string `json:"ref"`
	URL         string `json:"url"`
	StatusCode  int    `json:"status_code"`
	Resource    string `json:"resource"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"-"`
}

type NetworkEvidenceLog struct {
	mu       sync.Mutex
	next     int
	pageHost string
	entries  []NetworkEvidenceEntry
}

func NewNetworkEvidenceLog() *NetworkEvidenceLog {
	return &NetworkEvidenceLog{}
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
	l.mu.Unlock()
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
	entry := NetworkEvidenceEntry{
		Ref:         fmt.Sprintf("n%d", l.next),
		URL:         rawURL,
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
	var out []NetworkEvidenceEntry
	for i := len(l.entries) - 1; i >= 0 && len(out) < maxResults; i-- {
		entry := l.entries[i]
		if query == "" || networkEntryMatches(entry, query) {
			out = append(out, cloneNetworkEntry(entry))
		}
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
		if entry.Ref == refOrURL || entry.URL == refOrURL {
			return cloneNetworkEntry(entry), true
		}
	}
	return NetworkEvidenceEntry{}, false
}

func networkEntryMatches(entry NetworkEvidenceEntry, query string) bool {
	if strings.Contains(strings.ToLower(entry.URL), query) ||
		strings.Contains(strings.ToLower(entry.ContentType), query) ||
		strings.Contains(strings.ToLower(entry.Resource), query) {
		return true
	}
	return strings.Contains(strings.ToLower(string(entry.Body)), query)
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
	if len(body) == 0 || len(body) > maxNetworkEvidenceBodyBytes {
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
	return host == pageHost || strings.HasSuffix(host, "."+pageHost) || strings.HasSuffix(pageHost, "."+host)
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
		Description: "Search captured same-site browser XHR/fetch JSON or text responses from the current session. Use after browser_navigate/browser_snapshot reports partial dynamic content, empty metric widgets, or labels without values. Returns compact refs; read a selected response with browser_network_read.",
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
			entries := s.network.Search(query, args.MaxResults)
			return formatNetworkSearchResults(query, entries), nil
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
            "max_bytes": {
                "type": "integer",
                "minimum": 1,
                "maximum": %d,
                "default": %d,
                "description": "Maximum response body bytes to return."
            }
        }
    }`, maxNetworkReadBytes, defaultNetworkReadBytes))
	return &agent.Tool{
		Name:        "browser_network_read",
		Description: "Read a captured same-site browser XHR/fetch response by ref or exact URL. Returns bounded JSON/text evidence with the source URL. Use this instead of guessing values from rendered labels when a dynamic dashboard hides metric text.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Ref      string `json:"ref"`
				MaxBytes int    `json:"max_bytes"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_network_read with only documented fields: ref and max_bytes"); err != nil {
				return "", err
			}
			ref := strings.TrimSpace(args.Ref)
			if ref == "" {
				return "", browserInvalidArgs("ref is required", "retry with a ref returned by browser_network, for example n1")
			}
			maxBytes := args.MaxBytes
			if maxBytes <= 0 {
				maxBytes = defaultNetworkReadBytes
			}
			if maxBytes > maxNetworkReadBytes {
				return "", browserInvalidArgs(fmt.Sprintf("max_bytes %d exceeds browser_network_read cap %d", maxBytes, maxNetworkReadBytes), "retry with a smaller max_bytes value")
			}
			entry, ok := s.network.Get(ref)
			if !ok {
				return "", fmt.Errorf("network response %q was not found in the current browser session\nFailure: kind=not_found\nNext: call browser_network with a distinctive query from the current page or navigate/wait until the dashboard has loaded its XHR/fetch responses", ref)
			}
			return formatNetworkReadResult(entry, maxBytes), nil
		},
	}
}

func formatNetworkSearchResults(query string, entries []NetworkEvidenceEntry) string {
	var b strings.Builder
	b.WriteString("BROWSER NETWORK EVIDENCE\n")
	if query != "" {
		fmt.Fprintf(&b, "query: %q\n", query)
	}
	if len(entries) == 0 {
		b.WriteString("MATCHES: none\n")
		b.WriteString("Next: wait for the page to load dynamic data, try a shorter label/entity/API-path query, interact with the relevant tab, or mark hidden fields unverified.\n")
		return b.String()
	}
	b.WriteString("MATCHES:\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "- %s status=%d resource=%s content_type=%s url=%s\n", entry.Ref, entry.StatusCode, entry.Resource, entry.ContentType, entry.URL)
		preview := textutil.Preview(textutil.CompactWhitespace(string(entry.Body)), maxNetworkPreviewBytes)
		if preview != "" {
			fmt.Fprintf(&b, "  preview: %s\n", preview)
		}
	}
	b.WriteString("Next: call browser_network_read with the most relevant ref before citing values.\n")
	return b.String()
}

func formatNetworkReadResult(entry NetworkEvidenceEntry, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = defaultNetworkReadBytes
	}
	body := entry.Body
	omitted := 0
	if len(body) > maxBytes {
		omitted = len(body) - maxBytes
		body = body[:maxBytes]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SourceAccess: browser_network_url=%s; ref=%s; status=%d; content_type=%s; source_method=network_xhr_fetch\n", entry.URL, entry.Ref, entry.StatusCode, entry.ContentType)
	fmt.Fprintf(&b, "BODY_BYTES: %d", len(entry.Body))
	if omitted > 0 {
		fmt.Fprintf(&b, " (showing %d, omitted %d)", len(body), omitted)
	}
	b.WriteString("\n")
	b.Write(body)
	if omitted > 0 {
		fmt.Fprintf(&b, "\n[... %d bytes omitted; retry with a narrower query or max_bytes up to %d ...]\n", omitted, maxNetworkReadBytes)
	}
	return b.String()
}

func sortedNetworkRefs(entries []NetworkEvidenceEntry) []string {
	refs := make([]string, 0, len(entries))
	for _, entry := range entries {
		refs = append(refs, entry.Ref)
	}
	sort.Strings(refs)
	return refs
}
