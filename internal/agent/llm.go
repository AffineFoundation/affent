// Package agent contains Affent's internal runtime loop: chat-completions
// streaming, conversation persistence, tool dispatch, memory, and compaction.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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

var retryableHTTPStatuses = map[int]bool{
	408: true, // Request Timeout
	429: true, // Too Many Requests
}

var errStreamEndedWithoutFinish = errors.New("stream ended without finish")
var errStreamIdleTimeout = errors.New("stream idle timeout")

func isRetryableStatus(code int) bool {
	if retryableHTTPStatuses[code] {
		return true
	}
	return code >= 500 && code <= 599
}

// MaxRespectedRetryAfter caps the server-supplied wait so a misbehaving
// or hostile endpoint can't stall the loop forever ("Retry-After:
// 86400" would otherwise mean "come back tomorrow"). Hit this cap and
// we fall back to exponential backoff for that attempt.
const MaxRespectedRetryAfter = 5 * time.Minute

const (
	maxLLMErrorBodyBytes   = 64 * 1024
	maxLLMRequestBodyBytes = 8 * 1024 * 1024
	streamEventChanBuffer  = 32
	streamScannerInitBytes = 64 * 1024
	streamScannerMaxBytes  = 4 * 1024 * 1024
)

// Stream accumulation safety caps. These are runtime guardrails, not
// sampling knobs: they bound memory held while assembling one upstream
// response before the agent loop can apply conversation/event caps.
const (
	maxStreamContentBytes   = 1 * 1024 * 1024
	maxStreamReasoningBytes = 1 * 1024 * 1024
	maxStreamToolArgBytes   = 1 * 1024 * 1024
	maxStreamToolCalls      = 64
)

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
	// Sampling is forwarded to upstream on every Chat call when fields
	// are non-nil. Affent itself doesn't tune anything — this is the
	// pass-through hook operators / eval drivers use to pin
	// temperature=0 for determinism, bound max_tokens, etc. Per-request
	// overrides aren't supported yet; this is a per-LLMClient default.
	Sampling SamplingDefaults
}

// SamplingDefaults carries the OpenAI-shape sampling knobs forwarded on
// every Chat call. Pointers distinguish "unset → omit field → upstream
// default" from "explicitly zero → forward 0 → deterministic decode".
// temperature=0 is a meaningful eval value and must not be confused
// with "no preference".
type SamplingDefaults struct {
	Temperature *float64
	TopP        *float64
	MaxTokens   *int
	Seed        *int64
}

func (s SamplingDefaults) Validate() error {
	if s.Temperature != nil {
		t := *s.Temperature
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
	}
	if s.TopP != nil {
		t := *s.TopP
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
	}
	if s.MaxTokens != nil && *s.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be a positive integer")
	}
	return nil
}

const DefaultBaseURL = "https://api.openai.com/v1"

func NewLLMClient(baseURL, apiKey, model string) *LLMClient {
	if baseURL == "" {
		baseURL = DefaultBaseURL
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
			ToolCalls:  sanitizeToolCallArgs(m.ToolCalls),
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
	}
	return out
}

// ensureToolCallIDs guarantees every ToolCall has a non-empty ID,
// generating one when the upstream model omitted it. Some providers
// (notably non-OpenAI ones routed through proxies — observed on
// DeepSeek tool-call mode and certain chutes-hosted models) emit a
// tool_calls fragment with the function name but no `id`. Without
// this fix the persistence path leaves ID="", runTurn locally
// generates a fresh "call_xxx" for the tool response, and the
// resulting (assistant.tool_calls[id=""], tool[tool_call_id="call_xxx"])
// pair fails the linkage check every strict OpenAI-compat backend
// applies on the NEXT request — turning one missing id into a
// permanently-broken session.
//
// Mutates in place — the assistant ChatMessage will be persisted
// with these IDs, so subsequent dispatch and the future wire copy
// see the same value.
func ensureToolCallIDs(calls []ToolCall) {
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = "call_" + uuid.NewString()
		}
	}
}

// sanitizeToolCallArgs replaces unparseable tool_call.function.arguments
// with "{}" before they go on the wire. Strict OpenAI-compat upstreams
// reject the entire chat completion with HTTP 400 (e.g. "function.arguments
// parameter must be in JSON format") when a prior assistant message has
// a malformed args string — which happens when
// the model is cut off mid-tool-call (max_tokens hit, output cap, or
// just a flaky decode). Without this guard, one bad tool_call from
// the model permanently bricks the turn: every subsequent step gets
// the same 400, the loop bails with reason=error, and the model never
// gets a chance to retry with cleaner args.
//
// The on-disk Conversation log keeps the original malformed string for
// debug / replay; only the wire copy is normalized. The matching
// tool.result already carries the "decode args" error message so the
// model can see what went wrong and correct itself.
//
// Returns the slice verbatim (no allocation) when nothing needs fixing
// — the common case is every tool_call is well-formed.
func sanitizeToolCallArgs(in []ToolCall) []ToolCall {
	if len(in) == 0 {
		return in
	}
	var out []ToolCall
	for i := range in {
		args := in[i].Function.Arguments
		if args != "" && json.Valid([]byte(args)) {
			continue
		}
		if out == nil {
			out = append([]ToolCall(nil), in...)
		}
		out[i].Function.Arguments = "{}"
	}
	if out == nil {
		return in
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
	Model      string        `json:"model"`
	Messages   []wireMessage `json:"messages"`
	Tools      []ToolDef     `json:"tools,omitempty"`
	ToolChoice string        `json:"tool_choice,omitempty"`
	Stream     bool          `json:"stream"`
	// Stream usage in the final SSE chunk (OpenAI-compat extension).
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	// Sampling knobs forwarded from LLMClient.Sampling when non-nil.
	// omitempty on pointer types omits the field when the pointer is
	// nil, so the wire request only carries values the operator
	// actually set — a temperature=0 value (explicitly set to 0) still
	// makes it through because the pointer is non-nil.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
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
	if estimateChatRequestBodyBytes(c.Model, msgs, tools) > maxLLMRequestBodyBytes {
		return nil, fmt.Errorf("chat request body exceeds %d-byte cap before marshal; context window likely too large", maxLLMRequestBodyBytes)
	}
	reqBody := chatRequest{
		Model:         c.Model,
		Messages:      toWireMessages(msgs),
		Tools:         tools,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Temperature:   c.Sampling.Temperature,
		TopP:          c.Sampling.TopP,
		MaxTokens:     c.Sampling.MaxTokens,
		Seed:          c.Sampling.Seed,
	}
	if len(tools) == 0 {
		reqBody.ToolChoice = "none"
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	if len(body) > maxLLMRequestBodyBytes {
		return nil, fmt.Errorf("chat request body is %d bytes; exceeds %d-byte cap; context window likely too large", len(body), maxLLMRequestBodyBytes)
	}
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
		// Cap the error-body read. Real upstream error envelopes
		// (OpenAI, Anthropic, vLLM, sglang) are well under 64 KiB; an
		// unbounded ReadAll would let a hostile or misconfigured
		// endpoint OOM the loop by serving a multi-GB HTML error page
		// on a 502.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMErrorBodyBytes))
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		base := fmt.Errorf("chat http %d: %s", resp.StatusCode, errBody)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			base = fmt.Errorf(
				"chat auth failed (status=%d endpoint=%s model=%s): %w\nNext: verify the API key is valid for this endpoint, and that the model name exists on the upstream provider",
				resp.StatusCode, c.BaseURL+"/chat/completions", c.Model, base,
			)
		}
		if isRetryableStatus(resp.StatusCode) {
			return nil, &RetryableError{Err: base, Status: resp.StatusCode, RetryAfter: retryAfter}
		}
		return nil, base
	}

	out := make(chan StreamEvent, streamEventChanBuffer)
	go consumeStream(ctx, resp.Body, out)
	return out, nil
}

func estimateChatRequestBodyBytes(model string, msgs []ChatMessage, tools []ToolDef) int {
	n := 256 + len(model)
	for _, m := range msgs {
		n += 64 + len(m.Role) + len(m.Content) + len(m.ToolCallID) + len(m.Name)
		for _, tc := range m.ToolCalls {
			n += 96 + len(tc.ID) + len(tc.Type) + len(tc.Function.Name) + len(tc.Function.Arguments)
		}
		if n > maxLLMRequestBodyBytes {
			return n
		}
	}
	for _, t := range tools {
		n += 128 + len(t.Type) + len(t.Function.Name) + len(t.Function.Description) + len(t.Function.Parameters)
		if n > maxLLMRequestBodyBytes {
			return n
		}
	}
	return n
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
	sc.Buffer(make([]byte, 0, streamScannerInitBytes), streamScannerMaxBytes)

	// idleTimeout is reset every time a chunk arrives. If the gap
	// between chunks exceeds it, the watchdog calls body.Close() which
	// makes sc.Scan() return false on the next iteration and we exit.
	// After we see a finish_reason chunk we tighten the gap to
	// StreamPostFinishTimeout so a forgotten [DONE] from a buggy
	// upstream (notably GLM tool_calls through chutes) can't park us
	// here for the full callCtx wall-clock budget.
	idleTimeout := StreamIdleTimeout
	var watchdogFired atomic.Bool
	watchdog := time.AfterFunc(idleTimeout, func() {
		watchdogFired.Store(true)
		_ = body.Close()
	})
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
				if len(final.ReasoningContent)+len(ch.Delta.ReasoningContent) > maxStreamReasoningBytes {
					emit(StreamEvent{Err: fmt.Errorf("stream reasoning_content exceeds %d-byte cap", maxStreamReasoningBytes)})
					return
				}
				final.ReasoningContent += ch.Delta.ReasoningContent
				if !emit(StreamEvent{ReasoningDelta: ch.Delta.ReasoningContent}) {
					return
				}
			}
			if ch.Delta.Content != "" {
				if len(final.Content)+len(ch.Delta.Content) > maxStreamContentBytes {
					emit(StreamEvent{Err: fmt.Errorf("stream assistant content exceeds %d-byte cap", maxStreamContentBytes)})
					return
				}
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
					if _, exists := calls[idx]; !exists && len(calls) >= maxStreamToolCalls {
						emit(StreamEvent{Err: fmt.Errorf("stream tool_calls exceeds %d-call cap", maxStreamToolCalls)})
						return
					}
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
					if len(target.Function.Arguments)+len(tc.Function.Arguments) > maxStreamToolArgBytes {
						emit(StreamEvent{Err: fmt.Errorf("stream tool call arguments exceed %d-byte cap", maxStreamToolArgBytes)})
						return
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
			if watchdogFired.Load() {
				emit(StreamEvent{Err: &RetryableError{Err: streamIdleTimeoutError(StreamIdleTimeout)}})
				return
			}
			// Genuine mid-stream failure (TCP reset, server hung up,
			// idle stall before any finish chunk). Mark retryable so
			// the loop can take another shot.
			emit(StreamEvent{Err: &RetryableError{Err: fmt.Errorf("stream read: %w", err)}})
			return
		}
	}
	if !finishSeen {
		if watchdogFired.Load() {
			emit(StreamEvent{Err: &RetryableError{Err: streamIdleTimeoutError(StreamIdleTimeout)}})
			return
		}
		emit(StreamEvent{Err: &RetryableError{Err: errStreamEndedWithoutFinish}})
		return
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

func streamIdleTimeoutError(timeout time.Duration) error {
	return fmt.Errorf("stream idle timeout after %s before finish_reason; no SSE chunk arrived within StreamIdleTimeout: %w", timeout, errStreamIdleTimeout)
}
