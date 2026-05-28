package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/google/uuid"
)

// chatRequest is the subset of OpenAI's chat-completions request body
// affentserve cares about. Extension fields (session_id /
// affent_session_id) are tolerated alongside the standard ones.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	// SessionID accepts either `session_id` (short) or
	// `affent_session_id` (the namespaced form that response chunks
	// emit). The asymmetry came up in real-LLM testing: a caller
	// copy-pasting the response field into a request used to silently
	// open a fresh session. AffentSessionID wins when both are set —
	// the namespaced field is the canonical channel and can't collide
	// with a future OpenAI extension. The X-Affent-Session-Id request
	// header takes precedence over both body fields when present —
	// proxies and middleware can pin a session without rewriting the
	// JSON body.
	SessionID       string `json:"session_id"`
	AffentSessionID string `json:"affent_session_id"`
	// StreamOptions is OpenAI's per-request streaming control. The
	// only field we honor is include_usage: when true AND stream=true,
	// the SSE response emits a final chunk with token counts before
	// [DONE]. Without it, eval rigs consuming the streaming endpoint
	// have no way to read usage — the non-streaming response already
	// has a `usage` field but switching paths just for token counts
	// is awkward.
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	// The other OpenAI knobs (temperature, top_p, stop, presence_penalty,
	// frequency_penalty, …) are accepted and dropped. affent's LLMClient
	// doesn't forward them — the upstream provider's defaults apply.
	// Surfacing them here would suggest tuning works through affent
	// when in practice it doesn't.
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// resolvedSessionID picks the affent-namespaced field when present so
// the caller's intent isn't ambiguous if a future OpenAI release ships
// a `session_id` of its own.
func (r *chatRequest) resolvedSessionID() string {
	if r.AffentSessionID != "" {
		return r.AffentSessionID
	}
	return r.SessionID
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
//     hands it to agent.SendUser. Earlier turns are already in
//     agent runtime's on-disk Conversation log, keyed by session_id.
//   - Streaming maps agent runtime's SSE event stream to OpenAI's
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
		// MaxBytesReader surfaces "body too large" distinctly. The old
		// io.LimitReader silently truncated at the limit, so a client
		// posting >4 MiB would get a misleading "decode body:
		// unexpected EOF" 400 instead of a clear "request too large"
		// signal — operators can't tell whether the client sent
		// malformed JSON or just an oversized payload.
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024*1024)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeJSONErrorTyped(w, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("request body exceeds %d-byte limit", mbe.Limit), err, "bad_request")
				return
			}
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
		loopSetupGoal := sessionLoopSetupGoalFromMessage(userText)
		if loopSetupGoal != "" && (!pool.cfg.EnableLoopProtocol || pool.cfg.EvalMode) {
			writeJSONErrorTyped(w, http.StatusConflict, "session mode unavailable", errors.New("loop protocol is not available"), "mode_unavailable")
			return
		}

		// X-Affent-Session-Id header takes precedence over body fields
		// so proxies / middleware can pin a session without rewriting
		// the JSON body. Falls back to body.affent_session_id, then
		// body.session_id, then empty (= fresh session).
		sessionID := r.Header.Get("X-Affent-Session-Id")
		if sessionID == "" {
			sessionID = req.resolvedSessionID()
		}
		sess, err := pool.GetOrCreate(sessionID)
		if err != nil {
			if errors.Is(err, ErrShuttingDown) {
				w.Header().Set("Retry-After", "5")
				writeJSONError(w, http.StatusServiceUnavailable, "server shutting down", err)
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "create session", err)
			return
		}
		turnOpts := agent.TurnOptions{}
		if loopSetupGoal != "" {
			if _, err := sess.ensureLoopProtocolInitializedWithCreated(loopSetupGoal); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "initialize loop protocol", err)
				return
			}
			userText = sessionLoopSetupPrompt(loopSetupGoal)
			turnOpts.UserMode = sessionMessageModeLoopSetup
			turnOpts.ForceLoopCalibrationQuestion = true
			turnOpts.UserDisplayText = sessionLoopSetupDisplayText(loopSetupGoal)
		}

		// Subscribe BEFORE SendUser so we never miss the turn.start
		// event. Generous buffer; fanout drops events to slow
		// subscribers, not blocks the loop.
		subID, subCh := sess.Subscribe(512)
		defer sess.Unsubscribe(subID)

		ctx := r.Context()
		turnID, err := sess.SendUserWithOptions(ctx, userText, turnOpts)
		if err != nil {
			switch {
			case errors.Is(err, agent.ErrTurnInFlight):
				w.Header().Set("Retry-After", "1")
				writeJSONError(w, http.StatusConflict, "session busy", err)
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// Client disconnected between body-parse and SendUser.
				// 499 is nginx's "client closed request"; OpenAI clients
				// won't see it (the conn is dead) but it gives proxies
				// and logs the right signal.
				writeJSONError(w, 499, "client disconnected", err)
			default:
				// e.g. conv.Append failed — disk full, permission
				// denied. Don't tell the caller it's busy when the
				// server itself failed.
				writeJSONError(w, http.StatusInternalServerError, "send user", err)
			}
			return
		}

		// The configured cfg.Model is the actual backend being driven
		// (the LLMClient was wired with it at session-build time). When
		// it's set, that's what the response label has to say — echoing
		// the client's req.Model would be a lie if the two diverge, and
		// /v1/models already truthfully advertises cfg.Model so a client
		// comparing the two would catch the inconsistency. Only fall
		// through to req.Model when cfg.Model is empty (rare; means the
		// operator didn't configure a default and is letting the client
		// choose). The "affent" floor is the last-resort label so the
		// response object is never empty.
		modelLabel := cfg.Model
		if modelLabel == "" {
			modelLabel = req.Model
		}
		if modelLabel == "" {
			modelLabel = "affent"
		}

		if req.Stream {
			includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
			streamChatCompletion(w, ctx, sess, turnID, modelLabel, subCh, includeUsage)
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
// captured in agent runtime's Conversation log keyed by session_id.
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
			case sse.TypeMessageDone:
				// Capture the upstream-model's finish_reason from the
				// final assistant message so a max_tokens truncation
				// surfaces as "length" instead of being flattened to
				// "stop" by the turn-end mapping below. Ignore
				// "tool_calls" — that's an intermediate step, the
				// next message.done from the model's continuation
				// is the one that carries the terminal reason.
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					if p.FinishReason == "tool_calls" {
						out.Content = ""
						continue
					}
					if p.Text != "" {
						// Non-streaming OpenAI responses should expose the
						// terminal assistant answer, not concatenate every
						// progress note a model emitted before tool calls.
						out.Content = p.Text
					}
					if p.FinishReason != "" {
						out.FinishReason = p.FinishReason
					}
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
					// Turn-end mapping only overrides the model-level
					// finish_reason captured above when the turn ended
					// for an affent-specific reason (max_turns,
					// cancelled, error). A clean "completed" turn keeps
					// the upstream-model value so "length" doesn't get
					// silently rewritten to "stop".
					if p.Reason != sse.TurnEndCompleted {
						out.FinishReason = openAIFinishReason(p.Reason)
					}
					if p.Reason == sse.TurnEndError && out.Error == "" {
						out.Error = "turn ended with error"
					}
				}
				return out, nil
			}
		}
	}
}

// openAIFinishReason maps agent runtime's TurnEnd reason vocabulary onto the
// OpenAI chat-completions `finish_reason` field. The two systems
// don't fully overlap — affent has step-limit and cancelled, OpenAI
// has length and content_filter — so we pick the closest semantic.
//
// max_turns → "length" because both mean "ran out of budget"; clients
// that retry on length also want to retry on max_turns. Everything
// else collapses to "stop" since OpenAI lacks a dedicated cancelled
// or error code in finish_reason.
func openAIFinishReason(turnEndReason string) string {
	switch turnEndReason {
	case sse.TurnEndMaxTurns:
		return "length"
	default:
		return "stop"
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
	// Mirror the streaming-path header so clients that ignore the
	// non-standard affent_session_id JSON field can still find it.
	w.Header().Set("X-Affent-Session-Id", sessionID)
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

// streamChatCompletion translates agent runtime's SSE stream into OpenAI's
// chat.completion.chunk protocol, line by line. On client disconnect
// (ctx.Done) we propagate cancellation into the affent Loop so the
// browser / LLM doesn't keep churning with no listener.
func streamChatCompletion(w http.ResponseWriter, ctx context.Context, sess *Session, turnID, model string, ch <-chan sse.Event, includeUsage bool) {
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
	// Surface the session id as a response header too, so clients that
	// only consume the event stream (and never JSON-decode the
	// affent_session_id field on each chunk) can still pin to and
	// resume this session via Last-Used / DELETE.
	w.Header().Set("X-Affent-Session-Id", sessionID)

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

	// Track the model's last terminal finish_reason from message.done
	// events so a max_tokens truncation surfaces as "length" instead
	// of being flattened to "stop" by the turn-end mapping. See the
	// parallel logic in bufferChatCompletion.
	modelFinish := ""

	// Per-turn token totals captured from the Loop's single
	// TypeUsage event. Only forwarded to the client if they
	// requested stream_options.include_usage. The chunk shape
	// matches OpenAI's: empty choices array + a usage object,
	// emitted between the last delta-bearing chunk and [DONE].
	var inputTokens, outputTokens int

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
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					if p.FinishReason != "" && p.FinishReason != "tool_calls" {
						modelFinish = p.FinishReason
					}
				}
			case sse.TypeThinkingDelta:
				var p sse.ThinkingDeltaPayload
				if err := json.Unmarshal(ev.Data, &p); err == nil && p.Delta != "" {
					send(map[string]any{"reasoning_content": p.Delta}, "")
				}
			case sse.TypeUsage:
				var p struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				}
				if err := json.Unmarshal(ev.Data, &p); err == nil {
					inputTokens += p.InputTokens
					outputTokens += p.OutputTokens
				}
			case sse.TypeError:
				// Surface non-recoverable loop errors as an OpenAI-shape
				// error chunk before closing the stream. Recoverable
				// errors are part of the retry path and not signaled to
				// the client.
				var p struct {
					Message     string `json:"message"`
					Recoverable bool   `json:"recoverable"`
				}
				if err := json.Unmarshal(ev.Data, &p); err == nil && !p.Recoverable && p.Message != "" {
					raw, _ := json.Marshal(map[string]any{
						"id":      id,
						"object":  "chat.completion.chunk",
						"created": created,
						"model":   model,
						"error": map[string]any{
							"message": p.Message,
							"type":    "loop_error",
						},
					})
					_, _ = w.Write([]byte("data: "))
					_, _ = w.Write(raw)
					_, _ = w.Write([]byte("\n\n"))
					flusher.Flush()
				}
			case sse.TypeTurnEnd:
				var p struct {
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal(ev.Data, &p)
				// Prefer the upstream-model's finish_reason on clean
				// "completed" turns so max_tokens truncation is visible.
				// affent-specific reasons (max_turns, cancelled, error)
				// override since they describe something the model
				// itself didn't see.
				finish := openAIFinishReason(p.Reason)
				if p.Reason == sse.TurnEndCompleted && modelFinish != "" {
					finish = modelFinish
				}
				send(map[string]any{}, finish)
				if includeUsage {
					// OpenAI's stream_options.include_usage contract:
					// a final chunk with empty choices and a populated
					// usage object, BEFORE [DONE]. SDKs that opt in
					// know to skip the empty-choices chunk for content
					// and read usage from it instead.
					usageChunk, _ := json.Marshal(map[string]any{
						"id":                id,
						"object":            "chat.completion.chunk",
						"created":           created,
						"model":             model,
						"affent_session_id": sessionID,
						"choices":           []any{},
						"usage": map[string]any{
							"prompt_tokens":     inputTokens,
							"completion_tokens": outputTokens,
							"total_tokens":      inputTokens + outputTokens,
						},
					})
					_, _ = w.Write([]byte("data: "))
					_, _ = w.Write(usageChunk)
					_, _ = w.Write([]byte("\n\n"))
					flusher.Flush()
				}
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		}
	}
}
