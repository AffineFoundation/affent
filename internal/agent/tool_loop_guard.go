package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path"
	"strings"
)

const (
	identicalToolCallBlockThreshold = 3
	toolFailureWarnThreshold        = 3
	toolFailureHaltThreshold        = 8
)

type toolLoopGuard struct {
	callCounts    map[toolCallKey]int
	failureCounts map[string]int
	haltedTools   map[string]bool
}

type toolCallKey struct {
	name string
	hash uint64
}

func newToolLoopGuard() *toolLoopGuard {
	return &toolLoopGuard{
		callCounts:    map[toolCallKey]int{},
		failureCounts: map[string]int{},
		haltedTools:   map[string]bool{},
	}
}

func (g *toolLoopGuard) recordAttempt(tool string, args json.RawMessage) string {
	if g == nil {
		return ""
	}
	if g.haltedTools[tool] {
		return fmt.Sprintf("loop_guard: tool %q has already failed %d consecutive times this turn. Stop retrying it and choose a different approach.\nNext: use a different tool, change the evidence source, or answer from the evidence already gathered.", tool, toolFailureHaltThreshold)
	}
	key := toolCallKey{name: tool, hash: hashCanonicalToolArgs(tool, args)}
	g.callCounts[key]++
	if g.callCounts[key] >= identicalToolCallBlockThreshold {
		return fmt.Sprintf("loop_guard: blocked repeated call to %q with the same effective arguments after %d attempts this turn.\nNext: change the arguments, use a different tool, or answer from the evidence already gathered.", tool, g.callCounts[key])
	}
	return ""
}

func (g *toolLoopGuard) recordOutcome(tool string, ok bool) string {
	if g == nil {
		return ""
	}
	if ok {
		g.failureCounts[tool] = 0
		return ""
	}
	g.failureCounts[tool]++
	switch g.failureCounts[tool] {
	case toolFailureWarnThreshold:
		return fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Read the latest error before retrying.\nNext: change the arguments, verify prerequisites with another tool, or stop using %q if the same error persists.", tool, toolFailureWarnThreshold, tool)
	case toolFailureHaltThreshold:
		g.haltedTools[tool] = true
		return fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Stop retrying it and choose a different approach.\nNext: use a different tool, change the evidence source, or answer from the evidence already gathered.", tool, toolFailureHaltThreshold)
	default:
		return ""
	}
}

func hashCanonicalToolArgs(tool string, args json.RawMessage) uint64 {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		v = string(args)
	} else {
		v = normalizeLoopGuardArgs(tool, v)
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		canonical = args
	}
	h := fnv.New64a()
	_, _ = h.Write(canonical)
	return h.Sum64()
}

func normalizeLoopGuardArgs(tool string, v any) any {
	obj, ok := v.(map[string]any)
	if !ok || !loopGuardNormalizesPathArgs(tool) {
		return v
	}
	out := make(map[string]any, len(obj))
	for k, value := range obj {
		if s, ok := value.(string); ok && loopGuardPathArg(k) {
			out[k] = cleanLoopGuardPathArg(s)
			continue
		}
		out[k] = value
	}
	return out
}

func loopGuardNormalizesPathArgs(tool string) bool {
	switch tool {
	case "read_file", "write_file", "edit_file", "list_files":
		return true
	default:
		return false
	}
}

func loopGuardPathArg(key string) bool {
	switch key {
	case "path", "cwd":
		return true
	default:
		return false
	}
}

func cleanLoopGuardPathArg(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return value
	}
	cleaned := path.Clean(value)
	if cleaned == "." && value != "." {
		return ""
	}
	return strings.TrimPrefix(cleaned, "./")
}
