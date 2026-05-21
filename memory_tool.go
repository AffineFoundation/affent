package affent

import (
	"context"
	"encoding/json"
	"fmt"
)

// memoryActions enumerates the action values the `memory` tool accepts.
var memoryActions = []string{"add", "replace", "remove", "search"}

// memoryTool builds the `memory` tool. Four actions × two targets ×
// topic-bucketed sub-storage:
//
//   - add / replace / remove operate on a single entry within a
//     (target, topic) bucket. Topic is target-memory-only; for
//     target=user the single profile applies.
//   - search returns ranked entries from a topic (or all topics in
//     target=memory). Use to read on-demand topics that aren't
//     auto-injected into the system prompt.
//
// Current state of the always-in-prompt buckets (core + user) reaches
// the agent through the session-start snapshot. On-demand topics
// surface via search.
func memoryTool(store MemoryStore) *Tool {
	schema, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"action", "target"},
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        memoryActions,
				"description": "add (new entry), replace (update via unique old_text substring), remove (delete via unique old_text substring), search (return ranked entries matching query).",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{string(TargetMemory), string(TargetUser)},
				"description": "memory = your own notes (env, conventions, lessons learned); user = what you know about the user (name, preferences, communication style).",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Topic-bucket name (target=memory only; ignored for target=user). \"core\" lands entries in the always-in-prompt digest; other names go to on-demand topics retrieved via search. Defaults to \"general\". Pick durable, semantic names (\"auth\", \"deploy\", \"lessons\") so topics accumulate cleanly over many sessions.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Entry text. Required for add and replace. Keep compact and information-dense (one entry should pack related facts together, not split them into many short rows).",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Short unique substring identifying the entry to replace or remove.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query for action=search. Plain English keywords; tokenized and stopword-filtered.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "How many search results to return. Default 5, max 20.",
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
		Name: "memory",
		Description: "Save and recall durable information across sessions. Memory is topic-bucketed: " +
			"the 'core' topic is always in the system prompt (use for tight, durable facts), other " +
			"topics are on-demand and retrieved via the search action — capacity grows with topic " +
			"count, not single-file cap.\n\n" +
			"WHEN TO SAVE (proactively):\n" +
			"- the user corrects you or says 'remember this' / 'don't do that again'\n" +
			"- the user shares a preference, habit, or personal detail (name, role, timezone, coding style)\n" +
			"- you discover something stable about the environment (OS, installed tools, project structure)\n" +
			"- you learn a convention, API quirk, or workflow specific to this user's setup\n\n" +
			"DO NOT save: task progress, completed-work logs, raw data dumps, large code blocks, " +
			"or anything trivially re-discoverable.\n\n" +
			"TARGETS: 'user' for who the user is (preferences, communication style); 'memory' " +
			"for your own notes (env, conventions, lessons). When in doubt, prefer 'memory'.\n\n" +
			"TOPICS (target=memory only): pick semantic names ('auth', 'deploy', 'lessons'). " +
			"'core' = always in prompt; everything else = on-demand via search. Defaults to 'general'.\n\n" +
			"ACTIONS: 'add' to append; 'replace' / 'remove' use a short unique old_text substring; " +
			"'search' returns ranked entries matching a query across a topic (or all topics when " +
			"topic is omitted).",
		Schema: json.RawMessage(schema),
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
			target := MemoryTarget(p.Target)

			var resp MemoryResponse
			var err error
			switch p.Action {
			case "":
				resp = MemoryResponse{Target: target, Topic: p.Topic, Message: "action is required"}
			case "add":
				if p.Content == "" {
					resp = MemoryResponse{Target: target, Topic: p.Topic, Message: "content is required for action=add"}
					break
				}
				resp, err = store.Add(target, p.Topic, p.Content)
			case "replace":
				if p.OldText == "" || p.Content == "" {
					resp = MemoryResponse{Target: target, Topic: p.Topic, Message: "old_text and content are required for action=replace"}
					break
				}
				resp, err = store.Replace(target, p.Topic, p.OldText, p.Content)
			case "remove":
				if p.OldText == "" {
					resp = MemoryResponse{Target: target, Topic: p.Topic, Message: "old_text is required for action=remove"}
					break
				}
				resp, err = store.Remove(target, p.Topic, p.OldText)
			case "search":
				if p.Query == "" {
					resp = MemoryResponse{Target: target, Topic: p.Topic, Message: "query is required for action=search"}
					break
				}
				resp, err = store.Search(target, p.Topic, p.Query, p.TopK)
			default:
				resp = MemoryResponse{Target: target, Topic: p.Topic, Message: fmt.Sprintf("unknown action %q (expected add, replace, remove, search)", p.Action)}
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
