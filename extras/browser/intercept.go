package browser

import (
	"strings"
	"sync/atomic"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// InterceptConfig governs the request interceptor's behavior.
//
// The interceptor is the right place to:
//   - drop resources the agent never sees (images, fonts, media) so
//     pages load faster and weigh less;
//   - silently fail tracker / analytics / ad domain requests so CF +
//     similar WAFs don't get the rich fingerprint they'd otherwise
//     correlate against;
//   - layer in a response cache for benchmark replay.
//
// Defaults: image, font, media blocked; stylesheet allowed (we need
// CSS for visibility checks in snapshot); a starter tracker domain
// list blocked.
type InterceptConfig struct {
	// BlockedResourceTypes is the set of Chromium resource types that
	// get failed before the network request fires. Empty leaves the
	// default block list active; pass `[]string{}` (non-nil empty) to
	// disable all type-based blocking.
	BlockedResourceTypes []string

	// AllowedResourceTypes overrides the default block list when
	// non-empty: anything not on this list is blocked. Wins over
	// BlockedResourceTypes when both are set.
	AllowedResourceTypes []string

	// BlockedDomains is a list of URL substrings (host/path patterns)
	// to fail. Matched against `request.URL` with strings.Contains.
	// Empty leaves the default list active.
	BlockedDomains []string

	// AllowAllDomains disables the default tracker block list, leaving
	// only the BlockedDomains values active. Use when you specifically
	// want to capture all third-party traffic (debugging).
	AllowAllDomains bool

	// Cache, when non-nil, intercepts network responses and serves
	// from disk when a fresh cache entry exists. New responses are
	// recorded automatically. Misses fall through to the network.
	Cache ResponseCache
}

// DefaultBlockedResourceTypes matches affent-browser's "minimize page
// weight for LLM consumption" stance — drop binary assets and fonts
// that don't reach the snapshot or text representation.
var DefaultBlockedResourceTypes = []string{
	"Image", "Media", "Font",
}

// DefaultBlockedDomains is a starter tracker block list. Curated from
// uBlock Origin's most-pinged third-party domains plus signals
// discovered empirically when running the benchmark (e.g. ad-delivery,
// btloader, id5-sync — all observed leaking into the response cache
// during CoinGecko / Open-Meteo navigation). Not exhaustive — operators
// with stricter needs should supply their own list — but covers the
// failure mode where stale per-session third-party state poisons the
// cache and stalls subsequent Chrome navigations on replay.
var DefaultBlockedDomains = []string{
	// Analytics
	"google-analytics.com",
	"googletagmanager.com",
	"google-analytics.l.google.com",
	"analytics.google.com",
	"region1.google-analytics.com",
	"ssl.google-analytics.com",
	"stats.g.doubleclick.net",
	// Ads (display + RTB)
	"doubleclick.net",
	"googlesyndication.com",
	"adservice.google.com",
	"adsystem.com",
	"adnxs.com",
	"adsrvr.org",
	"ad-delivery.net",
	"btloader.com",
	"id5-sync.com",
	"static.criteo.net",
	"creative-serving.com",
	"openx.net",
	"pubmatic.com",
	"rubiconproject.com",
	// Consent / cookie banners (typically inject extra trackers)
	"onetrust.com",
	"cookielaw.org",
	// Social pixels
	"facebook.net",
	"connect.facebook.net",
	"pixel.facebook.com",
	// Session-recording / heatmaps
	"hotjar.com",
	"static.hotjar.com",
	"clarity.ms",
	// Product analytics
	"cdn.heapanalytics.com",
	"segment.io",
	"cdn.segment.com",
	"mixpanel.com",
	"api.mixpanel.com",
	"amplitude.com",
	"api.amplitude.com",
	"intercom.io",
	"widget.intercom.io",
	// Auth / sign-in widgets (serve per-session JS)
	"accounts.google.com/gsi",
	"apis.google.com/js",
	// Error reporting / RUM (still trigger fingerprints)
	"sentry.io",
	"newrelic.com",
	"datadoghq.com",
	"bugsnag.com",
	"rollbar.com",
	"raygun.io",
	"cloudflareinsights.com",
	"static.cloudflareinsights.com",
}

// resolvedConfig returns the effective lists with defaults applied.
func (c InterceptConfig) resolved() InterceptConfig {
	out := c
	if out.BlockedResourceTypes == nil && len(out.AllowedResourceTypes) == 0 {
		out.BlockedResourceTypes = DefaultBlockedResourceTypes
	}
	if !out.AllowAllDomains {
		if out.BlockedDomains == nil {
			out.BlockedDomains = DefaultBlockedDomains
		}
	}
	return out
}

// isReplaySafeHeader reports whether a cached header value can be
// served back to the browser on replay without leaking per-session
// state or breaking content-negotiation invariants. The block list
// is conservative — we drop anything that names a session, signs a
// request, or carries content-length / date semantics that the
// browser computes itself.
func isReplaySafeHeader(name string) bool {
	lower := strings.ToLower(name)
	// Headers that carry session state or one-time tokens.
	switch lower {
	case "set-cookie",
		"cookie",
		"authorization",
		"www-authenticate",
		"proxy-authenticate",
		"x-csrf-token",
		"x-xsrf-token",
		"x-request-id",
		"x-amz-request-id",
		"cf-ray",
		"cf-cache-status",
		"cf-mitigated",
		"server-timing",
		"date",
		"age",
		"expires",
		"last-modified",
		"etag",
		"content-length":
		return false
	}
	// Strip nonce-bearing CSP directives — replaying a stale nonce
	// blocks legitimate inline scripts on the new navigation.
	if lower == "content-security-policy" || lower == "content-security-policy-report-only" {
		return false
	}
	return true
}

// InterceptStats tracks per-session counters. Exposed for tests and
// for callers that want to log throughput / hit-rate metrics.
type InterceptStats struct {
	BlockedByType   atomic.Int64
	BlockedByDomain atomic.Int64
	CacheHit        atomic.Int64
	CacheMiss       atomic.Int64
	NetworkFetch    atomic.Int64
	// CacheWrite counts successful out-of-band cache populations from
	// the observer (separate from CacheMiss, which only records that
	// the intercept stage didn't find a fresh entry). Useful for
	// operators checking whether the new Chromium-native fetch path
	// is actually keeping the cache populated.
	CacheWrite atomic.Int64
}

// installInterceptor wires a rod HijackRouter onto the page that
// implements the configured block / cache policy. Returns the router
// so the session can Stop() it on close.
func installInterceptor(page *rod.Page, cfg InterceptConfig, stats *InterceptStats) (*rod.HijackRouter, error) {
	cfg = cfg.resolved()
	router := page.HijackRequests()
	// Register one catch-all handler. Per-resource-type Add() entries
	// would require pre-enumerating proto.NetworkResourceType values;
	// the wildcard handler is simpler and inspects the type itself.
	allowedSet := map[string]bool{}
	for _, t := range cfg.AllowedResourceTypes {
		allowedSet[strings.ToLower(t)] = true
	}
	blockedSet := map[string]bool{}
	for _, t := range cfg.BlockedResourceTypes {
		blockedSet[strings.ToLower(t)] = true
	}

	err := router.Add("*", proto.NetworkResourceType(""), func(h *rod.Hijack) {
		req := h.Request
		url := req.URL().String()
		// Resource-type filter
		rt := strings.ToLower(string(req.Type()))
		if len(allowedSet) > 0 {
			if !allowedSet[rt] {
				stats.BlockedByType.Add(1)
				h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
				return
			}
		} else if blockedSet[rt] {
			stats.BlockedByType.Add(1)
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		// Domain filter
		for _, d := range cfg.BlockedDomains {
			if d == "" {
				continue
			}
			if strings.Contains(url, d) {
				stats.BlockedByDomain.Add(1)
				h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
				return
			}
		}
		// Cache hit?
		if cfg.Cache != nil {
			if entry, ok, _ := cfg.Cache.Get(req.Req().Context(), url); ok {
				stats.CacheHit.Add(1)
				h.Response.SetHeader("X-Affent-Cache", "HIT")
				h.Response.Payload().ResponseCode = entry.StatusCode
				// Replay headers but strip ones that carry per-
				// session state. Keeping the original Set-Cookie /
				// Authorization / CSP-with-nonce headers leaks state
				// across sessions and has been observed to stall
				// Chrome's navigation pipeline.
				for k, vs := range entry.Headers {
					if isReplaySafeHeader(k) {
						for _, v := range vs {
							h.Response.SetHeader(k, v)
						}
					}
				}
				h.Response.SetBody(entry.Body)
				return
			}
			stats.CacheMiss.Add(1)
		}
		// CACHE MISS / NO CACHE: hand the request back to Chromium so
		// the network fetch happens on Chrome's own TLS stack. The
		// alternative — h.LoadResponse(http.DefaultClient, true) —
		// re-issues the request through Go's net/http, exposing Go's
		// crypto/tls ClientHello (JA3) to the server. Cloudflare and
		// peer fingerprinters identify that fingerprint as
		// non-browser traffic and serve a challenge, even when the
		// JS-side stealth patch and HTTP UA are both correct.
		// Letting Chromium fetch preserves the Chrome TLS fingerprint
		// + the per-session __cf_bm / cf_clearance cookies that real
		// users implicitly carry.
		//
		// Trade-off: with ContinueRequest, the response never lands
		// in our hijack callback, so we cannot Put it into the
		// FileResponseCache from here. Chrome's internal HTTP cache
		// (workspace user-data-dir) still operates within the
		// session. Cross-session population of FileResponseCache will
		// require a future Response-stage interceptor (proto.Fetch
		// requestStage="Response") — out of scope for v1.
		stats.NetworkFetch.Add(1)
		h.ContinueRequest(&proto.FetchContinueRequest{})
	})
	if err != nil {
		return nil, err
	}
	go router.Run()
	return router, nil
}

