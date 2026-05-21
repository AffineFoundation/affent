package affent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRequestBody_StripsReasoning pins the wire-format contract: the
// request body sent upstream must not contain reasoning_content. Some
// providers (DeepSeek, Kimi-thinking, GLM) emit it on responses but
// reject it on inbound messages with a 400.
func TestRequestBody_StripsReasoning(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
		{
			Role:             "assistant",
			Content:          "the answer",
			ReasoningContent: "I should think step by step about this...",
		},
	}
	body, err := json.Marshal(chatRequest{
		Model:    "test",
		Messages: toWireMessages(msgs),
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("request body must not contain reasoning_content; got %s", got)
	}
	if !strings.Contains(got, `"content":"the answer"`) {
		t.Errorf("expected visible content to survive; got %s", got)
	}
}

// TestSanitizeToolCallArgs_ReplacesMalformedWithEmptyObject pins the
// wire-format guard against truncated / corrupt tool_call argument
// strings. Real surface: a model cut off mid-tool-call (max_tokens hit)
// emits `{"path":"long-pa` — affent dispatch already returns an Error
// for the parse failure, but the assistant message stays in the log
// with the broken args. The very NEXT chat completion sends that bad
// tool_call upstream, strict OpenAI-compat upstreams return 400
// "arguments parameter must be in JSON format", the turn ends with
// reason=error, and the model never gets to retry. Replacing the
// broken args with "{}" on the wire keeps the turn alive — the
// matching tool.result already explains what went wrong.
func TestSanitizeToolCallArgs_ReplacesMalformedWithEmptyObject(t *testing.T) {
	mk := func(args string) ToolCall {
		var tc ToolCall
		tc.ID = "call_1"
		tc.Type = "function"
		tc.Function.Name = "f"
		tc.Function.Arguments = args
		return tc
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid passes through", `{"k":"v"}`, `{"k":"v"}`},
		{"empty becomes {}", "", "{}"},
		{"truncated becomes {}", `{"k":"long-pa`, "{}"},
		{"plain text becomes {}", "not json at all", "{}"},
		{"valid array passes through", `[1,2,3]`, `[1,2,3]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeToolCallArgs([]ToolCall{mk(c.in)})
			if got[0].Function.Arguments != c.want {
				t.Errorf("got %q want %q", got[0].Function.Arguments, c.want)
			}
		})
	}
}

// TestSanitizeToolCallArgs_PartialCorruption pins that with multiple
// tool_calls in one assistant message, only the bad ones are rewritten;
// the good ones survive byte-for-byte.
func TestSanitizeToolCallArgs_PartialCorruption(t *testing.T) {
	mk := func(args string) ToolCall {
		var tc ToolCall
		tc.Type = "function"
		tc.Function.Arguments = args
		return tc
	}
	in := []ToolCall{
		mk(`{"path":"/tmp/a.txt"}`),
		mk(`{"path":"/tmp/b`), // truncated
		mk(`{"path":"/tmp/c.txt"}`),
	}
	out := sanitizeToolCallArgs(in)
	if out[0].Function.Arguments != `{"path":"/tmp/a.txt"}` {
		t.Errorf("good[0] mutated: %q", out[0].Function.Arguments)
	}
	if out[1].Function.Arguments != `{}` {
		t.Errorf("bad[1] not rewritten: %q", out[1].Function.Arguments)
	}
	if out[2].Function.Arguments != `{"path":"/tmp/c.txt"}` {
		t.Errorf("good[2] mutated: %q", out[2].Function.Arguments)
	}
	// Ensure the input slice was NOT mutated (copy-on-write contract).
	if in[1].Function.Arguments != `{"path":"/tmp/b` {
		t.Errorf("input slice was mutated; sanitizer must copy-on-write")
	}
}

// TestConversationLog_KeepsReasoning pins the local-state contract:
// the conversation JSONL log must preserve reasoning_content so resume
// and downstream training rollouts can read it back.
func TestConversationLog_KeepsReasoning(t *testing.T) {
	msg := ChatMessage{
		Role:             "assistant",
		Content:          "the answer",
		ReasoningContent: "step-by-step thinking",
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"reasoning_content":"step-by-step thinking"`) {
		t.Errorf("conversation log dropped reasoning_content: %s", body)
	}
}
