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

// summaryPrefix tags the synthetic user message that holds a rolling
// summary, so subsequent compactions can find it and feed it back to
// the summarizer (rolling / incremental summarization, matching
// OpenHands' LLMSummarizingCondenser behaviour).
const summaryPrefix = "[summary of earlier work] "

// LLMSummaryCompactor implements rolling LLM summarization. Layout
// after a successful compaction:
//
//	[system…]                           ← preserved verbatim
//	[first KeepFirst non-system msgs]   ← preserved verbatim
//	[user: "[summary of earlier work] …"] ← single rolling summary
//	[last KeepLast msgs (boundary-safe)] ← preserved verbatim
//
// On the next compaction the previous summary is parsed back out and
// included in the prompt as <PREVIOUS SUMMARY>, so the summarizer
// updates an evolving state document instead of summarizing from
// scratch each time. This is OpenHands' approach (RollingCondenser ←
// LLMSummarizingCondenser); it keeps long-running sessions stable
// without paying to re-summarize old work each pass.
type LLMSummaryCompactor struct {
	// LLM drives summarization. Required.
	LLM *LLMClient

	// TriggerMsgs is the proactive threshold: if the conversation has
	// fewer messages, Compact short-circuits without work. Zero
	// disables proactive compaction (reactive overflow path still
	// works). Default 150 (matches OpenHands max_size).
	TriggerMsgs int

	// KeepFirst is how many leading non-system messages to preserve
	// verbatim — typically the initial user prompt. Default 1.
	// Anything higher and the rolling summary loses ground each pass.
	KeepFirst int

	// KeepLast is how many trailing messages to preserve verbatim.
	// Default 10. Backed up to a safe boundary so tool_calls / role=tool
	// pairs aren't severed.
	KeepLast int

	// MaxEventLength caps the per-event chars sent to the summarizer
	// (long tool outputs get truncated). Default 10_000, matching
	// OpenHands max_event_length.
	MaxEventLength int

	// SummaryPrompt overrides the default summarization instruction.
	SummaryPrompt string
}

// defaultSummaryPrompt is OpenHands' LLMSummarizingCondenser prompt,
// reused verbatim. It's the de-facto standard among open-source SWE
// agents — structured fields (USER_CONTEXT / COMPLETED / PENDING /
// CODE_STATE / etc.), example-driven, rolling — and avoids reinventing
// a less-validated alternative.
//
// Source:
//
//	openhands/memory/condenser/impl/llm_summarizing_condenser.py
const defaultSummaryPrompt = `You are maintaining a context-aware state summary for an interactive agent. You will be given a list of events corresponding to actions taken by the agent, and the most recent previous summary if one exists. Track:

USER_CONTEXT: (Preserve essential user requirements, goals, and clarifications in concise form)

COMPLETED: (Tasks completed so far, with brief results)
PENDING: (Tasks that still need to be done)
CURRENT_STATE: (Current variables, data structures, or relevant state)

For code-specific tasks, also include:
CODE_STATE: {File paths, function signatures, data structures}
TESTS: {Failing cases, error messages, outputs}
CHANGES: {Code edits, variable updates}
DEPS: {Dependencies, imports, external calls}
VERSION_CONTROL_STATUS: {Repository state, current branch, PR status, commit history}

PRIORITIZE:
1. Adapt tracking format to match the actual task type
2. Capture key user requirements and goals
3. Distinguish between completed and pending tasks
4. Keep all sections concise and relevant

SKIP: Tracking irrelevant details for the current task type

Example formats:

For code tasks:
USER_CONTEXT: Fix FITS card float representation issue
COMPLETED: Modified mod_float() in card.py, all tests passing
PENDING: Create PR, update documentation
CODE_STATE: mod_float() in card.py updated
TESTS: test_format() passed
CHANGES: str(val) replaces f"{val:.16G}"
DEPS: None modified
VERSION_CONTROL_STATUS: Branch: fix-float-precision, Latest commit: a1b2c3d

For other tasks:
USER_CONTEXT: Write 20 haikus based on coin flip results
COMPLETED: 15 haikus written for results [T,H,T,H,T,H,T,T,H,T,H,T,H,T,H]
PENDING: 5 more haikus needed
CURRENT_STATE: Last flip: Heads, Haiku count: 15/20`

// Compact implements Compactor.
func (c *LLMSummaryCompactor) Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	if c.LLM == nil {
		return nil, errors.New("LLMSummaryCompactor.LLM is nil")
	}
	keepFirst := c.KeepFirst
	if keepFirst <= 0 {
		keepFirst = 1
	}
	keepLast := c.KeepLast
	if keepLast <= 0 {
		keepLast = 10
	}
	if c.TriggerMsgs > 0 && len(msgs) <= c.TriggerMsgs {
		return msgs, nil
	}

	// Split off leading system messages — never touched.
	sysHead := 0
	for sysHead < len(msgs) && msgs[sysHead].Role == "system" {
		sysHead++
	}
	if len(msgs)-sysHead <= keepFirst+keepLast+1 {
		return msgs, nil
	}

	// "head" preserves the first KeepFirst non-system messages verbatim.
	headEnd := sysHead + keepFirst

	// If a previous summary already follows the head (left there by an
	// earlier rolling pass), pull it out so we can feed it back to the
	// summarizer as <PREVIOUS SUMMARY>. Otherwise summarize from scratch.
	prevSummary := ""
	summaryEnd := headEnd
	if headEnd < len(msgs) {
		if m := msgs[headEnd]; m.Role == "user" && strings.HasPrefix(m.Content, summaryPrefix) {
			prevSummary = strings.TrimSpace(strings.TrimPrefix(m.Content, summaryPrefix))
			summaryEnd = headEnd + 1
		}
	}

	// Tail anchor — back up so we don't sever an assistant.tool_calls
	// from its role=tool replies.
	tailStart := backUpToSafeBoundary(msgs, len(msgs)-keepLast)
	if tailStart <= summaryEnd {
		return msgs, nil
	}

	middle := msgs[summaryEnd:tailStart]
	if len(middle) == 0 {
		return msgs, nil
	}

	summary, err := c.summarize(ctx, prevSummary, middle)
	if err != nil {
		return nil, fmt.Errorf("summarize: %w", err)
	}

	out := make([]ChatMessage, 0, headEnd+1+(len(msgs)-tailStart))
	out = append(out, msgs[:headEnd]...)
	out = append(out, ChatMessage{
		Role:    "user",
		Content: summaryPrefix + summary,
	})
	out = append(out, msgs[tailStart:]...)
	return out, nil
}

// backUpToSafeBoundary moves cut earlier (toward 0) until it doesn't
// land between an assistant.tool_calls and its role=tool replies.
func backUpToSafeBoundary(msgs []ChatMessage, cut int) int {
	if cut <= 0 || cut >= len(msgs) {
		return cut
	}
	for cut > 0 && msgs[cut].Role == "tool" {
		cut--
	}
	if cut > 0 && len(msgs[cut].ToolCalls) > 0 {
		cut--
	}
	return cut
}

func (c *LLMSummaryCompactor) summarize(ctx context.Context, prevSummary string, events []ChatMessage) (string, error) {
	prompt := c.SummaryPrompt
	if prompt == "" {
		prompt = defaultSummaryPrompt
	}
	maxLen := c.MaxEventLength
	if maxLen <= 0 {
		maxLen = 10_000
	}

	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n")
	b.WriteString("<PREVIOUS SUMMARY>\n")
	b.WriteString(prevSummary)
	b.WriteString("\n</PREVIOUS SUMMARY>\n\n")
	for i, ev := range events {
		fmt.Fprintf(&b, "<EVENT id=%d>\n%s\n</EVENT>\n",
			i, truncateChars(formatEvent(ev), maxLen))
	}
	b.WriteString("\nNow summarize the events using the rules above.")

	// OpenHands sends the full prompt as a single user message rather
	// than splitting system/user. Matching that shape avoids surprises
	// from chat templates that treat system specially.
	req := []ChatMessage{{Role: "user", Content: b.String()}}
	stream, err := c.LLM.Chat(ctx, req, nil)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for ev := range stream {
		if ev.Err != nil {
			return "", ev.Err
		}
		if ev.ContentDelta != "" {
			out.WriteString(ev.ContentDelta)
		}
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return "", errors.New("compactor: empty summary")
	}
	return s, nil
}

// formatEvent renders one ChatMessage in a compact textual form for the
// summarizer. Tool calls and tool results are inlined so the model sees
// "what did the agent try, what came back".
func formatEvent(m ChatMessage) string {
	var b strings.Builder
	switch m.Role {
	case "user":
		b.WriteString("USER: ")
		b.WriteString(m.Content)
	case "assistant":
		b.WriteString("ASSISTANT")
		if m.ReasoningContent != "" {
			b.WriteString(" [thinking: ")
			b.WriteString(truncateChars(m.ReasoningContent, 500))
			b.WriteString("]")
		}
		if m.Content != "" {
			b.WriteString(": ")
			b.WriteString(m.Content)
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "\n  → tool %s args=%s", tc.Function.Name, truncateChars(tc.Function.Arguments, 300))
		}
	case "tool":
		fmt.Fprintf(&b, "TOOL_RESULT[%s]: %s", m.Name, m.Content)
	default:
		fmt.Fprintf(&b, "%s: %s", m.Role, m.Content)
	}
	return b.String()
}

func truncateChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// IsContextOverflow reports whether err looks like an upstream "input
// length exceeds maximum context window" rejection — the trigger for
// reactive compaction.
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
