package main

import (
	"net/http/httptest"
	"testing"

	"github.com/affinefoundation/affent/sse"
)

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
