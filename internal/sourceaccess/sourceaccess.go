package sourceaccess

import (
	"fmt"
	"strings"
)

// Info captures the normalized fields we care about from SourceAccess lines.
type Info struct {
	URLField                    string
	AccessedURL                 string
	RequestedURL                string
	PageTextBelow               string
	RenderedBrowserSourceStatus string
	SourceMethod                string
	JSONPath                    string
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
			info.URLField = "fetched_url"
			info.AccessedURL = strings.TrimSpace(strings.TrimPrefix(field, "fetched_url="))
		case strings.HasPrefix(field, "browser_rendered_url="):
			info.URLField = "browser_rendered_url"
			info.AccessedURL = strings.TrimSpace(strings.TrimPrefix(field, "browser_rendered_url="))
		case strings.HasPrefix(field, "browser_network_url="):
			info.URLField = "browser_network_url"
			info.AccessedURL = strings.TrimSpace(strings.TrimPrefix(field, "browser_network_url="))
		case strings.HasPrefix(field, "requested_url="):
			info.RequestedURL = strings.TrimSpace(strings.TrimPrefix(field, "requested_url="))
		case strings.HasPrefix(field, "page_text_below="):
			info.PageTextBelow = strings.TrimSpace(strings.TrimPrefix(field, "page_text_below="))
		case strings.HasPrefix(field, "rendered_browser_source_status="):
			info.RenderedBrowserSourceStatus = strings.TrimSpace(strings.TrimPrefix(field, "rendered_browser_source_status="))
		case strings.HasPrefix(field, "source_method="):
			info.SourceMethod = strings.TrimSpace(strings.TrimPrefix(field, "source_method="))
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

// IsNetworkSource reports whether the source line came from captured browser
// XHR/fetch evidence rather than rendered page text or a direct fetch.
func (i Info) IsNetworkSource() bool {
	return i.URLField == "browser_network_url" || i.SourceMethod == "network_xhr_fetch"
}

// IsDynamicPartial reports that rendered browser text was available but the
// source itself declared hidden/dynamic fields that still need stronger
// evidence before use as dashboard facts.
func (i Info) IsDynamicPartial() bool {
	switch i.PageTextBelow {
	case "partial_dynamic_page_evidence":
		return true
	}
	switch i.RenderedBrowserSourceStatus {
	case "partial_dynamic_page_evidence":
		return true
	}
	return false
}

// HasDynamicPartialEvidence reports that a rendered/browser source was reached
// but its visible DOM also warned about dynamic metric widgets or equivalent
// partial evidence. Callers should treat this as "readable page evidence with
// hidden fields", not as a complete factual source for dashboard metrics.
func HasDynamicPartialEvidence(result string) bool {
	if info, ok := FirstInfoFromResult(result); ok && info.IsDynamicPartial() {
		return true
	}
	return strings.Contains(result, "empty_dynamic_metric_widgets:")
}

// FirstInfoFromResult returns the first SourceAccess record visible in a full
// tool result. The boolean reports whether a SourceAccess line was found.
func FirstInfoFromResult(result string) (Info, bool) {
	info := Info{}
	found := false
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SourceAccess:") && !found {
			info = ParseLine(line)
			found = true
			continue
		}
		if found && strings.HasPrefix(line, "JSON_PATH:") {
			info.JSONPath = strings.TrimSpace(strings.TrimPrefix(line, "JSON_PATH:"))
			break
		}
	}
	return info, found
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
// Callers supply the canonical URL field name (fetched_url,
// browser_rendered_url, or browser_network_url) and any additional
// semicolon-delimited fields.
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
