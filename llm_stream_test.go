package affent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hangAfterFinish exercises the GLM tool_call regression: server emits
// a finish_reason chunk and then keeps the connection open without
// sending [DONE]. The watchdog should force-close the body within
// ~StreamPostFinishTimeout instead of waiting for callCtx.
func TestConsumeStream_HangAfterFinish(t *testing.T) {
	prevPost := StreamPostFinishTimeout
	StreamPostFinishTimeout = 200 * time.Millisecond
	t.Cleanup(func() { StreamPostFinishTimeout = prevPost })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		fl.Flush()
		// Hold connection open without [DONE]. Test should NOT wait
		// for this.
		time.Sleep(10 * time.Second)
	}))
	t.Cleanup(srv.Close)

	c := NewLLMClient(srv.URL, "", "fake")
	// Give the call a generous wall budget; we expect the watchdog,
	// not callCtx, to be what cuts us off.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	stream, err := c.Chat(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var sawFinish bool
	for ev := range stream {
		if ev.Err != nil {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
		if ev.Finish != nil {
			sawFinish = true
		}
	}
	elapsed := time.Since(start)
	if !sawFinish {
		t.Fatalf("expected Finish event after watchdog cut, got none")
	}
	// Should have exited within roughly 1 post-finish-timeout. Give
	// generous slack for goroutine scheduling.
	if elapsed > 2*time.Second {
		t.Fatalf("watchdog didn't fire fast enough: elapsed=%v (want <2s)", elapsed)
	}
}

// idleNoChunks: server flushes headers but never sends a single SSE
// chunk. Watchdog should close the body after StreamIdleTimeout and
// surface a retryable error (no finish_reason was ever seen).
func TestConsumeStream_IdleStallNoFinish(t *testing.T) {
	prevIdle := StreamIdleTimeout
	StreamIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { StreamIdleTimeout = prevIdle })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(10 * time.Second)
	}))
	t.Cleanup(srv.Close)

	c := NewLLMClient(srv.URL, "", "fake")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	stream, err := c.Chat(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var sawErr error
	for ev := range stream {
		if ev.Err != nil {
			sawErr = ev.Err
		}
	}
	elapsed := time.Since(start)
	if sawErr == nil {
		t.Fatalf("expected retryable stream error, got none")
	}
	var re *RetryableError
	if !errors.As(sawErr, &re) {
		t.Fatalf("error not flagged retryable: %v", sawErr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("idle watchdog didn't fire fast enough: elapsed=%v", elapsed)
	}
	if !strings.Contains(sawErr.Error(), "stream read") {
		t.Logf("informational: error message = %q", sawErr.Error())
	}
}
