package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleSessionMessage_StartsTurn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-active/messages", strings.NewReader(`{"content":"hello"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	var resp sessionMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.SessionID != "message-active" || !strings.HasPrefix(resp.TurnID, "turn_") {
		t.Fatalf("response = %+v, want session id + turn id", resp)
	}
}

func TestHandleSessionMessage_ReopensDurableSession(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "message-durable")

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-durable/messages", strings.NewReader(`{"content":"resume this"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "message-durable") == nil {
		t.Fatal("POST messages should reopen durable session")
	}
}

func TestHandleSessionMessage_RejectsInvalidBody(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cases := []struct {
		name  string
		body  string
		wants []string
	}{
		{"empty content", `{"content":"   "}`, []string{"content is required"}},
		{"unknown field", `{"content":"hello","role":"user"}`, []string{"unknown field", "role"}},
		{"multiple objects", `{"content":"hello"} {"content":"again"}`, []string{"single JSON object"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-bad/messages", strings.NewReader(c.body))
			w := httptest.NewRecorder()
			handleSessionRoutes(pool).ServeHTTP(w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
			for _, want := range c.wants {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatalf("body %q does not contain %q", w.Body.String(), want)
				}
			}
		})
	}
}

func TestHandleSessionMessage_BusySessionReturnsConflict(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})

	pool := newTestPool(t, 4, "5m")
	pool.cfg.BaseURL = srv.URL

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/message-busy/messages", strings.NewReader(`{"content":"first"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202; body=%s", got, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/sessions/message-busy/messages", strings.NewReader(`{"content":"second"}`))
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("second status = %d, want 409; body=%s", got, w.Body.String())
	}
	if got := w.Result().Header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

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
