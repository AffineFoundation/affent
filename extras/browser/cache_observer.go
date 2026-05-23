package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// observerBodyFetchConcurrency caps how many `Network.getResponseBody`
// CDP calls can be in flight at once across the observer's spawned
// goroutines. Empirically, leaving it unbounded — one goroutine per
// loadingFinished event — saturated rod's CDP channel on a page like
// open-meteo's docs (hundreds of small JS/CSS fetches) and stalled
// affent's Loop indefinitely. The semaphore guarantees forward
// progress: cache writes for slower responses are skipped rather
// than blocking the event dispatch loop.
const observerBodyFetchConcurrency = 16

func shouldFetchResponseBodyForCache(encodedDataLength float64) bool {
	return encodedDataLength <= 0 || encodedDataLength <= maxCachedResponseBodyBytes
}

type responseCachePutResult interface {
	PutResult(ctx context.Context, url string, entry *CachedResponse) (stored bool, err error)
}

func putResponseCache(ctx context.Context, cache ResponseCache, url string, entry *CachedResponse) (bool, error) {
	if cache == nil {
		return false, nil
	}
	if c, ok := cache.(responseCachePutResult); ok {
		return c.PutResult(ctx, url, entry)
	}
	err := cache.Put(ctx, url, entry)
	return err == nil, err
}

// startCacheObserver subscribes to the page's Network domain events
// and writes successful responses into the configured ResponseCache.
// Runs in a background goroutine until the page is closed.
//
// This is the companion to the intercept handler. Since we removed
// the LoadResponse-based cache write path (it forced Go's TLS
// fingerprint, tripping Cloudflare), cache population now happens
// out-of-band:
//
//	NetworkResponseReceived → buffer URL + status + headers
//	NetworkLoadingFinished  → call Network.getResponseBody, fold into
//	                          cache via cache.Put
//
// Chrome's own networking handles the actual fetch — the observer
// only reads the result. Cloudflare etc. see real Chrome traffic,
// not Go traffic.
//
// Concurrency: per-response goroutines are gated by a semaphore so a
// burst of sub-resources doesn't saturate the CDP channel. Cap
// (observerBodyFetchConcurrency) is per-session.
//
// Note: getResponseBody is a one-shot read; calling it twice fails.
// We tolerate the error (means the body was already drained, e.g. by
// a separate hijack callback).
func startCacheObserver(page *rod.Page, cache ResponseCache, stats *InterceptStats) {
	if cache == nil {
		return
	}
	type pending struct {
		url        string
		statusCode int
		headers    http.Header
	}
	var pendingMap sync.Map
	sem := make(chan struct{}, observerBodyFetchConcurrency)

	go page.EachEvent(
		func(e *proto.NetworkResponseReceived) {
			if e.Response == nil || e.Response.URL == "" {
				return
			}
			if !strings.HasPrefix(strings.ToLower(e.Response.URL), "http") {
				return
			}
			status := int(e.Response.Status)
			if status < 200 || status >= 400 {
				return
			}
			// Skip the URLs we always reject. Cheap pre-check so we
			// don't waste a CDP getResponseBody call.
			if isChallengePathURL(e.Response.URL) {
				return
			}
			// e.Response.Headers is map[string]gson.JSON (NetworkHeaders).
			// Use gson's .Str() to flatten; for keys whose value is
			// numeric or a list we fall back to the JSON literal.
			hdrs := http.Header{}
			for k, v := range e.Response.Headers {
				if s := v.Str(); s != "" {
					hdrs.Set(k, s)
				} else {
					hdrs.Set(k, v.String())
				}
			}
			pendingMap.Store(string(e.RequestID), pending{
				url:        e.Response.URL,
				statusCode: status,
				headers:    hdrs,
			})
		},
		func(e *proto.NetworkLoadingFinished) {
			key := string(e.RequestID)
			raw, ok := pendingMap.LoadAndDelete(key)
			if !ok {
				return
			}
			p := raw.(pending)
			reqID := e.RequestID
			if !shouldFetchResponseBodyForCache(e.EncodedDataLength) {
				return
			}
			// Best-effort acquire a semaphore slot. If we're already
			// at the concurrency cap, drop this body fetch — better
			// to skip a cache write than to back-pressure the CDP
			// channel and block the affent Loop. The dropped entry
			// will be fetched fresh on the next request that touches
			// the same URL.
			select {
			case sem <- struct{}{}:
			default:
				return
			}
			go func() {
				defer func() { <-sem }()
				body, err := getResponseBody(page, reqID)
				if err != nil {
					return
				}
				if len(body) > maxCachedResponseBodyBytes {
					return
				}
				entry := &CachedResponse{
					URL:        p.url,
					StatusCode: p.statusCode,
					Headers:    p.headers,
					Body:       body,
				}
				stored, err := putResponseCache(context.Background(), cache, p.url, entry)
				if err == nil && stored && stats != nil {
					stats.CacheWrite.Add(1)
				}
			}()
		},
		// Drop pending state on loading failures so the sync.Map
		// doesn't grow without bound when navigations are cancelled.
		func(e *proto.NetworkLoadingFailed) {
			pendingMap.Delete(string(e.RequestID))
		},
	)()
}

// getResponseBody calls Network.getResponseBody and decodes the
// payload (Chrome returns base64 for binary bodies).
func getResponseBody(page *rod.Page, reqID proto.NetworkRequestID) ([]byte, error) {
	r, err := proto.NetworkGetResponseBody{RequestID: reqID}.Call(page)
	if err != nil {
		return nil, err
	}
	return decodeRespBody(r)
}

// decodeRespBody is split out so we can unit test the encoding logic
// independently of CDP.
func decodeRespBody(r *proto.NetworkGetResponseBodyResult) ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil response")
	}
	if r.Base64Encoded {
		return base64.StdEncoding.DecodeString(r.Body)
	}
	return []byte(r.Body), nil
}
