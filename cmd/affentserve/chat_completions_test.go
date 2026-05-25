package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/sse"
)

// TestChatCompletions_HeaderBeatsBodyForSessionPinning pins the
// precedence contract surfaced in real-rollout testing: a caller
// passing X-Affent-Session-Id on the request header expected the
// session to be reused, but only the body fields were being read.
// New requests silently spawned fresh sessions, breaking pinning
// for proxies / middleware that can't rewrite the JSON body.
func TestChatCompletions_HeaderBeatsBodyForSessionPinning(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	cfg := Config{Model: "fake"}
	handler := handleChatCompletions(cfg, pool)

	// Body says session "from-body" but header overrides with "from-header".
	body := `{"model":"fake","messages":[{"role":"user","content":"hi"}],"session_id":"from-body"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("X-Affent-Session-Id", "from-header")
	rec := httptest.NewRecorder()
	// We don't need the upstream LLM to answer — it will error, but
	// the session creation step happens first and that's what the
	// pool will record. Cancel via a closed-on-completion ctx.
	handler(rec, req)

	if _, err := pool.Get("from-header"); err != nil {
		t.Errorf("pool should contain 'from-header' session: %v", err)
	}
	if _, err := pool.Get("from-body"); err == nil {
		t.Errorf("pool should NOT contain 'from-body' — header must win")
	}
}

// TestChatCompletions_ResponseLabelsWithConfiguredModel pins that the
// response's "model" field reports the actually-driven backend
// (cfg.Model), not whatever the client put in its request. Pre-fix
// the precedence was req.Model > cfg.Model > "affent", so a client
// asking for "gpt-5" got a response labeled "gpt-5" even though
// affentserve was wired to drive "qwen-plus" — actively misleading,
// and inconsistent with /v1/models which already truthfully reports
// cfg.Model.
//
// Mock LLM returns a final assistant message immediately so the
// full handler path runs end-to-end.
func TestChatCompletions_ResponseLabelsWithConfiguredModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	pool := newTestPool(t, 4, "5m")
	pool.cfg.BaseURL = srv.URL
	pool.cfg.Model = "qwen-plus"

	cfg := Config{Model: "qwen-plus", BaseURL: srv.URL}
	handler := handleChatCompletions(cfg, pool)

	// Client claims model=gpt-5; affentserve is configured for qwen-plus.
	body := `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	if resp.Model != "qwen-plus" {
		t.Errorf("response model = %q, want %q (cfg.Model must win over req.Model)", resp.Model, "qwen-plus")
	}
}

// TestChatCompletions_ResponseLabelsEchoRequestWhenCfgEmpty pins
// the escape hatch: when cfg.Model is left empty (operator wants
// the client to pick), the response label echoes the request's
// model so OpenAI SDKs that compare request/response model strings
// still match.
func TestChatCompletions_ResponseLabelsEchoRequestWhenCfgEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	pool := newTestPool(t, 4, "5m")
	pool.cfg.BaseURL = srv.URL
	pool.cfg.Model = "" // operator declined to configure a default

	cfg := Config{BaseURL: srv.URL}
	handler := handleChatCompletions(cfg, pool)

	body := `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	if resp.Model != "gpt-5" {
		t.Errorf("response model = %q, want gpt-5 (cfg.Model empty → echo request)", resp.Model)
	}
}

// TestResolvedSessionID_PrefersAffentNamespacedField pins the
// request-vs-response symmetry fix. Real-LLM testing surfaced the
// asymmetry: response chunks carry `affent_session_id`, but request
// bodies only used to honor `session_id`. A caller copy-pasting the
// response field name to pin to an existing session got a fresh one
// instead. Both keys now work; affent_session_id wins when both are
// set so a hypothetical future OpenAI session_id field can't shadow
// the caller's affent-specific intent.
func TestResolvedSessionID_PrefersAffentNamespacedField(t *testing.T) {
	cases := []struct {
		name       string
		session    string
		affentSess string
		want       string
	}{
		{"only short field", "sess_short", "", "sess_short"},
		{"only namespaced field", "", "sess_ns", "sess_ns"},
		{"both — namespaced wins", "sess_short", "sess_ns", "sess_ns"},
		{"neither — empty (pool creates fresh)", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &chatRequest{SessionID: c.session, AffentSessionID: c.affentSess}
			if got := r.resolvedSessionID(); got != c.want {
				t.Errorf("resolvedSessionID = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBufferChatCompletion_PrefersModelFinishReason pins that the
// OpenAI-compat layer surfaces the model's actual finish_reason
// ("length" for max_tokens truncation, "content_filter", etc.) on a
// clean "completed" turn instead of flattening every clean termination
// to "stop". Affent-specific reasons (max_turns, cancelled, error)
// still override.
func TestBufferChatCompletion_PrefersModelFinishReason(t *testing.T) {
	cases := []struct {
		name              string
		messageDoneFinish string
		turnEndReason     string
		want              string
	}{
		{"model 'length' on clean turn surfaces as length", "length", sse.TurnEndCompleted, "length"},
		{"model 'stop' on clean turn stays stop", "stop", sse.TurnEndCompleted, "stop"},
		{"intermediate 'tool_calls' ignored, final 'stop' kept", "stop", sse.TurnEndCompleted, "stop"},
		{"max_turns overrides any model finish_reason", "stop", sse.TurnEndMaxTurns, "length"},
		{"cancelled overrides", "stop", sse.TurnEndCancelled, "stop"}, // openAIFinishReason maps cancelled→stop
		{"no model finish_reason falls back to turn-end map", "", sse.TurnEndCompleted, "stop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chan sse.Event, 8)
			turnID := "turn-test"
			// Emit an intermediate tool_calls done first to make sure
			// the filter only adopts terminal finish_reasons.
			pushMessageDone(ch, turnID, "intermediate", "tool_calls")
			if c.messageDoneFinish != "" {
				pushMessageDone(ch, turnID, "final content", c.messageDoneFinish)
			}
			pushTurnEnd(ch, turnID, c.turnEndReason)

			pool := newTestPool(t, 4, "5m")
			sess, _ := pool.GetOrCreate("buf-test")
			out, _ := bufferChatCompletion(context.Background(), sess, turnID, ch)
			if out.FinishReason != c.want {
				t.Errorf("FinishReason = %q, want %q (case: %s)", out.FinishReason, c.want, c.name)
			}
		})
	}
}

func TestBufferChatCompletion_ReturnsTerminalAnswerOnly(t *testing.T) {
	ch := make(chan sse.Event, 16)
	turnID := "turn-test"
	pushMessageDelta(ch, turnID, "I will keep searching.")
	pushMessageDone(ch, turnID, "I will keep searching.", "tool_calls")
	pushMessageDelta(ch, turnID, "final answer")
	pushMessageDone(ch, turnID, "final answer", "stop")
	pushTurnEnd(ch, turnID, sse.TurnEndCompleted)

	pool := newTestPool(t, 4, "5m")
	sess, _ := pool.GetOrCreate("buf-terminal")
	out, err := bufferChatCompletion(context.Background(), sess, turnID, ch)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "final answer" {
		t.Fatalf("Content = %q, want terminal answer only", out.Content)
	}
}

func pushMessageDelta(ch chan sse.Event, turnID, delta string) {
	ev, _ := sse.NewEvent(sse.TypeMessageDelta, sse.MessageDeltaPayload{
		TurnID: turnID, Delta: delta,
	})
	ch <- ev
}

func pushMessageDone(ch chan sse.Event, turnID, text, finish string) {
	ev, _ := sse.NewEvent(sse.TypeMessageDone, sse.MessageDonePayload{
		TurnID: turnID, Text: text, FinishReason: finish,
	})
	ch <- ev
}

func pushTurnEnd(ch chan sse.Event, turnID, reason string) {
	ev, _ := sse.NewEvent(sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: reason})
	ch <- ev
}

func TestOpenAIFinishReason(t *testing.T) {
	cases := map[string]string{
		sse.TurnEndMaxTurns:  "length",
		sse.TurnEndCompleted: "stop",
		sse.TurnEndCancelled: "stop",
		sse.TurnEndError:     "stop",
		"":                   "stop",
		"unknown":            "stop",
	}
	for in, want := range cases {
		if got := openAIFinishReason(in); got != want {
			t.Errorf("openAIFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestChatCompletions_OversizedBodyReturns413 pins that a request
// body larger than the configured limit gets a clean 413 with a
// "request too large" message, not a misleading 400 about decode.
// Earlier the body was read through io.LimitReader, which silently
// truncated at the limit and let the json decoder fail downstream
// with "unexpected end of input" — operators couldn't tell whether
// the client sent malformed JSON or just an oversized payload.
func TestChatCompletions_OversizedBodyReturns413(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cfg := Config{Model: "fake"}
	handler := handleChatCompletions(cfg, pool)

	// Pad past the 4 MiB limit. The padding sits inside a string
	// field of a well-formed JSON object so a parser that DID see
	// the whole body would accept it — meaning the rejection has to
	// come from the size guard, not from JSON validation.
	pad := strings.Repeat("x", 4*1024*1024+1024)
	body := `{"model":"fake","messages":[{"role":"user","content":"` + pad + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if got := rec.Result().StatusCode; got != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", got)
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("response body should mention the size limit; got %q", rec.Body.String())
	}
}

// TestChatCompletions_SendUserErrors_MapsToDistinctStatuses pins the
// error-routing fix. Pre-fix every SendUser error became 409 "session
// busy", which:
//   - hid I/O failures (conv.Append) behind a misleading "retry me"
//     status — clients would retry the busy path forever, never knowing
//     the server itself was broken.
//   - reported "busy" for a client that had already disconnected, which
//     showed up in logs as phantom 409s with no corresponding 200.
//
// Now ErrTurnInFlight → 409 (the only real busy signal), client-disconnect
// → 499 (proxy-friendly), and everything else → 500.
func TestChatCompletions_SendUserErrors_MapsToDistinctStatuses(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cfg := Config{Model: "fake"}
	handler := handleChatCompletions(cfg, pool)

	// Cancelled ctx path: SendUser's entry-time ctx.Err() check returns
	// context.Canceled. Must map to 499, not 409.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := `{"model":"fake","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if got := rec.Result().StatusCode; got != 499 {
		t.Errorf("cancelled ctx: status = %d, want 499", got)
	}
}

func TestWriteChatCompletionResponse_SetsSessionIDHeader(t *testing.T) {
	w := httptest.NewRecorder()
	out := &bufferedTurnResult{Content: "hi", FinishReason: "stop"}
	writeChatCompletionResponse(w, "sess_abc123", "fake-model", out)

	if got := w.Header().Get("X-Affent-Session-Id"); got != "sess_abc123" {
		t.Fatalf("X-Affent-Session-Id = %q, want %q", got, "sess_abc123")
	}
	if got := w.Result().StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestWriteChatCompletionResponse_SetsSessionIDHeaderOnError(t *testing.T) {
	w := httptest.NewRecorder()
	out := &bufferedTurnResult{Error: "upstream blew up"}
	writeChatCompletionResponse(w, "sess_xyz", "fake-model", out)

	if got := w.Header().Get("X-Affent-Session-Id"); got != "sess_xyz" {
		t.Fatalf("error path must still set X-Affent-Session-Id, got %q", got)
	}
	if got := w.Result().StatusCode; got != 502 {
		t.Fatalf("status = %d, want 502 (BadGateway)", got)
	}
}
