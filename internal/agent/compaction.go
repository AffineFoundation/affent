package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sourceaccess"
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
const loopProtocolSummaryPrefix = "LOOP_PROTOCOL:"

// DefaultSummaryTriggerMsgs is the conventional proactive threshold
// borrowed from OpenHands V1 (max_size = 240). Used by callers that
// want a sane "compact long sessions" default without picking a
// number themselves.
const DefaultSummaryTriggerMsgs = 240

// DefaultSummaryKeepLast is the OpenHands V1 keep_last value (10).
const DefaultSummaryKeepLast = 10

const (
	compactReasoningMaxChars  = 500
	compactToolArgsMaxChars   = 300
	compactDelegationMaxText  = 6000
	compactDelegationMaxList  = 8
	compactMemoryMaxText      = 500
	compactPlanStepMaxText    = 300
	compactWebEvidenceMaxText = 1800
	compactWorkspaceMaxText   = 1800
	compactWorkspaceLineMax   = 240
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
	summary = ensureLoopProtocolSummaryAnchor(summary, prevSummary, middle)

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

func ensureLoopProtocolSummaryAnchor(summary, prevSummary string, events []ChatMessage) string {
	summary = strings.TrimSpace(summary)
	if summary == "" || strings.Contains(summary, loopProtocolSummaryPrefix) {
		return summary
	}
	anchor := latestLoopProtocolSummaryAnchor(events)
	if anchor == "" {
		anchor = latestLoopProtocolSummaryAnchorFromText(prevSummary)
	}
	if anchor == "" {
		return summary
	}
	return summary + "\n" + anchor
}

func latestLoopProtocolSummaryAnchor(events []ChatMessage) string {
	for i := len(events) - 1; i >= 0; i-- {
		if anchor := loopProtocolSummaryAnchorFromText(events[i].Content); anchor != "" {
			return anchor
		}
	}
	return ""
}

func latestLoopProtocolSummaryAnchorFromText(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, loopProtocolSummaryPrefix) {
			return line
		}
	}
	return loopProtocolSummaryAnchorFromText(text)
}

func loopProtocolSummaryAnchorFromText(text string) string {
	idx := strings.LastIndex(text, "AFFENT LOOP PROTOCOL:")
	if idx < 0 {
		return ""
	}
	payload, ok := loopProtocolFeedPayloadFromBlock("", text[idx:])
	if !ok {
		return ""
	}
	parts := []string{"active"}
	if payload.ProtocolPath != "" {
		parts = append(parts, "path="+payload.ProtocolPath)
	}
	if payload.Mode != "" {
		parts = append(parts, "mode="+payload.Mode)
	}
	if payload.FeedNumber > 0 {
		parts = append(parts, fmt.Sprintf("feed=%d", payload.FeedNumber))
	}
	if payload.ProtocolFeeds > 0 {
		parts = append(parts, fmt.Sprintf("feeds=%d", payload.ProtocolFeeds))
	}
	if payload.LoopID != "" {
		parts = append(parts, "loop_id="+payload.LoopID)
	}
	if payload.Status != "" {
		parts = append(parts, "status="+payload.Status)
	}
	if payload.PlanLabel != "" {
		parts = append(parts, "plan="+payload.PlanLabel)
	}
	if payload.PlanCurrentStepIndex > 0 || payload.PlanCurrentStepStatus != "" {
		step := fmt.Sprintf("current=%d:%s", payload.PlanCurrentStepIndex, payload.PlanCurrentStepStatus)
		parts = append(parts, strings.TrimRight(step, ":"))
	}
	if payload.PlanCurrentStep != "" {
		parts = append(parts, fmt.Sprintf("step=%q", textutil.Preview(payload.PlanCurrentStep, compactPlanStepMaxText)))
	}
	return loopProtocolSummaryPrefix + " " + strings.Join(parts, " ") + "; reload LOOP.md when north-star, current situation, rules, or recovery protocol details are needed after compaction."
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
	case MemoryToolName:
		if out, ok := compactMemoryResultForSummary(content); ok {
			return out
		}
	case PlanToolName:
		if out, ok := compactPlanResultForSummary(content); ok {
			return out
		}
	case SessionSearchToolName:
		if out, ok := compactSessionSearchResultForSummary(content); ok {
			return out
		}
	case "browser_network":
		if out, ok := compactBrowserNetworkResultForSummary(content); ok {
			return out
		}
	case "web_fetch", "browser_snapshot", "browser_find", "browser_network_read":
		if out, ok := compactSourceAccessResultForSummary(content); ok {
			return out
		}
	case "file_context":
		if out, ok := compactFileContextResultForSummary(content); ok {
			return out
		}
	case "read_file":
		if out, ok := compactReadFileResultForSummary(content); ok {
			return out
		}
	case "shell":
		if out, ok := compactShellResultForSummary(content); ok {
			return out
		}
	case "repo_search", "symbol_context", "list_files":
		if out, ok := compactTextToolResultForSummary(content); ok {
			return out
		}
	}
	return content
}

func compactBrowserNetworkResultForSummary(content string) (string, bool) {
	body := strings.TrimSpace(content)
	if !strings.HasPrefix(body, "BROWSER NETWORK EVIDENCE") {
		return "", false
	}

	type networkMatch struct {
		line      string
		preview   string
		jsonPaths string
	}

	var currentPage, query, next string
	noMatches := false
	inMatches := false
	var matches []networkMatch
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || trimmed == "BROWSER NETWORK EVIDENCE":
			continue
		case strings.HasPrefix(trimmed, "CURRENT_PAGE:"):
			currentPage = strings.TrimSpace(strings.TrimPrefix(trimmed, "CURRENT_PAGE:"))
		case strings.HasPrefix(trimmed, "query:"):
			query = strings.TrimSpace(strings.TrimPrefix(trimmed, "query:"))
		case trimmed == "MATCHES: none":
			noMatches = true
			inMatches = false
		case trimmed == "MATCHES:":
			inMatches = true
		case strings.HasPrefix(trimmed, "Next:"):
			next = strings.TrimSpace(trimmed)
			inMatches = false
		case inMatches && strings.HasPrefix(trimmed, "- "):
			if len(matches) < compactDelegationMaxList {
				matches = append(matches, networkMatch{line: strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))})
			}
		case inMatches && strings.HasPrefix(trimmed, "preview:") && len(matches) > 0:
			matches[len(matches)-1].preview = strings.TrimSpace(strings.TrimPrefix(trimmed, "preview:"))
		case inMatches && strings.HasPrefix(trimmed, "json_paths:") && len(matches) > 0:
			matches[len(matches)-1].jsonPaths = strings.TrimSpace(strings.TrimPrefix(trimmed, "json_paths:"))
		}
	}

	var b strings.Builder
	b.WriteString("browser_network:")
	if currentPage != "" {
		fmt.Fprintf(&b, " current_page=%s", textutil.Preview(currentPage, 500))
	}
	if query != "" {
		fmt.Fprintf(&b, " query=%s", textutil.Preview(query, 240))
	}
	switch {
	case noMatches:
		b.WriteString(" match_status=none")
	case len(matches) > 0:
		fmt.Fprintf(&b, " matches=%d", len(matches))
	}
	if len(matches) > 0 {
		b.WriteString("\nrefs:")
		for _, match := range matches {
			b.WriteString("\n- ")
			b.WriteString(textutil.Preview(strings.TrimSpace(match.line), compactWorkspaceLineMax))
			if match.preview != "" {
				b.WriteString("\n  preview: ")
				b.WriteString(textutil.Preview(textutil.CompactWhitespace(match.preview), compactMemoryMaxText))
			}
			if match.jsonPaths != "" {
				b.WriteString("\n  json_paths: ")
				b.WriteString(textutil.Preview(strings.TrimSpace(match.jsonPaths), compactMemoryMaxText))
			}
		}
	}
	if next != "" {
		b.WriteString("\n")
		b.WriteString(textutil.Preview(next, compactMemoryMaxText))
	}
	if b.String() == "browser_network:" {
		b.WriteString("\ntext_preview:\n")
		b.WriteString(textutil.Preview(body, compactWebEvidenceMaxText))
	}
	return b.String(), true
}

func compactMemoryResultForSummary(content string) (string, bool) {
	var resp memory.MemoryResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", false
	}
	if resp.Target == "" && resp.Message == "" && len(resp.Entries) == 0 && len(resp.Results) == 0 && len(resp.Topics) == 0 {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ok=%t", resp.OK)
	if resp.Target != "" {
		fmt.Fprintf(&b, " target=%s", resp.Target)
	}
	if resp.Topic != "" {
		fmt.Fprintf(&b, " topic=%s", resp.Topic)
	}
	if resp.Usage != nil {
		fmt.Fprintf(&b, " usage=%d%%,%d/%d chars,%d entries", resp.Usage.Percent, resp.Usage.CharsUsed, resp.Usage.CharsLimit, resp.Usage.EntryCount)
	}
	if strings.TrimSpace(resp.Message) != "" {
		fmt.Fprintf(&b, "\nmessage: %s", textutil.Preview(strings.TrimSpace(resp.Message), compactMemoryMaxText))
	}
	if len(resp.Entries) > 0 {
		b.WriteString("\nentries:")
		appendCompactStringList(&b, resp.Entries)
	}
	if len(resp.Matches) > 0 {
		b.WriteString("\nmatches:")
		appendCompactStringList(&b, resp.Matches)
	}
	if len(resp.Results) > 0 {
		b.WriteString("\nresults:")
		appendCompactMemoryResults(&b, resp.Results)
	}
	if len(resp.Topics) > 0 {
		b.WriteString("\ntopics:")
		appendCompactMemoryTopics(&b, resp.Topics)
	}
	return b.String(), true
}

func compactSessionSearchResultForSummary(content string) (string, bool) {
	var resp SessionSearchResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", false
	}
	if resp.Query == "" && resp.Message == "" && resp.Total == 0 && len(resp.Results) == 0 {
		return "", false
	}
	var b strings.Builder
	if strings.TrimSpace(resp.Query) != "" {
		fmt.Fprintf(&b, "query: %s", textutil.Preview(strings.TrimSpace(resp.Query), compactMemoryMaxText))
	}
	if resp.Total > 0 || len(resp.Results) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "total: %d result(s)", resp.Total)
	}
	if strings.TrimSpace(resp.Message) != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "message: %s", textutil.Preview(strings.TrimSpace(resp.Message), compactMemoryMaxText))
	}
	if len(resp.Results) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("results:")
		appendCompactSessionSearchHits(&b, resp.Results)
	}
	return b.String(), true
}

func compactSourceAccessResultForSummary(content string) (string, bool) {
	info, ok := sourceaccess.FirstInfoFromResult(content)
	if !ok {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "source_access: %s=%s", info.URLField, textutil.Preview(strings.TrimSpace(info.AccessedURL), 500))
	if info.RequestedURL != "" && info.RequestedURL != info.AccessedURL {
		fmt.Fprintf(&b, " requested_url=%s", textutil.Preview(strings.TrimSpace(info.RequestedURL), 500))
	}
	if info.PageTextBelow != "" {
		fmt.Fprintf(&b, " page_text_below=%s", textutil.Preview(strings.TrimSpace(info.PageTextBelow), 120))
	}
	if info.RenderedBrowserSourceStatus != "" {
		fmt.Fprintf(&b, " rendered_status=%s", textutil.Preview(strings.TrimSpace(info.RenderedBrowserSourceStatus), 120))
	}
	if info.SourceMethod != "" {
		fmt.Fprintf(&b, " source_method=%s", textutil.Preview(strings.TrimSpace(info.SourceMethod), 120))
	}
	if info.Ref != "" {
		fmt.Fprintf(&b, " ref=%s", textutil.Preview(strings.TrimSpace(info.Ref), 120))
	}
	if info.HTTPStatus != "" {
		fmt.Fprintf(&b, " http_status=%s", textutil.Preview(strings.TrimSpace(info.HTTPStatus), 120))
	}
	if info.ContentType != "" {
		fmt.Fprintf(&b, " content_type=%s", textutil.Preview(strings.TrimSpace(info.ContentType), 120))
	}
	if info.JSONPath != "" {
		fmt.Fprintf(&b, " json_path=%s", textutil.Preview(strings.TrimSpace(info.JSONPath), 240))
	}
	if body := sourceAccessBodyPreview(content); body != "" {
		b.WriteString("\nbody_preview:\n")
		b.WriteString(body)
	}
	return b.String(), true
}

func sourceAccessBodyPreview(content string) string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "SourceAccess:") {
			continue
		}
		if strings.HasPrefix(trimmed, "JSON_PATH:") {
			continue
		}
		if strings.HasPrefix(trimmed, "BODY_BYTES:") {
			continue
		}
		lines = append(lines, line)
	}
	body := strings.TrimSpace(strings.Join(lines, "\n"))
	if body == "" {
		return ""
	}
	return textutil.Preview(body, compactWebEvidenceMaxText)
}

func compactFileContextResultForSummary(content string) (string, bool) {
	var resp fileContextResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", false
	}
	if resp.Path == "" && resp.Bytes == 0 && resp.Lines == 0 && resp.Warning == "" && len(resp.Head) == 0 && len(resp.Matches) == 0 && len(resp.Tail) == 0 && len(resp.Symbols) == 0 {
		return "", false
	}
	var b strings.Builder
	if resp.Path != "" {
		fmt.Fprintf(&b, "file_context: path=%s", textutil.Preview(strings.TrimSpace(resp.Path), 500))
	} else {
		b.WriteString("file_context:")
	}
	if resp.Bytes > 0 {
		fmt.Fprintf(&b, " bytes=%d", resp.Bytes)
	}
	if resp.Lines > 0 {
		fmt.Fprintf(&b, " lines=%d", resp.Lines)
	}
	if resp.Truncated {
		b.WriteString(" truncated=true")
	}
	if strings.TrimSpace(resp.Query) != "" {
		fmt.Fprintf(&b, " query=%s", textutil.Preview(strings.TrimSpace(resp.Query), 240))
	}
	if strings.TrimSpace(resp.Warning) != "" {
		fmt.Fprintf(&b, "\nwarning: %s", textutil.Preview(strings.TrimSpace(resp.Warning), compactWorkspaceMaxText))
	}
	if len(resp.Symbols) > 0 {
		b.WriteString("\nsymbols:")
		appendCompactFileContextSymbols(&b, resp.Symbols)
	}
	if len(resp.Matches) > 0 {
		b.WriteString("\nmatches:")
		appendCompactFileContextSpans(&b, resp.Matches)
	}
	if len(resp.Head) > 0 {
		b.WriteString("\nhead:")
		appendCompactFileContextLines(&b, resp.Head)
	}
	if len(resp.Tail) > 0 {
		b.WriteString("\ntail:")
		appendCompactFileContextLines(&b, resp.Tail)
	}
	return b.String(), true
}

func compactReadFileResultForSummary(content string) (string, bool) {
	body := strings.TrimSpace(content)
	if body == "" {
		return "", false
	}
	return "file_body_preview:\n" + textutil.Preview(body, compactWorkspaceMaxText), true
}

func compactShellResultForSummary(content string) (string, bool) {
	body := strings.TrimSpace(content)
	if body == "" {
		return "", false
	}
	exitLine := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[exit ") && strings.HasSuffix(trimmed, "]") {
			exitLine = trimmed
		}
	}
	var b strings.Builder
	if exitLine != "" {
		fmt.Fprintf(&b, "exit: %s\n", exitLine)
	}
	b.WriteString("output_preview:\n")
	b.WriteString(textutil.Preview(body, compactWorkspaceMaxText))
	return b.String(), true
}

func compactTextToolResultForSummary(content string) (string, bool) {
	body := strings.TrimSpace(content)
	if body == "" {
		return "", false
	}
	return "text_preview:\n" + textutil.Preview(body, compactWorkspaceMaxText), true
}

func compactPlanResultForSummary(content string) (string, bool) {
	var st planState
	if err := json.Unmarshal([]byte(content), &st); err != nil {
		return "", false
	}
	if st.Message == "" && st.UpdatedAt == "" && len(st.Steps) == 0 {
		return "", false
	}
	var b strings.Builder
	if st.Message != "" {
		fmt.Fprintf(&b, "message: %s", textutil.Preview(strings.TrimSpace(st.Message), compactMemoryMaxText))
	}
	if st.UpdatedAt != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "updated_at: %s", textutil.Preview(strings.TrimSpace(st.UpdatedAt), 80))
	}
	if len(st.Steps) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("steps:")
		appendCompactPlanSteps(&b, st.Steps)
	}
	return b.String(), true
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

func appendCompactFileContextSymbols(b *strings.Builder, symbols []fileContextSymbol) {
	limit := len(symbols)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, sym := range symbols[:limit] {
		b.WriteString("\n- ")
		if sym.Line > 0 {
			fmt.Fprintf(b, "line=%d ", sym.Line)
		}
		if sym.Kind != "" {
			b.WriteString("kind=")
			b.WriteString(textutil.Preview(strings.TrimSpace(sym.Kind), 80))
			b.WriteByte(' ')
		}
		if sym.Name != "" {
			b.WriteString("name=")
			b.WriteString(textutil.Preview(strings.TrimSpace(sym.Name), 160))
		}
		if strings.TrimSpace(sym.Signature) != "" {
			b.WriteString(" signature=")
			b.WriteString(textutil.Preview(textutil.CompactWhitespace(sym.Signature), compactWorkspaceLineMax))
		}
	}
	if len(symbols) > limit {
		fmt.Fprintf(b, "\n- ... %d more symbol(s)", len(symbols)-limit)
	}
}

func appendCompactFileContextSpans(b *strings.Builder, spans []fileContextSpan) {
	limit := len(spans)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, span := range spans[:limit] {
		b.WriteString("\n- ")
		if span.StartLine > 0 || span.EndLine > 0 {
			fmt.Fprintf(b, "lines=%d-%d ", span.StartLine, span.EndLine)
		}
		if span.HitLine > 0 {
			fmt.Fprintf(b, "hit=%d ", span.HitLine)
		}
		b.WriteString(textutil.Preview(textutil.CompactWhitespace(span.Text), compactWorkspaceLineMax))
	}
	if len(spans) > limit {
		fmt.Fprintf(b, "\n- ... %d more match(es)", len(spans)-limit)
	}
}

func appendCompactFileContextLines(b *strings.Builder, lines []fileContextLine) {
	limit := len(lines)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, line := range lines[:limit] {
		b.WriteString("\n- ")
		if line.Line > 0 {
			fmt.Fprintf(b, "L%d: ", line.Line)
		}
		b.WriteString(textutil.Preview(textutil.CompactWhitespace(line.Text), compactWorkspaceLineMax))
	}
	if len(lines) > limit {
		fmt.Fprintf(b, "\n- ... %d more line(s)", len(lines)-limit)
	}
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

func appendCompactMemoryResults(b *strings.Builder, results []memory.MemorySearchResult) {
	limit := len(results)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, result := range results[:limit] {
		b.WriteString("\n- ")
		if result.Topic != "" {
			b.WriteString("topic=")
			b.WriteString(textutil.Preview(strings.TrimSpace(result.Topic), 120))
			b.WriteByte(' ')
		}
		if result.CreatedAt != "" {
			b.WriteString("created_at=")
			b.WriteString(textutil.Preview(strings.TrimSpace(result.CreatedAt), 80))
			b.WriteByte(' ')
		}
		if result.Score > 0 {
			fmt.Fprintf(b, "score=%.3f ", result.Score)
		}
		b.WriteString(textutil.Preview(strings.TrimSpace(result.Snippet), compactMemoryMaxText))
	}
	if len(results) > limit {
		fmt.Fprintf(b, "\n- ... %d more result(s)", len(results)-limit)
	}
}

func appendCompactMemoryTopics(b *strings.Builder, topics []memory.MemoryTopicSummary) {
	limit := len(topics)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, topic := range topics[:limit] {
		b.WriteString("\n- ")
		b.WriteString(textutil.Preview(strings.TrimSpace(topic.Topic), 120))
		fmt.Fprintf(b, " entries=%d chars=%d", topic.Entries, topic.Chars)
		if topic.NewestAt != "" {
			b.WriteString(" newest_at=")
			b.WriteString(textutil.Preview(strings.TrimSpace(topic.NewestAt), 80))
		}
	}
	if len(topics) > limit {
		fmt.Fprintf(b, "\n- ... %d more topic(s)", len(topics)-limit)
	}
}

func appendCompactSessionSearchHits(b *strings.Builder, hits []SessionSearchHit) {
	limit := len(hits)
	if limit > compactDelegationMaxList {
		limit = compactDelegationMaxList
	}
	for _, hit := range hits[:limit] {
		b.WriteString("\n- ")
		if hit.SessionID != "" {
			b.WriteString("session=")
			b.WriteString(textutil.Preview(strings.TrimSpace(hit.SessionID), 120))
			b.WriteByte(' ')
		}
		if hit.TurnIdx > 0 {
			fmt.Fprintf(b, "turn=%d ", hit.TurnIdx)
		}
		if hit.MessageIdx > 0 {
			fmt.Fprintf(b, "message=%d ", hit.MessageIdx)
		}
		if hit.Role != "" {
			b.WriteString("role=")
			b.WriteString(textutil.Preview(strings.TrimSpace(hit.Role), 40))
			b.WriteByte(' ')
		}
		if hit.ContextIncluded {
			b.WriteString("context=true ")
		}
		if hit.ModTime != "" {
			b.WriteString("mod_time=")
			b.WriteString(textutil.Preview(strings.TrimSpace(hit.ModTime), 80))
			b.WriteByte(' ')
		}
		if hit.Score > 0 {
			fmt.Fprintf(b, "score=%.3f ", hit.Score)
		}
		if len(hit.MatchedTerms) > 0 {
			b.WriteString("terms=")
			appendCompactInlineList(b, hit.MatchedTerms, maxActivePlanEvidenceRefs, 80)
			b.WriteByte(' ')
		}
		b.WriteString(textutil.Preview(strings.TrimSpace(hit.Snippet), compactMemoryMaxText))
	}
	if len(hits) > limit {
		fmt.Fprintf(b, "\n- ... %d more hit(s)", len(hits)-limit)
	}
}

func appendCompactPlanSteps(b *strings.Builder, steps []planStep) {
	limit := len(steps)
	if limit > maxPlanSteps {
		limit = maxPlanSteps
	}
	for i, step := range steps[:limit] {
		status := strings.TrimSpace(step.Status)
		if status == "" {
			status = "pending"
		}
		fmt.Fprintf(b, "\n- %d. [%s] %s", i+1, status, textutil.Preview(strings.TrimSpace(step.Text), compactPlanStepMaxText))
		if len(step.Evidence) > 0 {
			b.WriteString(" evidence=")
			appendCompactInlineList(b, step.Evidence, maxActivePlanEvidenceRefs, maxActivePlanEvidenceRefBytes)
		}
		if strings.TrimSpace(step.Note) != "" {
			b.WriteString(" note=")
			b.WriteString(textutil.Preview(strings.TrimSpace(step.Note), maxActivePlanNoteBytes))
		}
	}
	if len(steps) > limit {
		fmt.Fprintf(b, "\n- ... %d more step(s)", len(steps)-limit)
	}
}

func appendCompactInlineList(b *strings.Builder, items []string, maxItems, maxText int) {
	if maxItems <= 0 {
		maxItems = len(items)
	}
	limit := len(items)
	if limit > maxItems {
		limit = maxItems
	}
	wrote := 0
	for _, item := range items[:limit] {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if wrote > 0 {
			b.WriteString("; ")
		}
		b.WriteString(textutil.Preview(item, maxText))
		wrote++
	}
	if len(items) > limit {
		if wrote > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(b, "... %d more", len(items)-limit)
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
