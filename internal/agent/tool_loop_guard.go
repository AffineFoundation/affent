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

// perTurnCallCaps maps tool names to maximum total calls per parent turn,
// counting attempts with any arguments. Most tools (read_file, shell,
// memory, session_search) have no cap because the model legitimately
// needs many calls in a single turn. The exceptions are delegation
// tools whose budget is itself an entire bounded child Loop. Three
// calls per turn is already strong evidence of over-delegation, and a
// fourth call almost always burns context without progress.
//
// Today only run_task is capped. subagent_run is uncapped because it
// already has depth/recursion guards and is the lower-level developer
// surface; if usage patterns warrant a cap there too, add it here.
//
// Editing this map is a design decision, not a configuration knob:
// the cap exists to enforce the focused-task surface's "bounded
// delegation" contract, not to be tuned per deployment.
var perTurnCallCaps = map[string]int{
	FocusedTaskToolName: 3,
}

type toolLoopGuard struct {
	callCounts    map[toolCallKey]int
	failureCounts map[string]int
	haltedTools   map[string]bool
	// perToolCounts tracks total per-turn call counts for tools that
	// appear in perTurnCallCaps. Entries are created lazily so most
	// turns pay no allocation for this layer.
	perToolCounts map[string]int
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
	// Per-turn-call cap is checked BEFORE the args-hash logic so a
	// model that varies arguments across N+1 delegation attempts still
	// gets the over-delegation message rather than the misleading
	// "same effective arguments" message. The args-hash guard remains
	// a secondary defense for repeats with identical inputs.
	if cap, capped := perTurnCallCaps[tool]; capped {
		if g.perToolCounts[tool] >= cap {
			return fmt.Sprintf("loop_guard: tool %q exceeded the per-turn delegation cap of %d calls. Each focused task is itself a bounded child Loop with its own budget; spawning more in one turn burns parent context without progress.\nNext: answer from the focused-task results you already have, or issue a single broader objective instead of multiple narrow ones.", tool, cap)
		}
		if g.perToolCounts == nil {
			g.perToolCounts = map[string]int{}
		}
		g.perToolCounts[tool]++
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
