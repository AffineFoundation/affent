package affent

import (
	"context"
	"encoding/json"
	"fmt"
)

// memoryActions enumerates the action values the `memory` tool accepts.
var memoryActions = []string{"add", "replace", "remove"}

// memoryTool builds the `memory` tool: three actions (add / replace /
// remove) across two targets (memory / user). Current state reaches
// the agent through the session-start snapshot and through
// MemoryResponse.Entries on every mutation.
func memoryTool(store MemoryStore) *Tool {
	schema, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"action", "target"},
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        memoryActions,
				"description": "add (new entry), replace (update via unique old_text substring), remove (delete via unique old_text substring).",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{string(TargetMemory), string(TargetUser)},
				"description": "memory = your own notes (env, conventions, lessons learned); user = what you know about the user (name, preferences, communication style).",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Entry text. Required for add and replace. Keep compact and information-dense (one entry should pack related facts together, not split them into many short rows).",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Short unique substring identifying the entry to replace or remove. The substring just has to match exactly one entry; you do not need the full text.",
			},
		},
	})
	if err != nil {
		// Building a static schema from constants cannot fail at
		// runtime; panic surfaces a programmer error in tests rather
		// than silently shipping a broken tool.
		panic(fmt.Sprintf("memory tool schema marshal: %v", err))
	}
	return &Tool{
		Name: "memory",
		Description: "Save durable information to persistent memory that survives across sessions. " +
			"Memory is injected into the system prompt at every session start, so keep entries " +
			"compact and focused on facts that will still matter later.\n\n" +
			"WHEN TO SAVE (proactively, do not wait to be asked):\n" +
			"- the user corrects you or says 'remember this' / 'don't do that again'\n" +
			"- the user shares a preference, habit, or personal detail (name, role, timezone, coding style)\n" +
			"- you discover something stable about the environment (OS, installed tools, project structure)\n" +
			"- you learn a convention, API quirk, or workflow specific to this user's setup\n\n" +
			"DO NOT save: task progress, completed-work logs, raw data dumps, large code blocks, " +
			"or anything trivially re-discoverable. Use the workspace's regular files for those.\n\n" +
			"TARGETS: 'user' for who the user is (preferences, communication style); 'memory' " +
			"for your own notes (environment, conventions, lessons learned). When in doubt, " +
			"prefer 'memory'.\n\n" +
			"ACTIONS: 'add' to append; 'replace' / 'remove' use a short unique old_text substring " +
			"to identify the target entry (you do not need the full text).",
		Schema: json.RawMessage(schema),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Action  string `json:"action"`
				Target  string `json:"target"`
				Content string `json:"content"`
				OldText string `json:"old_text"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			target := MemoryTarget(p.Target)

			var resp MemoryResponse
			var err error
			switch p.Action {
			case "":
				resp = MemoryResponse{Target: target, Message: "action is required"}
			case "add":
				if p.Content == "" {
					resp = MemoryResponse{Target: target, Message: "content is required for action=add"}
					break
				}
				resp, err = store.Add(target, p.Content)
			case "replace":
				if p.OldText == "" || p.Content == "" {
					resp = MemoryResponse{Target: target, Message: "old_text and content are required for action=replace"}
					break
				}
				resp, err = store.Replace(target, p.OldText, p.Content)
			case "remove":
				if p.OldText == "" {
					resp = MemoryResponse{Target: target, Message: "old_text is required for action=remove"}
					break
				}
				resp, err = store.Remove(target, p.OldText)
			default:
				resp = MemoryResponse{Target: target, Message: fmt.Sprintf("unknown action %q (expected add, replace, remove)", p.Action)}
			}
			if err != nil {
				return "", err
			}
			out, jerr := json.Marshal(resp)
			if jerr != nil {
				return "", jerr
			}
			return string(out), nil
		},
	}
}
