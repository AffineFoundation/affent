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
				"description": "add (new entry), replace (update via unique old_text substring), remove (delete via unique old_text substring), search (ranked entries matching query), list (enumerate topic names + entry counts without reading bodies — cheap discovery).",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{string(memory.TargetMemory), string(memory.TargetUser)},
				"description": "memory (default) = your own notes (env, conventions, lessons learned); user = what you know about the user (name, preferences, communication style). Omit for the common case of agent notes.",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Topic-bucket name (target=memory only; ignored for target=user). When saving MULTIPLE distinct facts, put each in its OWN topic — don't lump unrelated things into one bucket (e.g. tech stack, deploy procedure, and incident reports should be three separate topics, not one combined entry). Pick durable semantic names: \"stack\", \"deploy\", \"incidents\", \"auth\", \"conventions\", \"people\". Special: \"core\" lands the entry in the always-in-prompt digest (use sparingly for facts you need on every turn); everything else is retrieved on demand via action=search. Defaults to \"general\".",
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
		Description: "Save and recall durable information across sessions. Two-tier memory inspired by " +
			"working-memory + archival patterns (Letta/MemGPT) plus topic organization (OpenHands microagents):\n\n" +
			"TIER 1 — IN-PROMPT (always visible)\n" +
			"  - target=user             → user profile (who they are)\n" +
			"  - target=memory topic=core → durable facts you need every turn\n" +
			"  - target=memory topic=general (or unset) → default working notes\n\n" +
			"TIER 2 — ARCHIVAL (search-only, capacity grows by topic count)\n" +
			"  - target=memory topic=<name> → category bucket; read with action=search\n\n" +
			"DECISION TREE — when SAVING:\n" +
			"  1. About the user (preference, role, name, comms style)?       → target=user\n" +
			"  2. Hard constraint / fact you need on EVERY turn?              → topic=core (use sparingly)\n" +
			"  3. Domain knowledge that fits a named category?                → topic=<category>\n" +
			"     Good starter topics: stack, deploy, conventions, incidents, people, lessons, auth\n" +
			"     Pick semantic names that will still make sense in 6 months.\n" +
			"  4. Doesn't fit any topic yet?                                  → topic=general\n\n" +
			"DECISION TREE — when RECALLING:\n" +
			"  - Need core / general / user info?                             → already in the snapshot at session start, no tool call needed\n" +
			"  - Looking for something in a specific named topic you know?    → action=search topic=<name> query=<keywords>\n" +
			"  - Don't know which topic, or want a cross-topic answer?        → action=search (no topic) query=<keywords>\n" +
			"  - Want to see what topics exist before deciding?               → action=list (cheap; returns names + counts + newest_at, no bodies)\n\n" +
			"WHEN TO SAVE (proactively, don't wait to be asked):\n" +
			"  - user corrects you or says 'remember this' / 'don't do that again'\n" +
			"  - user shares a preference, habit, or stable personal detail\n" +
			"  - you discover something stable about the environment (OS, tools, project layout)\n" +
			"  - you learn a convention, API quirk, or workflow specific to this setup\n\n" +
			"SAVE ACCURATELY:\n" +
			"  - preserve the user's wording for constraints, preferences, and rules; do not add synonyms\n" +
			"    or reinterpretations that could change meaning\n" +
			"  - if a fact has a direction/order/negation (newest-to-oldest, do not, must), copy that\n" +
			"    part exactly\n" +
			"  - if you are unsure how to compress a fact safely, save the short original sentence\n\n" +
			"DO NOT save: task progress, completed-work logs, raw data dumps, large code blocks, " +
			"transient state, or anything trivially re-discoverable from files / shell.\n\n" +
			"ONE FACT PER ENTRY — split multi-part facts into separate add calls in their own " +
			"topics. Lumping 'tech stack + team rules + comms style' into one entry makes future " +
			"replace/remove ambiguous and search-relevance worse.\n\n" +
			"FRESHNESS — every entry carries a created_at timestamp (RFC3339). Search results\n" +
			"  expose it on each hit; list exposes the per-topic newest_at. Use this to judge\n" +
			"  staleness: a 6-month-old 'we use library X' might be wrong if the user has since\n" +
			"  said they migrated. When in doubt, replace (re-stamps to now) rather than appending.\n\n" +
			"ACTIONS\n" +
			"  add      — append new entry. Duplicate content is silently no-op.\n" +
			"  replace  — update an entry; old_text is a short unique substring. Re-stamps timestamp.\n" +
			"  remove   — delete an entry; same uniqueness rules as replace. Last entry removed → topic file deleted.\n" +
			"  search   — top-K ranked entries matching query (within a topic or all topics).\n" +
			"             Search scores BOTH content AND topic name, returns trimmed snippet\n" +
			"             + created_at — a query like 'incident' matches entries in the\n" +
			"             'incidents' topic even when the word doesn't appear in the body.\n" +
			"  list     — topic names + counts + newest_at, no bodies. Cheap discovery before search.",
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
