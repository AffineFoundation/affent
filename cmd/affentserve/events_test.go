package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

// TestHandleSessionEvents_UnknownSessionReturns404 pins the
// session-not-found path. /v1/sessions/{id}/events is the only
// way clients re-subscribe after a connection drop; if the
// session was evicted (LRU / idle GC / restart), the 404 with
// JSON error tells them to start a fresh session instead of
// hanging on a never-arriving stream.
func TestHandleSessionEvents_UnknownSessionReturns404(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/ghost/events", nil)
	w := httptest.NewRecorder()
	handleSessionEvents(pool, "ghost", w, r)

	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Errorf("status = %d, want 404", got)
	}
	if ct := w.Result().Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want JSON error shape", ct)
	}
	// Error body must mention the actual session-not-found cause so
	// operators grepping logs can spot it.
	body := w.Body.String()
	if !strings.Contains(body, "session not found") {
		t.Errorf("error body missing 'session not found': %s", body)
	}
}

// TestHandleSessionEvents_RejectsNonStreamingWriter pins the
// "streaming unsupported" guard. The handler asserts w to
// http.Flusher; on any wrapper that doesn't implement it (some
// test recorders, third-party middleware) we 500 with a clear
// message rather than 200-then-buffer-forever.
//
// httptest.NewRecorder DOES implement Flusher, so we use a
// custom no-flusher writer wrapping it.
func TestHandleSessionEvents_RejectsNonStreamingWriter(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("real")
	if err != nil {
		t.Fatal(err)
	}
	_ = s // session exists; the failure should be in the Flusher check

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/real/events", nil)
	w := &noFlusherWriter{ResponseWriter: httptest.NewRecorder()}
	handleSessionEvents(pool, "real", w, r)

	rec := w.ResponseWriter.(*httptest.ResponseRecorder)
	if got := rec.Result().StatusCode; got != http.StatusInternalServerError {
		t.Errorf("non-flusher writer should return 500; got %d", got)
	}
	if !strings.Contains(rec.Body.String(), "streaming unsupported") {
		t.Errorf("expected 'streaming unsupported' message; got %s", rec.Body.String())
	}
}

// noFlusherWriter wraps a recorder but does NOT implement
// http.Flusher, simulating a middleware that broke the streaming
// contract.
type noFlusherWriter struct{ http.ResponseWriter }

// TestWriteSSE_EncodesAndFlushes pins the 3-line helper that
// every SSE handler uses. The frame must be the canonical
// `event: <type>\nid: <n>\ndata: <json>\n\n` shape, AND Flush()
// must be called so the bytes actually reach the client (any
// buffering middleware would otherwise hold them until response
// close).
func TestWriteSSE_EncodesAndFlushes(t *testing.T) {
	rec := httptest.NewRecorder()
	spy := &flushSpyRecorder{ResponseRecorder: rec}

	ev, err := sse.NewEvent(sse.TypeUsage, sse.UsagePayload{
		TurnID: "turn1", InputTokens: 5, OutputTokens: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	ev.ID = 42
	writeSSE(spy, spy, ev)

	if !spy.flushed {
		t.Error("writeSSE must Flush — without it, SSE buffers and client sees nothing until close")
	}

	body := rec.Body.String()
	for _, want := range []string{
		"event: " + sse.TypeUsage,
		"id: 42",
		"data: ",
		`"input_tokens":5`,
		`"output_tokens":7`,
		"\n\n", // SSE frame terminator
	} {
		if !strings.Contains(body, want) {
			t.Errorf("frame missing %q in:\n%s", want, body)
		}
	}

	// The data line must be valid JSON parseable as the payload.
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var p sse.UsagePayload
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil {
			t.Errorf("data line not parseable as UsagePayload: %v", err)
		}
		if p.TurnID != "turn1" {
			t.Errorf("payload turn_id = %q, want turn1", p.TurnID)
		}
	}
}

// flushSpyRecorder embeds httptest.ResponseRecorder and records
// whether Flush was called. The recorder itself implements Flusher
// but its base Flush is a no-op for buffered writers; we wrap to
// observe.
type flushSpyRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushSpyRecorder) Flush() { f.flushed = true }
