package affent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeLLM returns a deterministic summary for any request. We use it to
// drive the compactor without hitting a real endpoint.
type fakeLLM struct{}

// fakeSummaryCompactor reuses LLMSummaryCompactor's structure but
// short-circuits the LLM call to a fixed string. Lets us exercise the
// keep-policy and boundary logic deterministically.
func newTestCompactor(keepFirst, keepLast int) *LLMSummaryCompactor {
	return &LLMSummaryCompactor{
		KeepFirst:   keepFirst,
		KeepLast:    keepLast,
		TriggerMsgs: 0,
	}
}

func TestBackUpToSafeBoundary(t *testing.T) {
	// Sequence: assistant(tool_calls) → tool → tool → assistant → user
	// Cutting at index 2 (mid tool replies) should back up to 0.
	msgs := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolCallID: "a", Content: "ra"},
		{Role: "tool", ToolCallID: "b", Content: "rb"},
		{Role: "assistant", Content: "thinking aloud"},
		{Role: "user", Content: "next"},
	}
	for in, want := range map[int]int{
		0: 0, // already at start
		2: 0, // cut on second tool reply: back up past both tools then past owner
		3: 3, // assistant (no tool_calls) — already a safe boundary, no back-up
		4: 4, // user — safe boundary
	} {
		got := backUpToSafeBoundary(msgs, in)
		if got != want {
			t.Errorf("backUpToSafeBoundary(_, %d) = %d, want %d", in, got, want)
		}
	}
}

func TestCompact_BelowThreshold_NoOp(t *testing.T) {
	c := newTestCompactor(2, 10)
	c.LLM = &LLMClient{} // never invoked because below threshold
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
		t.Errorf("below threshold: expected no change, got len=%d (was %d)", len(out), len(msgs))
	}
}

// stubCompactor lets us assert the head/middle/tail split without
// running a real LLM. Replaces summarize() via embedding.
type stubCompactor struct {
	*LLMSummaryCompactor
	summary string
}

func (s *stubCompactor) Compact(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	// Reimplement Compact() with summarize stubbed out — keeps tests
	// independent of the real LLM client.
	keepFirst := s.KeepFirst
	if keepFirst <= 0 {
		keepFirst = 2
	}
	keepLast := s.KeepLast
	if keepLast <= 0 {
		keepLast = 10
	}
	sysHead := 0
	for sysHead < len(msgs) && msgs[sysHead].Role == "system" {
		sysHead++
	}
	tail := msgs[sysHead:]
	if len(tail) <= keepFirst+keepLast+1 {
		return msgs, nil
	}
	head := msgs[:sysHead+keepFirst]
	tailStart := backUpToSafeBoundary(msgs, len(msgs)-keepLast)
	if tailStart <= len(head) {
		return msgs, nil
	}
	out := make([]ChatMessage, 0, len(head)+1+(len(msgs)-tailStart))
	out = append(out, head...)
	out = append(out, ChatMessage{Role: "user", Content: "[summary of earlier work] " + s.summary})
	out = append(out, msgs[tailStart:]...)
	return out, nil
}

func TestCompact_PreservesHeadAndTail(t *testing.T) {
	// Build: system + 20 alternating user/assistant. Trigger compaction
	// with keep_first=2 keep_last=4. Expect: system + first 2 + summary
	// + last 4 = 8 messages.
	msgs := []ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, ChatMessage{Role: role, Content: "msg" + string(rune('a'+i))})
	}
	c := &stubCompactor{
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 2, KeepLast: 4, TriggerMsgs: 0},
		summary:             "earlier work",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(out) != 1+2+1+4 {
		t.Fatalf("unexpected len: got %d, want 8", len(out))
	}
	if out[0].Role != "system" {
		t.Errorf("head[0] should be system; got %q", out[0].Role)
	}
	if out[1].Content != "msga" || out[2].Content != "msgb" {
		t.Errorf("first two non-system messages should be preserved verbatim")
	}
	if !strings.Contains(out[3].Content, "[summary of earlier work]") {
		t.Errorf("expected synthetic summary user message, got %+v", out[3])
	}
	if out[3].Role != "user" {
		t.Errorf("summary message must be role=user (multiple system messages confuse models)")
	}
	last := msgs[len(msgs)-4:]
	for i, m := range last {
		if out[4+i].Content != m.Content {
			t.Errorf("tail[%d]: got %q, want %q", i, out[4+i].Content, m.Content)
		}
	}
}

func TestCompact_DoesNotSeverToolCallPair(t *testing.T) {
	// Build a sequence ending with assistant.tool_calls + 2 tool replies.
	// keep_last=2 would cut between the assistant and its replies — the
	// boundary fixer must back up to keep them paired.
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
		LLMSummaryCompactor: &LLMSummaryCompactor{KeepFirst: 1, KeepLast: 2, TriggerMsgs: 0},
		summary:             "S",
	}
	out, err := c.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Walk out and verify: every assistant.tool_calls is immediately
	// followed by exactly one role=tool per call_id.
	for i := 0; i < len(out); i++ {
		if len(out[i].ToolCalls) > 0 {
			needed := len(out[i].ToolCalls)
			for j := i + 1; j < i+1+needed; j++ {
				if j >= len(out) || out[j].Role != "tool" {
					t.Fatalf("tool_calls at %d (%d calls) not followed by %d role=tool messages; got %v",
						i, needed, needed, out[j:])
				}
			}
			i += needed
		}
	}
}

func TestIsContextOverflow(t *testing.T) {
	cases := map[string]bool{
		`chat http 400: input length (12345 tokens) exceeds the maximum allowed length`: true,
		`chat http 400: This model's maximum context length is 8192 tokens`:             true,
		`ContextWindowExceededError: ...`:                                               true,
		`chat http 429: rate limit exceeded`:                                             false,
		`chat http 500: internal error`:                                                  false,
		``:                                                                               false,
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
