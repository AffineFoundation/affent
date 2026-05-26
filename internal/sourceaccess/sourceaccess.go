package sourceaccess

import (
	"fmt"
	"strings"
)

// Info captures the normalized fields we care about from SourceAccess lines.
type Info struct {
	AccessedURL                 string
	RequestedURL                string
	PageTextBelow               string
	RenderedBrowserSourceStatus string
}

// ParseLine extracts the accessed/requested URLs from a SourceAccess line.
// It accepts the full line or a trimmed fragment that still contains the
// SourceAccess prefix.
func ParseLine(line string) Info {
	info := Info{}
	line = strings.TrimSpace(line)
	if line == "" {
		return info
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "SourceAccess:"))
	for _, field := range strings.Split(line, ";") {
		field = strings.TrimSpace(field)
		switch {
		case strings.HasPrefix(field, "fetched_url="):
			info.AccessedURL = strings.TrimSpace(strings.TrimPrefix(field, "fetched_url="))
		case strings.HasPrefix(field, "browser_rendered_url="):
			info.AccessedURL = strings.TrimSpace(strings.TrimPrefix(field, "browser_rendered_url="))
		case strings.HasPrefix(field, "requested_url="):
			info.RequestedURL = strings.TrimSpace(strings.TrimPrefix(field, "requested_url="))
		case strings.HasPrefix(field, "page_text_below="):
			info.PageTextBelow = strings.TrimSpace(strings.TrimPrefix(field, "page_text_below="))
		case strings.HasPrefix(field, "rendered_browser_source_status="):
			info.RenderedBrowserSourceStatus = strings.TrimSpace(strings.TrimPrefix(field, "rendered_browser_source_status="))
		}
	}
	return info
}

// IsDiscoveryOnly reports whether the source line describes a page that is
// useful for navigation discovery but not for factual evidence.
func (i Info) IsDiscoveryOnly() bool {
	switch i.PageTextBelow {
	case "search_results_discovery_only", "not_found_page_discovery_only":
		return true
	}
	switch i.RenderedBrowserSourceStatus {
	case "search_results_discovery_only", "not_found_page_discovery_only":
		return true
	}
	return false
}

// FirstInfoFromResult returns the first SourceAccess record visible in a full
// tool result. The boolean reports whether a SourceAccess line was found.
func FirstInfoFromResult(result string) (Info, bool) {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SourceAccess:") {
			return ParseLine(line), true
		}
	}
	return Info{}, false
}

// AccessedURLFromResult returns the first accessed URL visible in a full tool
// result, preferring SourceAccess lines and falling back to plain URL: lines.
func AccessedURLFromResult(result string) string {
	if info, ok := FirstInfoFromResult(result); ok && info.AccessedURL != "" {
		return info.AccessedURL
	}
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "URL: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "URL: "))
		}
	}
	return ""
}

// FormatSourceAccessLine formats a single SourceAccess header line.
// Callers supply the canonical URL field name (fetched_url or
// browser_rendered_url) and any additional semicolon-delimited fields.
func FormatSourceAccessLine(urlField, accessedURL, requestedURL string, fields ...string) string {
	accessedURL = sanitizeHeaderValue(accessedURL)
	requested := sanitizeHeaderValue(requestedURL)
	requestedSuffix := ""
	if requested != "" && requested != accessedURL {
		requestedSuffix = "; requested_url=" + requested
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SourceAccess: %s=%s%s", urlField, accessedURL, requestedSuffix)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if !strings.HasPrefix(field, ";") {
			b.WriteString("; ")
			b.WriteString(field)
			continue
		}
		b.WriteString(field)
	}
	b.WriteString("\n")
	return b.String()
}

// FormatFetchedHeader formats the standard SourceAccess header used by
// web_fetch results.
func FormatFetchedHeader(finalURL, requestedURL string) string {
	return FormatSourceAccessLine("fetched_url", finalURL, requestedURL, "linked_urls_in_content=discovered_unverified_until_fetched")
}

// FormatBrowserHeader formats the standard SourceAccess header used by
// browser snapshots. The caller provides the body marker to keep the
// browser-specific page classification outside this shared helper.
func FormatBrowserHeader(renderedURL, requestedURL, pageTextBelow, extra string, snapshotID int64) string {
	return FormatSourceAccessLine("browser_rendered_url", renderedURL, requestedURL, pageTextBelow, fmt.Sprintf("; snapshot_id=%d%s", snapshotID, extra))
}

func sanitizeHeaderValue(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(v))
}
