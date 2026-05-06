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
