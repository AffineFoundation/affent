package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSessionCancel_AcceptsActiveSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	if _, err := pool.GetOrCreate("cancel-active"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/cancel-active/cancel", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	var resp sessionCancelResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "cancel-active" || !resp.Accepted {
		t.Fatalf("response = %+v, want accepted cancel-active", resp)
	}
}

func TestHandleSessionCancel_InactiveDurableSessionReturnsConflict(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "cancel-durable")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/cancel-durable/cancel", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "session_inactive") {
		t.Fatalf("body should explain inactive session: %s", w.Body.String())
	}
	if activeSessionByID(pool, "cancel-durable") != nil {
		t.Fatal("POST cancel must not reopen an inactive durable session")
	}
}

func TestHandleSessionCancel_RejectsUnsafeID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/../cancel", nil)
	w := httptest.NewRecorder()
	handleSessionCancel(pool, "..", w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}
