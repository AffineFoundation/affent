package affent

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Compactor shrinks an oversized conversation history before the next LLM
// call. The Loop invokes it when the conversation crosses a threshold
// (proactive) or when the upstream returns a context-overflow error
// (reactive). Implementations must preserve tool_calls / role=tool
// pairing — every assistant message with ToolCalls must be immediately
// followed by exactly one role=tool message per call_id, otherwise the
// upstream will reject the request with 400.
type Compactor interface {
	// Compact returns the shortened message slice. Returning the input
	// unchanged is allowed (e.g. the threshold isn't crossed yet).
	// Returning an empty slice is treated as "compaction is impossible
	// right now" — the caller will surface the original error if any.
	Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error)
}

// LLMSummaryCompactor is the default Compactor: keep the system prompt +
// the first KeepFirst non-system messages + the most recent KeepLast
// turn-aligned messages, and replace the middle with a single user
// message containing an LLM-generated summary.
//
// "Turn-aligned" means: when computing the keep_last cut point, never
// cut between an assistant.tool_calls message and its matching
// role=tool replies — back the cut up until the boundary lands at a
// pure assistant message or a user message.
type LLMSummaryCompactor struct {
	// LLM is reused to generate the summary. Required.
	LLM *LLMClient

	// TriggerMsgs is the proactive threshold. Compact() short-circuits if
	// len(msgs) <= TriggerMsgs. Zero disables the proactive path (the
	// reactive context-overflow path still works).
	TriggerMsgs int

	// KeepFirst is how many leading non-system messages to preserve
	// verbatim. Default 2 captures the initial user prompt plus
	// (optionally) the first assistant reply with task framing.
	KeepFirst int

	// KeepLast is how many trailing messages to preserve verbatim.
	// Default 10 keeps recent trajectory plus a couple of tool round
	// trips visible to the model.
	KeepLast int

	// SummaryPrompt overrides the default summarization instruction.
	SummaryPrompt string
}

const defaultSummaryPrompt = `You are summarizing the early portion of an
agent's working session so the latter portion fits in the context window.
Preserve:
  - The original task as stated by the user.
  - Files modified, what changed, and why.
  - Key facts the agent discovered (paths, function names, error messages).
  - Decisions made and the rationale.
Drop:
  - File reads that were just exploration with no follow-up.
  - Verbose tool outputs that aren't load-bearing.
  - Reasoning that didn't lead to a concrete change.
Output: 1-3 short paragraphs of plain text. No bullet lists, no preamble,
no XML/markdown markup.`

// Compact implements Compactor.
func (c *LLMSummaryCompactor) Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	if c.LLM == nil {
		return nil, errors.New("LLMSummaryCompactor.LLM is nil")
	}
	keepFirst := c.KeepFirst
	if keepFirst <= 0 {
		keepFirst = 2
	}
	keepLast := c.KeepLast
	if keepLast <= 0 {
		keepLast = 10
	}

	// Split off the leading system prompt(s) — they're never compacted.
	sysHead := 0
	for sysHead < len(msgs) && msgs[sysHead].Role == "system" {
		sysHead++
	}
	tail := msgs[sysHead:]

	// Not enough non-system messages to be worth compacting.
	if len(tail) <= keepFirst+keepLast+1 {
		return msgs, nil
	}

	// Anchor the head: keep system + first N non-system.
	head := msgs[:sysHead+keepFirst]

	// Anchor the tail: last K, but back up to a safe boundary so we
	// don't sever a tool_calls / role=tool pair.
	tailStart := len(msgs) - keepLast
	tailStart = backUpToSafeBoundary(msgs, tailStart)
	if tailStart <= len(head) {
		// Keep window already covers everything; nothing to summarize.
		return msgs, nil
	}

	middle := msgs[len(head):tailStart]
	if len(middle) == 0 {
		return msgs, nil
	}

	summary, err := c.summarize(ctx, middle)
	if err != nil {
		return nil, fmt.Errorf("summarize: %w", err)
	}

	out := make([]ChatMessage, 0, len(head)+1+(len(msgs)-tailStart))
	out = append(out, head...)
	out = append(out, ChatMessage{
		Role:    "user",
		Content: "[summary of earlier work] " + summary,
	})
	out = append(out, msgs[tailStart:]...)
	return out, nil
}

// backUpToSafeBoundary moves cut earlier (toward 0) until it doesn't land
// between an assistant.tool_calls and its role=tool replies.
func backUpToSafeBoundary(msgs []ChatMessage, cut int) int {
	if cut <= 0 || cut >= len(msgs) {
		return cut
	}
	// If msgs[cut] is a role=tool, the assistant.tool_calls that produced
	// it must be at some position < cut. Walk backwards past consecutive
	// tool messages, then past the assistant message that owns them.
	for cut > 0 && msgs[cut].Role == "tool" {
		cut--
	}
	if cut > 0 && len(msgs[cut].ToolCalls) > 0 {
		cut--
	}
	return cut
}

func (c *LLMSummaryCompactor) summarize(ctx context.Context, middle []ChatMessage) (string, error) {
	prompt := c.SummaryPrompt
	if prompt == "" {
		prompt = defaultSummaryPrompt
	}
	transcript := renderTranscript(middle)

	req := []ChatMessage{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Transcript to summarize:\n\n" + transcript},
	}
	stream, err := c.LLM.Chat(ctx, req, nil)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for ev := range stream {
		if ev.Err != nil {
			return "", ev.Err
		}
		if ev.ContentDelta != "" {
			b.WriteString(ev.ContentDelta)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", errors.New("compactor: empty summary")
	}
	return out, nil
}

// renderTranscript flattens messages into a plain-text transcript the
// summarizer can read. Tool calls and results are rendered compactly so
// the summarizer focuses on the *what changed* signal.
func renderTranscript(msgs []ChatMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			fmt.Fprintf(&b, "USER: %s\n\n", m.Content)
		case "assistant":
			if m.Content != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "  → tool %s args=%s\n", tc.Function.Name, truncate(tc.Function.Arguments, 200))
			}
			fmt.Fprintln(&b)
		case "tool":
			fmt.Fprintf(&b, "  ← tool result: %s\n\n", truncate(m.Content, 400))
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// IsContextOverflow reports whether err looks like an upstream "input
// length exceeds maximum context window" rejection — the trigger for
// reactive compaction. Different providers phrase this differently;
// pattern match is the only portable approach.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, kw := range []string{
		"context length", "context window", "maximum context",
		"maximum allowed length", "input length", "exceeds the maximum",
		"contextwindowexceedederror", "string too long",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
