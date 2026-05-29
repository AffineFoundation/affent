package agent

import (
	"strings"
	"testing"
)

func TestWorkspaceVerificationFreshnessBlocksMutationAfterVerification(t *testing.T) {
	msgs := []ChatMessage{
		assistantToolCall("verify", "shell", `{"command":"python3 -m unittest discover -s tests"}`),
		{Role: "tool", Name: "shell", ToolCallID: "verify", Content: "FAILED\n[exit 1]"},
		assistantToolCall("edit", "edit_file", `{"path":"todo_core/store.py"}`),
		{Role: "tool", Name: "edit_file", ToolCallID: "edit", Content: "replaced 1 occurrence(s) in todo_core/store.py"},
	}
	got := workspaceVerificationFreshnessFromMessages(msgs)
	if !got.Blocked {
		t.Fatalf("freshness guard should block after edit following verification: %+v", got)
	}
	if !strings.Contains(got.LastMutationSummary, "todo_core/store.py") ||
		!strings.Contains(got.LastVerifySummary, "python3 -m unittest") {
		t.Fatalf("freshness summaries = %+v", got)
	}
}

func TestWorkspaceVerificationFreshnessAllowsVerificationAfterMutation(t *testing.T) {
	msgs := []ChatMessage{
		assistantToolCall("edit", "edit_file", `{"path":"todo_core/store.py"}`),
		{Role: "tool", Name: "edit_file", ToolCallID: "edit", Content: "replaced 1 occurrence(s) in todo_core/store.py"},
		assistantToolCall("verify", "shell", `{"command":"python3 -m unittest discover -s tests"}`),
		{Role: "tool", Name: "shell", ToolCallID: "verify", Content: "FAILED\n[exit 1]"},
	}
	if got := workspaceVerificationFreshnessFromMessages(msgs); got.Blocked {
		t.Fatalf("freshness guard should allow final answer after post-edit verification: %+v", got)
	}
}

func TestWorkspaceVerificationFreshnessDoesNotInventVerificationRequirement(t *testing.T) {
	msgs := []ChatMessage{
		assistantToolCall("edit", "edit_file", `{"path":"README.md"}`),
		{Role: "tool", Name: "edit_file", ToolCallID: "edit", Content: "replaced 1 occurrence(s) in README.md"},
	}
	if got := workspaceVerificationFreshnessFromMessages(msgs); got.Blocked {
		t.Fatalf("freshness guard should not block when no verification workflow has started: %+v", got)
	}
}

func TestWorkspaceVerificationFreshnessIgnoresDocMutationAfterVerification(t *testing.T) {
	msgs := []ChatMessage{
		assistantToolCall("verify", "shell", `{"command":"python3 -m unittest discover -s tests"}`),
		{Role: "tool", Name: "shell", ToolCallID: "verify", Content: "OK\n[exit 0]"},
		assistantToolCall("readme", "write_file", `{"path":"README.md"}`),
		{Role: "tool", Name: "write_file", ToolCallID: "readme", Content: "wrote 120 bytes to README.md"},
	}
	if got := workspaceVerificationFreshnessFromMessages(msgs); got.Blocked {
		t.Fatalf("freshness guard should not block on documentation-only writes: %+v", got)
	}
}

func TestWorkspaceVerificationFreshnessGuardResult(t *testing.T) {
	conv, err := OpenConversationAt(t.TempDir() + "/conversation.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(assistantToolCall("verify", "shell", `{"command":"go test ./..."}`)); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "tool", Name: "shell", ToolCallID: "verify", Content: "ok\n[exit 0]"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(assistantToolCall("write", "write_file", `{"path":"internal/a.go"}`)); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "tool", Name: "write_file", ToolCallID: "write", Content: "wrote 12 bytes to internal/a.go"}); err != nil {
		t.Fatal(err)
	}
	result := WorkspaceVerificationFreshnessCompletionGuard(conv)()
	if !result.Blocked || result.Trigger != WorkspaceVerificationFreshnessGuardLabel ||
		!strings.Contains(result.RequiredAction, "Run the relevant test/build verification again") {
		t.Fatalf("guard result = %+v", result)
	}
}

func TestRegistrySupportsWorkspaceVerificationFreshnessGuard(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: "shell"})
	reg.Add(&Tool{Name: "edit_file"})
	if !RegistrySupportsWorkspaceVerificationFreshnessGuard(reg) {
		t.Fatal("shell plus edit_file should support freshness guard")
	}
	readOnly := NewRegistry()
	readOnly.Add(&Tool{Name: "shell"})
	readOnly.Add(&Tool{Name: "read_file"})
	if RegistrySupportsWorkspaceVerificationFreshnessGuard(readOnly) {
		t.Fatal("read-only workspace tools should not install freshness guard")
	}
}

func TestShellCommandLooksLikeVerificationIncludesUnittest(t *testing.T) {
	if !ShellCommandLooksLikeVerification("python3 -m unittest discover -s tests") {
		t.Fatal("python unittest should be classified as verification")
	}
}

func assistantToolCall(id, name, args string) ChatMessage {
	var call ToolCall
	call.ID = id
	call.Type = "function"
	call.Function.Name = name
	call.Function.Arguments = args
	return ChatMessage{Role: "assistant", ToolCalls: []ToolCall{call}}
}
