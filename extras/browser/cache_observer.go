package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// startCacheObserver subscribes to the page's Network domain events
// and writes successful responses into the configured ResponseCache.
// Runs in a background goroutine until the page is closed.
//
// This is the companion to the intercept handler. Since we removed
// the LoadResponse-based cache write path (it forced Go's TLS
// fingerprint, tripping Cloudflare), cache population now happens
// out-of-band:
//
//	NetworkResponseReceived → buffer URL + status + headers + MIME
//	NetworkLoadingFinished  → call Network.getResponseBody, fold into
//	                          cache via cache.Put
//
// Chrome's own networking handles the actual fetch — the observer
// only reads the result. Cloudflare etc. see real Chrome traffic,
// not Go traffic.
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
		mimeType   string
	}
	var pendingMap sync.Map

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
				mimeType:   e.Response.MIMEType,
			})
		},
		func(e *proto.NetworkLoadingFinished) {
			key := string(e.RequestID)
			raw, ok := pendingMap.LoadAndDelete(key)
			if !ok {
				return
			}
			p := raw.(pending)
			// Get body. May fail for navigations the page redirected
			// past, for already-drained streams, or for resources
			// blocked by our hijack handler.
			body, err := getResponseBody(page, e.RequestID)
			if err != nil {
				return
			}
			entry := &CachedResponse{
				URL:        p.url,
				StatusCode: p.statusCode,
				Headers:    p.headers,
				Body:       body,
			}
			if err := cache.Put(context.Background(), p.url, entry); err == nil {
				// Note: cache.Put rejects challenge bodies / paths
				// internally too; CacheMiss counter was bumped at
				// hijack time, this just records the write.
				_ = stats // placeholder if we add a CacheWrite counter
			}
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

// observerEnabled is a runtime kill-switch some tests want; not used
// in production. Wired through atomic so a debug session can flip it
// without restarting.
var observerEnabled atomic.Bool

func init() {
	observerEnabled.Store(true)
}
