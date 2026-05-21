package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractLastUser_PicksRightmostUser(t *testing.T) {
	msgs := []chatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second"},
	}
	got := extractLastUser(msgs)
	if got != "second" {
		t.Errorf("extractLastUser = %q, want %q", got, "second")
	}
}

func TestExtractLastUser_NoUserReturnsEmpty(t *testing.T) {
	msgs := []chatMessage{{Role: "system", Content: "sys"}}
	if got := extractLastUser(msgs); got != "" {
		t.Errorf("extractLastUser = %q, want empty", got)
	}
}

func TestRequireAuth_NoTokenIsPassThrough(t *testing.T) {
	called := false
	handler := requireAuth("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !called {
		t.Errorf("handler should have been invoked")
	}
	if w.Result().StatusCode != 204 {
		t.Errorf("status = %d", w.Result().StatusCode)
	}
}

func TestRequireAuth_RejectsMissingBearer(t *testing.T) {
	called := false
	handler := requireAuth("topsecret", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	for _, hdr := range []string{"", "Bearer wrong", "Basic foo"} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		if hdr != "" {
			r.Header.Set("Authorization", hdr)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Result().StatusCode != http.StatusUnauthorized {
			t.Errorf("hdr=%q got status %d, want 401", hdr, w.Result().StatusCode)
		}
	}
	if called {
		t.Errorf("inner handler should not have run for any bad header")
	}
}

func TestRequireAuth_AcceptsCorrectBearer(t *testing.T) {
	handler := requireAuth("topsecret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer topsecret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Result().StatusCode != 204 {
		t.Errorf("status = %d", w.Result().StatusCode)
	}
}

// TestRequireAuth_RejectsNearMissAndWrongLength locks the
// constant-time-comparison fix in place. The token's first byte
// matching is the byte-by-byte timing-attack inroad if we ever
// regress to a plain `!=` comparison; both same-length-wrong and
// different-length cases must produce 401.
func TestRequireAuth_RejectsNearMissAndWrongLength(t *testing.T) {
	handler := requireAuth("topsecret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	cases := []string{
		"Bearer topsecreX",  // same length, last byte off — would short-circuit late
		"Bearer topxxxxxx",  // same length, early divergence — would short-circuit early
		"Bearer topsecre",   // shorter — length mismatch
		"Bearer topsecrets", // longer — length mismatch
		"Bearer ",           // empty token after prefix
	}
	for _, h := range cases {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		r.Header.Set("Authorization", h)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if got := w.Result().StatusCode; got != http.StatusUnauthorized {
			t.Errorf("Authorization=%q produced %d, want 401", h, got)
		}
	}
}

// handleSessionRoutes path parsing — works without a real pool by
// passing a nil one and checking only the 404/400 boundary cases.

func TestHandleSessionRoutes_RejectsMissingID(t *testing.T) {
	h := handleSessionRoutes(nil)
	r := httptest.NewRequest("GET", "/v1/sessions/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Result().StatusCode)
	}
}

func TestHandleSessionRoutes_RejectsUnknownSub(t *testing.T) {
	h := handleSessionRoutes(nil)
	r := httptest.NewRequest("GET", "/v1/sessions/abc/garbage", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Result().StatusCode)
	}
}

func TestHealth_ReturnsOK(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	handleHealth(pool)(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body = %q", body)
	}
}

// TestHealth_ReturnsShuttingDown pins that /healthz starts returning
// 503 the moment Shutdown begins. Without it, a graceful-rollout load
// balancer keeps routing fresh traffic at a dying pod until the LB's
// own probe-failure threshold kicks in — wasting up to that many
// seconds of requests on a server that's about to disappear.
func TestHealth_ReturnsShuttingDown(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.Shutdown()
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	handleHealth(pool)(w, r)
	if got := w.Result().StatusCode; got != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", got)
	}
	if body := w.Body.String(); !strings.Contains(body, `"status":"shutting_down"`) {
		t.Errorf("body = %q, expected shutting_down", body)
	}
}
