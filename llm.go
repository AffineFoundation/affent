// Package affent is an embeddable agent loop core. It talks directly to
// an OpenAI-compatible chat completions endpoint, dispatches tool calls
// to in-process tool handlers registered on a Registry, and streams
// per-turn events through an SSE protocol (sse package).
package affent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RetryableError marks an error from a single chat call as a candidate
// for the loop's transient-retry path. It wraps the underlying error
// (network failure, mid-stream EOF, retryable HTTP status) so the loop
// can `errors.As` to detect it without coupling to the HTTP / network
// internals here.
//
// HTTP statuses considered retryable: 408, 429, 5xx.
//
// RetryAfter, when non-zero, is the server's hint (Retry-After header)
// for how long to wait before the next attempt. The loop prefers it
// over its own exponential backoff. OpenAI / Anthropic / Cloudflare-
// fronted endpoints set it on 429 / 5xx; chutes / many self-hosted
// vLLM proxies don't, in which case the loop falls back to its
// exponential schedule.
type RetryableError struct {
	Err        error
	Status     int           // 0 if not an HTTP-status failure
	RetryAfter time.Duration // 0 if the server didn't supply Retry-After
}

func (e *RetryableError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func isRetryableStatus(code int) bool {
	if code == 408 || code == 429 {
		return true
	}
	return code >= 500 && code <= 599
}

// MaxRespectedRetryAfter caps the server-supplied wait so a misbehaving
// or hostile endpoint can't stall the loop forever ("Retry-After:
// 86400" would otherwise mean "come back tomorrow"). Hit this cap and
// we fall back to exponential backoff for that attempt.
const MaxRespectedRetryAfter = 5 * time.Minute

// Stream watchdog. Some upstreams (notably GLM tool_call mode through
// chutes) emit a finish_reason chunk and then forget to send the
// trailing [DONE] / close the connection — which leaves the scanner
// blocked until callCtx (default 3min) finally fires. The watchdog
// cuts that wait short:
//
//   - StreamIdleTimeout — max gap between any two chunks before we
//     declare the stream stalled and force-close the body. Generous,
//     because reasoning models can pause a long while between deltas
//     while they think.
//   - StreamPostFinishTimeout — how long we wait after seeing a
//     finish_reason chunk for the (optional) usage chunk + [DONE]
//     follow-up. Tight on purpose: anything past this is a server bug.
//
// vars (not consts) so tests / niche callers can override.
var (
	StreamIdleTimeout       = 60 * time.Second
	StreamPostFinishTimeout = 5 * time.Second
)

// parseRetryAfter reads the Retry-After header per RFC 7231. Both
// integer-seconds and HTTP-date forms are valid; we handle integer
// seconds (the only form OpenAI / Anthropic / chutes-style nginx
// proxies actually emit). HTTP-date is silently ignored — the caller
// will fall back to its own backoff if RetryAfter stays 0.
func parseRetryAfter(headerVal string) time.Duration {
	v := strings.TrimSpace(headerVal)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	d := time.Duration(n) * time.Second
	if d > MaxRespectedRetryAfter {
		return 0
	}
	return d
}

// LLMClient calls a chat completions endpoint that follows OpenAI's
// schema. Works with OpenAI itself, Chutes, vLLM, sglang, OpenRouter,
// LiteLLM proxy, anything OpenAI-compatible.
type LLMClient struct {
	BaseURL string // e.g. "https://llm.chutes.ai/v1"
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func NewLLMClient(baseURL, apiKey, model string) *LLMClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &LLMClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		// No global timeout; chat streams can run minutes.
		HTTP: &http.Client{},
	}
}

// ---- request shapes ----

// ChatMessage is one entry in the conversation. We use OpenAI's role names.
// Tool messages carry results back to the model; assistant messages may
// include ToolCalls the model wants us to execute.
//
// ReasoningContent captures the model's hidden chain-of-thought when the
// provider emits it (DeepSeek / Kimi / GLM-thinking style). It is local
// state — persisted in the conversation log for replay/training but
// stripped before sending the next request, since OpenAI-compat upstreams
// reject this field on inbound messages.
type ChatMessage struct {
	Role             string     `json:"role"` // "system" | "user" | "assistant" | "tool"
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"` // tool name for role=tool (some providers want it)
}

// wireMessage is the on-the-wire request shape. Mirrors ChatMessage minus
// the local-only ReasoningContent — keeps json.Marshal of chatRequest from
// leaking it back to upstream.
type wireMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

func toWireMessages(msgs []ChatMessage) []wireMessage {
	out := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		out[i] = wireMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
	}
	return out
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // stringified JSON
	} `json:"function"`
}

// ToolDef is what we hand to the model so it knows what's callable.
type ToolDef struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"` // JSON Schema
	} `json:"function"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []ToolDef     `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	// Stream usage in the final SSE chunk (OpenAI-compat extension).
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ---- streaming response ----

// StreamEvent is a single delta we emit to the caller.
type StreamEvent struct {
	// One of these will be set.
	ContentDelta      string             // assistant text chunk
	ReasoningDelta    string             // reasoning ("thinking") stream chunk for o1/GLM-5.1/etc
	ToolCallStart     *ToolCallStart     // a new tool call has begun
	ToolCallArgsDelta *ToolCallArgsDelta // arguments JSON streaming in
	Finish            *FinishInfo        // turn ended (model side)
	Err               error
}

type ToolCallStart struct {
	Index int    // some providers stream tool calls indexed
	ID    string // call id
	Name  string
}

type ToolCallArgsDelta struct {
	Index int
	Delta string // partial JSON
}

type FinishInfo struct {
	Reason       string // "stop" | "tool_calls" | "length" | ...
	InputTokens  int
	OutputTokens int
	// Final accumulated assistant message (content + complete tool calls).
	Final ChatMessage
}

// Chat opens an SSE-style streaming chat completion. The returned channel
// closes when the response ends or ctx fires. The final message (with
// fully-assembled tool calls) is delivered as the last event before close
// in StreamEvent.Finish.Final.
func (c *LLMClient) Chat(ctx context.Context, msgs []ChatMessage, tools []ToolDef) (<-chan StreamEvent, error) {
	body, _ := json.Marshal(chatRequest{
		Model:         c.Model,
		Messages:      toWireMessages(msgs),
		Tools:         tools,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Network-level failures (DNS, connection refused, mid-headers
		// reset) are nearly always worth one more shot — the loop's
		// retry budget bounds how many.
		return nil, &RetryableError{Err: fmt.Errorf("chat request: %w", err)}
	}
	if resp.StatusCode/100 != 2 {
		errBody, _ := io.ReadAll(resp.Body)
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		base := fmt.Errorf("chat http %d: %s", resp.StatusCode, errBody)
		if isRetryableStatus(resp.StatusCode) {
			return nil, &RetryableError{Err: base, Status: resp.StatusCode, RetryAfter: retryAfter}
		}
		return nil, base
	}

	out := make(chan StreamEvent, 32)
	go consumeStream(ctx, resp.Body, out)
	return out, nil
}

// consumeStream parses an OpenAI-compatible SSE stream of chat-completion
// chunks and emits StreamEvents. The shape per chunk is:
//
//	data: {"choices":[{"delta":{"content":"...","tool_calls":[...]}, "finish_reason":null}], "usage":...}
//
// Tool calls stream as deltas: each chunk may add to function.arguments
// and (rarely) reveal new tool call ids.
func consumeStream(ctx context.Context, body io.ReadCloser, out chan<- StreamEvent) {
	defer close(out)
	defer body.Close()

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// idleTimeout is reset every time a chunk arrives. If the gap
	// between chunks exceeds it, the watchdog calls body.Close() which
	// makes sc.Scan() return false on the next iteration and we exit.
	// After we see a finish_reason chunk we tighten the gap to
	// StreamPostFinishTimeout so a forgotten [DONE] from a buggy
	// upstream (notably GLM tool_calls through chutes) can't park us
	// here for the full callCtx wall-clock budget.
	idleTimeout := StreamIdleTimeout
	watchdog := time.AfterFunc(idleTimeout, func() { _ = body.Close() })
	defer watchdog.Stop()

	// Accumulator for the final assistant message. ToolCalls is grown as
	// fragments arrive; Content is the full streamed text.
	var final ChatMessage
	final.Role = "assistant"
	calls := map[int]*ToolCall{} // index -> call
	var finishReason string
	var finishSeen bool
	var inputTokens, outputTokens int

	emit := func(ev StreamEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for sc.Scan() {
		watchdog.Reset(idleTimeout)
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		// streamingToolCall carries the per-delta `index` field that the
		// persistent ToolCall doesn't need to model. *int (rather than
		// int) distinguishes "explicitly 0" from "absent" — some
		// providers omit index on every fragment.
		type streamingToolCall struct {
			Index    *int   `json:"index"`
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		var chunk struct {
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Role             string              `json:"role"`
					Content          string              `json:"content"`
					ReasoningContent string              `json:"reasoning_content"` // GLM/DeepSeek "thinking"
					ToolCalls        []streamingToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
		}

		for _, ch := range chunk.Choices {
			if ch.Delta.ReasoningContent != "" {
				final.ReasoningContent += ch.Delta.ReasoningContent
				if !emit(StreamEvent{ReasoningDelta: ch.Delta.ReasoningContent}) {
					return
				}
			}
			if ch.Delta.Content != "" {
				final.Content += ch.Delta.Content
				if !emit(StreamEvent{ContentDelta: ch.Delta.Content}) {
					return
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				// Resolve which logical tool call this fragment belongs
				// to. Spec-conformant providers (OpenAI, Anthropic-via-
				// proxy) emit `index` on every fragment. Some chutes /
				// vLLM models omit it; fall back to len(calls) for new
				// headers and "last call seen" for orphan arg deltas so
				// the single-tool-call case stays compatible.
				isHeader := tc.ID != "" || tc.Function.Name != ""
				var idx int
				switch {
				case tc.Index != nil:
					idx = *tc.Index
				case isHeader:
					idx = len(calls)
				default:
					idx = -1
					for i := range calls {
						if i > idx {
							idx = i
						}
					}
				}
				if isHeader {
					nc := &ToolCall{ID: tc.ID, Type: "function"}
					nc.Function.Name = tc.Function.Name
					calls[idx] = nc
					if !emit(StreamEvent{ToolCallStart: &ToolCallStart{Index: idx, ID: tc.ID, Name: tc.Function.Name}}) {
						return
					}
				}
				if tc.Function.Arguments != "" {
					target := calls[idx]
					if target == nil {
						// Arg fragment for a call whose header we never
						// saw (or saw under a different index). Drop —
						// reconstructing the JSON without a name would
						// just confuse the model downstream.
						continue
					}
					target.Function.Arguments += tc.Function.Arguments
					if !emit(StreamEvent{ToolCallArgsDelta: &ToolCallArgsDelta{Index: idx, Delta: tc.Function.Arguments}}) {
						return
					}
				}
			}
			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
				if !finishSeen {
					finishSeen = true
					idleTimeout = StreamPostFinishTimeout
					watchdog.Reset(idleTimeout)
				}
			}
		}
	}

	if err := sc.Err(); err != nil {
		// If we already saw the finish_reason chunk, this read error
		// most likely means the watchdog closed the body because the
		// upstream forgot to send [DONE] within StreamPostFinishTimeout.
		// That's not a failure — we have the full assistant message
		// already. Fall through to assemble + emit Finish.
		if !finishSeen {
			// Genuine mid-stream failure (TCP reset, server hung up,
			// idle stall before any finish chunk). Mark retryable so
			// the loop can take another shot.
			emit(StreamEvent{Err: &RetryableError{Err: fmt.Errorf("stream read: %w", err)}})
			return
		}
	}

	// Assemble final tool calls in ascending index order so downstream
	// callers see a stable, model-intended sequence regardless of how
	// the chunks interleaved on the wire.
	if len(calls) > 0 {
		indices := make([]int, 0, len(calls))
		for i := range calls {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		final.ToolCalls = make([]ToolCall, 0, len(calls))
		for _, i := range indices {
			final.ToolCalls = append(final.ToolCalls, *calls[i])
		}
	}

	emit(StreamEvent{Finish: &FinishInfo{
		Reason:       finishReason,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Final:        final,
	}})
}

