package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
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
	if err := writeSSE(spy, spy, ev); err != nil {
		t.Fatalf("writeSSE on a healthy writer should not error: %v", err)
	}

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

// TestWriteSSE_BubblesWriteError pins that a downstream-broken
// connection surfaces as a non-nil error so handleSessionEvents
// can return immediately. Pre-fix writeSSE discarded write errors
// (_, _ = w.Write(...)), so the SSE loop kept draining events into
// a dead socket until Go's HTTP layer eventually noticed the close
// — many seconds of wasted goroutine time for an event stream
// that doesn't try to read.
func TestWriteSSE_BubblesWriteError(t *testing.T) {
	ev, err := sse.NewEvent(sse.TypeUsage, sse.UsagePayload{TurnID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	w := &errorWriter{}
	if err := writeSSE(w, w, ev); err == nil {
		t.Fatal("writeSSE on a broken writer must return the write error so the handler can bail")
	}
}

func TestHandleSessionHistory_ReplaysDurableEventsByLineCursor(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Shutdown)
	s, err := pool.GetOrCreate("history-client")
	if err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(memRoot, "history-client", "events.jsonl")
	for _, turnID := range []string{"turn-one", "turn-two"} {
		ev, err := sse.NewEvent(sse.TypeTurnStart, sse.TurnStartPayload{TurnID: turnID})
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately reuse the same event id: history pagination must
		// use JSONL line cursor, not process-local event ids that can
		// repeat across restart.
		ev.ID = 1
		s.events <- ev
	}
	waitForFileSubstring(t, tracePath, `"turn_id":"turn-two"`)

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/history-client/history?limit=2", nil)
	w := httptest.NewRecorder()
	handleSessionHistory(pool, "history-client", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", got, w.Body.String())
	}
	var page1 sessionHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v\n%s", err, w.Body.String())
	}
	if !page1.TraceSchemaDetected || page1.TraceSchemaVersion != sse.TraceSchemaVersion {
		t.Fatalf("trace schema metadata missing: %+v", page1)
	}
	if len(page1.Events) != 2 || page1.Events[0].Type != sse.TypeTraceMeta || page1.Events[1].Type != sse.TypeTurnStart {
		t.Fatalf("page1 events = %+v", page1.Events)
	}
	if !page1.HasMore || page1.NextAfter != 1 {
		t.Fatalf("page1 cursor = next_after:%d has_more:%v, want 1/true", page1.NextAfter, page1.HasMore)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/history-client/history?after=1&limit=2", nil)
	w = httptest.NewRecorder()
	handleSessionHistory(pool, "history-client", w, r)
	var page2 sessionHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v\n%s", err, w.Body.String())
	}
	if len(page2.Events) != 1 || page2.Events[0].Type != sse.TypeTurnStart || page2.NextAfter != 2 || page2.HasMore {
		t.Fatalf("page2 = %+v", page2)
	}
	var payload sse.TurnStartPayload
	if err := json.Unmarshal(page2.Events[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TurnID != "turn-two" {
		t.Fatalf("page2 turn_id = %q, want turn-two", payload.TurnID)
	}
}

func TestHandleSessionHistory_ReadsAfterRestartWithoutActiveSession(t *testing.T) {
	memRoot := t.TempDir()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    4,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	s, err := pool.GetOrCreate("history-restart")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := sse.NewEvent(sse.TypeUsage, sse.UsagePayload{TurnID: "t1", InputTokens: 1, OutputTokens: 2})
	if err != nil {
		t.Fatal(err)
	}
	s.events <- ev
	waitForFileSubstring(t, filepath.Join(memRoot, "history-restart", "events.jsonl"), `"turn_id":"t1"`)
	pool.Shutdown()

	pool2, err := NewSessionPool(cfg, zerologDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool2.Shutdown)
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/history-restart/history", nil)
	w := httptest.NewRecorder()
	handleSessionHistory(pool2, "history-restart", w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("status = %d, want 200: %s", got, string(body))
	}
	var resp sessionHistoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) < 2 {
		t.Fatalf("history should include trace.meta and usage after restart: %+v", resp.Events)
	}
}

func TestHandleSessionHistory_RejectsBadQueryAndUnsafeID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	for _, tc := range []struct {
		name      string
		sessionID string
		url       string
	}{
		{name: "bad after", sessionID: "abc", url: "/v1/sessions/abc/history?after=nope"},
		{name: "bad limit", sessionID: "abc", url: "/v1/sessions/abc/history?limit=0"},
		{name: "unsafe id", sessionID: "..", url: "/v1/sessions/../history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			w := httptest.NewRecorder()
			handleSessionHistory(pool, tc.sessionID, w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", got, w.Body.String())
			}
		})
	}
}

func TestHandleSessionHistory_MissingLogReturns404(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/missing/history", nil)
	w := httptest.NewRecorder()
	handleSessionHistory(pool, "missing", w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", got, w.Body.String())
	}
}

func zerologDiscard() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// errorWriter implements http.ResponseWriter + http.Flusher but
// returns an error from Write — the broken-pipe shape.
type errorWriter struct {
	hdr http.Header
}

func (e *errorWriter) Write([]byte) (int, error) { return 0, http.ErrBodyNotAllowed }
func (e *errorWriter) WriteHeader(int)           {}
func (e *errorWriter) Flush()                    {}
func (e *errorWriter) Header() http.Header {
	if e.hdr == nil {
		e.hdr = http.Header{}
	}
	return e.hdr
}
