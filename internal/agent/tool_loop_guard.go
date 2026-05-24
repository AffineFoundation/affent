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
	webFetchFailureWarnThreshold    = 2
	webFetchFailureHaltThreshold    = 4
)

// perTurnCallCaps maps tool names to maximum total calls per parent turn,
// counting attempts with any arguments. Most tools (read_file, shell,
// memory, session_search) have no cap because the model may legitimately
// need many calls in a single turn. The capped exceptions are stateful
// workflow tools where repeated calls usually indicate drift: run_task
// is a whole bounded child Loop, and plan is a compact task-state tool
// that should be updated sparingly rather than churned.
//
// subagent_run is uncapped because it already has depth/recursion guards
// and is the lower-level developer surface; if usage patterns warrant a
// cap there too, add it here.
//
// Editing this map is a design decision, not a configuration knob:
// these caps enforce runtime workflow contracts, not per-deployment
// policy.
var perTurnCallCaps = map[string]int{
	FocusedTaskToolName: 3,
	PlanToolName:        6,
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
		return fmt.Sprintf("loop_guard: tool %q has already failed %d consecutive times this turn. Stop retrying it and choose a different approach.\nNext: use a different tool, change the evidence source, or answer from the evidence already gathered.", tool, toolFailureHaltThresholdFor(tool))
	}
	// Per-turn-call cap is checked BEFORE the args-hash logic so a
	// model that varies arguments across N+1 delegation attempts still
	// gets the over-delegation message rather than the misleading
	// "same effective arguments" message. The args-hash guard remains
	// a secondary defense for repeats with identical inputs.
	if cap, capped := perTurnCallCaps[tool]; capped {
		if g.perToolCounts[tool] >= cap {
			return perTurnCapMessage(tool, cap)
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

func perTurnCapMessage(tool string, cap int) string {
	switch tool {
	case FocusedTaskToolName:
		return fmt.Sprintf("loop_guard: tool %q exceeded the per-turn delegation cap of %d calls. Each focused task is itself a bounded child Loop with its own budget; spawning more in one turn burns parent context without progress.\nNext: answer from the focused-task results you already have, or issue a single broader objective instead of multiple narrow ones.", tool, cap)
	case PlanToolName:
		return fmt.Sprintf("loop_guard: tool %q exceeded the per-turn planning cap of %d calls. Planning should summarize state, not become the task.\nNext: continue from the current plan state, execute the next concrete step, or give the user the best current result.", tool, cap)
	default:
		return fmt.Sprintf("loop_guard: tool %q exceeded the per-turn cap of %d calls.\nNext: stop repeating this tool and continue from the evidence already gathered.", tool, cap)
	}
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
	warnThreshold := toolFailureWarnThresholdFor(tool)
	haltThreshold := toolFailureHaltThresholdFor(tool)
	switch g.failureCounts[tool] {
	case warnThreshold:
		return toolFailureWarnMessage(tool, warnThreshold)
	case haltThreshold:
		g.haltedTools[tool] = true
		return fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Stop retrying it and choose a different approach.\nNext: use a different tool, change the evidence source, or answer from the evidence already gathered.", tool, haltThreshold)
	default:
		return ""
	}
}

func toolFailureWarnThresholdFor(tool string) int {
	if tool == "web_fetch" {
		return webFetchFailureWarnThreshold
	}
	return toolFailureWarnThreshold
}

func toolFailureHaltThresholdFor(tool string) int {
	if tool == "web_fetch" {
		return webFetchFailureHaltThreshold
	}
	return toolFailureHaltThreshold
}

func toolFailureWarnMessage(tool string, threshold int) string {
	if tool == "web_fetch" {
		return fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Read the latest Failure kind and Next guidance before fetching more URLs.\nNext: stop opening search results one by one; switch to a different source type, use another available inspection tool, or answer with clearly marked gaps.", tool, threshold)
	}
	return fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Read the latest error before retrying.\nNext: change the arguments, verify prerequisites with another tool, or stop using %q if the same error persists.", tool, threshold, tool)
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
