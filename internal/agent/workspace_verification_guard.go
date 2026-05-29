package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const WorkspaceVerificationFreshnessGuardLabel = "workspace_verification_stale"

type workspaceVerificationFreshness struct {
	Blocked             bool
	LastMutationSummary string
	LastVerifySummary   string
}

// WorkspaceVerificationFreshnessCompletionGuard blocks final answers when a
// turn has already entered a verification workflow, then performs another
// source mutation without rerunning verification. The guard is deliberately
// state-based: it looks at tool traffic, not final-answer wording.
func WorkspaceVerificationFreshnessCompletionGuard(conv *Conversation) CompletionGuard {
	return func() CompletionGuardResult {
		state := workspaceVerificationFreshnessFromConversation(conv)
		if !state.Blocked {
			return CompletionGuardResult{}
		}
		reason := "Workspace files changed after the latest verification command."
		if state.LastMutationSummary != "" {
			reason = fmt.Sprintf("%s Latest mutation: %s.", reason, state.LastMutationSummary)
		}
		if state.LastVerifySummary != "" {
			reason = fmt.Sprintf("%s Latest verification before that: %s.", reason, state.LastVerifySummary)
		}
		return CompletionGuardResult{
			Blocked:        true,
			ID:             "workspace-verification-freshness",
			Trigger:        WorkspaceVerificationFreshnessGuardLabel,
			Reason:         reason,
			RequiredAction: "Run the relevant test/build verification again after the latest workspace edit, then finalize from that result. If it fails, report the failure and next fix instead of claiming completion.",
			Prompt:         "AFFENT COMPLETION GUARD: Workspace files changed after the latest verification command. Do not finalize yet. Run the relevant test/build verification again after the latest edit, then answer from that result. If verification fails, report the failure and next fix with evidence instead of claiming completion.",
		}
	}
}

func RegistrySupportsWorkspaceVerificationFreshnessGuard(reg *Registry) bool {
	if reg == nil {
		return false
	}
	if _, ok := reg.Get("shell"); !ok {
		return false
	}
	if _, ok := reg.Get("write_file"); ok {
		return true
	}
	if _, ok := reg.Get("edit_file"); ok {
		return true
	}
	return false
}

func workspaceVerificationFreshnessFromConversation(conv *Conversation) workspaceVerificationFreshness {
	if conv == nil {
		return workspaceVerificationFreshness{}
	}
	return workspaceVerificationFreshnessFromMessages(conv.Snapshot())
}

func workspaceVerificationFreshnessFromMessages(messages []ChatMessage) workspaceVerificationFreshness {
	calls := map[string]workspaceGuardToolCall{}
	lastMutationSeq := 0
	lastVerificationSeq := 0
	lastMutationSummary := ""
	lastVerifySummary := ""
	seq := 0
	for _, msg := range messages {
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				if call.ID == "" {
					continue
				}
				calls[call.ID] = workspaceGuardToolCall{
					Name: strings.TrimSpace(call.Function.Name),
					Args: json.RawMessage(call.Function.Arguments),
				}
			}
			continue
		}
		if msg.Role != "tool" {
			continue
		}
		seq++
		call := calls[msg.ToolCallID]
		name := strings.TrimSpace(firstNonEmpty(msg.Name, call.Name))
		if name == "" {
			continue
		}
		switch {
		case workspaceGuardMutationRequiresVerification(name, call.Args) && !workspaceGuardToolResultFailed(msg.Content):
			lastMutationSeq = seq
			lastMutationSummary = workspaceGuardMutationSummary(name, call.Args)
		case name == "shell":
			command := workspaceGuardArgString(call.Args, "command")
			if ShellCommandLooksLikeVerification(command) {
				lastVerificationSeq = seq
				lastVerifySummary = workspaceGuardPreview(command, 160)
			}
		}
	}
	if lastVerificationSeq == 0 || lastMutationSeq == 0 || lastMutationSeq <= lastVerificationSeq {
		return workspaceVerificationFreshness{}
	}
	return workspaceVerificationFreshness{
		Blocked:             true,
		LastMutationSummary: lastMutationSummary,
		LastVerifySummary:   lastVerifySummary,
	}
}

type workspaceGuardToolCall struct {
	Name string
	Args json.RawMessage
}

func workspaceGuardMutationRequiresVerification(name string, args json.RawMessage) bool {
	switch name {
	case "write_file", "edit_file":
		return workspaceGuardPathRequiresVerification(workspaceGuardArgString(args, "path"))
	default:
		return false
	}
}

func workspaceGuardPathRequiresVerification(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".rst", ".txt":
		return false
	default:
		return true
	}
}

func workspaceGuardToolResultFailed(result string) bool {
	result = strings.TrimSpace(result)
	return strings.HasPrefix(result, "Error:") || strings.Contains(result, "\n[exit 1]")
}

func workspaceGuardMutationSummary(name string, args json.RawMessage) string {
	if path := workspaceGuardArgString(args, "path"); path != "" {
		return name + " " + path
	}
	return name
}

func workspaceGuardArgString(args json.RawMessage, key string) string {
	var obj map[string]any
	if len(args) == 0 || !json.Valid(args) || json.Unmarshal(args, &obj) != nil {
		return ""
	}
	value, _ := obj[key].(string)
	return strings.TrimSpace(value)
}

func workspaceGuardPreview(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}
