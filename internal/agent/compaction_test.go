package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFormatEvent_Roles pins the per-role rendering the summarizer
// sees. Each <EVENT> block in the summarizer prompt comes from
// formatEvent; if the format silently changes (e.g. tool_calls
// dropped from assistant messages) the summary quality degrades
// without anything obvious going red.
func TestFormatEvent_Roles(t *testing.T) {
	cases := []struct {
		name string
		msg  ChatMessage
		want []string // substrings that must appear
	}{
		{
			name: "plain user",
			msg:  ChatMessage{Role: "user", Content: "what's the weather"},
			want: []string{"USER:", "what's the weather"},
		},
		{
			name: "assistant content + tool_calls",
			msg: ChatMessage{
				Role:    "assistant",
				Content: "let me look that up",
				ToolCalls: []ToolCall{{Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "weather", Arguments: `{"city":"sf"}`}}},
			},
			want: []string{"ASSISTANT", "let me look that up", "tool weather", `{"city":"sf"}`},
		},
		{
			name: "assistant reasoning surfaces as [thinking: ...]",
			msg: ChatMessage{
				Role:             "assistant",
				ReasoningContent: "I should call the weather API",
				Content:          "checking",
			},
			want: []string{"ASSISTANT", "[thinking:", "weather API", "checking"},
		},
		{
			name: "tool result names its tool",
			msg:  ChatMessage{Role: "tool", Name: "weather", Content: "59F"},
			want: []string{"TOOL_RESULT[weather]", "59F"},
		},
		{
			name: "unknown role falls through",
			msg:  ChatMessage{Role: "system", Content: "be helpful"},
			want: []string{"system:", "be helpful"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatEvent(c.msg)
			for _, s := range c.want {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in %q", s, got)
				}
			}
		})
	}
}

// TestTruncateChars pins the byte-cap + UTF-8-safe truncation +
// "...(truncated)" marker. Called both from formatEvent and from
// the summarizer prompt body — a regression would silently truncate
// mid-rune and leak invalid UTF-8 into the summarizer LLM call.
func TestTruncateChars(t *testing.T) {
	t.Run("under limit unchanged", func(t *testing.T) {
		if got := truncateChars("hello", 100); got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
	})
	t.Run("over limit gets marker", func(t *testing.T) {
		got := truncateChars(strings.Repeat("a", 100), 30)
		if !strings.HasSuffix(got, "...(truncated)") {
			t.Errorf("missing truncation marker: %q", got)
		}
	})
	t.Run("multibyte boundary doesn't split rune", func(t *testing.T) {
		// "你" is 3 bytes. Cap at 2 should align back to 0.
		got := truncateChars("你好", 2)
		if !strings.HasSuffix(got, "...(truncated)") {
			t.Errorf("missing marker: %q", got)
		}
		head := strings.TrimSuffix(got, "...(truncated)")
		for _, r := range head {
			if r == 0xFFFD {
				t.Errorf("produced invalid UTF-8: %q", got)
			}
		}
	})
}

// TestLLMSummaryCompactor_Compact_Real drives the REAL Compact()
// method (not the stub at the top of this file) through a fake LLM.
// The stub-based tests cover the slicing logic; this one pins that
// the live method actually invokes the LLM and threads its response
// into the synthetic summary message at the right slot.
func TestLLMSummaryCompactor_Compact_Real(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		lines := []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"FAKE SUMMARY"},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	llm := NewLLMClient(srv.URL, "", "fake")

	mk := func(role, content string) ChatMessage {
		return ChatMessage{Role: role, Content: content}
	}

	t.Run("nil LLM rejected", func(t *testing.T) {
		c := &LLMSummaryCompactor{TriggerMsgs: 0}
		_, err := c.Compact(context.Background(), []ChatMessage{mk("user", "x")})
		if err == nil {
			t.Error("nil LLM must error")
		}
	})

	t.Run("below trigger is no-op", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 100, KeepFirst: 2, KeepLast: 5}
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "q"),
			mk("assistant", "a"),
		}
		got, _ := c.Compact(context.Background(), msgs)
		if len(got) != len(msgs) {
			t.Errorf("below-trigger compact must not change len; got %d want %d", len(got), len(msgs))
		}
	})

	t.Run("above trigger folds middle into one summary msg", func(t *testing.T) {
		c := &LLMSummaryCompactor{LLM: llm, TriggerMsgs: 0, KeepFirst: 2, KeepLast: 3}
		// 1 system + 2 head + 6 middle + 3 tail = 12.
		msgs := []ChatMessage{
			mk("system", "be helpful"),
			mk("user", "head1"),
			mk("assistant", "head2"),
			mk("user", "mid1"),
			mk("assistant", "mid2"),
			mk("user", "mid3"),
			mk("assistant", "mid4"),
			mk("user", "mid5"),
			mk("assistant", "mid6"),
			mk("user", "tail1"),
			mk("assistant", "tail2"),
			mk("user", "tail3"),
		}
		got, err := c.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		// Expect 1 system + 2 head + 1 summary + 3 tail = 7.
		if len(got) != 7 {
			t.Fatalf("expected 7 msgs after compaction, got %d: %+v", len(got), got)
		}
		if got[3].Role != "user" || !strings.HasPrefix(got[3].Content, summaryPrefix) {
			t.Errorf("position 3 must be synthetic summary; got role=%q content=%q",
				got[3].Role, got[3].Content)
		}
		if !strings.Contains(got[3].Content, "FAKE SUMMARY") {
			t.Errorf("LLM response must be embedded in summary content; got %q", got[3].Content)
		}
		if got[1].Content != "head1" || got[6].Content != "tail3" {
			t.Errorf("head/tail bookends wrong: head=%q tail=%q", got[1].Content, got[6].Content)
		}
	})
}

func TestBackUpToSafeBoundary(t *testing.T) {
	// Sequence: assistant(tool_calls) → tool → tool → assistant → user
	msgs := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolCallID: "a", Content: "ra"},
		{Role: "tool", ToolCallID: "b", Content: "rb"},
		{Role: "assistant", Content: "thinking aloud"},
		{Role: "user", Content: "next"},
	}
	for in, want := range map[int]int{
		0: 0, // already at start
		2: 0, // mid tool reply chain — back up past tools then past owner
		3: 3, // assistant (no tool_calls) is a safe boundary, no back-up
		4: 4, // user is safe
	} {
		got := backUpToSafeBoundary(msgs, in)
		if got != want {
			t.Errorf("backUpToSafeBoundary(_, %d) = %d, want %d", in, got, want)
		}
	}
}

// stubCompactor replicates the rolling Compact() flow but stubs out the
// LLM call. Lets us exercise the head/middle/tail split + previous
// summary detection deterministically.
type stubCompactor struct {
	*LLMSummaryCompactor
	summary string
}

func (s *stubCompactor) Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	keepFirst := s.KeepFirst
	if keepFirst <= 0 {
		keepFirst = 2
	}
	keepLast := s.KeepLast
	if keepLast <= 0 {
		keepLast = 10
	}
	if s.TriggerMsgs > 0 && len(msgs) <= s.TriggerMsgs {
		return msgs, nil
	}

	sysHead := 0
	for sysHead < len(msgs) && msgs[sysHead].Role == "system" {
		sysHead++
	}
	if len(msgs)-sysHead <= keepFirst+keepLast+1 {
		return msgs, nil
	}

	headEnd := forwardToSafeBoundary(msgs, sysHead+keepFirst)
	summaryEnd := headEnd
	if headEnd < len(msgs) {
		if m := msgs[headEnd]; m.Role == "user" && strings.HasPrefix(m.Content, summaryPrefix) {
			summaryEnd = headEnd + 1
		}
	}
	tailStart := backUpToSafeBoundary(msgs, len(msgs)-keepLast)
	if tailStart <= summaryEnd {
		return msgs, nil
	}
	middle := msgs[summaryEnd:tailStart]
	if len(middle) == 0 {
		return msgs, nil
	}

	out := make([]ChatMessage, 0, headEnd+1+(len(msgs)-tailStart))
	out = append(out, msgs[:headEnd]...)
	out = append(out, ChatMessage{Role: "user", Content: summaryPrefix + s.summary})
	out = append(out, msgs[tailStart:]...)
	return out, nil
}

func TestCompact_BelowThreshold_NoOp(t *testing.T) {
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 10, TriggerMsgs: 100},
		summary:             "S",
	}
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(msgs) {
		t.Errorf("below threshold: expected no change, got len=%d", len(out))
	}
}

func TestCompact_PreservesHeadAndTail(t *testing.T) {
	// system + 20 alternating user/assistant. KeepFirst=1, KeepLast=4 →
	// system + 1 head + 1 summary + 4 tail = 7.
	msgs := []ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "msg" + string(rune('a'+i))})
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 4},
		summary:             "earlier work",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(out) != 1+1+1+4 {
		t.Fatalf("unexpected len: got %d, want 7", len(out))
	}
	if out[0].Role != "system" {
		t.Errorf("head[0] should be system; got %q", out[0].Role)
	}
	if out[1].Content != "msga" {
		t.Errorf("first non-system message should be preserved verbatim; got %q", out[1].Content)
	}
	if !strings.Contains(out[2].Content, summaryPrefix) {
		t.Errorf("expected synthetic summary user message at index 2; got %+v", out[2])
	}
	last := msgs[len(msgs)-4:]
	for i, m := range last {
		if out[3+i].Content != m.Content {
			t.Errorf("tail[%d]: got %q, want %q", i, out[3+i].Content, m.Content)
		}
	}
}

// Rolling: a second compaction pass should detect the existing summary
// (left by the first pass), not start over from msg #1.
func TestCompact_RollingDoesNotMultiplySummary(t *testing.T) {
	// Conversation already in post-compaction shape, then has more events
	// appended that need re-compaction.
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "initial task"},
		{Role: "user", Content: summaryPrefix + "old summary"},
	}
	for i := 0; i < 30; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "ev" + string(rune('a'+i))})
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 4},
		summary:             "rolled-up summary",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Expect: system + initial task + ONE summary (the new one replaces
	// the old) + 4 tail events. Crucially not two summaries stacked.
	summaryCount := 0
	for _, m := range out {
		if m.Role == "user" && strings.HasPrefix(m.Content, summaryPrefix) {
			summaryCount++
		}
	}
	if summaryCount != 1 {
		t.Fatalf("expected exactly one rolling summary, got %d. out=%+v", summaryCount, out)
	}
	if len(out) != 1+1+1+4 {
		t.Fatalf("unexpected len: got %d, want 7", len(out))
	}
}

func TestCompact_DoesNotSeverToolCallPair(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1"}, {ID: "t2"}}},
		{Role: "tool", ToolCallID: "t1", Content: "r1"},
		{Role: "tool", ToolCallID: "t2", Content: "r2"},
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 2},
		summary:             "S",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	for i := 0; i < len(out); i++ {
		if len(out[i].ToolCalls) > 0 {
			needed := len(out[i].ToolCalls)
			for j := i + 1; j < i+1+needed; j++ {
				if j >= len(out) || out[j].Role != "tool" {
					t.Fatalf("tool_calls at %d (%d calls) not followed by %d role=tool messages; got %+v",
						i, needed, needed, out[j:])
				}
			}
			i += needed
		}
	}
}

// TestCompact_DoesNotSeverToolCallPair_AtHeadBoundary pins the symmetric
// boundary check at the HEAD side. The tail side already had
// backUpToSafeBoundary protecting it, but if KeepFirst landed the head
// right after an assistant.tool_calls, its tool replies got swept into
// the middle and summarized — leaving the resulting head with an
// assistant.tool_calls and NO matching tool replies. Strict
// OpenAI-compat upstreams reject that pairing on the next request,
// turning long sessions into reason=error the moment compaction fires.
func TestCompact_DoesNotSeverToolCallPair_AtHeadBoundary(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "t1"}, {ID: "t2"}}},
		{Role: "tool", ToolCallID: "t1", Content: "r1"},
		{Role: "tool", ToolCallID: "t2", Content: "r2"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "u4"},
		{Role: "assistant", Content: "a4"},
	}
	// KeepFirst=2 means head=[system, user, assistant(tc)] without the
	// safety fix — the tool replies go into the middle.
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 2, KeepLast: 2},
		summary:             "S",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	for i := 0; i < len(out); i++ {
		if len(out[i].ToolCalls) > 0 {
			needed := len(out[i].ToolCalls)
			for j := i + 1; j < i+1+needed; j++ {
				if j >= len(out) || out[j].Role != "tool" {
					t.Fatalf("assistant.tool_calls at %d (%d calls) not followed by %d role=tool messages; got conv:\n%+v",
						i, needed, needed, out)
				}
			}
			i += needed
		}
	}
}

func TestIsContextOverflow(t *testing.T) {
	cases := map[string]bool{
		// Already covered before this iteration:
		`chat http 400: input length (12345 tokens) exceeds the maximum allowed length`: true,
		`chat http 400: This model's maximum context length is 8192 tokens`:             true,
		`ContextWindowExceededError: ...`:                                               true,
		// Real provider phrasings broadened in iter 16:
		`chat http 400: prompt is too long: 100000 tokens > 80000 maximum`:    true, // Anthropic via proxy
		`chat http 400: input is too long`:                                    true, // Anthropic
		`chat http 413: Request too large`:                                    true, // Groq
		`chat http 400: error code "request_too_large"`:                       true, // Groq enum
		`chat http 400: too many tokens in messages`:                          true, // some Together/vLLM builds
		`chat http 400: context_length_exceeded`:                              true, // vLLM error code
		`chat http 400: ... is greater than the maximum allowed token count`:            true, // Fireworks/Together
		// "Range of input length should be [1, N]" — another flavor matched
		// via the existing "input length" keyword. Pinned so a future cleanup
		// of the keyword list can't silently break reactive compaction.
		`chat http 400: InvalidParameter: Range of input length should be [1, 229376]`: true,
		// Non-overflow errors must still be classified as false:
		`chat http 429: rate limit exceeded`: false,
		`chat http 500: internal error`:      false,
		``:                                   false,
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := IsContextOverflow(err); got != want {
			t.Errorf("IsContextOverflow(%q) = %v, want %v", msg, got, want)
		}
	}
}

// Defensive: prompt body should match OpenHands' V1 standard verbatim
// (we deliberately don't fork the wording).
func TestDefaultSummaryPrompt_StandardFields(t *testing.T) {
	required := []string{
		"USER_CONTEXT", "TASK_TRACKING", "COMPLETED", "PENDING",
		"CURRENT_STATE", "CODE_STATE", "TESTS", "CHANGES", "DEPS",
		"VERSION_CONTROL_STATUS", "PRIORITIZE", "SKIP", "Example formats",
	}
	for _, kw := range required {
		if !strings.Contains(defaultSummaryPrompt, kw) {
			t.Errorf("default prompt missing standard field %q", kw)
		}
	}
	// PRESERVE TASK IDs is the V1-specific instruction; guard against
	// accidental drop.
	if !strings.Contains(defaultSummaryPrompt, "PRESERVE TASK IDs") {
		t.Error("default prompt missing 'PRESERVE TASK IDs' V1 instruction")
	}
}
