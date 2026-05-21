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

// TestConsumeStream_ParallelToolCalls covers the OpenAI-style streaming
// shape where the model issues two parallel tool calls and their
// argument fragments arrive interleaved. The model-supplied `index`
// field is what disambiguates which call each fragment belongs to.
func TestConsumeStream_ParallelToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		// Two parallel call headers.
		w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"foo","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"bar","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		// Interleaved arg fragments — index field must route correctly.
		w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"y\":"}}]},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":"}}]},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}}]},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":null}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		fl.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	c := NewLLMClient(srv.URL, "", "fake")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := c.Chat(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var finish *FinishInfo
	for ev := range stream {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Finish != nil {
			finish = ev.Finish
		}
	}
	if finish == nil {
		t.Fatal("no Finish event")
	}
	if got, want := len(finish.Final.ToolCalls), 2; got != want {
		t.Fatalf("tool call count = %d, want %d (calls=%+v)", got, want, finish.Final.ToolCalls)
	}
	// Index 0 = foo({"x":1})
	if got, want := finish.Final.ToolCalls[0].Function.Name, "foo"; got != want {
		t.Errorf("call[0].name = %q, want %q", got, want)
	}
	if got, want := finish.Final.ToolCalls[0].Function.Arguments, `{"x":1}`; got != want {
		t.Errorf("call[0].args = %q, want %q — interleaved arg fragments routed to wrong call", got, want)
	}
	// Index 1 = bar({"y":2})
	if got, want := finish.Final.ToolCalls[1].Function.Name, "bar"; got != want {
		t.Errorf("call[1].name = %q, want %q", got, want)
	}
	if got, want := finish.Final.ToolCalls[1].Function.Arguments, `{"y":2}`; got != want {
		t.Errorf("call[1].args = %q, want %q", got, want)
	}
}
