package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/affinefoundation/affent/sse"
	"github.com/google/uuid"
)

// chatRequest is the subset of OpenAI's chat-completions request body
// affentserve cares about. Extension fields (session_id) are tolerated
// alongside the standard ones.
type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	SessionID string        `json:"session_id"`
	// We accept and ignore the other OpenAI knobs (temperature, top_p,
	// stop, etc.) because affent owns sampling via its LLMClient.
	// Surfacing them here would only be misleading.
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// handleChatCompletions wires the OpenAI chat-completions contract.
//
// Behavior:
//   - The full message history is NOT replayed into affent on every
//     request. The server treats `messages` like a conversation
//     identifier: it pulls the LAST user message from the array and
//     hands it to affent.SendUser. Earlier turns are already in
//     affent's on-disk Conversation log, keyed by session_id.
//   - Streaming maps affent's SSE event stream to OpenAI's
//     chat.completion.chunk delta protocol. Reasoning lands in
//     `delta.reasoning_content` (the DeepSeek/Kimi convention). Tool
//     calls do NOT appear in the OpenAI stream — they're internal to
//     the loop. Consumers who want them subscribe to
//     /v1/sessions/{id}/events.
//   - Non-streaming buffers content and returns a single OpenAI
//     response object at turn.end.
func handleChatCompletions(cfg Config, pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONErrorTyped(w, http.StatusMethodNotAllowed, "method not allowed", nil, "bad_request")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "read body", err)
			return
		}
		var req chatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "decode body", err)
			return
		}
		if len(req.Messages) == 0 {
			writeJSONError(w, http.StatusBadRequest, "messages must be non-empty", nil)
			return
		}
		userText := extractLastUser(req.Messages)
		if userText == "" {
			writeJSONError(w, http.StatusBadRequest, "last message must have role=user with non-empty content", nil)
			return
		}

		sess, err := pool.GetOrCreate(req.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "create session", err)
			return
		}

		// Subscribe BEFORE SendUser so we never miss the turn.start
		// event. Generous buffer; fanout drops events to slow
		// subscribers, not blocks the loop.
		subID, subCh := sess.Subscribe(512)
		defer sess.Unsubscribe(subID)

		ctx := r.Context()
		turnID, err := sess.SendUser(ctx, userText)
		if err != nil {
			writeJSONError(w, http.StatusConflict, "session busy", err)
			return
		}

		modelLabel := req.Model
		if modelLabel == "" {
			modelLabel = cfg.Model
		}
		if modelLabel == "" {
			modelLabel = "affent"
		}

		if req.Stream {
			streamChatCompletion(w, ctx, sess, turnID, modelLabel, subCh)
			return
		}
		out, err := bufferChatCompletion(ctx, sess, turnID, subCh)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "run turn", err)
			return
		}
		writeChatCompletionResponse(w, sess.ID, modelLabel, out)
	}
}

// extractLastUser pulls the rightmost role=user message's content out
// of a chat history. We treat the rest of the history as already
// captured in affent's Conversation log keyed by session_id.
func extractLastUser(msgs []chatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// bufferedTurnResult is what bufferChatCompletion produces and what
// the non-streaming response uses.
type bufferedTurnResult struct {
	Content          string
	ReasoningContent string
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	Error            string
}

// bufferChatCompletion drains events for a single turn until turn.end
// or the request context is cancelled. On cancellation we propagate
// the cancel into the affent Loop (sess.CancelTurn) so the browser /
// LLM call doesn't keep running to MaxTurnSteps with no listener —
// matters at benchmark scale where many questions may time out.
func bufferChatCompletion(ctx context.Context, sess *Session, turnID string, ch <-chan sse.Event) (*bufferedTurnResult, error) {
	out := &bufferedTurnResult{FinishReason: "stop"}
	for {
		select {
		case <-ctx.Done():
			sess.CancelTurn()
			return out, ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return out, errors.New("session event stream closed before turn.end")
			}
			if !eventForTurn(ev, turnID) {
				continue
			}
			switch ev.Type {
			case sse.TypeMessageDelta:
				var p sse.MessageDeltaPayload
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					out.Content += p.Delta
				}
			case sse.TypeThinkingDelta:
				var p sse.ThinkingDeltaPayload
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					out.ReasoningContent += p.Delta
				}
			case sse.TypeUsage:
				var p struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				}
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					out.PromptTokens += p.InputTokens
					out.CompletionTokens += p.OutputTokens
				}
			case sse.TypeError:
				var p struct {
					Message     string `json:"message"`
					Recoverable bool   `json:"recoverable"`
				}
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					if !p.Recoverable {
						out.Error = p.Message
					}
				}
			case sse.TypeTurnEnd:
				var p struct {
					Reason string `json:"reason"`
				}
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					switch p.Reason {
					case "cancelled":
						out.FinishReason = "stop"
					case "error":
						out.FinishReason = "stop"
						if out.Error == "" {
							out.Error = "turn ended with error"
						}
					default:
						out.FinishReason = "stop"
					}
				}
				return out, nil
			}
		}
	}
}

// eventForTurn filters to events tagged with the given turn id. Most
// payloads embed it via the leading {"turn_id": "..."} field.
func eventForTurn(ev sse.Event, turnID string) bool {
	var p struct {
		TurnID string `json:"turn_id"`
	}
	if err := json.Unmarshal(ev.Data, &p); err != nil {
		return false
	}
	return p.TurnID == turnID
}

func writeChatCompletionResponse(w http.ResponseWriter, sessionID, model string, out *bufferedTurnResult) {
	w.Header().Set("Content-Type", "application/json")
	if out.Error != "" {
		writeJSONError(w, http.StatusBadGateway, "loop error: "+out.Error, nil)
		return
	}
	resp := map[string]any{
		"id":                "chatcmpl-" + uuid.NewString(),
		"object":            "chat.completion",
		"created":           time.Now().Unix(),
		"model":             model,
		"affent_session_id": sessionID,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":              "assistant",
					"content":           out.Content,
					"reasoning_content": out.ReasoningContent,
				},
				"finish_reason": out.FinishReason,
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     out.PromptTokens,
			"completion_tokens": out.CompletionTokens,
			"total_tokens":      out.PromptTokens + out.CompletionTokens,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// streamChatCompletion translates affent's SSE stream into OpenAI's
// chat.completion.chunk protocol, line by line. On client disconnect
// (ctx.Done) we propagate cancellation into the affent Loop so the
// browser / LLM doesn't keep churning with no listener.
func streamChatCompletion(w http.ResponseWriter, ctx context.Context, sess *Session, turnID, model string, ch <-chan sse.Event) {
	sessionID := sess.ID
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported by writer", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	id := "chatcmpl-" + uuid.NewString()
	created := time.Now().Unix()

	send := func(delta map[string]any, finish string) {
		choice := map[string]any{
			"index": 0,
			"delta": delta,
		}
		if finish != "" {
			choice["finish_reason"] = finish
		}
		chunk := map[string]any{
			"id":                id,
			"object":            "chat.completion.chunk",
			"created":           created,
			"model":             model,
			"affent_session_id": sessionID,
			"choices":           []any{choice},
		}
		raw, _ := json.Marshal(chunk)
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
	}

	send(map[string]any{"role": "assistant"}, "")

	keepAlive := time.NewTicker(sseKeepAliveInterval)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			sess.CancelTurn()
			return
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				sess.CancelTurn()
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !eventForTurn(ev, turnID) {
				continue
			}
			switch ev.Type {
			case sse.TypeMessageDelta:
				var p sse.MessageDeltaPayload
				if err := json.Unmarshal(ev.Data, &p); err == nil && p.Delta != "" {
					send(map[string]any{"content": p.Delta}, "")
				}
			case sse.TypeThinkingDelta:
				var p sse.ThinkingDeltaPayload
				if err := json.Unmarshal(ev.Data, &p); err == nil && p.Delta != "" {
					send(map[string]any{"reasoning_content": p.Delta}, "")
				}
			case sse.TypeTurnEnd:
				var p struct {
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal(ev.Data, &p)
				finish := "stop"
				if p.Reason == "error" {
					finish = "stop"
				}
				send(map[string]any{}, finish)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		}
	}
}
