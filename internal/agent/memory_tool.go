package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/memory"
)

// memoryActions enumerates the action values the `memory` tool accepts.
var memoryActions = []string{"add", "replace", "remove", "search", "list"}

// memoryTool builds the `memory` tool. Five actions × two targets ×
// topic-bucketed sub-storage:
//
//   - add / replace / remove operate on a single entry within a
//     (target, topic) bucket. Topic is target-memory-only; for
//     target=user the single profile applies.
//   - search returns ranked entries from a topic (or all topics in
//     target=memory) with created_at timestamps. Use to read on-
//     demand topics that aren't auto-injected into the system prompt.
//   - list enumerates topic names + entry counts + newest_at without
//     reading bodies. Cheap discovery before deciding what to search.
//
// Current state of the always-in-prompt buckets (core + general +
// user) reaches the agent through the session-start snapshot. Custom
// topics surface via search.
func memoryTool(store memory.MemoryStore) *Tool {
	schema, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"action"},
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        memoryActions,
				"description": "add, replace, remove, search, or list.",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{string(memory.TargetMemory), string(memory.TargetUser)},
				"description": "memory (default) for project/env notes; user for stable user preferences/details.",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Memory topic. Use core sparingly; otherwise use semantic names like stack, deploy, auth, conventions. Defaults to general.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Entry text for add/replace. Keep compact and durable.",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Unique substring identifying the entry to replace/remove.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Search result count. Default 5, max 20.",
				"minimum":     1,
				"maximum":     20,
			},
		},
	})
	if err != nil {
		// Building a static schema from constants cannot fail at runtime.
		panic(fmt.Sprintf("memory tool schema marshal: %v", err))
	}
	return &Tool{
		Name:        "memory",
		Description: "Save or recall durable facts across sessions. Use target=user for stable user preferences/details; target=memory topic=core only for facts needed every turn; named topics for project/domain facts. Actions: add, replace, remove, search, list. Do not save transient task progress, raw dumps, or facts easily re-read from files.",
		Schema:      json.RawMessage(schema),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Action  string `json:"action"`
				Target  string `json:"target"`
				Topic   string `json:"topic"`
				Content string `json:"content"`
				OldText string `json:"old_text"`
				Query   string `json:"query"`
				TopK    int    `json:"top_k"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			// Default target to "memory" — the agent-notes case is far
			// more common than user-profile updates, and forcing the
			// model to specify on every call (when omission previously
			// errored with "invalid memory target") wastes ~1.7k tokens
			// per retry on a multi-search turn.
			if p.Target == "" {
				p.Target = string(memory.TargetMemory)
			}
			target := memory.MemoryTarget(p.Target)

			var resp memory.MemoryResponse
			var err error
			switch p.Action {
			case "":
				resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "action is required"}
			case "add":
				if p.Content == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "content is required for action=add"}
					break
				}
				resp, err = store.Add(target, p.Topic, p.Content)
			case "replace":
				if p.OldText == "" || p.Content == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "old_text and content are required for action=replace"}
					break
				}
				resp, err = store.Replace(target, p.Topic, p.OldText, p.Content)
			case "remove":
				if p.OldText == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "old_text is required for action=remove"}
					break
				}
				resp, err = store.Remove(target, p.Topic, p.OldText)
			case "search":
				if p.Query == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "query is required for action=search"}
					break
				}
				resp, err = store.Search(target, p.Topic, p.Query, p.TopK)
			case "list":
				if lister, ok := store.(interface {
					ListTopics(memory.MemoryTarget) (memory.MemoryResponse, error)
				}); ok {
					resp, err = lister.ListTopics(target)
				} else {
					// A custom MemoryStore may not implement the
					// optional list extension.
					// Surface a sane explanation rather than panic.
					resp = memory.MemoryResponse{Target: target, Message: "this MemoryStore does not support action=list"}
				}
			default:
				resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: fmt.Sprintf("unknown action %q (expected one of: %s)", p.Action, strings.Join(memoryActions, ", "))}
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
