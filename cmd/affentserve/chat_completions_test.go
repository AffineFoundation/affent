package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/sse"
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
		name        string
		session     string
		affentSess  string
		want        string
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
