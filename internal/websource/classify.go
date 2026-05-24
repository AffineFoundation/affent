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
		"x.com",
		"twitter.com",
		"facebook.com",
		"instagram.com",
		"linkedin.com",
		"tiktok.com",
		"threads.net",
	})
}

func hasHostSuffix(host string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
