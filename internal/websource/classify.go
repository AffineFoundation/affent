package websource

import "strings"

// NormalizeHost canonicalizes a URL hostname for source-classification rules.
func NormalizeHost(host string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
}

// IsSearchResultPage reports whether host/path is a search-engine results page
// rather than a source page. Path should be URL-escaped or already lower-case;
// the predicate only needs stable ASCII prefixes.
func IsSearchResultPage(host, path string) bool {
	host = NormalizeHost(host)
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "/search" || strings.HasPrefix(path, "/search/") {
		return true
	}
	switch host {
	case "google.com", "bing.com", "duckduckgo.com", "search.brave.com", "search.yahoo.com", "yahoo.com", "baidu.com", "yandex.com":
		return path == "" || path == "/" || strings.HasPrefix(path, "/search") || strings.HasPrefix(path, "/html") || strings.HasPrefix(path, "/s")
	default:
		return false
	}
}

// IsRedirectorHost reports common redirect/short-link wrappers. These are not
// necessarily blocked, but direct readers should prefer the final canonical URL.
func IsRedirectorHost(host string) bool {
	switch NormalizeHost(host) {
	case "t.co", "bit.ly", "tinyurl.com", "goo.gl", "lnkd.in", "l.facebook.com", "out.reddit.com":
		return true
	default:
		return false
	}
}

// IsSocialOrDiscussionHost reports sources that are useful for community
// sentiment but often weak or inaccessible as direct page evidence.
func IsSocialOrDiscussionHost(host string) bool {
	return hasHostSuffix(NormalizeHost(host), []string{
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
	})
}

// IsKnownDirectReaderTrapHost reports hosts that routinely reject plain HTTP
// readers. These are stronger than social/discussion hints: web_fetch can skip
// them before network dispatch, and loop guard can stop same-host retries
// after one structured failure.
func IsKnownDirectReaderTrapHost(host string) bool {
	return hasHostSuffix(NormalizeHost(host), []string{
		"coingecko.com",
		"coinmarketcap.com",
		"geckoterminal.com",
		"x.com",
		"twitter.com",
		"facebook.com",
		"instagram.com",
		"linkedin.com",
		"tiktok.com",
		"threads.net",
	})
}

// IsLikelyCollectionPage reports broad index/listing routes that are often
// weak direct-reader targets for research tasks. A specific detail page,
// API/text/export endpoint, docs page, or source repository is usually a
// better follow-up URL.
func IsLikelyCollectionPage(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	path = strings.TrimSuffix(path, "/")
	switch path {
	case "/coins", "/en/coins", "/markets", "/tokens", "/projects", "/subnets", "/validators":
		return true
	default:
		return false
	}
}

// IsLikelyTextOrAPIPath reports whether a URL path names an endpoint that is
// plausibly useful after ordinary dashboard/page routes on the same host
// returned only a dynamic shell. It is intentionally path-only: callers still
// need normal URL/security checks before fetching.
func IsLikelyTextOrAPIPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" || path == "/" {
		return false
	}
	for _, prefix := range []string{
		"/api/",
		"/apis/",
		"/graphql",
		"/rpc/",
		"/v1/",
		"/v2/",
		"/v3/",
		"/data/",
		"/datasets/",
		"/download/",
		"/downloads/",
		"/export/",
		"/exports/",
		"/raw/",
		"/static/",
		"/.well-known/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	for _, suffix := range []string{
		".json",
		".jsonl",
		".ndjson",
		".csv",
		".tsv",
		".txt",
		".md",
		".xml",
		".rss",
		".atom",
		".yaml",
		".yml",
	} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func hasHostSuffix(host string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
