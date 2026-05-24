package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"path"
	"strings"

	"github.com/affinefoundation/affent/internal/toolfailure"
)

const (
	identicalToolCallBlockThreshold = 3
	toolFailureWarnThreshold        = 3
	toolFailureHaltThreshold        = 8
	webFetchFailureWarnThreshold    = 2
	webFetchFailureHaltThreshold    = 4

	loopGuardCallCapKind             = "loop_guard_call_cap"
	loopGuardHaltedToolKind          = "loop_guard_halted_tool"
	loopGuardRepeatedCallKind        = "loop_guard_repeated_call"
	loopGuardRepeatedFailuresKind    = "loop_guard_repeated_failures"
	loopGuardRepeatedFailedInputKind = "loop_guard_repeated_failed_input"
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
	failedCalls   map[toolCallKey]toolCallFailure
	failedHosts   map[string]toolCallFailure
	// perToolCounts tracks total per-turn call counts for tools that
	// appear in perTurnCallCaps. Entries are created lazily so most
	// turns pay no allocation for this layer.
	perToolCounts map[string]int
}

type toolCallKey struct {
	name string
	hash uint64
}

type toolCallFailure struct {
	count int
	kind  string
}

func newToolLoopGuard() *toolLoopGuard {
	return &toolLoopGuard{
		callCounts:    map[toolCallKey]int{},
		failureCounts: map[string]int{},
		haltedTools:   map[string]bool{},
		failedCalls:   map[toolCallKey]toolCallFailure{},
	}
}

func (g *toolLoopGuard) recordAttempt(tool string, args json.RawMessage) string {
	if g == nil {
		return ""
	}
	if g.haltedTools[tool] {
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q has already failed %d consecutive times this turn. Stop retrying it and choose a different approach.\nNext: use a different tool, change the evidence source, or answer from the evidence already gathered.", tool, toolFailureHaltThresholdFor(tool)),
			loopGuardHaltedToolKind,
		)
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
	if failure := g.failedCalls[key]; shouldBlockRepeatedFailedCall(tool, failure) {
		return repeatedFailedCallMessage(tool, failure)
	}
	if host := canonicalFetchHost(tool, args); host != "" {
		if failure := g.failedHosts[host]; shouldBlockFailedFetchHost(host, failure) {
			return repeatedFailedFetchHostMessage(host, failure)
		}
	}
	g.callCounts[key]++
	if g.callCounts[key] >= identicalToolCallBlockThreshold {
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: blocked repeated call to %q with the same effective arguments after %d attempts this turn.\nNext: change the arguments, use a different tool, or answer from the evidence already gathered.", tool, g.callCounts[key]),
			loopGuardRepeatedCallKind,
		)
	}
	return ""
}

func perTurnCapMessage(tool string, cap int) string {
	switch tool {
	case FocusedTaskToolName:
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q exceeded the per-turn delegation cap of %d calls. Each focused task is itself a bounded child Loop with its own budget; spawning more in one turn burns parent context without progress.\nNext: answer from the focused-task results you already have, or issue a single broader objective instead of multiple narrow ones.", tool, cap),
			loopGuardCallCapKind,
		)
	case PlanToolName:
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q exceeded the per-turn planning cap of %d calls. Planning should summarize state, not become the task.\nNext: continue from the current plan state, execute the next concrete step, or give the user the best current result.", tool, cap),
			loopGuardCallCapKind,
		)
	default:
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q exceeded the per-turn cap of %d calls.\nNext: stop repeating this tool and continue from the evidence already gathered.", tool, cap),
			loopGuardCallCapKind,
		)
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
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Stop retrying it and choose a different approach.\nNext: use a different tool, change the evidence source, or answer from the evidence already gathered.", tool, haltThreshold),
			loopGuardHaltedToolKind,
		)
	default:
		return ""
	}
}

func (g *toolLoopGuard) recordToolResult(tool string, args json.RawMessage, result string, isErr bool) (string, bool) {
	outcomeOK := toolOutcomeCountsAsSuccess(tool, result, isErr)
	guardResult := g.recordOutcome(tool, outcomeOK)
	g.recordArgumentOutcome(tool, args, result, outcomeOK)
	return guardResult, outcomeOK
}

func (g *toolLoopGuard) recordArgumentOutcome(tool string, args json.RawMessage, result string, ok bool) {
	if g == nil || !tracksFailedArguments(tool) {
		return
	}
	key := toolCallKey{name: tool, hash: hashCanonicalToolArgs(tool, args)}
	if ok {
		delete(g.failedCalls, key)
		if host := canonicalFetchHost(tool, args); host != "" && g.failedHosts != nil {
			delete(g.failedHosts, host)
		}
		return
	}
	failure := g.failedCalls[key]
	failure.count++
	if kind := toolFailureKindForOutcome(tool, result, true); kind != "" {
		failure.kind = kind
	}
	g.failedCalls[key] = failure

	if host := canonicalFetchHost(tool, args); host != "" {
		if g.failedHosts == nil {
			g.failedHosts = map[string]toolCallFailure{}
		}
		hostFailure := g.failedHosts[host]
		hostFailure.count++
		if failure.kind != "" {
			hostFailure.kind = failure.kind
		}
		g.failedHosts[host] = hostFailure
	}
}

func tracksFailedArguments(tool string) bool {
	switch tool {
	case "web_fetch", "web_search":
		return true
	default:
		return false
	}
}

func shouldBlockRepeatedFailedCall(tool string, failure toolCallFailure) bool {
	if failure.count <= 0 {
		return false
	}
	switch tool {
	case "web_fetch":
		return shouldBlockRepeatedFailedFetch(failure)
	case "web_search":
		return true
	default:
		return false
	}
}

func shouldBlockRepeatedFailedFetch(failure toolCallFailure) bool {
	switch failure.kind {
	case "timeout", "network_error", "server_error":
		return failure.count >= 2
	default:
		return true
	}
}

func shouldBlockFailedFetchHost(host string, failure toolCallFailure) bool {
	switch failure.kind {
	case "blocked", "rate_limited", "private_network_blocked":
		threshold := 2
		if isKnownDirectFetchTrapHost(host) {
			threshold = 1
		}
		return failure.count >= threshold
	default:
		return false
	}
}

func isKnownDirectFetchTrapHost(host string) bool {
	for _, suffix := range []string{
		"x.com",
		"twitter.com",
		"facebook.com",
		"instagram.com",
		"linkedin.com",
		"tiktok.com",
		"threads.net",
	} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func canonicalFetchHost(tool string, args json.RawMessage) string {
	if tool != "web_fetch" {
		return ""
	}
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host
}

func repeatedFailedCallMessage(tool string, failure toolCallFailure) string {
	kind := failure.kind
	if kind == "" {
		kind = "unknown"
	}
	switch tool {
	case "web_fetch":
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: blocked repeated failed call to %q with the same effective URL after previous Failure kind=%s.\nNext: do not retry the same failing URL; choose a different source, use another available inspection tool, or answer with clearly marked gaps.", tool, kind),
			loopGuardRepeatedFailedInputKind,
		)
	case "web_search":
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: blocked repeated failed call to %q with the same effective query after previous Failure kind=%s.\nNext: change the query with more distinctive entities, official domains, tickers, subnet ids, or source terms; use known source URLs if available, or answer with clearly marked gaps.", tool, kind),
			loopGuardRepeatedFailedInputKind,
		)
	default:
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: blocked repeated failed call to %q with the same effective arguments after previous Failure kind=%s.\nNext: change the arguments, use a different tool, or answer from the evidence already gathered.", tool, kind),
			loopGuardRepeatedFailedInputKind,
		)
	}
}

func repeatedFailedFetchHostMessage(host string, failure toolCallFailure) string {
	kind := failure.kind
	if kind == "" {
		kind = "unknown"
	}
	return withLoopGuardFailureKind(
		fmt.Sprintf("loop_guard: blocked web_fetch to host %q after %d previous URL failures from that host with Failure kind=%s.\nNext: stop trying more URLs from this host in this turn; switch to a canonical/API/text source, use another available inspection tool, or answer with this host marked as blocked/unverified.", host, failure.count, kind),
		loopGuardRepeatedFailedInputKind,
	)
}

func toolOutcomeCountsAsSuccess(tool, result string, isErr bool) bool {
	if isErr {
		return false
	}
	return !isNoEvidenceResult(tool, result)
}

func isNoEvidenceWebFetchResult(result string) bool {
	return toolfailure.IsNoEvidenceWebFetchResult(result)
}

func isNoEvidenceResult(tool, result string) bool {
	return toolfailure.IsNoEvidenceResult(tool, result)
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
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Read the latest Failure kind and Next guidance before fetching more URLs.\nNext: stop opening search results one by one; switch to a different source type, use another available inspection tool, or answer with clearly marked gaps.", tool, threshold),
			loopGuardRepeatedFailuresKind,
		)
	}
	if tool == "web_search" {
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Read the latest Failure kind and Next guidance before searching again.\nNext: stop repeating broad searches; use more distinctive entities or official domains, switch to known source URLs, or answer with clearly marked gaps.", tool, threshold),
			loopGuardRepeatedFailuresKind,
		)
	}
	return withLoopGuardFailureKind(
		fmt.Sprintf("loop_guard: tool %q has failed %d consecutive times this turn. Read the latest error before retrying.\nNext: change the arguments, verify prerequisites with another tool, or stop using %q if the same error persists.", tool, threshold, tool),
		loopGuardRepeatedFailuresKind,
	)
}

func withLoopGuardFailureKind(message, kind string) string {
	return message + "\nFailure: kind=" + kind
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
