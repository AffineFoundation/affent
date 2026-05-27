package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"path"
	"strings"

	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/toolfailure"
	"github.com/affinefoundation/affent/internal/websource"
)

const (
	identicalToolCallBlockThreshold = 3
	toolFailureWarnThreshold        = 3
	toolFailureHaltThreshold        = 8
	webFetchFailureWarnThreshold    = 2
	webFetchFailureHaltThreshold    = 8
	browserInteractionWarnThreshold = 2
	browserInteractionHaltThreshold = 5
	browserFindNoMatchThreshold     = 3
	browserNetworkNoMatchThreshold  = 3
	browserScrollNoMoveThreshold    = 2

	loopGuardCallCapKind             = "loop_guard_call_cap"
	loopGuardHaltedToolKind          = "loop_guard_halted_tool"
	loopGuardRepeatedCallKind        = "loop_guard_repeated_call"
	loopGuardRepeatedFailuresKind    = "loop_guard_repeated_failures"
	loopGuardRepeatedFailedInputKind = "loop_guard_repeated_failed_input"
	loopGuardDirectReaderWarningKind = "loop_guard_direct_reader_warning"
	loopGuardNoNewEvidenceKind       = "loop_guard_no_new_evidence"

	maxDirectReaderWarningURLs = 64
)

// perTurnCallCaps maps tool names to maximum total calls per parent turn,
// counting attempts with any arguments. Most tools (read_file, shell,
// memory, session_search) have no cap because the model may legitimately
// need many calls in a single turn. The capped exceptions are stateful
// workflow tools where repeated calls usually indicate drift: run_task
// is a whole bounded child Loop, plan is a compact task-state tool
// that should be updated sparingly rather than churned, and external
// research/browser tools are costly enough that a single turn should
// converge instead of probing indefinitely.
//
// subagent_run is uncapped because it already has depth/recursion guards
// and is the lower-level developer surface; if usage patterns warrant a
// cap there too, add it here.
//
// Editing this map is a design decision, not a configuration knob:
// these caps enforce runtime workflow contracts, not per-deployment
// policy.
var perTurnCallCaps = map[string]int{
	FocusedTaskToolName:    3,
	PlanToolName:           6,
	"web_search":           4,
	"web_fetch":            8,
	"browser_navigate":     8,
	"browser_snapshot":     4,
	"browser_find":         8,
	"browser_network":      8,
	"browser_network_read": 8,
	"browser_click":        5,
	"browser_scroll":       5,
	"browser_type":         5,
	"browser_wait":         4,
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
	perToolCounts              map[string]int
	directReaderWarningURLs    map[string]bool
	browserFindNoMatchURL      string
	browserFindNoMatchCount    int
	browserNetworkNoMatchPage  string
	browserNetworkNoMatchCount int
	browserScrollNoMovePage    string
	browserScrollNoMoveDir     string
	browserScrollNoMoveCount   int
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
	if host, path := canonicalFetchHostPath(tool, args); host != "" {
		if failure := g.failedHosts[host]; shouldBlockFailedFetchHost(host, path, failure) {
			return repeatedFailedFetchHostMessage(host, failure)
		}
	}
	if u := canonicalFetchURL(tool, args); u != "" && g.directReaderWarningURLs[u] {
		return directReaderWarningFetchMessage(u)
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
	case "web_search", "web_fetch":
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: tool %q exceeded the per-turn external-research cap of %d calls. More broad fetching/searching is likely to burn context without improving the answer.\nNext: answer from the verified sources already gathered, or state the remaining gaps clearly instead of trying another URL/query.", tool, cap),
			loopGuardCallCapKind,
		)
	default:
		if isBrowserTool(tool) {
			return withLoopGuardFailureKind(
				fmt.Sprintf("loop_guard: tool %q exceeded the per-turn browser cap of %d calls. Repeated browser actions on dynamic/search pages usually add navigation noise after this point.\nNext: answer from the verified visible evidence already gathered, use only the strongest SourceAccess results, and mark unavailable metrics as gaps.", tool, cap),
				loopGuardCallCapKind,
			)
		}
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
	g.recordDirectReaderWarnings(tool, result)
	g.recordArgumentOutcome(tool, args, result, outcomeOK)
	if browserGuard := g.recordBrowserFindNoMatch(tool, result, isErr); browserGuard != "" {
		if guardResult != "" {
			guardResult += "\n\n" + browserGuard
		} else {
			guardResult = browserGuard
		}
		outcomeOK = false
	}
	if browserGuard := g.recordBrowserNetworkNoMatch(tool, result, isErr); browserGuard != "" {
		if guardResult != "" {
			guardResult += "\n\n" + browserGuard
		} else {
			guardResult = browserGuard
		}
		outcomeOK = false
	}
	if browserGuard := g.recordBrowserScrollNoMovement(tool, result, isErr); browserGuard != "" {
		if guardResult != "" {
			guardResult += "\n\n" + browserGuard
		} else {
			guardResult = browserGuard
		}
		outcomeOK = false
	}
	return guardResult, outcomeOK
}

func (g *toolLoopGuard) recordBrowserFindNoMatch(tool, result string, isErr bool) string {
	if g == nil || tool != "browser_find" || isErr {
		return ""
	}
	info, _ := sourceaccess.FirstInfoFromResult(result)
	u := info.AccessedURL
	if u == "" {
		u = "__unknown_browser_page__"
	}
	if !browserFindNoMatches(result) {
		if g.browserFindNoMatchURL == u {
			g.browserFindNoMatchCount = 0
		}
		return ""
	}
	if g.browserFindNoMatchURL != u {
		g.browserFindNoMatchURL = u
		g.browserFindNoMatchCount = 0
	}
	g.browserFindNoMatchCount++
	if g.browserFindNoMatchCount < browserFindNoMatchThreshold {
		return ""
	}
	return browserFindNoNewEvidenceMessage(u, g.browserFindNoMatchCount)
}

func browserFindNoMatches(result string) bool {
	for _, line := range strings.Split(result, "\n") {
		if strings.TrimSpace(line) == "MATCHES: none" {
			return true
		}
	}
	return false
}

func browserFindNoNewEvidenceMessage(rawURL string, count int) string {
	source := "the current rendered page"
	if rawURL != "" && rawURL != "__unknown_browser_page__" {
		source = fmt.Sprintf("%q", rawURL)
	}
	return withLoopGuardFailureKind(
		fmt.Sprintf("loop_guard: browser_find returned no matches on %s %d times this turn. Repeating page-text searches is unlikely to add evidence.\nNext: inspect the current browser_snapshot once for visible labels, use browser_network/browser_network_read for hidden XHR/fetch data if available, navigate to a different source, or answer that the field is not visible in the inspected page.", source, count),
		loopGuardNoNewEvidenceKind,
	)
}

func (g *toolLoopGuard) recordBrowserNetworkNoMatch(tool, result string, isErr bool) string {
	if g == nil || tool != "browser_network" || isErr {
		return ""
	}
	page := browserNetworkCurrentPage(result)
	if page == "" {
		page = "__unknown_browser_page__"
	}
	if !browserNetworkNoMatches(result) {
		if g.browserNetworkNoMatchPage == page {
			g.browserNetworkNoMatchCount = 0
		}
		return ""
	}
	if g.browserNetworkNoMatchPage != page {
		g.browserNetworkNoMatchPage = page
		g.browserNetworkNoMatchCount = 0
	}
	g.browserNetworkNoMatchCount++
	if g.browserNetworkNoMatchCount < browserNetworkNoMatchThreshold {
		return ""
	}
	return browserNetworkNoNewEvidenceMessage(page, g.browserNetworkNoMatchCount)
}

func browserNetworkNoMatches(result string) bool {
	if !strings.Contains(result, "BROWSER NETWORK EVIDENCE") {
		return false
	}
	for _, line := range strings.Split(result, "\n") {
		if strings.TrimSpace(line) == "MATCHES: none" {
			return true
		}
	}
	return false
}

func browserNetworkCurrentPage(result string) string {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CURRENT_PAGE:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "CURRENT_PAGE:"))
		}
	}
	return ""
}

func browserNetworkNoNewEvidenceMessage(rawURL string, count int) string {
	source := "the current rendered page"
	if rawURL != "" && rawURL != "__unknown_browser_page__" {
		source = fmt.Sprintf("%q", rawURL)
	}
	return withLoopGuardFailureKind(
		fmt.Sprintf("loop_guard: browser_network returned no captured response matches on %s %d times this turn. Repeating network searches is unlikely to add evidence unless the page loads new XHR/fetch responses.\nNext: inspect the current browser_snapshot once for visible labels, interact with the relevant tab or wait once if data has not loaded, use a known API/text/source endpoint, or mark hidden fields unverified.", source, count),
		loopGuardNoNewEvidenceKind,
	)
}

func (g *toolLoopGuard) recordBrowserScrollNoMovement(tool, result string, isErr bool) string {
	if g == nil || tool != "browser_scroll" || isErr {
		return ""
	}
	direction, noMovement := browserScrollNoMovement(result)
	page := sourceaccess.AccessedURLFromResult(result)
	if page == "" {
		page = "__unknown_browser_page__"
	}
	if !noMovement {
		if g.browserScrollNoMovePage == page {
			g.browserScrollNoMoveCount = 0
			g.browserScrollNoMoveDir = ""
		}
		return ""
	}
	if g.browserScrollNoMovePage != page || g.browserScrollNoMoveDir != direction {
		g.browserScrollNoMovePage = page
		g.browserScrollNoMoveDir = direction
		g.browserScrollNoMoveCount = 0
	}
	g.browserScrollNoMoveCount++
	if g.browserScrollNoMoveCount < browserScrollNoMoveThreshold {
		return ""
	}
	return browserScrollNoMovementMessage(page, direction, g.browserScrollNoMoveCount)
}

func browserScrollNoMovement(result string) (string, bool) {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SCROLL:") {
			continue
		}
		direction := ""
		noMovement := false
		for _, field := range strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "SCROLL:"))) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch key {
			case "direction":
				direction = value
			case "movement":
				noMovement = value == "none"
			}
		}
		return direction, noMovement
	}
	return "", false
}

func browserScrollNoMovementMessage(rawURL, direction string, count int) string {
	source := "the current rendered page"
	if rawURL != "" && rawURL != "__unknown_browser_page__" {
		source = fmt.Sprintf("%q", rawURL)
	}
	if direction == "" {
		direction = "same direction"
	}
	return withLoopGuardFailureKind(
		fmt.Sprintf("loop_guard: browser_scroll produced no page movement on %s %d times this turn while scrolling %s. Repeating boundary scrolls is unlikely to reveal new evidence.\nNext: stop scrolling this page in the same direction; use browser_find/browser_snapshot for visible labels, browser_network/browser_network_read for hidden XHR/fetch data, click a visible tab/pagination control, navigate to a more specific source, or mark the field unavailable.", source, count, direction),
		loopGuardNoNewEvidenceKind,
	)
}

func (g *toolLoopGuard) recordDirectReaderWarnings(tool, result string) {
	if g == nil || tool != "web_search" || !strings.Contains(result, "Direct-reader warning") {
		return
	}
	currentURL := ""
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if u := canonicalWebURLFromStandaloneLine(line); u != "" {
			currentURL = u
			continue
		}
		if strings.Contains(line, "Direct-reader warning") && currentURL != "" {
			if g.directReaderWarningURLs == nil {
				g.directReaderWarningURLs = map[string]bool{}
			}
			if len(g.directReaderWarningURLs) >= maxDirectReaderWarningURLs {
				return
			}
			g.directReaderWarningURLs[currentURL] = true
		}
	}
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

func shouldBlockFailedFetchHost(host, fetchPath string, failure toolCallFailure) bool {
	switch failure.kind {
	case "blocked", "rate_limited", "private_network_blocked":
		threshold := 2
		if websource.IsKnownDirectReaderTrapHost(host) {
			threshold = 1
		}
		return failure.count >= threshold
	case "dynamic_shell":
		return failure.count >= 2 && !websource.IsLikelyTextOrAPIPath(fetchPath)
	default:
		return false
	}
}

func canonicalFetchHost(tool string, args json.RawMessage) string {
	host, _ := canonicalFetchHostPath(tool, args)
	return host
}

func canonicalFetchHostPath(tool string, args json.RawMessage) (string, string) {
	if tool != "web_fetch" {
		return "", ""
	}
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", ""
	}
	u, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil {
		return "", ""
	}
	return websource.NormalizeHost(u.Hostname()), u.EscapedPath()
}

func canonicalFetchURL(tool string, args json.RawMessage) string {
	if tool != "web_fetch" {
		return ""
	}
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ""
	}
	return canonicalWebURL(p.URL)
}

func canonicalWebURLFromStandaloneLine(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 1 {
		return ""
	}
	return canonicalWebURL(fields[0])
}

func canonicalWebURL(raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), `"'<>),.;`)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	port := u.Port()
	u.Scheme = scheme
	u.Host = websource.NormalizeHost(u.Hostname())
	if port != "" {
		u.Host += ":" + port
	}
	u.Fragment = ""
	return u.String()
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

func directReaderWarningFetchMessage(rawURL string) string {
	return withLoopGuardFailureKind(
		fmt.Sprintf("loop_guard: blocked web_fetch to %q because web_search marked that URL with Direct-reader warning in this turn.\nNext: do not spend direct page-reading calls on that URL; use the search snippet only as weak discovery/sentiment evidence, choose a canonical API/text/source URL, or answer with this source marked unverified.", rawURL),
		loopGuardDirectReaderWarningKind,
	)
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
	if isBrowserInteractionTool(tool) {
		return browserInteractionWarnThreshold
	}
	return toolFailureWarnThreshold
}

func toolFailureHaltThresholdFor(tool string) int {
	if tool == "web_fetch" {
		return webFetchFailureHaltThreshold
	}
	if isBrowserInteractionTool(tool) {
		return browserInteractionHaltThreshold
	}
	return toolFailureHaltThreshold
}

func isBrowserInteractionTool(tool string) bool {
	switch tool {
	case "browser_click", "browser_scroll", "browser_type", "browser_wait":
		return true
	default:
		return false
	}
}

func isBrowserTool(tool string) bool {
	switch tool {
	case "browser_navigate", "browser_snapshot", "browser_find", "browser_network", "browser_network_read", "browser_click", "browser_scroll", "browser_type", "browser_wait", "browser_screenshot":
		return true
	default:
		return false
	}
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
	if isBrowserInteractionTool(tool) {
		return withLoopGuardFailureKind(
			fmt.Sprintf("loop_guard: browser interaction tool %q has failed %d consecutive times this turn. Dynamic pages often hide, cover, or delay controls, and more clicking/scrolling usually burns context without new evidence.\nNext: stop interacting with the same page unless it visibly changed; use browser_find/browser_snapshot once for targeted text, navigate directly to a canonical URL, switch sources, or answer with a marked gap.", tool, threshold),
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
