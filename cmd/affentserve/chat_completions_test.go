package main

import (
	"net/http/httptest"
	"testing"

	"github.com/affinefoundation/affent/sse"
)

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
