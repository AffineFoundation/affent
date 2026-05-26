package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/textutil"
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

// DefaultSummaryTriggerMsgs is the conventional proactive threshold
// borrowed from OpenHands V1 (max_size = 240). Used by callers that
// want a sane "compact long sessions" default without picking a
// number themselves.
const DefaultSummaryTriggerMsgs = 240

// DefaultSummaryKeepLast is the OpenHands V1 keep_last value (10).
const DefaultSummaryKeepLast = 10

const (
	compactReasoningMaxChars = 500
	compactToolArgsMaxChars  = 300
	compactDelegationMaxText = 6000
	compactDelegationMaxList = 8
)

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

	// TriggerMsgs is the proactive threshold: when the conversation
	// has at MORE than this many messages, Compact runs; otherwise
	// it short-circuits. Set to zero to skip the threshold entirely
	// (Compact runs every call) — the reactive overflow path uses
	// this internally by cloning the compactor with TriggerMsgs=0.
	// Pick DefaultSummaryTriggerMsgs for an OpenHands-style default.
	TriggerMsgs int

	// KeepFirst is how many leading non-system messages to preserve
	// verbatim — typically the initial user prompt(s). Default 2,
	// matching OpenHands V1.
	KeepFirst int

	// KeepLast is how many trailing messages to preserve verbatim.
	// Default DefaultSummaryKeepLast. Backed up to a safe boundary
	// so tool_calls / role=tool pairs aren't severed.
	KeepLast int

	// MaxEventLength caps the per-event chars sent to the summarizer
	// (long tool outputs get truncated). Default 10_000, matching
	// OpenHands max_event_length.
	MaxEventLength int

	// SummaryPrompt overrides the default summarization instruction.
	SummaryPrompt string
}

// defaultSummaryPrompt mirrors OpenHands' V1 LLMSummarizingCondenser
// prompt verbatim (modulo Jinja {% for %} which we render in Go).
// It's the de-facto standard among open-source SWE agents —
// structured fields, example-driven, rolling — and avoids
// reinventing a less-validated alternative.
//
// Source: github.com/OpenHands/software-agent-sdk
//
//	openhands-sdk/openhands/sdk/context/condenser/prompts/summarizing_prompt.j2
//
// V1 differs from the older monolithic prompt in two ways:
//  1. Adds a TASK_TRACKING field with hard MUST-include semantics, to
//     preserve task IDs across condensations.
//  2. Treats the previous summary as just another event in the list
//     (no separate <PREVIOUS SUMMARY> block); we surface it as the
//     first <EVENT> in the rendered list.
const defaultSummaryPrompt = `You are maintaining a context-aware state summary for an interactive agent.
You will be given a list of events corresponding to actions taken by the agent, which will include previous summaries.
If the events being summarized contain ANY task-tracking, you MUST include a TASK_TRACKING section to maintain continuity.
When referencing tasks make sure to preserve exact task IDs and statuses.

Track:

USER_CONTEXT: (Preserve essential user requirements, goals, and clarifications in concise form)

TASK_TRACKING: {Active tasks, their IDs and statuses - PRESERVE TASK IDs}

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
		keepFirst = 2
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
	// Push the boundary forward past any in-flight tool_calls/tool-replies
	// group so the head doesn't end with an assistant.tool_calls whose
	// replies just got summarized away.
	headEnd := forwardToSafeBoundary(msgs, sysHead+keepFirst)

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

// forwardToSafeBoundary moves cut later (toward len) past any role=tool
// messages so the head ends after a complete assistant.tool_calls /
// tool-replies group. The symmetric counterpart to backUpToSafeBoundary:
// without it, KeepFirst landing right after an assistant.tool_calls
// sweeps the matching tool replies into the summarized middle, and
// strict OpenAI-compat upstreams reject the resulting head's orphan
// tool_calls on the next request.
func forwardToSafeBoundary(msgs []ChatMessage, cut int) int {
	if cut <= 0 || cut >= len(msgs) {
		return cut
	}
	for cut < len(msgs) && msgs[cut].Role == "tool" {
		cut++
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
	// V1 prompt expects the previous summary inline as the first event,
	// not in a dedicated <PREVIOUS SUMMARY> block. Match that shape.
	if prevSummary != "" {
		fmt.Fprintf(&b, "<EVENT>\nPrevious summary: %s\n</EVENT>\n",
			truncateChars(prevSummary, maxLen))
	}
	for _, ev := range events {
		fmt.Fprintf(&b, "<EVENT>\n%s\n</EVENT>\n",
			truncateChars(formatEvent(ev), maxLen))
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
			b.WriteString(truncateChars(m.ReasoningContent, compactReasoningMaxChars))
			b.WriteString("]")
		}
		if m.Content != "" {
			b.WriteString(": ")
			b.WriteString(m.Content)
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "\n  → tool %s args=%s", tc.Function.Name, truncateChars(tc.Function.Arguments, compactToolArgsMaxChars))
		}
	case "tool":
		fmt.Fprintf(&b, "TOOL_RESULT[%s]: %s", m.Name, compactToolResultForSummary(m.Name, m.Content))
	default:
		fmt.Fprintf(&b, "%s: %s", m.Role, m.Content)
	}
	return b.String()
}

func compactToolResultForSummary(toolName, content string) string {
	switch toolName {
	case SubagentToolName:
		if out, ok := compactSubagentResultForSummary(content); ok {
			return out
		}
	case FocusedTaskToolName:
		if out, ok := compactFocusedTaskResultForSummary(content); ok {
			return out
		}
	}
	return content
}

func compactSubagentResultForSummary(content string) (string, bool) {
	var resp subagentResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", false
	}
	if strings.TrimSpace(resp.Report) == "" && resp.ChildSessionID == "" && resp.Mode == "" {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ok=%t", resp.OK)
	if resp.Mode != "" {
		fmt.Fprintf(&b, " mode=%s", resp.Mode)
	}
	if resp.TurnEndReason != "" {
		fmt.Fprintf(&b, " turn_end=%s", resp.TurnEndReason)
	}
	if resp.ChildSessionID != "" {
		fmt.Fprintf(&b, " child_session_id=%s", resp.ChildSessionID)
	}
	if resp.Depth > 0 || resp.MaxDepth > 0 {
		fmt.Fprintf(&b, " depth=%d/%d", resp.Depth, resp.MaxDepth)
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		fmt.Fprintf(&b, " usage=%d/%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	if strings.TrimSpace(resp.Error) != "" {
		fmt.Fprintf(&b, "\nerror: %s", textutil.Preview(strings.TrimSpace(resp.Error), 500))
	}
	if len(resp.LoopErrors) > 0 {
		b.WriteString("\nloop_errors:")
		appendCompactStringList(&b, resp.LoopErrors)
	}
	if strings.TrimSpace(resp.Report) != "" {
		b.WriteString("\nreport:\n")
		b.WriteString(textutil.Preview(strings.TrimSpace(resp.Report), compactDelegationMaxText))
	}
	if len(resp.ToolCalls) > 0 {
		b.WriteString("\ntool_calls:")
		appendCompactDelegationToolCalls(&b, resp.ToolCalls)
	}
	return b.String(), true
}

func compactFocusedTaskResultForSummary(content string) (string, bool) {
	var resp FocusedTaskResult
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", false
	}
	if strings.TrimSpace(resp.Summary) == "" && len(resp.Findings) == 0 && resp.ChildSessionID == "" {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ok=%t", resp.OK)
	if resp.TaskType != "" {
		fmt.Fprintf(&b, " task_type=%s", resp.TaskType)
	}
	if resp.TurnEndReason != "" {
		fmt.Fprintf(&b, " turn_end=%s", resp.TurnEndReason)
	}
	if resp.ChildSessionID != "" {
		fmt.Fprintf(&b, " child_session_id=%s", resp.ChildSessionID)
	}
	if resp.Depth > 0 {
		fmt.Fprintf(&b, " depth=%d", resp.Depth)
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		fmt.Fprintf(&b, " usage=%d/%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	if strings.TrimSpace(resp.Error) != "" {
		fmt.Fprintf(&b, "\nerror: %s", textutil.Preview(strings.TrimSpace(resp.Error), 500))
	}
	if strings.TrimSpace(resp.Summary) != "" {
		fmt.Fprintf(&b, "\nsummary: %s", textutil.Preview(strings.TrimSpace(resp.Summary), compactDelegationMaxText))
	}
	if len(resp.Findings) > 0 {
		b.WriteString("\nfindings:")
		limit := len(resp.Findings)
		if limit > compactDelegationMaxList {
			limit = compactDelegationMaxList
		}
		for _, f := range resp.Findings[:limit] {
			b.WriteString("\n- ")
			b.WriteString(textutil.Preview(strings.TrimSpace(f.Claim), 500))
			if f.Source != "" {
				b.WriteString(" source=")
				b.WriteString(textutil.Preview(strings.TrimSpace(f.Source), 240))
			}
			if f.Evidence != "" {
				b.WriteString(" evidence=")
				b.WriteString(textutil.Preview(strings.TrimSpace(f.Evidence), 700))
			}
			if f.Confidence != "" {
				b.WriteString(" confidence=")
				b.WriteString(strings.TrimSpace(f.Confidence))
			}
			if f.Severity != "" {
				b.WriteString(" severity=")
				b.WriteString(strings.TrimSpace(f.Severity))
			}
		}
		if len(resp.Findings) > limit {
			fmt.Fprintf(&b, "\n- ... %d more finding(s)", len(resp.Findings)-limit)
		}
	}
	if len(resp.Warnings) > 0 {
		b.WriteString("\nwarnings:")
		appendCompactStringList(&b, resp.Warnings)
	}
	if len(resp.NotFound) > 0 {
		b.WriteString("\nnot_found:")
		appendCompactStringList(&b, resp.NotFound)
	}
	if len(resp.SuggestedNext) > 0 {
		b.WriteString("\nsuggested_next:")
		appendCompactStringList(&b, resp.SuggestedNext)
	}
	if len(resp.ToolCalls) > 0 {
		b.WriteString("\ntool_calls:")
		appendCompactDelegationToolCalls(&b, resp.ToolCalls)
	}
	return b.String(), true
}

func appendCompactStringList(b *strings.Builder, items []string) {
	limit := len(items)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, item := range items[:limit] {
		if strings.TrimSpace(item) == "" {
			continue
		}
		b.WriteString("\n- ")
		b.WriteString(textutil.Preview(strings.TrimSpace(item), 500))
	}
	if len(items) > limit {
		fmt.Fprintf(b, "\n- ... %d more item(s)", len(items)-limit)
	}
}

func appendCompactDelegationToolCalls(b *strings.Builder, calls []subagentToolCall) {
	limit := len(calls)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, call := range calls[:limit] {
		b.WriteString("\n- ")
		b.WriteString(call.Tool)
		if args := compactDelegationArgs(call.Args); args != "" {
			b.WriteByte(' ')
			b.WriteString(args)
		}
		if call.ExitCode != 0 {
			fmt.Fprintf(b, " exit=%d", call.ExitCode)
		}
	}
	if len(calls) > limit {
		fmt.Fprintf(b, "\n- ... %d more tool call(s)", len(calls)-limit)
	}
}

func compactDelegationArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"path", "command", "query", "url", "task_type", "mode", "objective", "task"} {
		value, ok := args[key]
		if !ok {
			continue
		}
		s, ok := value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		return key + "=" + textutil.Preview(strings.TrimSpace(s), 240)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return textutil.Preview(fmt.Sprint(args), 240)
	}
	return textutil.Preview(string(raw), 240)
}

func truncateChars(s string, n int) string {
	return textutil.Preview(s, n, "...(truncated)")
}

// IsContextOverflow reports whether err looks like an upstream "input
// length exceeds maximum context window" rejection — the trigger for
// reactive compaction. The keyword list covers the phrasing each
// major OpenAI-compatible provider actually emits (collected from
// production errors, not the spec):
//
//   - OpenAI / Azure OpenAI: "maximum context length is N tokens. However, your messages resulted in ..."
//   - Anthropic via proxy:  "prompt is too long: N tokens > M maximum", "input is too long"
//   - Anthropic SDK:        "ContextWindowExceededError"
//   - Groq:                 "Request too large", "request_too_large"
//   - DeepSeek / Kimi:      "the messages length exceeds the maximum"
//   - Together / Fireworks: "input length is greater than the maximum allowed", "is greater than the maximum allowed token count"
//   - vLLM / sglang / TGI:  "context_length_exceeded", "string too long"
//   - Chutes / OpenRouter:  pass-through of any of the above
//
// contextOverflowKeywords covers the phrasing each major
// OpenAI-compatible provider emits when input length exceeds the
// context window. Collected from production errors, not the spec.
var contextOverflowKeywords = []string{
	"context length", "context window", "maximum context",
	"context_length_exceeded",
	"maximum allowed length", "maximum allowed token",
	"input length", "input is too long",
	"prompt is too long", "too many tokens",
	"exceeds the maximum",
	"request too large", "request_too_large",
	"contextwindowexceedederror", "string too long",
}

func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, kw := range contextOverflowKeywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
