package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// TestRequestBody_StripsReasoning pins the wire-format contract: the
// request body sent upstream must not contain reasoning_content. Some
// providers emit it on responses but reject it on inbound messages.
func TestRequestBody_StripsReasoning(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
		{
			Role:             "assistant",
			Content:          "the answer",
			ReasoningContent: "I should think step by step about this...",
		},
	}
	body, err := json.Marshal(chatRequest{
		Model:    "test",
		Messages: toWireMessages(msgs),
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("request body must not contain reasoning_content; got %s", got)
	}
	if !strings.Contains(got, `"content":"the answer"`) {
		t.Errorf("expected visible content to survive; got %s", got)
	}
}

// TestRequestBody_SamplingForwarding pins the contract that
// LLMClient.Sampling actually reaches the wire request when set, and
// stays out of it when unset (so the upstream provider's defaults
// apply). temperature=0 is the eval-critical edge case: a pointer to
// 0.0 must marshal as "temperature":0, NOT be elided like an unset
// field would be.
func TestRequestBody_SamplingForwarding(t *testing.T) {
	t.Run("all unset → no sampling fields on the wire", func(t *testing.T) {
		body, _ := json.Marshal(chatRequest{Model: "m", Messages: toWireMessages(nil), Stream: true})
		got := string(body)
		for _, k := range []string{"temperature", "top_p", "max_tokens", "seed"} {
			if strings.Contains(got, k) {
				t.Errorf("unset Sampling must omit %q; got %s", k, got)
			}
		}
	})
	t.Run("temperature=0 must reach the wire as a literal 0", func(t *testing.T) {
		zero := 0.0
		body, _ := json.Marshal(chatRequest{
			Model: "m", Messages: toWireMessages(nil), Stream: true,
			Temperature: &zero,
		})
		got := string(body)
		if !strings.Contains(got, `"temperature":0`) {
			t.Errorf("temperature=0 must marshal as literal 0 (deterministic decode); got %s", got)
		}
	})
	t.Run("non-zero values pass through", func(t *testing.T) {
		temp := 0.7
		top := 0.95
		max := 512
		seed := int64(42)
		body, _ := json.Marshal(chatRequest{
			Model: "m", Messages: toWireMessages(nil), Stream: true,
			Temperature: &temp,
			TopP:        &top,
			MaxTokens:   &max,
			Seed:        &seed,
		})
		got := string(body)
		for _, want := range []string{`"temperature":0.7`, `"top_p":0.95`, `"max_tokens":512`, `"seed":42`} {
			if !strings.Contains(got, want) {
				t.Errorf("expected %q in wire body; got %s", want, got)
			}
		}
	})
}

func TestSanitizeToolCallArgs_ReplacesMalformedWithEmptyObject(t *testing.T) {
	mk := func(args string) ToolCall {
		var tc ToolCall
		tc.ID = "call_1"
		tc.Type = "function"
		tc.Function.Name = "f"
		tc.Function.Arguments = args
		return tc
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid passes through", `{"k":"v"}`, `{"k":"v"}`},
		{"empty becomes {}", "", "{}"},
		{"truncated becomes {}", `{"k":"long-pa`, "{}"},
		{"plain text becomes {}", "not json at all", "{}"},
		{"valid array passes through", `[1,2,3]`, `[1,2,3]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeToolCallArgs([]ToolCall{mk(c.in)})
			if got[0].Function.Arguments != c.want {
				t.Errorf("got %q want %q", got[0].Function.Arguments, c.want)
			}
		})
	}
}

func TestSanitizeToolCallArgs_PartialCorruption(t *testing.T) {
	mk := func(args string) ToolCall {
		var tc ToolCall
		tc.Type = "function"
		tc.Function.Arguments = args
		return tc
	}
	in := []ToolCall{
		mk(`{"path":"/tmp/a.txt"}`),
		mk(`{"path":"/tmp/b`),
		mk(`{"path":"/tmp/c.txt"}`),
	}
	out := sanitizeToolCallArgs(in)
	if out[0].Function.Arguments != `{"path":"/tmp/a.txt"}` {
		t.Errorf("good[0] mutated: %q", out[0].Function.Arguments)
	}
	if out[1].Function.Arguments != `{}` {
		t.Errorf("bad[1] not rewritten: %q", out[1].Function.Arguments)
	}
	if out[2].Function.Arguments != `{"path":"/tmp/c.txt"}` {
		t.Errorf("good[2] mutated: %q", out[2].Function.Arguments)
	}
	if in[1].Function.Arguments != `{"path":"/tmp/b` {
		t.Errorf("input slice was mutated; sanitizer must copy-on-write")
	}
}

// TestEnsureToolCallIDs_BackfillsMissingButLeavesPresent pins the
// contract: every call gets a non-empty ID after the pass, and IDs
// that were already populated by the model are kept verbatim so a
// downstream replay still references the same id. Without the
// backfill, providers that omit ids on tool_call fragments (observed
// on certain DeepSeek tool-call configurations and chutes-routed
// models) would persist assistant.tool_calls[id=""], runTurn would
// locally generate a "call_xxx" for the tool response, and the next
// LLM request would fail the assistant↔tool linkage check.
func TestEnsureToolCallIDs_BackfillsMissingButLeavesPresent(t *testing.T) {
	calls := []ToolCall{
		{ID: "", Type: "function"},              // missing
		{ID: "call_existing", Type: "function"}, // already set
		{ID: "", Type: "function"},              // also missing
	}
	calls[0].Function.Name = "a"
	calls[1].Function.Name = "b"
	calls[2].Function.Name = "c"

	ensureToolCallIDs(calls)

	if calls[0].ID == "" || !strings.HasPrefix(calls[0].ID, "call_") {
		t.Errorf("missing id #0 not backfilled to call_<uuid>; got %q", calls[0].ID)
	}
	if calls[1].ID != "call_existing" {
		t.Errorf("existing id was overwritten; got %q", calls[1].ID)
	}
	if calls[2].ID == "" || !strings.HasPrefix(calls[2].ID, "call_") {
		t.Errorf("missing id #2 not backfilled to call_<uuid>; got %q", calls[2].ID)
	}
	if calls[0].ID == calls[2].ID {
		t.Errorf("two missing ids got the same backfill; must be unique")
	}
}

// TestParseRetryAfter pins the RFC 7231 integer-seconds parse plus
// the MaxRespectedRetryAfter cap. The cap matters because the value
// comes from untrusted upstream — a hostile or misbehaving server
// emitting "Retry-After: 86400" would otherwise pin the loop's next
// attempt 24 hours out. parseRetryAfter returns 0 in that case so
// runStep falls back to its own exponential schedule.
func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		// Empty / whitespace → 0 (caller falls back).
		{"", 0},
		{"  ", 0},
		// Non-positive / non-numeric → 0.
		{"0", 0},
		{"-5", 0},
		{"not-a-number", 0},
		{"5.5", 0}, // strconv.Atoi rejects floats; the spec is integer seconds.
		// HTTP-date form silently ignored (spec allows but we don't parse).
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0},
		// Valid integers pass through.
		{"1", 1 * time.Second},
		{"30", 30 * time.Second},
		{"60", 60 * time.Second},
		// At the cap.
		{"300", MaxRespectedRetryAfter},
		// Past the cap → 0, caller's exponential takes over.
		{"301", 0},
		{"86400", 0}, // 24h — the obvious hostile-server case.
		// Whitespace trim.
		{" 30 ", 30 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := parseRetryAfter(c.in); got != c.want {
				t.Errorf("parseRetryAfter(%q) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

// TestIsRetryableStatus pins the HTTP-status classification used by
// the LLM client. 408 / 429 / 5xx are server-side / overload signals
// safe to retry; everything else (400, 401, 403, 404, etc.) is the
// caller's fault and retrying just burns budget.
func TestIsRetryableStatus(t *testing.T) {
	retry := []int{408, 429, 500, 502, 503, 504, 599}
	for _, code := range retry {
		if !isRetryableStatus(code) {
			t.Errorf("isRetryableStatus(%d) = false, want true", code)
		}
	}
	noRetry := []int{200, 201, 301, 302, 400, 401, 403, 404, 422, 600}
	for _, code := range noRetry {
		if isRetryableStatus(code) {
			t.Errorf("isRetryableStatus(%d) = true, want false", code)
		}
	}
}

// TestIsTransient pins the error-classifier used by runStep's retry
// gate. The categorization is load-bearing: false-positive (treating
// a non-retryable error as transient) wastes retry budget and ships
// a duplicate request; false-negative (treating a transient error
// as terminal) gives up too early on flaky upstreams.
func TestIsTransient(t *testing.T) {
	t.Run("nil → false", func(t *testing.T) {
		if isTransient(nil) {
			t.Error("nil error should not be transient")
		}
	})
	t.Run("plain error → false", func(t *testing.T) {
		if isTransient(errors.New("oops")) {
			t.Error("arbitrary error should not be transient")
		}
	})
	t.Run("context.Canceled → false (caller asked to stop)", func(t *testing.T) {
		if isTransient(context.Canceled) {
			t.Error("context.Canceled is the user's decision, not a transient failure")
		}
	})
	t.Run("context.DeadlineExceeded → true (upstream too slow)", func(t *testing.T) {
		if !isTransient(context.DeadlineExceeded) {
			t.Error("DeadlineExceeded should be transient — next attempt might be faster")
		}
	})
	t.Run("RetryableError → true", func(t *testing.T) {
		err := &RetryableError{Err: errors.New("upstream 503")}
		if !isTransient(err) {
			t.Error("RetryableError should be transient — that's the entire point of the type")
		}
	})
	t.Run("wrapped RetryableError → true", func(t *testing.T) {
		err := fmt.Errorf("step 3: %w", &RetryableError{Err: errors.New("upstream 503")})
		if !isTransient(err) {
			t.Error("errors.As should find a wrapped RetryableError")
		}
	})
	t.Run("io.ErrUnexpectedEOF → true (mid-stream cut)", func(t *testing.T) {
		if !isTransient(io.ErrUnexpectedEOF) {
			t.Error("mid-stream EOF is a classic flaky-network case worth retrying")
		}
	})
}

func TestConversationLog_KeepsReasoning(t *testing.T) {
	msg := ChatMessage{
		Role:             "assistant",
		Content:          "the answer",
		ReasoningContent: "step-by-step thinking",
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"reasoning_content":"step-by-step thinking"`) {
		t.Errorf("conversation log dropped reasoning_content: %s", body)
	}
}
