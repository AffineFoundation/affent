package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/affinefoundation/affent/internal/memory"
)

const (
	// MemoryToolName is the registry name for the durable memory tool.
	MemoryToolName      = "memory"
	memoryActionAdd     = "add"
	memoryActionReplace = "replace"
	memoryActionRemove  = "remove"
	memoryActionSearch  = "search"
	memoryActionList    = "list"
)

const (
	maxMemoryActionBytes  = 16
	maxMemoryTargetBytes  = 16
	maxMemoryTopicBytes   = 128
	maxMemoryContentBytes = 16 * 1024
	maxMemoryOldTextBytes = 4 * 1024
)

var memoryActions = []string{
	memoryActionAdd, memoryActionReplace, memoryActionRemove,
	memoryActionSearch, memoryActionList,
}

type memoryToolArgs struct {
	Action  string `json:"action"`
	Target  string `json:"target"`
	Topic   string `json:"topic"`
	Content string `json:"content"`
	OldText string `json:"old_text"`
	Query   string `json:"query"`
	TopK    int    `json:"top_k"`
}

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
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"action"},
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   maxMemoryActionBytes,
				"enum":        memoryActions,
				"description": "add, replace, remove, search, or list.",
			},
			"target": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   maxMemoryTargetBytes,
				"enum":        []string{string(memory.TargetMemory), string(memory.TargetUser)},
				"description": "memory (default) for project/env notes; user for stable user preferences/details.",
			},
			"topic": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   maxMemoryTopicBytes,
				"description": "Memory topic. Use core sparingly; otherwise use semantic names like stack, deploy, auth, conventions. Defaults to general.",
			},
			"content": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   maxMemoryContentBytes,
				"description": "Entry text for add/replace. Keep compact and durable.",
			},
			"old_text": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   maxMemoryOldTextBytes,
				"description": "Unique substring identifying the entry to replace/remove.",
			},
			"query": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   memory.MaxSearchQueryBytes,
				"description": "Search query.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Search result count. Default %d, max %d.", memory.DefaultSearchTopK, memory.MaxSearchTopK),
				"default":     memory.DefaultSearchTopK,
				"minimum":     1,
				"maximum":     memory.MaxSearchTopK,
			},
		},
	})
	if err != nil {
		// Building a static schema from constants cannot fail at runtime.
		panic(fmt.Sprintf("memory tool schema marshal: %v", err))
	}
	return &Tool{
		Name:        MemoryToolName,
		Description: "Save or recall durable facts across sessions. Use target=user for stable user preferences/details; target=memory topic=core only for facts needed every turn; named topics for project/domain facts. Actions: add, replace, remove, search, list. Save verified conventions when the task asks to preserve them for future sessions; store the positive reusable rule, not excluded transient examples. Do not save transient task progress, raw dumps, or routine facts easily re-read from files.",
		Schema:      json.RawMessage(schema),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			p, present, err := decodeMemoryToolArgs(args)
			if err != nil {
				return "", formatMemoryDecodeArgsError(err)
			}
			p.Action = strings.TrimSpace(p.Action)
			p.Target = strings.TrimSpace(p.Target)
			p.Topic = strings.TrimSpace(p.Topic)
			// Default target to "memory" — the agent-notes case is far
			// more common than user-profile updates, and forcing the
			// model to specify on every call (when omission previously
			// errored with "invalid memory target") wastes ~1.7k tokens
			// per retry on a multi-search turn.
			if p.Target == "" {
				p.Target = string(memory.TargetMemory)
			}
			p.Content = strings.TrimSpace(p.Content)
			p.OldText = strings.TrimSpace(p.OldText)
			if resp, ok := rejectUnusedMemoryArgs(p, present); ok {
				out, jerr := json.Marshal(resp)
				if jerr != nil {
					return "", jerr
				}
				return string(out), nil
			}
			if resp, ok := validateMemoryToolInputLengths(p); ok {
				out, jerr := json.Marshal(resp)
				if jerr != nil {
					return "", jerr
				}
				return string(out), nil
			}
			target := memory.MemoryTarget(p.Target)

			var resp memory.MemoryResponse
			var storeErr error
			switch p.Action {
			case "":
				resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "action is required. Next: retry with action=list to discover topics, action=search with query to recall, or action=add with content to save a durable fact."}
			case memoryActionAdd:
				if p.Content == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "content is required for action=add. Next: retry with compact durable content, target=memory for project facts or target=user for stable user preferences."}
					break
				}
				resp, storeErr = store.Add(target, p.Topic, p.Content)
			case memoryActionReplace:
				if p.OldText == "" || p.Content == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "old_text and content are required for action=replace. Next: search/list first, then retry with a unique old_text substring and the full replacement content."}
					break
				}
				resp, storeErr = store.Replace(target, p.Topic, p.OldText, p.Content)
			case memoryActionRemove:
				if p.OldText == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "old_text is required for action=remove. Next: search/list first, then retry with a unique old_text substring from the entry to remove."}
					break
				}
				resp, storeErr = store.Remove(target, p.Topic, p.OldText)
			case memoryActionSearch:
				p.Query = memory.NormalizeSearchQuery(p.Query)
				p.TopK = memory.NormalizeSearchTopK(p.TopK)
				if p.Query == "" {
					resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: "query is required for action=search. Next: retry with 2-6 specific keywords, or use action=list to discover available topics first."}
					break
				}
				resp, storeErr = store.Search(target, p.Topic, p.Query, p.TopK)
			case memoryActionList:
				if lister, ok := store.(interface {
					ListTopics(memory.MemoryTarget) (memory.MemoryResponse, error)
				}); ok {
					resp, storeErr = lister.ListTopics(target)
				} else {
					// A custom MemoryStore may not implement the
					// optional list extension.
					// Surface a sane explanation rather than panic.
					resp = memory.MemoryResponse{Target: target, Message: "this MemoryStore does not support action=list. Next: use action=search with a specific query instead."}
				}
			default:
				resp = memory.MemoryResponse{Target: target, Topic: p.Topic, Message: fmt.Sprintf("unknown action %q (expected one of: %s). Next: retry with one valid action: add, replace, remove, search, or list.", p.Action, strings.Join(memoryActions, ", "))}
			}
			if storeErr != nil {
				return "", storeErr
			}
			out, jerr := json.Marshal(resp)
			if jerr != nil {
				return "", jerr
			}
			return string(out), nil
		},
	}
}

func formatMemoryDecodeArgsError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("decode args for memory: %w\nFailure: kind=invalid_args\nNext: retry memory with a single JSON object using only documented fields: action, target, topic, content, old_text, query, top_k. Use action=search with query to recall, action=list to discover topics, or action=add/replace/remove with compact durable text.", err)
}

func decodeMemoryToolArgs(args json.RawMessage) (memoryToolArgs, map[string]bool, error) {
	var p memoryToolArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return memoryToolArgs{}, nil, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return memoryToolArgs{}, nil, errors.New("arguments must contain a single JSON object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return memoryToolArgs{}, nil, err
	}
	present := make(map[string]bool, len(raw))
	for field := range raw {
		present[field] = true
	}
	return p, present, nil
}

func rejectUnusedMemoryArgs(p memoryToolArgs, present map[string]bool) (memory.MemoryResponse, bool) {
	allowed := map[string]bool{"action": true}
	switch p.Action {
	case "":
		return memory.MemoryResponse{}, false
	case memoryActionAdd:
		allowed["target"] = true
		allowed["topic"] = true
		allowed["content"] = true
	case memoryActionReplace:
		allowed["target"] = true
		allowed["topic"] = true
		allowed["old_text"] = true
		allowed["content"] = true
	case memoryActionRemove:
		allowed["target"] = true
		allowed["topic"] = true
		allowed["old_text"] = true
	case memoryActionSearch:
		allowed["target"] = true
		allowed["topic"] = true
		allowed["query"] = true
		allowed["top_k"] = true
	case memoryActionList:
		allowed["target"] = true
	default:
		return memory.MemoryResponse{}, false
	}
	var unused []string
	for field := range present {
		if !allowed[field] {
			unused = append(unused, field)
		}
	}
	if len(unused) == 0 {
		return memory.MemoryResponse{}, false
	}
	sort.Strings(unused)
	target := memory.MemoryTarget(p.Target)
	if target == "" {
		target = memory.TargetMemory
	}
	verb := "is"
	if len(unused) > 1 {
		verb = "are"
	}
	return memory.MemoryResponse{
		Target: target,
		Topic:  p.Topic,
		Message: fmt.Sprintf("%s %s not used when action=%s. Next: retry memory with only the fields that action uses.",
			strings.Join(unused, ", "), verb, p.Action),
	}, true
}

func validateMemoryToolInputLengths(p memoryToolArgs) (memory.MemoryResponse, bool) {
	target := memory.TargetMemory
	if p.Target == string(memory.TargetUser) {
		target = memory.TargetUser
	}
	check := func(name, value string, max int) (memory.MemoryResponse, bool) {
		if len(value) <= max {
			return memory.MemoryResponse{}, false
		}
		return memory.MemoryResponse{
			Target: target,
			Message: fmt.Sprintf("%s must be at most %d bytes. Next: retry with a compact %s; do not pass raw dumps or oversized identifiers to memory.",
				name, max, name),
		}, true
	}
	if resp, ok := check("action", p.Action, maxMemoryActionBytes); ok {
		return resp, true
	}
	if resp, ok := check("target", p.Target, maxMemoryTargetBytes); ok {
		return resp, true
	}
	if resp, ok := check("topic", p.Topic, maxMemoryTopicBytes); ok {
		return resp, true
	}
	if resp, ok := check("content", p.Content, maxMemoryContentBytes); ok {
		return resp, true
	}
	if resp, ok := check("old_text", p.OldText, maxMemoryOldTextBytes); ok {
		return resp, true
	}
	return memory.MemoryResponse{}, false
}
