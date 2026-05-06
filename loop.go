package affent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/affinefoundation/affent/sse"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// DefaultPerCallTimeout caps how long a single chat completion (one
// round of the inner loop) may take. Without a cap, a misbehaving /
// disconnected LLM endpoint can leave the turn hung forever, which
// then blocks subsequent cron fires with ErrTurnInFlight.
//
// Override per-Loop via Loop.PerCallTimeout.
const DefaultPerCallTimeout = 3 * time.Minute

// DefaultTransientRetries / DefaultTransientBackoff govern how the
// loop reacts to LLM call failures the caller probably can't fix
// (HTTP 408/429/5xx, network resets, mid-stream EOF, per-call
// timeout). The loop emits an error event with Recoverable=true and
// retries the same step after backoff*2^attempt.
const (
	DefaultTransientRetries = 3
	DefaultTransientBackoff = 4 * time.Second
)

// PerLLMCallTimeout is the legacy name for DefaultPerCallTimeout.
// Kept as an alias so external callers that referenced the old
// constant don't break.
const PerLLMCallTimeout = DefaultPerCallTimeout

// MaxToolResultBytesInContext caps how much of a tool's output we feed
// back into the LLM as conversation history. The full result still goes
// out in the SSE event (so the UI can show whatever it wants), but the
// model only sees this prefix. Without a cap, a single `curl` of a
// large API response can balloon the prompt for every subsequent turn.
const MaxToolResultBytesInContext = 8 * 1024

// MaxToolResultPreviewInEvent is what we put in the tool.result event
// payload's result_summary. Bigger than the in-context cap is fine
// because front-ends might want to render more for the user even if
// the model doesn't see it; smaller is fine too. 4 KiB is a comfortable
// chat-bubble length.
const MaxToolResultPreviewInEvent = 4 * 1024

// Loop is the model<->tools cycle. One Loop per session. Stateful via the
// attached Conversation; tools are looked up in Tools.
type Loop struct {
	LLM          *LLMClient
	Tools        *Registry
	Conv         *Conversation
	Events       chan<- sse.Event
	Log          zerolog.Logger
	MaxTurnSteps int // assistant<->tool round trips per user turn (default 16)

	// PerCallTimeout overrides DefaultPerCallTimeout for this loop.
	// Zero means "use the default".
	PerCallTimeout time.Duration

	// MaxTransientRetries is how many times to retry a single LLM call
	// on a transient error (HTTP 408/429/5xx, net resets, per-call
	// timeout, mid-stream EOF). Zero falls back to
	// DefaultTransientRetries; negative disables retry entirely.
	MaxTransientRetries int

	// TransientBackoff is the initial wait between retries; each
	// subsequent attempt doubles it. Zero means DefaultTransientBackoff.
	TransientBackoff time.Duration

	mu       sync.Mutex
	current  string // currently active turn_id; empty if idle
	cancelFn context.CancelFunc

	// eventSeq numbers every published event monotonically per loop.
	// Lets trace consumers detect drops and order events independently
	// of any downstream ring buffer's own ID space.
	eventSeq atomic.Int64
}

// SystemPrompt is fed once at session start. Kept short; the model figures
// most things out from tool descriptions.
const DefaultSystemPrompt = `You are the user's general-purpose agent inside their personal "dev box": a
persistent /home/agent and /workspace bind-mounted into a Docker container.
You have a 'shell' tool for arbitrary bash commands and 'read_file' /
'write_file' / 'edit_file' / 'list_files' for the workspace.

When the user asks to schedule, automate, or repeat something on a cron-like
cadence, use 'schedule_create' (and 'schedule_list' / 'schedule_set_enabled'
/ 'schedule_delete' for management). Schedules created this way appear in
the user's Cron tab and each fire opens a new turn in a session bound to
that cron job. Do NOT roll your own cron via 'shell' (don't write crontab
files, don't background a sleep loop) -- the schedule_* tools are the
right path.

Tool budget: each turn caps at ~10 tool calls. Most models drift into
"one more search" loops. After 5 tool calls in a turn, lean toward
answering with what you already have rather than fetching more. Going
past 8 calls is almost always wrong; if you genuinely need more, tell
the user what's missing and ask for guidance.

Tool outputs are truncated for your context after ~8KB. If you see a
"[... N more bytes truncated]" marker and need the rest, re-run the
command piping through head/tail/grep/sed, or save the output to a file
under /workspace and read it in chunks.

Be concise. When given a task, execute it; don't lecture. Use the shell
freely for git, curl, python, node, builds, installs -- the box is the
user's, you can write to /home/agent/.local/, /home/agent/.cache/, the
whole /workspace tree, and /tmp. The container's rootfs is read-only, so
'apt-get install' won't work; use 'pip install --user' / 'uv tool install'
/ 'npm install' (local) instead.

Don't promise things you didn't actually do. Don't claim a file exists
without checking. After running a tool, report what you saw.
`

// EnsureSystemPrompt prepends the system prompt if the conversation is
// empty. Idempotent.
func (l *Loop) EnsureSystemPrompt(prompt string) error {
	if len(l.Conv.Snapshot()) > 0 {
		return nil
	}
	if prompt == "" {
		prompt = DefaultSystemPrompt
	}
	return l.Conv.Append(ChatMessage{Role: "system", Content: prompt})
}

// SendUser kicks off one turn for the given user message. Returns the
// turn_id once accepted; the actual work runs in a goroutine and emits
// events on Events. ErrTurnInFlight is returned if a turn is still alive.
func (l *Loop) SendUser(ctx context.Context, text string) (string, error) {
	l.mu.Lock()
	if l.current != "" {
		l.mu.Unlock()
		return "", ErrTurnInFlight
	}
	turnID := "turn_" + uuid.NewString()
	l.current = turnID
	turnCtx, cancel := context.WithCancel(context.Background())
	l.cancelFn = cancel
	l.mu.Unlock()

	if err := l.Conv.Append(ChatMessage{Role: "user", Content: text}); err != nil {
		l.takeTurn()
		cancel()
		return "", err
	}

	go func() {
		defer func() {
			l.takeTurn()
			cancel()
		}()
		l.runTurn(turnCtx, turnID, text)
	}()
	return turnID, nil
}

// Cancel aborts the current turn if any.
func (l *Loop) Cancel() {
	l.mu.Lock()
	cancel := l.cancelFn
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (l *Loop) takeTurn() {
	l.mu.Lock()
	l.current = ""
	l.cancelFn = nil
	l.mu.Unlock()
}

// runTurn loops assistant<->tool calls until the model emits a final
// answer (no tool calls), or we hit MaxTurnSteps.
func (l *Loop) runTurn(ctx context.Context, turnID, userText string) {
	steps := l.MaxTurnSteps
	if steps <= 0 {
		steps = 10
	}

	l.publish(sse.TypeTurnStart, sse.TurnStartPayload{TurnID: turnID})
	// Mirror the user's text into the event stream so SSE replays show
	// the full conversation, not just assistant output.
	l.publish(sse.TypeUserMessage, sse.UserMessagePayload{TurnID: turnID, Text: userText})

	totalIn, totalOut := 0, 0
	endReason := sse.TurnEndCompleted

	for step := 0; step < steps; step++ {
		if ctx.Err() != nil {
			endReason = sse.TurnEndCancelled
			break
		}

		final, reason, err := l.runStep(ctx, turnID)
		if err != nil {
			endReason = reason
			break
		}
		if final == nil {
			break
		}
		totalIn += final.InputTokens
		totalOut += final.OutputTokens

		if len(final.Final.ToolCalls) == 0 {
			break
		}

		// Execute every tool call in order, append each result to
		// conversation, then loop back to ask the model for the next step.
		for _, tc := range final.Final.ToolCalls {
			callID := tc.ID
			if callID == "" {
				callID = "call_" + uuid.NewString()
			}
			args := json.RawMessage(tc.Function.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			var argsView map[string]any
			_ = json.Unmarshal(args, &argsView)

			l.publish(sse.TypeToolRequest, sse.ToolRequestPayload{
				TurnID: turnID, CallID: callID, Tool: tc.Function.Name, Args: argsView,
			})
			result := l.Tools.dispatch(ctx, tc.Function.Name, args)
			isErr := false
			if len(result) >= 6 && result[:6] == "Error:" {
				isErr = true
			}
			exit := 0
			if isErr {
				exit = 1
			}
			l.publish(sse.TypeToolResult, sse.ToolResultPayload{
				CallID: callID, ExitCode: exit, ResultSummary: previewN(result, MaxToolResultPreviewInEvent),
			})
			_ = l.Conv.Append(ChatMessage{
				Role:       "tool",
				Content:    truncateForContext(result, MaxToolResultBytesInContext),
				ToolCallID: callID,
				Name:       tc.Function.Name,
			})
		}
	}

	l.publish(sse.TypeUsage, sse.UsagePayload{TurnID: turnID, InputTokens: totalIn, OutputTokens: totalOut})
	l.publish(sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: endReason})
}

// consumeAndPersist drains a single LLM streaming call: emits
// message.delta + tool.request placeholders for fragments, persists
// the final assistant message in the conversation log, and returns
// the FinishInfo. The bool return reports whether any visible
// assistant content (message.delta) was streamed before the result —
// the loop uses it to decide whether a stream-cut error is safe to
// retry. (Reasoning deltas don't count: they're the model's hidden
// thinking, not user-visible output.)
func (l *Loop) consumeAndPersist(ctx context.Context, turnID string, stream <-chan StreamEvent) (*FinishInfo, bool, error) {
	var lastErr error
	var finish *FinishInfo
	var sawText bool
	for ev := range stream {
		if ev.Err != nil {
			lastErr = ev.Err
			continue
		}
		if ev.ReasoningDelta != "" {
			l.publish(sse.TypeThinkingDelta, sse.ThinkingDeltaPayload{TurnID: turnID, Delta: ev.ReasoningDelta})
		}
		if ev.ContentDelta != "" {
			sawText = true
			l.publish(sse.TypeMessageDelta, sse.MessageDeltaPayload{TurnID: turnID, Delta: ev.ContentDelta})
		}
		if ev.Finish != nil {
			finish = ev.Finish
		}
		// tool_call streaming events are useful for UI but our SSE schema
		// emits tool.request once per FULL call (after assembly). We
		// already do that in runTurn after seeing Finish.
		if ctx.Err() != nil {
			return nil, sawText, ctx.Err()
		}
	}
	if lastErr != nil {
		return nil, sawText, lastErr
	}
	if finish == nil {
		// The provider closed the stream without ever sending a
		// finish_reason chunk. Treat as transient — usually a chutes /
		// vllm proxy hiccup that resolves on retry.
		return nil, sawText, &RetryableError{Err: fmt.Errorf("stream ended without finish")}
	}
	if finish.Final.ReasoningContent != "" {
		// Mirror message.end for reasoning: a single event carrying the
		// full accumulated chain-of-thought, so consumers running with
		// --trace-skip-deltas (training, batch eval) still capture it.
		l.publish(sse.TypeThinkingDone, sse.ThinkingDonePayload{
			TurnID: turnID, Text: finish.Final.ReasoningContent,
		})
	}
	if sawText {
		// Close the streaming bubble so the UI's accumulator marks the
		// assistant text done before the next assistant message starts.
		l.publish(sse.TypeMessageDone, sse.MessageDonePayload{TurnID: turnID, Text: finish.Final.Content})
	}
	// Persist the assembled assistant message (content + tool_calls +
	// reasoning) so reload sees the same state. ReasoningContent is kept
	// in the conversation log for replay/training but stripped from
	// outbound requests by toWireMessages — DeepSeek/Kimi/GLM emit it
	// but reject it on inbound.
	_ = l.Conv.Append(finish.Final)
	return finish, sawText, nil
}

func (l *Loop) publish(t string, payload any) {
	ev, err := sse.NewEvent(t, payload)
	if err != nil {
		l.Log.Error().Err(err).Str("type", t).Msg("encode event")
		return
	}
	ev.ID = l.eventSeq.Add(1)
	select {
	case l.Events <- ev:
	default:
		// Best-effort: don't block the loop if the consumer is slow. The
		// SSE ring downstream should always drain, but we'd rather drop
		// a delta than deadlock.
		l.Log.Warn().Str("type", t).Msg("event channel full; dropped")
	}
}

func previewN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// truncateForContext is what the model sees for an oversized tool result.
// We keep the head (most "useful" lines from a typical curl/grep/cat
// output appear early), drop the middle, and tell the model what was cut
// + how to fetch more if it really needs to.
func truncateForContext(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf(
		"\n\n[... %d more bytes truncated. Re-run the command piping through head/tail/grep/sed, or save to a file under /workspace and read it in chunks, if you need more.]",
		len(s)-max,
	)
}

// ErrTurnInFlight is the loop-level mirror of session.ErrTurnInFlight. We
// can't import session here without a cycle (session imports nothing of
// ours; we don't want to depend on api either), so the runtime adapter
// translates this to the public error.
var ErrTurnInFlight = fmt.Errorf("turn already in flight")

// runStep performs a single LLM call (one assistant <-> tool round
// trip, before any tool dispatch). On a transient failure the call is
// retried up to MaxTransientRetries times with exponential backoff;
// each failed attempt emits an error event with Recoverable=true so
// the trace tells the story. On a non-transient failure or after all
// retries, the final error event is Recoverable=false and runStep
// returns the appropriate TurnEndReason.
//
// The "step" here is the model's *next* response. Each retry starts
// fresh: same conversation snapshot, no partial state preserved. If
// the previous attempt streamed message.delta events before failing,
// the next attempt's deltas are emitted on top — clients reconstructing
// the assistant message from deltas may see the earlier fragment as
// stale; the persisted ChatMessage only reflects the successful
// attempt.
func (l *Loop) runStep(ctx context.Context, turnID string) (*FinishInfo, string, error) {
	timeout := l.perCallTimeout()
	maxRetries := l.maxTransientRetries()
	backoff := l.transientBackoff()

	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil, sse.TurnEndCancelled, ctx.Err()
		}

		callCtx, callCancel := context.WithTimeout(ctx, timeout)
		stream, err := l.LLM.Chat(callCtx, l.Conv.Snapshot(), l.Tools.Defs())
		var final *FinishInfo
		var perr error
		var sawMessage bool
		var code string
		if err != nil {
			code = "llm_request"
		} else {
			final, sawMessage, perr = l.consumeAndPersist(callCtx, turnID, stream)
			if perr != nil {
				code = "llm_stream"
				err = perr
			}
		}
		callCancel()

		// Parent ctx cancel always wins over any inner error; surface
		// it as Cancelled rather than as a recoverable retry.
		if ctx.Err() != nil {
			return nil, sse.TurnEndCancelled, ctx.Err()
		}
		if err == nil {
			return final, "", nil
		}

		// If the model already streamed visible content before failing,
		// retrying produces a fresh response that the client's delta
		// accumulator can't reconcile with the partial text it already
		// received. Bail out clean rather than emit garbage. (Reasoning
		// deltas don't count — clients render those separately.)
		retryable := isTransient(err) && attempt < maxRetries && !sawMessage
		l.publish(sse.TypeError, sse.ErrorPayload{
			Code:        code,
			Message:     err.Error(),
			Recoverable: retryable,
		})
		if !retryable {
			return nil, sse.TurnEndError, err
		}

		// Server hint (Retry-After: <seconds>) wins over our own
		// schedule when present. Capped in parseRetryAfter so a bogus
		// value can't stall the loop indefinitely.
		wait := backoff << attempt
		var re *RetryableError
		if errors.As(err, &re) && re.RetryAfter > 0 {
			wait = re.RetryAfter
		}
		l.Log.Warn().
			Err(err).
			Int("attempt", attempt+1).
			Int("max", maxRetries).
			Dur("backoff", wait).
			Bool("server_hint", re != nil && re.RetryAfter > 0).
			Msg("transient LLM error; retrying")
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, sse.TurnEndCancelled, ctx.Err()
		}
	}
}

func (l *Loop) perCallTimeout() time.Duration {
	if l.PerCallTimeout > 0 {
		return l.PerCallTimeout
	}
	return DefaultPerCallTimeout
}

func (l *Loop) maxTransientRetries() int {
	switch {
	case l.MaxTransientRetries > 0:
		return l.MaxTransientRetries
	case l.MaxTransientRetries < 0:
		return 0
	default:
		return DefaultTransientRetries
	}
}

func (l *Loop) transientBackoff() time.Duration {
	if l.TransientBackoff > 0 {
		return l.TransientBackoff
	}
	return DefaultTransientBackoff
}

// isTransient classifies an error from a single LLM call as worth
// retrying. The actual retry budget lives in Loop; this just decides
// "is this even a candidate?".
//
// Categories:
//
//   - context.DeadlineExceeded — per-call timeout fired (parent ctx
//     cancellation is checked separately, before this is reached).
//   - *RetryableError — the llm package's sentinel for HTTP
//     408/429/5xx and network errors (DNS, refused, reset, mid-stream
//     EOF).
//   - any net.Error.Timeout() — defense in depth.
//   - io.ErrUnexpectedEOF — stream cut between chunks.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var re *RetryableError
	if errors.As(err, &re) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}
