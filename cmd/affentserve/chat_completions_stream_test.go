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

// pushEvent encodes an affent event payload + ID and ships it down a
// buffered channel for streamChatCompletion to consume. Mirrors how
// the real fanout publishes events.
func pushEvent(t *testing.T, ch chan sse.Event, typ string, payload any) {
	t.Helper()
	ev, err := sse.NewEvent(typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	ch <- ev
}

// TestStreamChatCompletion_OpenAIShape pins the full SSE chunk
// shape OpenAI SDKs expect when stream=true. Every chunk is
// `data: <json>\n\n` where the JSON has id / object="chat.completion.chunk"
// / created / model / choices[0].{delta,finish_reason} fields,
// and the stream terminates with `data: [DONE]\n\n`. Plus our
// custom affent_session_id field on each chunk so resume works.
//
// streamChatCompletion is otherwise 0% covered (it doesn't appear
// in any e2e test that runs without a real upstream). This pins
// the handler's contract end-to-end via a directly-constructed
// session + canned event channel.
func TestStreamChatCompletion_OpenAIShape(t *testing.T) {
	turnID := "turn_test_42"
	sess := &Session{ID: "sess_test_123"} // nil loop; CancelTurn is nil-safe

	ch := make(chan sse.Event, 16)
	pushEvent(t, ch, sse.TypeMessageDelta, sse.MessageDeltaPayload{TurnID: turnID, Delta: "Hello"})
	pushEvent(t, ch, sse.TypeMessageDelta, sse.MessageDeltaPayload{TurnID: turnID, Delta: ", world"})
	pushEvent(t, ch, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: turnID, Text: "Hello, world", FinishReason: "stop"})
	pushEvent(t, ch, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: sse.TurnEndCompleted})
	close(ch)

	w := httptest.NewRecorder()
	streamChatCompletion(w, context.Background(), sess, turnID, "fake-model", ch, false)

	body := w.Body.String()

	// SSE response headers.
	if got := w.Result().Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", got)
	}
	if got := w.Result().Header.Get("X-Affent-Session-Id"); got != "sess_test_123" {
		t.Errorf("X-Affent-Session-Id = %q, want sess_test_123", got)
	}
	if got := w.Result().Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("missing X-Accel-Buffering: no header (needed to defeat nginx buffering)")
	}

	// Stream MUST terminate with [DONE].
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "data: [DONE]") {
		t.Errorf("stream must end with 'data: [DONE]'; got tail:\n%s", body[max(0, len(body)-200):])
	}

	// Parse each data: line as a JSON chunk and check shape.
	var contentDeltas []string
	var sawRole, sawFinish bool
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Object          string `json:"object"`
			Model           string `json:"model"`
			AffentSessionID string `json:"affent_session_id"`
			Choices         []struct {
				Delta struct {
					Role             string `json:"role"`
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk not JSON: %v\nline=%s", err, line)
		}
		if chunk.Object != "chat.completion.chunk" {
			t.Errorf("object = %q, want chat.completion.chunk", chunk.Object)
		}
		if chunk.Model != "fake-model" {
			t.Errorf("model = %q, want fake-model", chunk.Model)
		}
		if chunk.AffentSessionID != "sess_test_123" {
			t.Errorf("affent_session_id = %q, want sess_test_123", chunk.AffentSessionID)
		}
		if len(chunk.Choices) > 0 {
			d := chunk.Choices[0].Delta
			if d.Role == "assistant" {
				sawRole = true
			}
			if d.Content != "" {
				contentDeltas = append(contentDeltas, d.Content)
			}
			if chunk.Choices[0].FinishReason != "" {
				sawFinish = true
			}
		}
	}

	if !sawRole {
		t.Errorf("first chunk must carry delta.role=assistant")
	}
	if got := strings.Join(contentDeltas, ""); got != "Hello, world" {
		t.Errorf("reconstructed content = %q, want 'Hello, world'", got)
	}
	if !sawFinish {
		t.Errorf("a chunk must carry finish_reason at end of stream")
	}
}

// TestStreamChatCompletion_NonStreamingWriter pins the type-assert
// guard. If w doesn't implement http.Flusher, the handler must 500
// with a clear message rather than 200-then-buffer-forever.
func TestStreamChatCompletion_NonStreamingWriter(t *testing.T) {
	sess := &Session{ID: "sess_x"}
	ch := make(chan sse.Event)
	close(ch)
	w := &noFlusherWriter{ResponseWriter: httptest.NewRecorder()}
	streamChatCompletion(w, context.Background(), sess, "turn_x", "m", ch, false)
	rec := w.ResponseWriter.(*httptest.ResponseRecorder)
	if got := rec.Result().StatusCode; got != http.StatusInternalServerError {
		t.Errorf("non-flusher writer: status = %d, want 500", got)
	}
}

// TestStreamChatCompletion_ErrorChunkOnNonRecoverable pins that
// non-recoverable loop errors get surfaced as a `data: {"error":...}`
// chunk before the stream closes — so SDK error handlers can
// observe the failure instead of the stream silently ending.
// Recoverable errors must NOT surface; they're part of the retry
// path and would confuse clients.
func TestStreamChatCompletion_ErrorChunkOnNonRecoverable(t *testing.T) {
	turnID := "turn_err"
	sess := &Session{ID: "sess_err"}
	ch := make(chan sse.Event, 8)

	// Recoverable error must NOT surface (it's part of the retry path).
	pushEvent(t, ch, sse.TypeError, sse.ErrorPayload{
		TurnID: turnID, Code: "transient", Message: "transient blip", Recoverable: true,
	})
	// Non-recoverable error MUST surface as a `data: error` chunk so SDK
	// error handlers can observe the failure mid-stream.
	pushEvent(t, ch, sse.TypeError, sse.ErrorPayload{
		TurnID: turnID, Code: "fatal", Message: "upstream blew up", Recoverable: false,
	})
	pushEvent(t, ch, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: sse.TurnEndError})
	close(ch)

	w := httptest.NewRecorder()
	streamChatCompletion(w, context.Background(), sess, turnID, "m", ch, false)
	body := w.Body.String()

	if strings.Contains(body, "transient blip") {
		t.Errorf("recoverable error MUST NOT surface to client; body:\n%s", body)
	}
	if !strings.Contains(body, "upstream blew up") {
		t.Errorf("non-recoverable error must surface as data: error chunk; body:\n%s", body)
	}
	if !strings.Contains(body, `"type":"loop_error"`) {
		t.Errorf("error chunk should carry type=loop_error so SDK error handlers can branch; body:\n%s", body)
	}
}

// TestStreamChatCompletion_IncludeUsageEmitsFinalChunk pins
// OpenAI's stream_options.include_usage contract. When the client
// opts in, the SSE stream must emit a final chunk between the last
// finish_reason chunk and [DONE] with empty choices and a populated
// usage object. SDKs that read this skip the empty-choices chunk
// for content and parse usage from it instead.
//
// Without this, eval rigs streaming the chat-completions endpoint
// had no way to read token counts — they'd have to issue a separate
// /v1/stats poll, which doesn't correlate to a specific request.
func TestStreamChatCompletion_IncludeUsageEmitsFinalChunk(t *testing.T) {
	turnID := "turn_usage"
	sess := &Session{ID: "sess_usage"}

	ch := make(chan sse.Event, 8)
	pushEvent(t, ch, sse.TypeMessageDelta, sse.MessageDeltaPayload{TurnID: turnID, Delta: "ok"})
	pushEvent(t, ch, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: turnID, Text: "ok", FinishReason: "stop"})
	pushEvent(t, ch, sse.TypeUsage, sse.UsagePayload{TurnID: turnID, InputTokens: 123, OutputTokens: 45})
	pushEvent(t, ch, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: sse.TurnEndCompleted})
	close(ch)

	w := httptest.NewRecorder()
	streamChatCompletion(w, context.Background(), sess, turnID, "m", ch, true)
	body := w.Body.String()

	// The usage chunk MUST appear before [DONE].
	usageIdx := strings.Index(body, `"usage":{`)
	doneIdx := strings.Index(body, "data: [DONE]")
	if usageIdx < 0 {
		t.Fatalf("expected a usage chunk in the stream; body:\n%s", body)
	}
	if doneIdx < 0 {
		t.Fatalf("expected [DONE] terminator; body:\n%s", body)
	}
	if usageIdx > doneIdx {
		t.Errorf("usage chunk must come BEFORE [DONE]; usage@%d done@%d", usageIdx, doneIdx)
	}
	for _, want := range []string{
		`"prompt_tokens":123`,
		`"completion_tokens":45`,
		`"total_tokens":168`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("usage chunk missing %q; body:\n%s", want, body)
		}
	}
}

// TestStreamChatCompletion_NoUsageWithoutOptIn pins the opposite:
// when the client did NOT set stream_options.include_usage, the
// usage chunk must NOT appear — sending it to clients that didn't
// ask for it might confuse SDKs that expect choices-bearing chunks
// only.
func TestStreamChatCompletion_NoUsageWithoutOptIn(t *testing.T) {
	turnID := "turn_no_usage"
	sess := &Session{ID: "sess_no_usage"}

	ch := make(chan sse.Event, 4)
	pushEvent(t, ch, sse.TypeMessageDone, sse.MessageDonePayload{TurnID: turnID, Text: "ok", FinishReason: "stop"})
	pushEvent(t, ch, sse.TypeUsage, sse.UsagePayload{TurnID: turnID, InputTokens: 10, OutputTokens: 5})
	pushEvent(t, ch, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: sse.TurnEndCompleted})
	close(ch)

	w := httptest.NewRecorder()
	streamChatCompletion(w, context.Background(), sess, turnID, "m", ch, false)
	body := w.Body.String()

	if strings.Contains(body, `"usage":{`) {
		t.Errorf("usage chunk leaked despite include_usage=false; body:\n%s", body)
	}
}
