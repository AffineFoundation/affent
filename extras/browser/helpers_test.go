package browser

import (
	"encoding/base64"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

// TestIsReplaySafeHeader pins the predicate that the response cache
// uses to decide what to store + replay. Returning true on a
// session-state header (Set-Cookie / Authorization / CSP nonces /
// CF-Ray correlation id) would let a cached response replay a
// stale auth context or trip CSP on a fresh navigation. The list
// is curated against real CF/site interceptor behavior, so any
// silent change here can break previously-working benchmarks.
func TestIsReplaySafeHeader(t *testing.T) {
	unsafe := []string{
		// Session state.
		"Set-Cookie", "set-cookie", "Cookie", "Authorization",
		"WWW-Authenticate", "Proxy-Authenticate",
		// CSRF / request id tokens.
		"X-CSRF-Token", "x-xsrf-token", "X-Request-ID", "X-Amz-Request-ID",
		// Cloudflare correlation / state.
		"CF-Ray", "cf-cache-status", "CF-Mitigated",
		// Timing / cache-validation headers Chrome computes itself.
		"Server-Timing", "Date", "Age", "Expires", "Last-Modified",
		"ETag", "Content-Length",
		// Nonce-bearing CSP — replaying breaks legit inline scripts.
		"Content-Security-Policy", "Content-Security-Policy-Report-Only",
	}
	for _, h := range unsafe {
		if isReplaySafeHeader(h) {
			t.Errorf("isReplaySafeHeader(%q) = true; header carries session/timing state, must NOT replay", h)
		}
	}

	safe := []string{
		"Content-Type", "content-type",
		"Cache-Control",
		"X-Frame-Options",
		"Strict-Transport-Security",
		"Vary",
		"Server",
		"Link",
		"X-Custom-Whatever", // unknown headers default to safe
	}
	for _, h := range safe {
		if !isReplaySafeHeader(h) {
			t.Errorf("isReplaySafeHeader(%q) = false; benign header should replay", h)
		}
	}
}

// TestDecodeRespBody pins the cache-write path. The doc-comment on
// decodeRespBody literally says it was split out for unit testing,
// but no test existed — a regression where base64 decoding
// silently fails would surface as corrupted cached responses
// (binary blobs returned as raw text). Three cases:
//   - base64-encoded body (the Chromium common case for binaries)
//   - plain UTF-8 body (the common case for text/* responses)
//   - nil result (defensive — CDP can return nil on
//     getResponseBody for redirects / aborted requests)
func TestDecodeRespBody(t *testing.T) {
	t.Run("nil result errors", func(t *testing.T) {
		_, err := decodeRespBody(nil)
		if err == nil {
			t.Error("nil result must error to avoid downstream nil-deref")
		}
	})

	t.Run("plain body passes through", func(t *testing.T) {
		r := &proto.NetworkGetResponseBodyResult{
			Body:          "hello world",
			Base64Encoded: false,
		}
		got, err := decodeRespBody(r)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello world" {
			t.Errorf("got %q, want hello world", got)
		}
	})

	t.Run("base64 body decoded", func(t *testing.T) {
		raw := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A} // PNG magic
		r := &proto.NetworkGetResponseBodyResult{
			Body:          base64.StdEncoding.EncodeToString(raw),
			Base64Encoded: true,
		}
		got, err := decodeRespBody(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(raw) {
			t.Fatalf("decoded len = %d, want %d", len(got), len(raw))
		}
		for i, b := range raw {
			if got[i] != b {
				t.Errorf("byte %d = %x, want %x", i, got[i], b)
			}
		}
	})

	t.Run("base64 invalid bytes error", func(t *testing.T) {
		r := &proto.NetworkGetResponseBodyResult{
			Body:          "not-valid-base64!@#",
			Base64Encoded: true,
		}
		_, err := decodeRespBody(r)
		if err == nil {
			t.Error("invalid base64 must error")
		}
	})
}

// TestSessionConfig_Viewport pins the default-viewport contract:
// zero / negative dims fall back to 1280×800 (a sensible desktop
// default that Cloudflare's bot-detection treats as normal). A
// regression to 0×0 would launch Chromium with a tiny viewport and
// break any responsive site's layout.
func TestSessionConfig_Viewport(t *testing.T) {
	cases := []struct {
		name      string
		w, h      int
		wantW, wH int
	}{
		{"zero falls back to 1280x800", 0, 0, 1280, 800},
		{"negative falls back to 1280x800", -1, -5, 1280, 800},
		{"explicit values pass through", 1920, 1080, 1920, 1080},
		{"only width set: height defaults", 1600, 0, 1600, 800},
		{"only height set: width defaults", 0, 720, 1280, 720},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := SessionConfig{ViewportWidth: c.w, ViewportHeight: c.h}
			gotW, gotH := cfg.viewport()
			if gotW != c.wantW || gotH != c.wH {
				t.Errorf("viewport = (%d, %d), want (%d, %d)", gotW, gotH, c.wantW, c.wH)
			}
		})
	}
}
