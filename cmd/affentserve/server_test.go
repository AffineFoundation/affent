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
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	handleHealth(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("body = %q", body)
	}
}
