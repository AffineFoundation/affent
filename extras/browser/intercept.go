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

// DefaultBlockedDomains is a starter tracker block list. Drawn from
// uBlock Origin's most-pinged third-party domains; not exhaustive but
// covers ~80% of CF-correlated fingerprints. Operators with stricter
// needs should supply their own list.
var DefaultBlockedDomains = []string{
	// Analytics
	"google-analytics.com",
	"googletagmanager.com",
	"google-analytics.l.google.com",
	"analytics.google.com",
	"region1.google-analytics.com",
	"ssl.google-analytics.com",
	// Ads
	"doubleclick.net",
	"googlesyndication.com",
	"adservice.google.com",
	"adsystem.com",
	"adnxs.com",
	"adsrvr.org",
	// Tag managers / pixels
	"facebook.net",
	"connect.facebook.net",
	"pixel.facebook.com",
	"hotjar.com",
	"static.hotjar.com",
	"cdn.heapanalytics.com",
	"segment.io",
	"cdn.segment.com",
	"mixpanel.com",
	"api.mixpanel.com",
	"amplitude.com",
	"api.amplitude.com",
	"static.criteo.net",
	"intercom.io",
	"widget.intercom.io",
	"clarity.ms",
	// Error reporting / RUM (still trigger fingerprints)
	"sentry.io",
	"newrelic.com",
	"datadoghq.com",
	"bugsnag.com",
	"rollbar.com",
	"raygun.io",
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

// InterceptStats tracks per-session counters. Exposed for tests and
// for callers that want to log throughput / hit-rate metrics.
type InterceptStats struct {
	BlockedByType   atomic.Int64
	BlockedByDomain atomic.Int64
	CacheHit        atomic.Int64
	CacheMiss       atomic.Int64
	NetworkFetch    atomic.Int64
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
				for k, vs := range entry.Headers {
					for _, v := range vs {
						h.Response.SetHeader(k, v)
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

